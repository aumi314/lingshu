package api

import "testing"

// 调用限制可在系统设置里改，0 表示不限；非法值拒绝。
func TestCallLimitsSettings(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	var s struct {
		CallLimits struct {
			Concurrency   int64 `json:"user_concurrency"`
			RPM           int64 `json:"user_rpm"`
			ReserveOutput int64 `json:"reserve_output_tokens"`
		} `json:"call_limits"`
	}
	get := func() {
		if code, b := a.do("GET", "/api/admin/settings", nil, &s); code != 200 {
			t.Fatalf("%d %s", code, b)
		}
	}
	get()
	if s.CallLimits.Concurrency != 10 || s.CallLimits.RPM != 120 || s.CallLimits.ReserveOutput != 8192 {
		t.Fatalf("默认值不对: %+v", s.CallLimits)
	}
	if code, b := a.do("PUT", "/api/admin/settings", map[string]any{"user_concurrency": 0, "user_rpm": 30, "reserve_output_tokens": 4096}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	get()
	if s.CallLimits.Concurrency != 0 || s.CallLimits.RPM != 30 || s.CallLimits.ReserveOutput != 4096 {
		t.Fatalf("保存后读回不对: %+v", s.CallLimits)
	}
	if code, _ := a.do("PUT", "/api/admin/settings", map[string]any{"user_rpm": -1}, nil); code != 400 {
		t.Fatalf("负数应拒绝: %d", code)
	}
}
