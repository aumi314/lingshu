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
