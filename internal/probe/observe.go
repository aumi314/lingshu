package probe

import (
	"fmt"
	"sort"
	"strings"
)

// observe 从全部请求里看请求质量：注入、不可见字符、上游混用、搜索痕迹、流式完整性、协议字段。
// 只说明观察到的现象，不计入总分：上游有注入不等于模型是假的，两件事分开看。
func observe(calls, tokenCalls []Call, base *Baseline) []Item {
	var out []Item
	ok := make([]Call, 0, len(calls))
	for _, c := range calls {
		if c.Error == "" {
			ok = append(ok, c)
		}
	}

	// 注入：最短的基准请求（"Reply with exactly: ok"）的输入 token；直连官方 API 只有十几个，Codex 号池会带上 Codex 指令
	if len(tokenCalls) > 0 && tokenCalls[0].UsageReported {
		anchor := tokenCalls[0].InputTokens
		it := Item{Key: "injection", Title: "注入", Details: map[string]any{"anchor_tokens": anchor, "official_codex_tokens": base.TokenBase}}
		switch {
		case anchor < 200:
			it.Status, it.Summary = Pass, fmt.Sprintf("基准请求只有 %d 个输入 token，没有明显注入", anchor)
		case abs(anchor-int64(base.TokenBase)) <= 150:
			it.Status, it.Summary = Info, fmt.Sprintf("每个请求带约 %d 个输入 token，和官方 Codex 环境一致（%d），上游应是 Codex 号池；这部分按输入计费", anchor, base.TokenBase)
		case anchor > int64(base.TokenBase)+150:
			it.Status, it.Summary = Warn, fmt.Sprintf("每个请求带约 %d 个输入 token，比官方 Codex 环境（%d）多约 %d，上游额外注入了内容", anchor, base.TokenBase, anchor-int64(base.TokenBase))
		default:
			it.Status, it.Summary = Info, fmt.Sprintf("每个请求带约 %d 个输入 token（官方 Codex 环境是 %d），上游注入了一段提示词", anchor, base.TokenBase)
		}
		// 部分请求多出一大截：号池里有的上游注入更多
		var extra []int64
		short := 0
		for _, c := range ok {
			if !c.UsageReported || (c.Item != "tokens" && c.Item != "fingerprint" && c.Item != "pair" && c.Item != "instruction") {
				continue
			}
			short++
			if c.InputTokens > anchor+1000 {
				extra = append(extra, c.InputTokens-anchor)
			}
		}
		if len(extra) > 0 {
			sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
			it.Status = Warn
			it.Summary += fmt.Sprintf("；另有 %d / %d 个短请求多出约 %d 个输入 token，这部分也会计费", len(extra), short, extra[len(extra)/2])
		}
		out = append(out, it)
	}

	// 不可见字符：零宽空格这类，可能是上游加的溯源水印或篡改
	marks := map[string]int{}
	hit := 0
	for _, c := range ok {
		found := false
		for _, r := range c.Text {
			if name, bad := invisibleNames[r]; bad {
				marks[name]++
				found = true
			}
		}
		if found {
			hit++
		}
	}
	if hit > 0 {
		var names []string
		for n := range marks {
			names = append(names, n)
		}
		sort.Strings(names)
		out = append(out, Item{Key: "invisible", Title: "不可见字符", Status: Warn,
			Summary: fmt.Sprintf("%d / %d 个回答里有 %s，官方回答没有，可能是上游加的水印", hit, len(ok), strings.Join(names, "、")), Details: map[string]any{"counts": marks}})
	} else {
		out = append(out, Item{Key: "invisible", Title: "不可见字符", Status: Pass, Summary: "回答里没有零宽字符等不可见字符"})
	}

	// 上游混用：按响应编号前缀和 service_tier 分组，出现多组说明同一渠道背后不止一种上游
	groups := map[string]int{}
	for _, c := range ok {
		groups[idPrefix(c.ID)+" · service_tier "+orDash(c.ServiceTier)]++
	}
	var gs []string
	for g, n := range groups {
		gs = append(gs, fmt.Sprintf("%s：%d 次", g, n))
	}
	sort.Strings(gs)
	mixed := 0
	for _, n := range groups {
		if n >= 2 {
			mixed++
		}
	}
	if mixed > 1 {
		out = append(out, Item{Key: "mixed", Title: "上游混用", Status: Warn, Summary: fmt.Sprintf("响应格式分成 %d 组，同一渠道背后不止一种上游（号池混用）", mixed), Details: map[string]any{"groups": gs}})
	} else {
		out = append(out, Item{Key: "mixed", Title: "上游混用", Status: Pass, Summary: "响应格式一致", Details: map[string]any{"groups": gs}})
	}

	// 搜索痕迹：检测题都要求直接回答，回答里出现搜索来源说明上游强行挂了搜索工具
	searched := 0
	for _, c := range ok {
		if strings.Contains(c.Text, "utm_source=openai") {
			searched++
		}
	}
	if searched > 0 {
		out = append(out, Item{Key: "search", Title: "搜索痕迹", Status: Warn, Summary: fmt.Sprintf("%d 个回答带网络搜索来源，上游可能强行开着搜索", searched)})
	}

	// 流式完整性：正常结束、有结束原因、有用量
	unfinished, noUsage := 0, 0
	for _, c := range ok {
		if !c.Done || c.FinishReason == "" {
			unfinished++
		}
		if !c.UsageReported {
			noUsage++
		}
	}
	st := Item{Key: "stream", Title: "流式完整性", Status: Pass, Summary: fmt.Sprintf("%d 个流全部正常结束、带完整用量", len(ok))}
	if unfinished+noUsage > 0 {
		st.Status, st.Summary = Warn, fmt.Sprintf("%d 个流没有正常结束，%d 个没有返回用量（共 %d）", unfinished, noUsage, len(ok))
	}
	burst := 0
	for _, c := range ok {
		if c.OutputTokens-c.ReasoningTokens >= 50 && c.TotalMS-c.FirstTokenMS < 200 {
			burst++
		}
	}
	if burst > 0 {
		st.Summary += fmt.Sprintf("；%d 个回答是攒好后一次发出的（假流式）", burst)
	}
	out = append(out, st)

	// 协议：请求失败、缺响应编号
	failed, noID := len(calls)-len(ok), 0
	for _, c := range ok {
		if c.ID == "" {
			noID++
		}
	}
	pr := Item{Key: "protocol", Title: "协议字段", Status: Pass, Summary: "全部请求成功，响应编号等字段齐全"}
	if failed+noID > 0 {
		pr.Status, pr.Summary = Warn, fmt.Sprintf("%d 个请求失败，%d 个响应缺编号", failed, noID)
		if failed > 0 {
			pr.Summary += "；例如：" + firstError(calls)
		}
	}
	out = append(out, pr)
	return out
}

var invisibleNames = map[rune]string{'\u200b': "零宽空格 U+200B", '\u200c': "零宽非连接符 U+200C", '\u200d': "零宽连接符 U+200D", '\u2060': "词连接符 U+2060", '\ufeff': "零宽不换行空格 U+FEFF"}

func idPrefix(id string) string {
	for _, p := range []string{"resp_", "chatcmpl-", "msg_", "gen-"} {
		if strings.HasPrefix(id, p) {
			return p
		}
	}
	if id == "" {
		return "无编号"
	}
	return "其他格式"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// metrics 汇总延迟、速度和用量。
func metrics(calls []Call) map[string]any {
	var firsts []int
	var in, out, cached, streamOut, streamMS int64
	errs := 0
	for _, c := range calls {
		if c.Error != "" {
			errs++
			continue
		}
		in += c.InputTokens
		out += c.OutputTokens
		cached += c.CachedTokens
		if c.FirstTokenMS > 0 {
			firsts = append(firsts, c.FirstTokenMS)
		}
		if visible := c.OutputTokens - c.ReasoningTokens; visible >= 20 && c.TotalMS-c.FirstTokenMS >= 300 {
			streamOut += visible
			streamMS += int64(c.TotalMS - c.FirstTokenMS)
		}
	}
	m := map[string]any{"requests": len(calls), "errors": errs, "input_tokens": in, "output_tokens": out, "cached_tokens": cached}
	if len(firsts) > 0 {
		m["first_token_ms"] = median(firsts)
	}
	if streamMS > 0 {
		m["tokens_per_sec"] = float64(int(float64(streamOut)/float64(streamMS)*10000)) / 10
	}
	return m
}
