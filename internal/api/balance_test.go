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

func itoa(n int64) string { return decimal.NewFromInt(n).String() }

type balanceItemResp struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type channelResp struct {
	ID               int64             `json:"id"`
	Balance          []balanceItemResp `json:"balance"`
	BalanceCheckedAt *string           `json:"balance_checked_at"`
	BalanceError     string            `json:"balance_error"`
}

func TestChannelBalanceQuery(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	ctx := context.Background()

	fail := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/user/balance":
			io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"33.5"}]}`)
		case "/api/v1/credits":
			io.WriteString(w, `{"data":{"total_credits":10,"total_usage":0.022}}`)
		// sub2api：/v1/usage 直接给美元余额
		case "/sub2/v1/usage", "/v1/usage":
			io.WriteString(w, `{"balance":21.5,"remaining":21.5,"unit":"USD","isValid":true,"planName":"钱包余额"}`)
		// new-api：令牌不限额，返回账户剩余（内部单位），用账单接口的已用反推换算比例
		case "/api/usage/token/":
			io.WriteString(w, `{"code":true,"data":{"total_available":-21399,"total_used":21399,"unlimited_quota":true,"user_quota":{"total_available":603869,"total_used":2099896131}}}`)
		case "/v1/dashboard/billing/subscription":
			io.WriteString(w, `{"object":"billing_subscription","hard_limit_usd":100000000}`)
		case "/v1/dashboard/billing/usage":
			io.WriteString(w, `{"object":"list","total_usage":4.2798}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	e.store.Pool.Exec(ctx, `INSERT INTO channels (id, name, template, api_key, base_url_chat) VALUES
		(1, 'ds', 'deepseek', 'k1', $1), (2, 'glm', 'zhipu', 'k2', $1), (3, 'or', 'openrouter', 'k3', $1),
		(4, 'sub2', 'sub2api', 'k4', $1 || '/v1'), (5, 'na', 'new-api', 'k5', $1 || '/v1')`, up.URL)
	// 老版本 new-api：令牌不限额、不返回账户额度，只能用 OpenAI 旧账单接口算“上限 − 已用”（code gpt 2026-11-12 实测）
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/usage/token/":
			io.WriteString(w, `{"code":true,"data":{"total_available":-16782767,"total_granted":395288,"total_used":17178055,"unlimited_quota":true}}`)
		case "/v1/dashboard/billing/subscription":
			io.WriteString(w, `{"object":"billing_subscription","hard_limit_usd":100}`)
		case "/v1/dashboard/billing/usage":
			io.WriteString(w, `{"object":"list","total_usage":4892.3662}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer old.Close()
	e.store.Pool.Exec(ctx, `INSERT INTO channels (id, name, template, api_key, base_url_chat) VALUES (6, 'na-old', 'new-api', 'k6', $1)`, old.URL+"/v1")

	// 只有管理员能查
	if code, _ := e.login("bob").do("POST", "/api/admin/channels/1/balance", nil, nil); code != 403 {
		t.Fatalf("普通用户不应能查余额: %d", code)
	}
	// 没有余额接口的渠道直接拒绝，不发请求
	if code, body := a.do("POST", "/api/admin/channels/2/balance", nil, nil); code != 400 || !strings.Contains(body, "没有余额接口") {
		t.Fatalf("智谱应提示没有余额接口: %d %s", code, body)
	}
	if code, body := a.do("POST", "/api/admin/channels/99/balance", nil, nil); code != 404 {
		t.Fatalf("渠道不存在应 404: %d %s", code, body)
	}

	// DeepSeek：原样保存厂商返回的币种和金额
	var got struct {
		Balance   []balanceItemResp `json:"balance"`
		CheckedAt string            `json:"checked_at"`
	}
	if code, body := a.do("POST", "/api/admin/channels/1/balance", nil, &got); code != 200 {
		t.Fatalf("查询 deepseek: %d %s", code, body)
	}
	if len(got.Balance) != 1 || got.Balance[0].Currency != "CNY" ||
		!decimal.RequireFromString(got.Balance[0].Amount).Equal(decimal.RequireFromString("33.5")) || got.CheckedAt == "" {
		t.Fatalf("deepseek 结果 = %+v", got)
	}

	// OpenRouter：剩余 = 总额 − 已用
	got.Balance = nil
	if code, body := a.do("POST", "/api/admin/channels/3/balance", nil, &got); code != 200 {
		t.Fatalf("查询 openrouter: %d %s", code, body)
	}
	if len(got.Balance) != 1 || got.Balance[0].Currency != "USD" ||
		!decimal.RequireFromString(got.Balance[0].Amount).Equal(decimal.RequireFromString("9.978")) {
		t.Fatalf("openrouter 结果 = %+v", got)
	}

	// 中转模板：sub2api 读 /v1/usage；new-api 优先账户余额（单位按账单接口反推，50 万 = 1 美元），老版本用“上限 − 已用”
	for _, c := range []struct {
		id   int64
		want string
	}{{4, "21.5"}, {5, "1.2077"}, {6, "51.0763"}} {
		got.Balance = nil
		if code, body := a.do("POST", "/api/admin/channels/"+itoa(c.id)+"/balance", nil, &got); code != 200 {
			t.Fatalf("查询渠道 %d: %d %s", c.id, code, body)
		}
		if len(got.Balance) != 1 || got.Balance[0].Currency != "USD" || !decimal.RequireFromString(got.Balance[0].Amount).Equal(decimal.RequireFromString(c.want)) {
			t.Errorf("渠道 %d 余额 = %+v，期望 $%s", c.id, got.Balance, c.want)
		}
	}

	// 渠道列表带上快照
	var list []channelResp
	if code, body := a.do("GET", "/api/admin/channels", nil, &list); code != 200 {
		t.Fatalf("列表: %d %s", code, body)
	}
	var ds *channelResp
	for i := range list {
		if list[i].ID == 1 {
			ds = &list[i]
		}
	}
	if ds == nil || len(ds.Balance) != 1 || ds.Balance[0].Currency != "CNY" || ds.BalanceCheckedAt == nil {
		t.Fatalf("列表里的余额快照 = %+v", ds)
	}

	// 查询失败：返回 502，记下错误，保留上一次的数字
	fail = true
	if code, body := a.do("POST", "/api/admin/channels/1/balance", nil, nil); code != 502 || !strings.Contains(body, "500") {
		t.Fatalf("上游出错应 502: %d %s", code, body)
	}
	list = nil
	a.do("GET", "/api/admin/channels", nil, &list)
	for i := range list {
		if list[i].ID != 1 {
			continue
		}
		if list[i].BalanceError == "" {
			t.Error("查询失败应记下错误")
		}
		if len(list[i].Balance) != 1 || !decimal.RequireFromString(list[i].Balance[0].Amount).Equal(decimal.RequireFromString("33.5")) {
			t.Errorf("查询失败不应清掉上一次的数字: %+v", list[i].Balance)
		}
	}
}
