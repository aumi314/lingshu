package probe

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewProbeStopsBeforeBody(t *testing.T) {
	for _, test := range []struct {
		name string
		call Call
		stop string
	}{
		{"inflated first usage", Call{UsageReported: true, InputTokens: 280000}, "billing_anomaly"},
		{"quota", Call{HTTPStatus: http.StatusPaymentRequired}, "quota"},
		{"denied", Call{HTTPStatus: http.StatusForbidden, Error: "access denied"}, "preflight_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sent atomic.Int32
			report, err := Run(context.Background(), func(_ context.Context, request Request) Call {
				sent.Add(1)
				if request.Item != "tokens" && request.Item != "instruction" {
					t.Errorf("sent %s before preflight passed", request.Item)
				}
				return test.call
			}, Spec{Baseline: "gpt-6.1-sol", Mode: ModeQuality, Seed: 1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if report.StopReason != test.stop || report.Score != nil || len(report.Items) != 0 {
				t.Fatalf("unexpected report: reason=%q score=%v items=%v", report.StopReason, report.Score, report.Items)
			}
			count := int(sent.Load())
			if count < 1 || count > 3 || report.Counts.Sent != count || report.Counts.Planned != 26 || report.Counts.Skipped != 26-count {
				t.Fatalf("sent %d, counts %+v", count, report.Counts)
			}
			if test.stop == "billing_anomaly" && (report.StopCallIndex == nil || report.Calls[*report.StopCallIndex].InputTokens != 280000) {
				t.Fatal("missing usage evidence for stopped call")
			}
		})
	}
}

func TestFingerprintMarginIsSymmetric(t *testing.T) {
	for _, models := range [][2]string{{"target", "other"}, {"other", "target"}} {
		for _, test := range []struct {
			distance float64
			clear    bool
		}{{0.240, false}, {0.250, true}, {0.260, true}} {
			_, _, clear := clearFingerprintLeader([]Ranked{{Model: models[0], Distance: 0.200}, {Model: models[1], Distance: test.distance}})
			if clear != test.clear {
				t.Errorf("ranking %v, second distance %.3f: clear = %v", models, test.distance, clear)
			}
		}
	}
}

// 完整伪装下费用保护按模板的官方基准算预期：Codex 号池正常多出约 4200 不中止，上游虚报 28 万仍在核验阶段中止。
func TestQualityBillingGuardUsesTemplateBase(t *testing.T) {
	spec := Spec{Baseline: "gpt-6.1-sol", Mode: ModeQuality, Seed: 1, Disguise: true, Template: "codex 0.160.0", TemplateHash: "4468e298e601"}
	for _, test := range []struct {
		input int64
		stop  string
	}{{14061, ""}, {280000, "billing_anomaly"}} {
		report, err := Run(context.Background(), func(_ context.Context, request Request) Call {
			if !request.FullDisguise {
				t.Errorf("质量测试的 %s 请求应带完整伪装", request.Item)
			}
			return Call{UsageReported: true, InputTokens: test.input, Text: "ok", Done: true, FinishReason: "stop", ID: "resp_1"}
		}, spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.StopReason != test.stop || !report.Disguise || report.TemplateHash != "4468e298e601" {
			t.Errorf("input %d: stop=%q disguise=%v template=%q", test.input, report.StopReason, report.Disguise, report.TemplateHash)
		}
	}
}

// 网关超时（Cloudflare 524 等）算可重试；大海捞针重试后仍超时记“没测成”，不判不达标、不计分。
func TestNeedleGatewayTimeoutIsNotAFailure(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusGatewayTimeout, 524} {
		if !gatewayError(Call{HTTPStatus: status}) {
			t.Errorf("HTTP %d 应算网关错误", status)
		}
	}
	p := &plan{needleCodes: []string{"A", "B", "C"}}
	it := p.judgeNeedle([]Call{{HTTPStatus: 524, Error: "HTTP 524 <!DOCTYPE html>"}})
	if it.Status != Info {
		t.Errorf("524 应记没测成: %+v", it)
	}
	if it := p.judgeNeedle([]Call{{HTTPStatus: 500, Error: "HTTP 500"}}); it.Status != Fail {
		t.Errorf("其他失败仍判不通过: %+v", it)
	}
}

// 推理判定（2026-11-19 超超、pikaqiu 的质量测试）：单个请求上游报错记没测成；有推理 token 的上游出现零推理作答判不达标；high 对照题答错要关注。
func TestReasoningJudgement(t *testing.T) {
	_, bs, _ := Load()
	base := bs["gpt-6.1-sol"]
	probs := []MathProblem{{Kind: "recurrence_hard", Answer: 634}, {Kind: "recurrence_hard", Answer: 634}, {Kind: "digit_sum", Answer: 504315}, {Kind: "digit_sum", Answer: 504315}}
	right := func(text string, reasoning int64) Call {
		return Call{UsageReported: true, Done: true, Text: text, ReasoningTokens: reasoning}
	}
	effortOK := []Call{right("42839", 436), right("42839", 492)}
	for _, test := range []struct {
		name   string
		cs     []Call
		effort []Call
		status Status
		want   string
	}{
		{"全对", []Call{right("634", 1267), right("634", 1034), right("504315", 111), right("504315", 122)}, effortOK, Pass, "答对 4 / 4"},
		{"一个 402 记没测成", []Call{right("634", 1267), {HTTPStatus: 402, Error: "HTTP 402"}, right("504315", 111), right("504315", 122)}, effortOK, Pass, "1 个请求上游报错没测成"},
		{"零推理作答", []Call{right("471", 0), right("634", 1034), right("504315", 111), right("504315", 122)}, effortOK, Fail, "推理被跳过"},
		{"high 对照题零推理", []Call{right("634", 1267), right("634", 1034), right("504315", 111), right("504315", 122)}, []Call{right("42839", 436), right("2257", 0)}, Fail, "推理被跳过"},
		{"high 对照题答错", []Call{right("634", 1267), right("634", 1034), right("504315", 111), right("504315", 122)}, []Call{right("42839", 436), right("40000", 480)}, Warn, "推理强度对照题答错 1 / 2"},
		{"上游不报推理 token", []Call{right("634", 0), right("634", 0), right("504315", 0), right("504315", 0)}, []Call{right("42839", 0), right("42839", 0)}, Pass, "上游没有返回推理 token"},
	} {
		p := &plan{math: probs, effortProb: MathProblem{Kind: "lattice_hard", Answer: 42839}}
		it := p.judgeReasoning(test.cs, test.effort, base)
		if it.Status != test.status || !strings.Contains(it.Summary, test.want) {
			t.Errorf("%s: %s %s", test.name, it.Status, it.Summary)
		}
	}
}

// 缓存计数：OpenAI 按 128 token 一块命中，不是整数倍说明是中转自己算的（2026-11-19 aresai 每次报 9724）。
func TestCacheCountObservation(t *testing.T) {
	_, bs, _ := Load()
	base := bs["gpt-6.1-sol"]
	find := func(cached ...int64) *Item {
		var calls []Call
		for _, n := range cached {
			calls = append(calls, Call{UsageReported: true, Done: true, Text: "ok", CachedTokens: n})
		}
		for _, it := range observe(calls, nil, nil, base, nil) {
			if it.Key == "cache" {
				return &it
			}
		}
		return nil
	}
	if it := find(9724, 9724, 9724); it == nil || it.Status != Warn {
		t.Errorf("每次 9724 应提示中转自己算缓存: %+v", it)
	}
	if it := find(18432, 8704, 0); it == nil || it.Status != Pass {
		t.Errorf("128 的整数倍应判原样: %+v", it)
	}
	if it := find(0, 0); it != nil {
		t.Errorf("没有命中缓存不出这一项: %+v", it)
	}
	if it := find(6336, 6336, 9728, 9728, 9728); it == nil || it.Status != Warn || !strings.Contains(it.Summary, "部分上游") {
		t.Errorf("一部分不是整数倍应提示混池: %+v", it)
	}
	if it := find(9724); it == nil || it.Status != Info {
		t.Errorf("只有一个样本只作参考: %+v", it)
	}
	for _, it := range observe([]Call{{UsageReported: true, CachedTokens: 9724}, {UsageReported: true, CachedTokens: 9724}}, nil, nil, nil, nil) {
		if it.Key == "cache" {
			t.Errorf("Claude（没有官方样本）不看缓存计数: %+v", it)
		}
	}
}

// 基准请求不能用固定的测活句：aresai 按关键词拦截 "Reply with exactly: ok"。
func TestProbePromptsAvoidFixedLivenessPhrase(t *testing.T) {
	for _, spec := range []Spec{{Baseline: "gpt-6.1-sol", Mode: ModeQuality, Seed: 7}, {Baseline: "claude-opus-5-5", Mode: ModeQuality, Seed: 7}} {
		var seen int32
		_, err := Run(context.Background(), func(_ context.Context, r Request) Call {
			if strings.Contains(r.Prompt, "Reply with exactly") {
				t.Errorf("%s 的 %s 请求还在用固定测活句", spec.Baseline, r.Item)
			}
			if r.Item == "tokens" || r.Item == "anchor" {
				atomic.AddInt32(&seen, 1)
			}
			return Call{UsageReported: true, InputTokens: 9850, Text: "ok", Done: true, FinishReason: "stop", ID: "resp_1"}
		}, spec, nil)
		if err != nil || seen == 0 {
			t.Errorf("%s: err=%v 基准请求 %d 个", spec.Baseline, err, seen)
		}
	}
}
