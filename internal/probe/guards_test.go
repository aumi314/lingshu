package probe

import (
	"context"
	"net/http"
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
			if count < 1 || count > 3 || report.Counts.Sent != count || report.Counts.Planned != 12 || report.Counts.Skipped != 12-count {
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
