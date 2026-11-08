package api

import (
	"context"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/store"
)

// 调用日志的负载与响应：峰值 RPM / TPM 取最忙的那一分钟，响应时间取成功请求的中位数。
func TestLogLoadMetrics(t *testing.T) {
	e := newEnv(t)
	// 12:00 三个请求（各 150 token，首字 1s、总 3s）；12:05 一个大请求（10000 token，首字 2s、总 9s）和一个失败的
	if _, err := e.store.Pool.Exec(context.Background(), `
		INSERT INTO request_logs (request_id, user_id, protocol, model, upstream_model, status, cost, input_tokens, output_tokens, stream, first_byte_ms, upstream_start_ms, total_ms, created_at) VALUES
		('a1', 2, 'chat', 'm', '', 'success', 0, 100, 50, true, 1500, 500, 3000, '2026-11-07T12:00:05Z'),
		('a2', 2, 'chat', 'm', '', 'success', 0, 100, 50, true, 1500, 500, 3000, '2026-11-07T12:00:30Z'),
		('a3', 2, 'chat', 'm', '', 'success', 0, 100, 50, true, 1500, 500, 3000, '2026-11-07T12:00:59Z'),
		('b1', 2, 'chat', 'm', '', 'success', 0, 9000, 1000, true, 2500, 500, 9000, '2026-11-07T12:05:10Z'),
		('b2', 2, 'chat', 'm', '', 'upstream_error', 0, 0, 0, true, 0, 500, 100, '2026-11-07T12:05:20Z')`); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Summary store.LogSummary `json:"summary"`
	}
	for _, c := range []struct {
		who  *client
		path string
	}{{e.login("admin"), "/api/admin/logs"}, {e.login("bob"), "/api/me/logs"}} {
		if code, body := c.who.do("GET", c.path+"?type=request&from=2026-11-07T00:00:00Z&to=2026-11-08T00:00:00Z", nil, &out); code != 200 {
			t.Fatalf("%s: %d %s", c.path, code, body)
		}
		s := out.Summary
		at := func(p *time.Time) string {
			if p == nil {
				return ""
			}
			return p.UTC().Format("15:04")
		}
		if s.PeakRPM != 3 || at(s.PeakRPMAt) != "12:00" || s.PeakTPM != 10000 || at(s.PeakTPMAt) != "12:05" {
			t.Errorf("%s 峰值: RPM %d @%s, TPM %d @%s", c.path, s.PeakRPM, at(s.PeakRPMAt), s.PeakTPM, at(s.PeakTPMAt))
		}
		if s.MedianFirstTokenMS == nil || *s.MedianFirstTokenMS != 1000 || s.MedianTotalMS == nil || *s.MedianTotalMS != 3000 {
			t.Errorf("%s 中位数: 首字 %v 总耗时 %v（失败的请求不算）", c.path, s.MedianFirstTokenMS, s.MedianTotalMS)
		}
	}
	// 没有调用时为 0 / 空，不报错
	if code, _ := e.login("admin").do("GET", "/api/admin/logs?type=request&from=2020-01-01T00:00:00Z&to=2020-01-02T00:00:00Z", nil, &out); code != 200 || out.Summary.PeakRPM != 0 || out.Summary.MedianTotalMS != nil {
		t.Errorf("空区间: %d %+v", code, out.Summary)
	}
}
