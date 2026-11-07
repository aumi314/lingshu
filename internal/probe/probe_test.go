package probe

import (
	"context"
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
