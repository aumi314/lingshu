package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"
)

// 集合：key 绑集合，按模型走提供它的成员分组的渠道、按成员的倍率计费；同一模型在多个成员里时按分组排序取第一个，停用的成员跳过。
func TestCollectionRoutesByModel(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	// gpt（×0.5）提供 std-chat；claude（×2）提供 other-chat 和 std-chat；relay 是集合，包含两者
	e.exec(`INSERT INTO groups (id, name, ratio, sort_order) VALUES (2, 'gpt', 0.5, 2), (3, 'claude', 2, 3)`)
	e.exec(`INSERT INTO groups (id, name, sort_order, is_collection) VALUES (4, 'relay', 4, true)`)
	e.exec(`INSERT INTO collection_groups VALUES (4, 3), (4, 2)`)
	e.exec(`INSERT INTO models (id, name, input_price, output_price, cache_read_price) VALUES (3, 'other-chat', 1, 2, 0.1)`)
	e.exec(`INSERT INTO channels (id, name, api_key, base_url_chat) VALUES (4, 'c-claude', 'k', $1)`, e.channelBase(1))
	e.exec(`INSERT INTO channel_groups VALUES (1, 2), (4, 3)`)
	e.exec(`INSERT INTO channel_models VALUES (4, 3, ''), (4, 1, '')`)
	e.exec(`UPDATE api_keys SET group_id = 4`)

	base := decimal.RequireFromString("0.00182") // okUpstream 的用量按倍率 1 算的费用
	call := func(model string) (int, string) {
		resp := e.post("/v1/chat/completions", `{"model":"`+model+`"}`, nil)
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("X-Lingshu-Request-Id")
	}
	check := func(model string, ratio decimal.Decimal, group string) {
		t.Helper()
		code, id := call(model)
		if code != http.StatusOK {
			t.Fatalf("%s 状态码 %d", model, code)
		}
		if got, _ := e.cost(id); !got.Equal(base.Mul(ratio)) {
			t.Errorf("%s 扣费 %s，期望 %s", model, got, base.Mul(ratio))
		}
		var name string
		var groupID int64
		e.store.Pool.QueryRow(context.Background(), `SELECT group_name, group_id FROM request_logs WHERE request_id = $1`, id).Scan(&name, &groupID)
		if name != group || groupID != 4 {
			t.Errorf("%s 日志分组 %q（id %d），期望 %q（id 4）", model, name, groupID, group)
		}
	}
	check("std-chat", decimal.RequireFromString("0.5"), "relay › gpt") // 两个成员都有，gpt 排在前面
	check("other-chat", decimal.NewFromInt(2), "relay › claude")

	// /v1/models 列出各成员模型的合集；集合自己不挂渠道，default 里的 std-msg 不在里面
	req, _ := http.NewRequest(http.MethodGet, e.gateway.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Data []struct{ ID string } }
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Data) != 2 || list.Data[0].ID != "other-chat" || list.Data[1].ID != "std-chat" {
		t.Errorf("模型列表 = %+v", list.Data)
	}

	// 停用 gpt：std-chat 改由 claude 提供
	e.exec(`UPDATE groups SET enabled = false WHERE id = 2`)
	check("std-chat", decimal.NewFromInt(2), "relay › claude")

	// 没有成员提供的模型：和普通分组一样报不存在
	if code, _ := call("std-msg"); code != http.StatusNotFound {
		t.Errorf("集合里没有的模型应 404，实际 %d", code)
	}
}
