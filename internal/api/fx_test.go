package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 汇率统一与实时汇率（2026-11-06）：实时模式自动跟随市场、异常不采用；人民币支付单价跟随系统汇率。
func TestFXLiveRateAndPaymentUnitPrice(t *testing.T) {
	e := newEnv(t)
	body, status := `{"date":"2026-11-05","rates":{"CNY":6.668449}}`, http.StatusOK
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write([]byte(body)) }))
	defer up.Close()
	e.api.FXURL = up.URL
	a, bob := e.login("admin"), e.login("bob")
	configurePayment(t, e, a, "epay")
	a.do("PUT", "/api/admin/settings", map[string]any{"payment_enabled": true}, nil)

	type settings struct {
		CNYPerUSD string `json:"cny_per_usd"`
		FXMode    string `json:"fx_mode"`
		FXStatus  struct {
			Rate, Date, Error, Source string
		} `json:"fx_status"`
	}
	get := func() settings {
		var s settings
		if code, b := a.do("GET", "/api/admin/settings", nil, &s); code != 200 {
			t.Fatalf("%d %s", code, b)
		}
		return s
	}
	epayPrice := func() string {
		var r struct {
			Methods []struct {
				Provider  string `json:"provider"`
				UnitPrice string `json:"unit_price"`
			} `json:"methods"`
		}
		bob.do("GET", "/api/me/payment-methods", nil, &r)
		for _, m := range r.Methods {
			if m.Provider == "epay" {
				return m.UnitPrice
			}
		}
		return ""
	}

	// 手动模式：EPay 单价就是系统汇率
	if code, b := a.do("PUT", "/api/admin/settings", map[string]any{"cny_per_usd": "7.1"}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if got := epayPrice(); got != "7.1" {
		t.Fatalf("EPay 单价应跟随系统汇率 7.1，得到 %q", got)
	}
	// 切到实时：马上拉取，保留 4 位小数；更新汇率记为系统操作
	a.do("PUT", "/api/admin/settings", map[string]any{"cny_per_usd": "6.7"}, nil)
	if code, b := a.do("PUT", "/api/admin/settings", map[string]any{"fx_mode": "live"}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if s := get(); s.FXMode != "live" || s.CNYPerUSD != "6.6684" || s.FXStatus.Rate != "6.6684" || s.FXStatus.Date != "2026-11-05" || s.FXStatus.Error != "" || s.FXStatus.Source != "Frankfurter" {
		t.Fatalf("切到实时后应立即采用市场汇率: %+v", s)
	}
	if got := epayPrice(); got != "6.6684" {
		t.Fatalf("EPay 单价应跟随实时汇率，得到 %q", got)
	}
	var actorNull bool
	if err := e.store.Pool.QueryRow(context.Background(), `SELECT actor_id IS NULL FROM operation_logs WHERE changes::text LIKE '%6.6684%' ORDER BY id DESC LIMIT 1`).Scan(&actorNull); err != nil || !actorNull {
		t.Fatalf("实时汇率更新应记为系统操作: %v %v", actorNull, err)
	}
	// 实时模式下不能手动改汇率
	if code, b := a.do("PUT", "/api/admin/settings", map[string]any{"cny_per_usd": "7"}, nil); code != 400 || !strings.Contains(b, "手动") {
		t.Fatalf("实时模式应拒绝手动改汇率: %d %s", code, b)
	}
	// 变动超过 10%：不采用，写明原因
	body = `{"date":"2026-11-06","rates":{"CNY":7.9}}`
	if err := e.api.RefreshFX(context.Background()); err == nil {
		t.Fatal("变动超过 10% 应报错")
	}
	if s := get(); s.CNYPerUSD != "6.6684" || !strings.Contains(s.FXStatus.Error, "10%") {
		t.Fatalf("异常汇率不应采用: %+v", s)
	}
	// 拉取失败：沿用当前汇率
	status = http.StatusInternalServerError
	e.api.RefreshFX(context.Background())
	if s := get(); s.CNYPerUSD != "6.6684" || !strings.Contains(s.FXStatus.Error, "继续使用当前汇率") {
		t.Fatalf("拉取失败应沿用当前汇率: %+v", s)
	}
	// 切回手动后可以填
	if code, b := a.do("PUT", "/api/admin/settings", map[string]any{"fx_mode": "manual", "cny_per_usd": "7"}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if s := get(); s.FXMode != "manual" || s.CNYPerUSD != "7" {
		t.Fatalf("切回手动: %+v", s)
	}
}
