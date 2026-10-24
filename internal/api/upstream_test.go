package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

type accountsResp struct {
	Accounts []struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Balance    string  `json:"balance"`
		ChannelIDs []int64 `json:"channel_ids"`
		SyncError  string  `json:"sync_error"`
	} `json:"accounts"`
}

func TestUpstreamAccountLifecycle(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	ctx := context.Background()
	e.store.Pool.Exec(ctx, `INSERT INTO channels (id, name, api_key) VALUES (1, 'ds', 'k1'), (2, 'other', 'k2')`)

	// 只有管理员能看
	if code, _ := e.login("bob").do("GET", "/api/admin/upstream-accounts", nil, nil); code != 403 {
		t.Errorf("普通用户应 403，实际 %d", code)
	}

	// 新建时填人民币余额：按汇率 6.7 换成美元
	var created struct{ ID int64 }
	code, body := a.do("POST", "/api/admin/upstream-accounts", map[string]any{
		"name": "DeepSeek 主账号", "cost_factor": "1", "channel_ids": []int64{1},
		"initial_balance": "67", "initial_currency": "CNY",
	}, &created)
	if code != 200 {
		t.Fatalf("新建: %d %s", code, body)
	}
	var list accountsResp
	a.do("GET", "/api/admin/upstream-accounts", nil, &list)
	if len(list.Accounts) != 1 || list.Accounts[0].Balance != "10" || len(list.Accounts[0].ChannelIDs) != 1 {
		t.Fatalf("列表 = %+v", list)
	}

	// 充值 $5，再请求花掉 $2（模拟），再校准成真实值 ¥80.4 = $12
	id := created.ID
	if code, body := a.do("POST", "/api/admin/upstream-accounts/"+itoa(id)+"/entries", map[string]any{"kind": "recharge", "amount": "5"}, nil); code != 200 {
		t.Fatalf("充值: %d %s", code, body)
	}
	e.store.Pool.Exec(ctx, `UPDATE upstream_accounts SET balance = balance - 2 WHERE id = $1`, id)
	if _, err := e.store.Pool.Exec(ctx, `INSERT INTO request_logs (request_id, user_id, group_id, protocol, model, status,
		input_price, output_price, cache_read_price, cache_write_price, group_ratio, upstream_account_id, upstream_cost)
		VALUES ('r1', 2, 1, 'chat', 'm', 'success', 0, 0, 0, 0, 1, $1, 2)`, id); err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Amount       string `json:"amount"`
		BalanceAfter string `json:"balance_after"`
	}
	if code, body := a.do("POST", "/api/admin/upstream-accounts/"+itoa(id)+"/entries", map[string]any{"kind": "calibrate", "amount": "80.4", "currency": "CNY"}, &entry); code != 200 {
		t.Fatalf("校准: %d %s", code, body)
	}
	// 估算 10 + 5 - 2 = 13，真实 12，偏差 -1
	if entry.BalanceAfter != "12" || entry.Amount != "-1" {
		t.Errorf("校准流水 = %+v", entry)
	}
	var entries []struct {
		Kind             string  `json:"kind"`
		OriginalAmount   string  `json:"original_amount"`
		OriginalCurrency string  `json:"original_currency"`
		EstimatedSpent   *string `json:"estimated_spent"`
	}
	a.do("GET", "/api/admin/upstream-accounts/"+itoa(id)+"/entries", nil, &entries)
	if len(entries) != 3 || entries[0].Kind != "calibrate" || entries[0].OriginalAmount != "80.4" || entries[0].OriginalCurrency != "CNY" {
		t.Fatalf("流水 = %+v", entries)
	}
	if entries[0].EstimatedSpent == nil || *entries[0].EstimatedSpent != "2" {
		t.Errorf("距上次校准估算花费应为 2，实际 %v", deref(entries[0].EstimatedSpent))
	}

	// 改关联渠道：换成渠道 2，渠道 1 解除关联
	a.do("PUT", "/api/admin/upstream-accounts/"+itoa(id), map[string]any{"name": "DeepSeek 主账号", "cost_factor": "1", "channel_ids": []int64{2}}, nil)
	var ch1 *int64
	e.store.Pool.QueryRow(ctx, `SELECT upstream_account_id FROM channels WHERE id = 1`).Scan(&ch1)
	if ch1 != nil {
		t.Error("渠道 1 应解除关联")
	}

	// 参数检查
	if code, _ := a.do("POST", "/api/admin/upstream-accounts", map[string]any{"name": "x", "cost_factor": "0"}, nil); code != 400 {
		t.Errorf("系数 0 应拒绝，实际 %d", code)
	}
	if code, _ := a.do("POST", "/api/admin/upstream-accounts/"+itoa(id)+"/entries", map[string]any{"kind": "recharge", "amount": "-1"}, nil); code != 400 {
		t.Errorf("负数充值应拒绝，实际 %d", code)
	}
	if code, _ := a.do("POST", "/api/admin/upstream-accounts/"+itoa(id)+"/entries", map[string]any{"kind": "calibrate", "amount": "1", "currency": "EUR"}, nil); code != 400 {
		t.Errorf("不支持的币种应拒绝，实际 %d", code)
	}

	// 一个渠道只能对应一个账户
	if code, body := a.do("POST", "/api/admin/upstream-accounts", map[string]any{"cost_factor": "1", "channel_ids": []int64{2}}, nil); code != 409 {
		t.Errorf("渠道已有账户时应拒绝: %d %s", code, body)
	}
	if code, _ := a.do("POST", "/api/admin/upstream-accounts", map[string]any{"cost_factor": "1", "channel_ids": []int64{1, 2}}, nil); code != 400 {
		t.Errorf("一个账户关联多个渠道应拒绝，实际 %d", code)
	}

	// 删除渠道：它的账户和流水一起删
	if code, _ := a.do("DELETE", "/api/admin/channels/2", nil, nil); code != 200 {
		t.Fatalf("删除渠道失败 %d", code)
	}
	var left int
	e.store.Pool.QueryRow(ctx, `SELECT count(*) FROM upstream_accounts`).Scan(&left)
	if left != 0 {
		t.Errorf("删除渠道后账户应一起删除，还剩 %d", left)
	}

	// 关闭估算（删除账户）：渠道还在，只是解除关联
	var again struct{ ID int64 }
	a.do("POST", "/api/admin/upstream-accounts", map[string]any{"cost_factor": "1", "channel_ids": []int64{1}}, &again)
	if code, _ := a.do("DELETE", "/api/admin/upstream-accounts/"+itoa(again.ID), nil, nil); code != 200 {
		t.Errorf("删除账户失败 %d", code)
	}
	var channels int
	e.store.Pool.QueryRow(ctx, `SELECT count(*) FROM channels WHERE id = 1 AND upstream_account_id IS NULL`).Scan(&channels)
	if channels != 1 {
		t.Error("删除账户后渠道应保留并解除关联")
	}
}

func TestUpstreamAccountSync(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	ctx := context.Background()
	fail := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"33.5"}]}`)
	}))
	defer up.Close()
	e.store.Pool.Exec(ctx, `INSERT INTO channels (id, name, template, api_key, base_url_chat) VALUES (1, 'ds', 'deepseek', 'k1', $1), (2, 'glm', 'zhipu', 'k2', $1)`, up.URL)
	e.store.Pool.Exec(ctx, `INSERT INTO upstream_accounts (id, name, auto_sync) VALUES (1, 'ds', true), (2, 'glm', false)`)
	e.store.Pool.Exec(ctx, `UPDATE channels SET upstream_account_id = id`)

	if code, body := a.do("POST", "/api/admin/upstream-accounts/1/sync", nil, nil); code != 200 {
		t.Fatalf("同步: %d %s", code, body)
	}
	var bal string
	e.store.Pool.QueryRow(ctx, `SELECT balance::text FROM upstream_accounts WHERE id = 1`).Scan(&bal)
	if !decimal.RequireFromString(bal).Equal(decimal.RequireFromString("5")) { // ¥33.5 / 6.7
		t.Errorf("同步后余额 = %s，期望 5", bal)
	}
	if code, body := a.do("POST", "/api/admin/upstream-accounts/2/sync", nil, nil); code != 400 || !strings.Contains(body, "手动校准") {
		t.Errorf("智谱没有余额接口，应提示手动校准: %d %s", code, body)
	}

	// 后台定时任务：失败时记下错误
	fail = true
	if code, body := a.do("POST", "/api/admin/upstream-accounts/1/sync", nil, nil); code != 502 {
		t.Errorf("上游出错应 502: %d %s", code, body)
	}
	var list accountsResp
	a.do("GET", "/api/admin/upstream-accounts", nil, &list)
	if list.Accounts[0].SyncError == "" {
		t.Error("同步失败应记下错误")
	}
}

func itoa(n int64) string { return decimal.NewFromInt(n).String() }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestReorderChannels(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	e.store.Pool.Exec(context.Background(), `INSERT INTO channels (id, name, api_key, sort_order) VALUES (1, 'a', 'k', 1), (2, 'b', 'k', 2), (3, 'c', 'k', 3)`)
	e.store.Pool.Exec(context.Background(), `SELECT setval('channels_id_seq', 3)`)
	names := func() string {
		var list []struct{ Name string }
		a.do("GET", "/api/admin/channels", nil, &list)
		var out []string
		for _, c := range list {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	if code, body := a.do("PUT", "/api/admin/channels/order", map[string]any{"ids": []int64{3, 1}}, nil); code != 200 {
		t.Fatalf("排序: %d %s", code, body)
	}
	if got := names(); got != "c,a,b" {
		t.Errorf("顺序 = %s，期望 c,a,b（没提到的排后面）", got)
	}
	if code, body := a.do("POST", "/api/admin/channels", map[string]any{"name": "d", "api_key": "k", "base_url_chat": "https://x.test/v1", "group_ids": []int64{1}, "models": []any{}}, nil); code >= 300 {
		t.Fatalf("新建渠道: %d %s", code, body)
	}
	if got := names(); got != "c,a,b,d" {
		t.Errorf("新渠道应排最后: %s", got)
	}
}

func TestModelPriceRules(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	model := map[string]any{
		"name": "gpt-x", "input_price": "2", "output_price": "10", "cache_read_price": "0.2", "cache_write_price": "2.5",
		"price_rules": []map[string]any{{"input_above": 272000, "input_price": "4", "output_price": "15", "cache_read_price": "0.4", "cache_write_price": "5"}},
	}
	var saved struct {
		ID         int64 `json:"id"`
		PriceRules []struct {
			Name string `json:"name"`
		} `json:"price_rules"`
	}
	if code, body := a.do("POST", "/api/admin/models", model, &saved); code != 200 {
		t.Fatalf("保存: %d %s", code, body)
	}
	if len(saved.PriceRules) != 1 || saved.PriceRules[0].Name != "输入超过 272K" {
		t.Errorf("规则应自动命名: %+v", saved)
	}
	var list []struct {
		Name       string           `json:"name"`
		PriceRules []map[string]any `json:"price_rules"`
	}
	a.do("GET", "/api/admin/models", nil, &list)
	if len(list) != 1 || len(list[0].PriceRules) != 1 || list[0].PriceRules[0]["output_price"] != "15" {
		t.Errorf("列表应带规则: %+v", list)
	}
	model["price_rules"] = []map[string]any{{"input_price": "1"}}
	if code, body := a.do("PUT", "/api/admin/models/"+itoa(saved.ID), model, nil); code != 400 || !strings.Contains(body, "至少要有一个条件") {
		t.Errorf("没有条件的规则应拒绝: %d %s", code, body)
	}

	// 用户端价格：规则价也乘分组倍率
	ctx := context.Background()
	e.store.Pool.Exec(ctx, `UPDATE groups SET ratio = 0.5 WHERE id = 1`)
	e.store.Pool.Exec(ctx, `INSERT INTO channels (id, name, api_key, base_url_chat) VALUES (1, 'c', 'k', 'https://x.test/v1')`)
	e.store.Pool.Exec(ctx, `INSERT INTO channel_groups VALUES (1, 1)`)
	e.store.Pool.Exec(ctx, `INSERT INTO channel_models VALUES (1, $1, '')`, saved.ID)
	_, raw := e.login("bob").do("GET", "/api/me/models", nil, nil)
	if !strings.Contains(raw, `"output_price":"7.5"`) {
		t.Errorf("用户看到的规则价应乘倍率 0.5（15 → 7.5）: %s", raw)
	}
}

func TestSystemSettingsExchangeRate(t *testing.T) {
	e := newEnv(t)
	e.api.PublicURL = "https://gateway.example.com"
	a := e.login("admin")
	if code, raw := a.do("GET", "/api/admin/settings", nil, nil); code != 200 || strings.Contains(raw, "cn_holidays") || !strings.Contains(raw, `"api_base_url":"https://gateway.example.com/v1"`) {
		t.Errorf("系统设置不应返回节假日字段：%d %s", code, raw)
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]any{"cny_per_usd": "7"}, nil); code != 200 {
		t.Fatalf("保存汇率: %d %s", code, body)
	}
	if code, raw := a.do("GET", "/api/admin/settings", nil, nil); code != 200 || !strings.Contains(raw, `"cny_per_usd":"7"`) || strings.Contains(raw, "cn_holidays") {
		t.Errorf("汇率应保存且无节假日字段：%d %s", code, raw)
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]string{"api_base_url": "https://team.example.com/v1/"}, nil); code != 200 {
		t.Fatalf("保存 API 接入地址: %d %s", code, body)
	}
	var endpoints map[string]string
	if code, body := e.login("bob").do("GET", "/api/me/endpoints", nil, &endpoints); code != 200 || endpoints["api_base_url"] != "https://team.example.com/v1" {
		t.Fatalf("用户应读取到保存后的 API 接入地址: %d %s %+v", code, body, endpoints)
	}
	for _, invalid := range []string{"", "http://team.example.com/v1", "https://team.example.com", "https://team.example.com/v2", "https://team.example.com/v1?x=1"} {
		if code, body := a.do("PUT", "/api/admin/settings", map[string]string{"api_base_url": invalid}, nil); code != 400 {
			t.Errorf("无效 API 接入地址 %q 应返回 400，得到 %d %s", invalid, code, body)
		}
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]string{"api_base_url": "http://localhost:8300/v1"}, nil); code != 200 {
		t.Errorf("本地开发地址应允许 HTTP: %d %s", code, body)
	}
}

func TestPaymentSettingPersistenceAndPartialUpdates(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	var settings struct {
		CNYPerUSD      string `json:"cny_per_usd"`
		PaymentEnabled bool   `json:"payment_enabled"`
	}
	check := func(rate string, enabled bool) {
		t.Helper()
		if code, body := a.do("GET", "/api/admin/settings", nil, &settings); code != 200 || settings.CNYPerUSD != rate || settings.PaymentEnabled != enabled {
			t.Fatalf("settings: %d %s", code, body)
		}
	}
	check("6.7", false)
	for _, step := range []struct {
		body    map[string]any
		rate    string
		enabled bool
	}{
		{map[string]any{"payment_enabled": true}, "6.7", true},
		{map[string]any{"cny_per_usd": "7"}, "7", true},
		{map[string]any{"payment_enabled": false}, "7", false},
	} {
		if code, body := a.do("PUT", "/api/admin/settings", step.body, nil); code != 200 {
			t.Fatalf("save settings: %d %s", code, body)
		}
		check(step.rate, step.enabled)
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]any{"cny_per_usd": "0", "payment_enabled": true}, nil); code != 400 {
		t.Fatalf("invalid rate: %d %s", code, body)
	}
	check("7", false)
	user := e.login("bob")
	if code, _ := user.do("GET", "/api/admin/settings", nil, nil); code != 403 {
		t.Fatalf("user read settings: %d", code)
	}
	if code, _ := user.do("PUT", "/api/admin/settings", map[string]any{"payment_enabled": true}, nil); code != 403 {
		t.Fatalf("user change payment setting: %d", code)
	}
	check("7", false)
}
