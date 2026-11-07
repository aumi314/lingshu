package probe

import (
	"context"
	"encoding/base64"
	"math/rand/v2"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// 官方样本完整编进程序，和脚本读到的一致。
func TestLoadBaselines(t *testing.T) {
	s, bs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Fingerprint.Prompts) != 195 {
		t.Errorf("题库应为 195 题: %d", len(s.Fingerprint.Prompts))
	}
	for _, m := range []string{"gpt-6.1-sol", "gpt-6-astra", "gpt-6.1-terra", "gpt-6-luna"} {
		b := bs[m]
		if b == nil || len(b.Answers) < 195 || b.TokenBase == 0 || len(b.TokenDeltas) != 12 || len(b.Math) == 0 {
			t.Fatalf("%s 样本不全: %+v", m, b)
		}
	}
	if bs["gpt-6.1-sol"].TokenBase != 4158 {
		t.Errorf("sol 基准 = %d", bs["gpt-6.1-sol"].TokenBase)
	}
	// 第 1 版按序号记录的答案要换回题目文本，和第 4 轮按文本记录的合并在一起
	if len(bs["gpt-6.1-sol"].Answers["Pick a random English first name."]) < 20 {
		t.Errorf("前 24 题应合并多轮答案: %d", len(bs["gpt-6.1-sol"].Answers["Pick a random English first name."]))
	}
}

func TestNormalizeAndFold(t *testing.T) {
	cases := map[string]string{"Týr.": "tyr", " Wisława Szymborska ": "wislawaszymborska", "Comté": "comte", "木漏れ日。": "木漏れ日", "🦦": "🦦", "Straße": "strasse"}
	for in, want := range cases {
		if got := Fold(Normalize(in)); got != want {
			t.Errorf("%q → %q，期望 %q", in, got, want)
		}
	}
}

// 区分作用强的题数和脚本 dist 算出来的一致（含第 4 轮验证样本，74 道 ≥ 0.3）。
func TestSeparatingPromptsMatchesScript(t *testing.T) {
	if n := len(SeparatingPrompts(0.3)); n != 74 {
		t.Errorf("区分作用 ≥ 0.3 的题 = %d，脚本是 74", n)
	}
}

// 用官方样本自己的一半答案去比，最近的应是它自己：terra、luna 和大模型分得开。
func TestRankIdentifiesSmallModels(t *testing.T) {
	_, bs, _ := Load()
	models := []string{"gpt-6.1-sol", "gpt-6-astra", "gpt-6.1-terra", "gpt-6-luna"}
	for _, m := range []string{"gpt-6.1-terra", "gpt-6-luna"} {
		half := map[string][]string{}
		for q, a := range bs[m].Answers {
			half[q] = a[:len(a)/2]
		}
		if r := Rank(half, models); r[0].Model != m {
			t.Errorf("%s 的答案最像 %s: %+v", m, r[0].Model, r)
		}
	}
}

func TestMathProblemsHaveAnswers(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, k := range []string{"recurrence", "recurrence_hard", "lattice", "lattice_hard", "subset_mod", "digit_sum"} {
		p := NewMathProblem(k, rng)
		if p.Question == "" || (p.Answer <= 0 && k != "recurrence" && k != "recurrence_hard") {
			t.Errorf("%s: %+v", k, p)
		}
	}
	// 数字和：1 到 9999999 里数字和为 36 的整数个数，脚本算出 436975
	r := rand.New(rand.NewPCG(3, 4))
	for range 200 {
		if p := NewMathProblem("digit_sum", r); p.Question == "How many integers n with 1 <= n < 10000000 have a digit sum equal to 36? Answer with the number only." {
			if p.Answer != 436975 {
				t.Fatalf("数字和 36 = %d", p.Answer)
			}
			return
		}
	}
	t.Fatal("没抽到数字和 36 的题")
}

// fakeUpstream 模拟一个上游：单词指纹按某个官方型号的样本随机作答，分词 token 按官方差值返回，结构化、工具、指令题照要求作答。
func fakeUpstream(t *testing.T, model string, seed uint64) Caller {
	s, bs, _ := Load()
	b := bs[model]
	rng := rand.New(rand.NewPCG(seed, 9))
	var mu sync.Mutex
	nonce := regexp.MustCompile(`Use exactly (\{.*\})`)
	return func(_ context.Context, req Request) Call {
		mu.Lock()
		defer mu.Unlock()
		c := Call{UsageReported: true, Done: true, FinishReason: "stop", ID: "chatcmpl-test", InputTokens: 4158, OutputTokens: 5}
		switch req.Item {
		case "tokens":
			c.InputTokens = 4158
			if req.Prompt == s.Tokens.Suffix {
				c.InputTokens = 4159
			}
			for name, text := range s.Tokens.Probes {
				if req.Prompt == text+s.Tokens.Suffix {
					c.InputTokens = 4159 + int64(b.TokenDeltas[name])
				}
			}
		case "fingerprint", "pair":
			q := strings.TrimSuffix(strings.TrimSuffix(req.Prompt, " "+s.Fingerprint.SuffixZh), s.Fingerprint.Suffix)
			if ans := b.Answers[q]; len(ans) > 0 {
				c.Text = ans[rng.IntN(len(ans))]
			}
		case "structured":
			c.Text = nonce.FindStringSubmatch(req.Prompt)[1]
		case "tool":
			city := strings.TrimSuffix(strings.TrimPrefix(req.Prompt, "What is the weather in "), " right now? Use the tool.")
			c.ToolCalls = []ToolCall{{Name: "get_weather", Arguments: `{"city":"` + city + `"}`}}
		case "instruction":
			if w := regexp.MustCompile(`single word (\w+)`).FindStringSubmatch(req.System); w != nil {
				c.Text = w[1]
			} else {
				c.Text = "Hello there!\n" + regexp.MustCompile(`\[\[END-[0-9a-f]+\]\]`).FindString(req.System)
			}
		default:
			c.Text = "0"
		}
		return c
	}
}

func itemOf(r *Report, key string) Item {
	for _, it := range append(r.Items, r.Observations...) {
		if it.Key == key {
			return it
		}
	}
	return Item{}
}

func TestRunIdentifiesModels(t *testing.T) {
	ctx := context.Background()
	// 真 sol：指纹、sol / astra 区分、分词、结构化、指令都通过
	r, err := Run(ctx, fakeUpstream(t, "gpt-6.1-sol", 1), Spec{Baseline: "gpt-6.1-sol", Quick: true, Seed: 11}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tokens", "fingerprint", "pair", "structured", "instruction"} {
		if it := itemOf(r, k); it.Status != Pass {
			t.Errorf("真 sol 的 %s 应通过: %+v", k, it)
		}
	}
	if itemOf(r, "reasoning").Status != Fail { // 模拟上游的数学题都答 0
		t.Errorf("数学题全错应不通过: %+v", itemOf(r, "reasoning"))
	}
	// terra 冒充 sol：指纹不通过，结论指出最像 terra
	r, _ = Run(ctx, fakeUpstream(t, "gpt-6.1-terra", 2), Spec{Baseline: "gpt-6.1-sol", Quick: true, Seed: 12}, nil)
	if it := itemOf(r, "fingerprint"); it.Status != Fail || !strings.Contains(r.Verdict, "最像 gpt-6.1-terra") {
		t.Errorf("terra 冒充 sol 应被识破: %+v / %s", it, r.Verdict)
	}
	// astra 冒充 sol：整体指纹分不开，但 sol / astra 专用题组能识破
	r, _ = Run(ctx, fakeUpstream(t, "gpt-6-astra", 3), Spec{Baseline: "gpt-6.1-sol", Quick: true, Seed: 13}, nil)
	if it := itemOf(r, "pair"); it.Status != Fail || !strings.Contains(it.Summary, "gpt-6-astra") {
		t.Errorf("astra 冒充 sol 应被专用题组识破: %+v", it)
	}
	// 请求质量：模拟上游没有注入多余内容、没有不可见字符
	if it := itemOf(r, "invisible"); it.Status != Pass {
		t.Errorf("不可见字符: %+v", it)
	}
}

// 号池混用：第 3 个会话打到拿 luna 冒充的上游（基准还多注入 233 个 token，和 2026-11-07 在 咔咔 上实测到的一样），其余会话是真 sol。
func TestRunCatchesMixedPool(t *testing.T) {
	sol, luna := fakeUpstream(t, "gpt-6.1-sol", 21), fakeUpstream(t, "gpt-6-luna", 22)
	mixed := func(ctx context.Context, req Request) Call {
		if req.Session != 2 {
			return sol(ctx, req)
		}
		c := luna(ctx, req)
		if req.Item == "anchor" || req.Item == "tokens" {
			c.InputTokens += 233
		}
		return c
	}
	r, err := Run(context.Background(), mixed, Spec{Baseline: "gpt-6.1-sol", Quick: true, Seed: 31}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if it := itemOf(r, "fingerprint"); it.Status != Fail || !strings.Contains(it.Summary, "第 3 个会话最像 gpt-6-luna") {
		t.Errorf("应指出第 3 个会话像 luna: %+v", it)
	}
	if !strings.Contains(r.Verdict, "部分请求疑似不是 gpt-6.1-sol") {
		t.Errorf("总结论: %s", r.Verdict)
	}
	if it := itemOf(r, "mixed"); it.Status != Warn || !strings.Contains(it.Summary, "基准输入 token 不同") || !strings.Contains(it.Summary, "指纹最像的型号不同") {
		t.Errorf("上游混用: %+v", it)
	}
	// 全是真 sol 时，各会话一致
	r, _ = Run(context.Background(), sol, Spec{Baseline: "gpt-6.1-sol", Quick: true, Seed: 32}, nil)
	if it := itemOf(r, "mixed"); it.Status != Pass {
		t.Errorf("真 sol 不应报上游混用: %+v", it)
	}
	if it := itemOf(r, "fingerprint"); it.Status != Pass || !strings.Contains(it.Summary, "4 个会话一致") {
		t.Errorf("真 sol 的指纹: %+v", it)
	}
}

// 大海捞针：完整档才有；三个编号都找到才通过，中转压缩掉中间一段就指出是第 2 个。
func TestNeedleInFullPreset(t *testing.T) {
	codes := regexp.MustCompile(`secret code number \d is (\w+)`)
	answer := func(drop int) Caller {
		base := fakeUpstream(t, "gpt-6.1-sol", 41)
		return func(ctx context.Context, req Request) Call {
			if req.Item != "needle" {
				return base(ctx, req)
			}
			var lines []string
			for i, m := range codes.FindAllStringSubmatch(req.Prompt, -1) {
				if i+1 != drop {
					lines = append(lines, m[1])
				}
			}
			return Call{UsageReported: true, Done: true, FinishReason: "stop", ID: "chatcmpl-test", InputTokens: 92000, Text: strings.Join(lines, "\n")}
		}
	}
	r, err := Run(context.Background(), answer(0), Spec{Baseline: "gpt-6.1-sol", Seed: 51}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if it := itemOf(r, "needle"); it.Status != Pass {
		t.Errorf("三个编号都答对应通过: %+v", it)
	}
	for _, c := range r.Calls {
		if c.Item == "needle" && (len(c.Prompt) > 4000 || !strings.Contains(c.Prompt, "中间省略")) {
			t.Errorf("大海捞针的提示词不应整段存进检测记录（%d 字节）", len(c.Prompt))
		}
	}
	r, _ = Run(context.Background(), answer(2), Spec{Baseline: "gpt-6.1-sol", Seed: 52}, nil)
	if it := itemOf(r, "needle"); it.Status != Fail || !strings.Contains(it.Summary, "第 2 个（约 50% 处）") {
		t.Errorf("中间的编号丢了应不通过并指出位置: %+v", it)
	}
	r, _ = Run(context.Background(), answer(0), Spec{Baseline: "gpt-6.1-sol", Quick: true, Seed: 53}, nil)
	if it := itemOf(r, "needle"); it.Key != "" {
		t.Errorf("快速档不做大海捞针: %+v", it)
	}
}

// fakeClaude 模拟一个 Claude 渠道。real 为 true 时按官方 Messages 格式作答；false 模拟把别家模型转成 Anthropic 格式的中转：
// 编号不是 msg_、没有 thinking、用量缺官方字段、PDF 读不了。
func fakeClaude(real bool) Caller {
	pdfCode := regexp.MustCompile(`check code (\w+)`)
	tag := regexp.MustCompile(`<[a-z0-9]{20}>`)
	return func(_ context.Context, req Request) Call {
		c := Call{UsageReported: true, Done: true, FinishReason: "end_turn", ID: "msg_011CgXItest", Model: "claude-opus-5-5", InputTokens: 90, OutputTokens: 20,
			EventTypes:  []string{"message_start", "content_block_start", "ping", "content_block_delta", "content_block_stop", "message_delta", "message_stop"},
			UsageFields: []string{"cache_creation", "cache_creation_input_tokens", "cache_read_input_tokens", "input_tokens", "output_tokens", "service_tier"}}
		if !real {
			c.ID, c.UsageFields = "chatcmpl-x", []string{"input_tokens", "output_tokens"}
		}
		switch req.Item {
		case "reasoning":
			c.Text = "0"
			if real {
				c.SignedThinking, c.ReasoningTokens = 1, 700
			}
		case "tool":
			city := strings.TrimSuffix(strings.TrimPrefix(req.Prompt, "What is the weather in "), " right now? Use the tool.")
			c.ToolCalls, c.FinishReason = []ToolCall{{Name: "get_weather", Arguments: `{"city":"` + city + `"}`}}, "tool_use"
		case "instruction":
			if w := regexp.MustCompile(`single word (\w+)`).FindStringSubmatch(req.System); w != nil {
				c.Text = w[1]
			} else {
				c.Text = "Hello there!\n" + regexp.MustCompile(`\[\[END-[0-9a-f]+\]\]`).FindString(req.System)
			}
		case "document":
			raw, _ := base64.StdEncoding.DecodeString(req.Document)
			if m := pdfCode.FindStringSubmatch(string(raw)); real && m != nil {
				c.Text = "Lingshu check code " + m[1]
			} else {
				c.Text = "I can't see any attachment."
			}
		case "echo":
			c.Text = tag.FindString(req.Prompt)
		default:
			c.Text = "ok"
		}
		return c
	}
}

func TestProtocolCheckForClaude(t *testing.T) {
	spec := Spec{Baseline: "claude-opus-5-5", UpstreamModel: "claude-opus-5-5", Quick: true, Seed: 61}
	r, err := Run(context.Background(), fakeClaude(true), spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != "protocol" {
		t.Errorf("mode = %q", r.Mode)
	}
	for _, k := range []string{"protocol_format", "tool", "instruction", "document", "injection"} {
		if it := itemOf(r, k); it.Status != Pass {
			t.Errorf("真 Claude 的 %s 应通过: %+v", k, it)
		}
	}
	if it := itemOf(r, "reasoning"); it.Status != Warn { // 有推理但模拟上游答错：没有官方样本，只记需关注
		t.Errorf("推理: %+v", it)
	}
	// 格式转换冒充：协议、推理、文档都不通过
	r, _ = Run(context.Background(), fakeClaude(false), spec, nil)
	for _, k := range []string{"protocol_format", "reasoning"} {
		if it := itemOf(r, k); it.Status != Fail {
			t.Errorf("格式转换冒充的 %s 应不通过: %+v", k, it)
		}
	}
	if it := itemOf(r, "document"); it.Status != Warn || !strings.Contains(it.Summary, "没读出 PDF") {
		t.Errorf("文档: %+v", it)
	}
	if !strings.HasPrefix(r.Verdict, "不达标") {
		t.Errorf("总结论: %s", r.Verdict)
	}
}

// Claude 的大海捞针用一半句子：同一段英文 Claude 的 token 数约是 GPT 的 2.2 倍。
func TestNeedleSizedByTokenizer(t *testing.T) {
	size := map[string]int{}
	capture := func(fam string, inner Caller) Caller {
		return func(ctx context.Context, req Request) Call {
			if req.Item == "needle" {
				size[fam] = len(req.Prompt)
			}
			return inner(ctx, req)
		}
	}
	Run(context.Background(), capture("gpt", fakeUpstream(t, "gpt-6.1-sol", 71)), Spec{Baseline: "gpt-6.1-sol", Seed: 71}, nil)
	Run(context.Background(), capture("claude", fakeClaude(true)), Spec{Baseline: "claude-opus-5-5", UpstreamModel: "claude-opus-5-5", Seed: 72}, nil)
	if size["claude"] == 0 || size["gpt"] == 0 || size["claude"]*2 > size["gpt"]+size["gpt"]/20 {
		t.Errorf("Claude 的大海捞针应约为 GPT 的一半长: %v", size)
	}
}
