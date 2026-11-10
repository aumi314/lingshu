package api

import "testing"

// 调用设置里的转接开关：默认开，能分别关掉并读回，不影响调用限制。
func TestBridgeSwitches(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	type limits struct {
		Concurrency int64 `json:"user_concurrency"`
		R2M         bool  `json:"bridge_responses_to_messages"`
		M2R         bool  `json:"bridge_messages_to_responses"`
	}
	read := func() limits {
		var s struct {
			CallLimits limits `json:"call_limits"`
		}
		if code, body := a.do("GET", "/api/admin/settings", nil, &s); code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		return s.CallLimits
	}
	if got := read(); !got.R2M || !got.M2R || got.Concurrency != 10 {
		t.Fatalf("默认两个方向都开、并发上限 10: %+v", got)
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]any{"bridge_messages_to_responses": false}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if got := read(); !got.R2M || got.M2R {
		t.Errorf("只关 Messages → Responses: %+v", got)
	}
}
