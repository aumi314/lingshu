package api

import (
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

// 集合的保存规则：成员只能是普通分组；类型创建后不变；集合不能直接挂渠道；普通分组不保存成员。
func TestCollectionSave(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	group := func(body map[string]any) (int, store.Group, string) {
		base := map[string]any{"ratio": "1", "enabled": true, "is_public": true, "description": ""}
		for k, v := range body {
			base[k] = v
		}
		var g store.Group
		method, path := "POST", "/api/admin/groups"
		if id, ok := body["id"]; ok {
			method, path = "PUT", "/api/admin/groups/"+itoa(id.(int64))
		}
		code, raw := a.do(method, path, base, &g)
		return code, g, raw
	}
	_, gpt, _ := group(map[string]any{"name": "gpt"})
	_, claude, _ := group(map[string]any{"name": "claude", "member_ids": []int64{gpt.ID}})
	if claude.IsCollection || len(claude.MemberIDs) != 0 {
		t.Fatalf("普通分组不应保存成员: %+v", claude)
	}
	code, relay, raw := group(map[string]any{"name": "relay", "is_collection": true, "member_ids": []int64{claude.ID, gpt.ID}})
	if code != 200 || !relay.IsCollection || len(relay.MemberIDs) != 2 || relay.MemberIDs[0] != gpt.ID {
		t.Fatalf("集合应保存成员并按分组排序返回: %d %s", code, raw)
	}
	if code, _, raw := group(map[string]any{"name": "all", "is_collection": true, "member_ids": []int64{relay.ID}}); code != 400 {
		t.Errorf("集合不能包含集合: %d %s", code, raw)
	}
	// 编辑时不带 member_ids 保留原成员；带 is_collection=false 也不改类型
	if code, g, raw := group(map[string]any{"id": relay.ID, "name": "relay2", "is_collection": false}); code != 200 || !g.IsCollection || len(g.MemberIDs) != 2 {
		t.Errorf("编辑应保留类型和成员: %d %s", code, raw)
	}
	if code, g, _ := group(map[string]any{"id": relay.ID, "name": "relay2", "member_ids": []int64{gpt.ID}}); code != 200 || len(g.MemberIDs) != 1 {
		t.Errorf("编辑成员后应只剩 gpt: %+v", g)
	}
	a.do("POST", "/api/admin/models", map[string]any{"name": "m1", "input_price": "1", "output_price": "1", "cache_read_price": "0", "cache_write_price": "0"}, nil)
	code, raw = a.do("POST", "/api/admin/channels", map[string]any{
		"name": "c", "template": "custom", "api_key": "sk-test-0123456789", "enabled": true,
		"base_url_chat": "https://example.com/v1", "group_ids": []int64{relay.ID},
		"models": []map[string]any{{"model_id": 1, "model_name": "m1"}},
	}, nil)
	if code != 400 {
		t.Errorf("集合不能直接挂渠道: %d %s", code, raw)
	}

	// 用户端价格：集合的模型按提供它的成员倍率显示（gpt ×0.5，m1 输入 $1 → $0.5）
	group(map[string]any{"id": gpt.ID, "name": "gpt", "ratio": "0.5"})
	if code, raw := a.do("POST", "/api/admin/channels", map[string]any{
		"name": "c", "template": "custom", "api_key": "sk-test-0123456789", "enabled": true,
		"base_url_chat": "https://example.com/v1", "group_ids": []int64{gpt.ID},
		"models": []map[string]any{{"model_id": 1, "model_name": "m1"}},
	}, nil); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	var mine struct {
		Models []store.PricedModel `json:"models"`
	}
	a.do("GET", "/api/me/models", nil, &mine)
	found := false
	for _, m := range mine.Models {
		if m.GroupID == relay.ID {
			found = true
			if m.Name != "m1" || m.Input.String() != "0.5" {
				t.Errorf("集合里的 m1 应按 gpt 的倍率显示 $0.5: %+v", m)
			}
		}
	}
	if !found {
		t.Errorf("用户端应列出集合里的模型: %+v", mine.Models)
	}
}
