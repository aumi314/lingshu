package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/aumi314/lingshu/internal/payments"
	"github.com/aumi314/lingshu/internal/store"
	"github.com/shopspring/decimal"
)

type paymentTransport func(*http.Request) (*http.Response, error)

func (f paymentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func configurePayment(t *testing.T, e *env, admin *client, provider string) payments.Config {
	t.Helper()
	e.api.PublicURL = "https://gateway.example"
	in := map[string]any{"enabled": true, "values": map[string]string{}, "secrets": map[string]string{}}
	if provider == "epay" {
		in["values"] = map[string]string{"merchant_id": "100", "gateway": "https://pay.example"}
		in["secrets"] = map[string]string{"key": "payment-fixture-secret"}
	} else {
		in["secrets"] = map[string]string{"api_key": "payment-fixture-secret", "webhook_secret": "webhook-fixture"}
	}
	var out struct {
		Config payments.Config `json:"config"`
	}
	code, body := admin.do("PUT", "/api/admin/payments/"+provider, in, &out)
	if code != 200 || strings.Contains(body, "payment-fixture-secret") || strings.Contains(body, "webhook-fixture") {
		t.Fatalf("save leaked secrets or failed: %d", code)
	}
	return out.Config
}

func TestPaymentPermissionsQuotesAndSecretLifecycle(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.login("admin"), e.login("bob")
	c := configurePayment(t, e, admin, "epay")
	for _, path := range []string{"/api/admin/payments"} {
		if code, _ := bob.do("GET", path, nil, nil); code != 403 {
			t.Fatalf("user accessed %s: %d", path, code)
		}
	}
	in := map[string]any{"provider": "epay", "method": "alipay", "credit_usd": "2", "request_key": "fixture-order-0001"}
	if code, _ := bob.do("POST", "/api/me/topups", in, nil); code != 403 {
		t.Fatal("global off did not stop new orders")
	}
	var testOrder payments.Order
	if code, body := admin.do("POST", "/api/admin/payments/epay/test-order", in, &testOrder); code != 200 || !testOrder.AdminTest {
		t.Fatal(code, body)
	}
	if code, _ := bob.do("GET", "/api/me/topups/"+testOrder.ID, nil, nil); code != 404 {
		t.Fatal("user accessed another user's order")
	}
	if _, err := e.store.Pool.Exec(context.Background(), `INSERT INTO settings(key,value) VALUES('payment_enabled','true') ON CONFLICT(key) DO UPDATE SET value='true'`); err != nil {
		t.Fatal(err)
	}
	var methods struct {
		Methods []json.RawMessage `json:"methods"`
	}
	bob.do("GET", "/api/me/payment-methods", nil, &methods)
	if len(methods.Methods) != 1 {
		t.Fatal("available methods", len(methods.Methods))
	}
	var o, retry payments.Order
	if code, body := bob.do("POST", "/api/me/topups", in, &o); code != 200 || !o.Amount.Equal(decimal.RequireFromString("13.4")) {
		t.Fatal(code, body)
	}
	bob.do("POST", "/api/me/topups", in, &retry)
	if retry.ID != o.ID {
		t.Fatal("retry created duplicate order")
	}
	in["credit_usd"] = "3"
	if code, _ := bob.do("POST", "/api/me/topups", in, nil); code != 409 {
		t.Fatal("request key reused for other amount")
	}
	var action, reused payments.Action
	if code, body := bob.do("POST", "/api/me/topups/"+o.ID+"/checkout", nil, &action); code != 200 {
		t.Fatal(code, body)
	}
	if code, _ := bob.do("POST", "/api/me/topups/"+o.ID+"/checkout", nil, &reused); code != 200 || action.Fields["sign"] != reused.Fields["sign"] {
		t.Fatal("checkout not reused")
	}
	if strings.Contains(action.Fields["notify_url"], "127.0.0.1") {
		t.Fatal("callback used request Host")
	}
	// 空密钥保留；单价修改不改变旧订单报价或回调，显式清除只影响新版本。
	var updated struct {
		Config payments.Config `json:"config"`
	}
	if code, body := admin.do("PUT", "/api/admin/payments/epay", map[string]any{"unit_price": "7", "secrets": map[string]string{"key": ""}}, &updated); code != 200 {
		t.Fatal(code, body)
	}
	if !updated.Config.ConfiguredSecrets["key"] || updated.Config.CallbackVersionID != c.CallbackVersionID {
		t.Fatal("blank secret was cleared or callback rotated")
	}
	updated.Config = payments.Config{}
	if code, _ := admin.do("PUT", "/api/admin/payments/epay", map[string]any{"enabled": false, "clear_secrets": []string{"key"}}, &updated); code != 200 || updated.Config.ConfiguredSecrets["key"] {
		t.Fatal("explicit clear failed")
	}
	old, err := e.store.PaymentConfigVersion(context.Background(), o.VersionID)
	if err != nil || old.Secrets["key"] != "payment-fixture-secret" || !old.UnitPrice.Equal(decimal.RequireFromString("6.7")) {
		t.Fatal("old credentials or quote changed")
	}
	// 关闭总开关、清除当前凭据后，旧订单通知依然能够入账。
	e.store.Pool.Exec(context.Background(), `UPDATE settings SET value='false' WHERE key='payment_enabled'`)
	params := epay.GenerateParams(map[string]string{"pid": "100", "trade_no": "receipt-1", "out_trade_no": o.ID, "money": "13.40", "trade_status": "TRADE_SUCCESS"}, old.Secrets["key"])
	v := url.Values{}
	for k, x := range params {
		v.Set(k, x)
	}
	callback := fmt.Sprintf("/api/payments/epay/%d/prod/notify?%s", c.CallbackVersionID, v.Encode())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(e.srv.URL + callback)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 || string(raw) != "success" {
				errs <- fmt.Errorf("callback HTTP %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var balance string
	var entries int
	e.store.Pool.QueryRow(context.Background(), `SELECT balance::text FROM users WHERE id=2`).Scan(&balance)
	e.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM balance_adjustments WHERE topup_order_id=$1 AND operator_id IS NULL`, o.ID).Scan(&entries)
	if !decimal.RequireFromString(balance).Equal(decimal.NewFromInt(7)) || entries != 1 {
		t.Fatal("duplicate credit", balance, entries)
	}
	var logs struct {
		Total   int64            `json:"total"`
		Summary store.LogSummary `json:"summary"`
	}
	if code, body := bob.do("GET", "/api/me/logs?type=topup", nil, &logs); code != 200 || logs.Total != 1 || logs.Summary.TopupTotal.String() != "2" {
		t.Fatalf("重复通知产生重复日志: %d %s", code, body)
	}
	if code, _ := bob.do("GET", fmt.Sprintf("/api/payments/epay/%d/test/notify?%s", c.CallbackVersionID, v.Encode()), nil, nil); code != 404 {
		t.Fatal("wrong environment accepted")
	}
}

func TestEPayRejectsTestEnvironment(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	configurePayment(t, e, admin, "epay")
	if code, body := admin.do("PUT", "/api/admin/payments/epay", map[string]string{"environment": "test"}, nil); code != 400 || !strings.Contains(body, "EPay 仅支持生产环境") {
		t.Fatal("EPay test environment was not rejected", code, body)
	}
	c, err := e.store.PaymentConfig(context.Background(), "epay")
	if err != nil || c.Environment != "prod" {
		t.Fatal("rejected environment changed saved config", err, c.Environment)
	}
}

func TestPaymentTimeoutIsPendingAndNoDuplicateSession(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	configurePayment(t, e, admin, "stripe")
	var attempts atomic.Int32
	e.api.Payments.HTTP.Transport = paymentTransport(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, errors.New("simulated connection reset")
	})
	var o payments.Order
	if code, body := admin.do("POST", "/api/admin/payments/stripe/test-order", map[string]string{"credit_usd": "1", "request_key": "timeout-fixture-0001"}, &o); code != 200 {
		t.Fatal(code, body)
	}
	if code, _ := admin.do("POST", "/api/me/topups/"+o.ID+"/checkout", nil, nil); code != 502 {
		t.Fatal("expected payment timeout")
	}
	if code, _ := admin.do("POST", "/api/me/topups/"+o.ID+"/checkout", nil, nil); code != 409 {
		t.Fatal("duplicate session attempt")
	}
	got, err := e.store.Topup(context.Background(), o.ID)
	if err != nil || got.Status != "pending" || attempts.Load() != 1 {
		t.Fatal("timeout marked as unpaid", err, attempts.Load())
	}
	if code, body := admin.do("POST", "/api/me/topups/"+o.ID+"/refresh", nil, nil); code != 200 || !strings.Contains(body, "待") {
		t.Fatal("missing awaiting notification feedback", code, body)
	}
}

func TestPaymentCallbackBeforeCheckoutResponseAndQueryRecovery(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	c := configurePayment(t, e, admin, "stripe")
	var o payments.Order
	admin.do("POST", "/api/admin/payments/stripe/test-order", map[string]string{"credit_usd": "1", "request_key": "early-fixture-0001"}, &o)
	var calls atomic.Int32
	e.api.Payments.HTTP.Transport = paymentTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method == "POST" {
			err := e.store.ApplyTopupEvent(context.Background(), "stripe", c.VersionID, payments.Event{OrderID: o.ID, Status: "success", TransactionID: "cs_early", Environment: "prod", Currency: "USD", Amount: decimal.NewFromInt(1), Total: decimal.NewFromInt(1)})
			if err != nil {
				t.Error(err)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"id":"cs_early","url":"https://checkout.stripe.com/session","livemode":true,"client_reference_id":%q,"currency":"usd","payment_status":"paid","amount_subtotal":100,"amount_total":100}`, o.ID)))}, nil
	})
	if code, body := admin.do("POST", "/api/me/topups/"+o.ID+"/checkout", nil, nil); code != 200 {
		t.Fatal(code, body)
	}
	got, _ := e.store.Topup(context.Background(), o.ID)
	if got.Status != "success" || got.CheckoutID != "cs_early" {
		t.Fatal("early notification overwritten")
	}
	// 另一笔订单模拟漏通知，由查单补齐，第二次刷新不再重复查上游。
	admin.do("POST", "/api/admin/payments/stripe/test-order", map[string]string{"credit_usd": "1", "request_key": "query-fixture-0002"}, &o)
	e.store.SaveTopupCheckout(context.Background(), o.ID, &payments.Action{CheckoutID: "cs_query"})
	e.api.Payments.HTTP.Transport = paymentTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"id":"cs_query","livemode":true,"client_reference_id":%q,"currency":"usd","payment_status":"paid","amount_subtotal":100,"amount_total":100}`, o.ID)))}, nil
	})
	for i := 0; i < 2; i++ {
		if code, body := admin.do("POST", "/api/me/topups/"+o.ID+"/refresh", nil, nil); code != 200 {
			t.Fatal(code, body)
		}
	}
	got, _ = e.store.Topup(context.Background(), o.ID)
	if got.Status != "success" || calls.Load() != 2 {
		t.Fatal("query recovery failed", calls.Load())
	}
}
