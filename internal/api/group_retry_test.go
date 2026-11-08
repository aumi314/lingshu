package api

import "testing"

// 服务分组的失败重试次数：新建默认 2；保存不带这个字段（旧客户端）时保留原值；只能 0–4。
func TestGroupRetryCount(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	base := map[string]any{"name": "retry-g", "ratio": "1", "enabled": true, "is_public": true, "user_group_ids": []int64{}, "description": ""}
	var created struct {
		ID         int64 `json:"id"`
		RetryCount *int  `json:"retry_count"`
	}
	if code, body := a.do("POST", "/api/admin/groups", base, &created); code != 200 || created.RetryCount == nil || *created.RetryCount != 2 {
		t.Fatalf("新建应返回默认的 2: %d %s", code, body)
	}
	id := created.ID
	retryOf := func() int {
		var gs []struct {
			ID         int64 `json:"id"`
			RetryCount int   `json:"retry_count"`
		}
		a.do("GET", "/api/admin/groups", nil, &gs)
		for _, g := range gs {
			if g.ID == id {
				return g.RetryCount
			}
		}
		t.Fatalf("没有分组 %d", id)
		return -1
	}
	if got := retryOf(); got != 2 {
		t.Fatalf("新建默认应为 2，实际 %d", got)
	}
	path := "/api/admin/groups/" + itoa(id)
	with := func(n any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		if n != nil {
			m["retry_count"] = n
		}
		return m
	}
	a.do("PUT", path, with(4), nil)
	if got := retryOf(); got != 4 {
		t.Fatalf("改成 4 后读回 %d", got)
	}
	a.do("PUT", path, with(nil), nil)
	if got := retryOf(); got != 4 {
		t.Errorf("不带 retry_count 保存应保留原值 4，实际 %d", got)
	}
	if code, _ := a.do("PUT", path, with(5), nil); code != 400 {
		t.Errorf("5 应拒绝: %d", code)
	}
}
