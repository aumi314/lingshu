package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/webhook"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

type Gateway struct {
	HTTP                 *http.Client
	PancakeVerifyOptions *pancake.VerifyWebhookOptions
}

func NewGateway() *Gateway {
	return &Gateway{HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (g *Gateway) request(ctx context.Context, method, rawURL string, body any, headers map[string]string, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return errors.New("支付服务地址无效")
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := g.HTTP.Do(r)
	if err != nil {
		return errors.New("支付服务暂未响应，请刷新订单核对付款状态，不要重复付款")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("支付服务返回 HTTP %d，请核对配置或稍后重试", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return errors.New("支付服务返回内容无法解析")
	}
	return nil
}

func (g *Gateway) epay(c Config) (*epay.Client, error) {
	return epay.NewClient(&epay.Config{PartnerID: c.Values["merchant_id"], Key: c.Secrets["key"]}, c.Values["gateway"])
}
func (g *Gateway) stripe(c Config) session.Client {
	return session.Client{B: stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{HTTPClient: g.HTTP, MaxNetworkRetries: stripe.Int64(0), LeveledLogger: &stripe.LeveledLogger{Level: stripe.LevelNull}}), Key: c.Secrets["api_key"]}
}
func creemBase(c Config) string {
	if c.Environment == "test" {
		return "https://test-api.creem.io/v1"
	}
	return "https://api.creem.io/v1"
}

func (g *Gateway) Checkout(ctx context.Context, c Config, o Order, base string) (*Action, error) {
	switch c.Provider {
	case "epay":
		client, err := g.epay(c)
		if err != nil {
			return nil, err
		}
		notify, _ := url.Parse(NotifyURL(base, c))
		ret, _ := url.Parse(ReturnURL(base, o.ID))
		u, fields, err := client.Purchase(&epay.PurchaseArgs{Type: o.Method, ServiceTradeNo: o.ID, Name: "Lingshu API 钱包充值", Money: o.Amount.StringFixed(2), Device: epay.PC, NotifyUrl: notify, ReturnUrl: ret})
		if err != nil {
			return nil, err
		}
		return &Action{Type: "form", URL: u, Fields: fields}, nil
	case "stripe":
		p := &stripe.CheckoutSessionParams{Params: stripe.Params{Context: ctx}, ClientReferenceID: stripe.String(o.ID), Mode: stripe.String("payment"), SuccessURL: stripe.String(ReturnURL(base, o.ID)), CancelURL: stripe.String(ReturnURL(base, o.ID)), LineItems: []*stripe.CheckoutSessionLineItemParams{{Quantity: stripe.Int64(1), PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{Currency: stripe.String(strings.ToLower(o.Currency)), UnitAmount: stripe.Int64(o.Amount.Shift(2).IntPart()), ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{Name: stripe.String("Lingshu API wallet top-up")}}}}}
		p.SetIdempotencyKey(o.ID)
		client := g.stripe(c)
		result, err := client.New(p)
		if err != nil {
			return nil, errors.New("Stripe 创建支付会话失败，请核对凭据并刷新订单")
		}
		if (c.Environment == "prod") != result.Livemode || result.URL == "" {
			return nil, ErrInvalidEvent
		}
		return &Action{Type: "redirect", URL: result.URL, CheckoutID: result.ID}, nil
	case "creem":
		var result struct {
			ID   string `json:"id"`
			URL  string `json:"checkout_url"`
			Mode string `json:"mode"`
		}
		err := g.request(ctx, "POST", creemBase(c)+"/checkouts", map[string]any{"product_id": c.Values["product_id"], "request_id": o.ID, "units": 1, "custom_price": o.Amount.Shift(2).IntPart(), "success_url": ReturnURL(base, o.ID), "metadata": map[string]string{"lingshu_order": o.ID}}, map[string]string{"x-api-key": c.Secrets["api_key"]}, &result)
		if err != nil {
			return nil, err
		}
		if result.ID == "" || result.URL == "" || (result.Mode != "" && result.Mode != c.Environment) {
			return nil, ErrInvalidEvent
		}
		return &Action{Type: "redirect", URL: result.URL, CheckoutID: result.ID}, nil
	case "waffo":
		return g.waffoCheckout(ctx, c, o, base)
	case "waffo_pancake":
		return g.pancakeCheckout(ctx, c, o)
	}
	return nil, errors.New("不支持的支付服务")
}

func (g *Gateway) Verify(ctx context.Context, c Config, r *http.Request) (*Event, error) {
	if c.Provider == "epay" {
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		values := r.URL.Query()
		if r.Method == "POST" {
			values = r.PostForm
		}
		params := map[string]string{}
		for k, v := range values {
			if len(v) != 1 {
				return nil, ErrInvalidEvent
			}
			params[k] = v[0]
		}
		client, err := g.epay(c)
		if err != nil {
			return nil, err
		}
		res, err := client.Verify(params)
		if err != nil || !res.VerifyStatus || params["pid"] != c.Values["merchant_id"] {
			return nil, ErrInvalidEvent
		}
		e := &Event{OrderID: res.ServiceTradeNo, Environment: c.Environment, Status: "pending"}
		if res.TradeStatus != epay.StatusTradeSuccess {
			return e, nil
		}
		amount, err := decimal.NewFromString(res.Money)
		if err != nil {
			return nil, ErrInvalidEvent
		}
		e.Status = "success"
		e.TransactionID = res.TradeNo
		e.Amount = amount
		e.Total = amount
		e.Currency = "CNY"
		return e, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	switch c.Provider {
	case "stripe":
		e, err := webhook.ConstructEventWithOptions(body, r.Header.Get("Stripe-Signature"), c.Secrets["webhook_secret"], webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
		if err != nil || (c.Environment == "prod") != e.Livemode {
			return nil, ErrInvalidEvent
		}
		switch e.Type {
		case stripe.EventTypeCheckoutSessionCompleted, stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded, stripe.EventTypeCheckoutSessionExpired, stripe.EventTypeCheckoutSessionAsyncPaymentFailed:
		default:
			return nil, nil
		}
		var s stripe.CheckoutSession
		if err := json.Unmarshal(e.Data.Raw, &s); err != nil {
			return nil, err
		}
		event := stripeEvent(&s, c)
		if e.Type == stripe.EventTypeCheckoutSessionExpired {
			event.Status = "expired"
		}
		if e.Type == stripe.EventTypeCheckoutSessionAsyncPaymentFailed {
			event.Status = "failed"
		}
		return event, nil
	case "creem":
		mac := hmac.New(sha256.New, []byte(c.Secrets["webhook_secret"]))
		mac.Write(body)
		sig, err := hex.DecodeString(r.Header.Get("creem-signature"))
		if err != nil || c.Secrets["webhook_secret"] == "" || !hmac.Equal(sig, mac.Sum(nil)) {
			return nil, ErrInvalidEvent
		}
		var envelope struct {
			Type   string        `json:"eventType"`
			Object creemCheckout `json:"object"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, err
		}
		if envelope.Type != "checkout.completed" {
			return nil, nil
		}
		return creemEvent(envelope.Object, c)
	case "waffo":
		return g.waffoVerify(c, body, r.Header.Get("X-SIGNATURE"))
	case "waffo_pancake":
		return g.pancakeVerify(c, body, r.Header.Get("X-Waffo-Signature"))
	}
	return nil, ErrInvalidEvent
}

func stripeEvent(s *stripe.CheckoutSession, c Config) *Event {
	e := &Event{OrderID: s.ClientReferenceID, Status: "pending", Environment: c.Environment, TransactionID: s.ID, Currency: strings.ToUpper(string(s.Currency)), Amount: decimal.NewFromInt(s.AmountSubtotal).Shift(-2), Total: decimal.NewFromInt(s.AmountTotal).Shift(-2)}
	if s.TotalDetails != nil {
		e.Tax = decimal.NewFromInt(s.TotalDetails.AmountTax).Shift(-2)
	}
	if s.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid {
		e.Status = "success"
	}
	if s.Status == stripe.CheckoutSessionStatusExpired {
		e.Status = "expired"
	}
	return e
}

type creemCheckout struct {
	ID        string `json:"id"`
	Mode      string `json:"mode"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Order     struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Currency string `json:"currency"`
		Amount   int64  `json:"amount"`
		Subtotal *int64 `json:"sub_total"`
		Tax      int64  `json:"tax_amount"`
		Paid     *int64 `json:"amount_paid"`
	} `json:"order"`
}

func creemEvent(v creemCheckout, c Config) (*Event, error) {
	if v.Mode != c.Environment {
		return nil, ErrInvalidEvent
	}
	e := &Event{OrderID: v.RequestID, Environment: v.Mode, Status: "pending", TransactionID: v.Order.ID, Currency: strings.ToUpper(v.Order.Currency)}
	if v.Status == "expired" {
		e.Status = "expired"
	}
	if v.Status != "completed" || v.Order.Status != "paid" {
		return e, nil
	}
	e.Status = "success"
	e.Tax = decimal.NewFromInt(v.Order.Tax).Shift(-2)
	total := v.Order.Amount
	if v.Order.Paid != nil {
		total = *v.Order.Paid
	}
	e.Total = decimal.NewFromInt(total).Shift(-2)
	e.Amount = e.Total.Sub(e.Tax)
	if v.Order.Subtotal != nil {
		e.Amount = decimal.NewFromInt(*v.Order.Subtotal).Shift(-2)
	}
	return e, nil
}

func rawString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return string(v)
}

func (g *Gateway) epayCall(ctx context.Context, c Config, act, id string) (map[string]json.RawMessage, error) {
	u, err := url.Parse(strings.TrimRight(c.Values["gateway"], "/") + "/api.php")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("act", act)
	q.Set("pid", c.Values["merchant_id"])
	q.Set("key", c.Secrets["key"])
	if id != "" {
		q.Set("out_trade_no", id)
	}
	u.RawQuery = q.Encode()
	var result map[string]json.RawMessage
	if err := g.request(ctx, "GET", u.String(), nil, nil, &result); err != nil {
		return nil, err
	}
	if rawString(result["code"]) != "1" {
		return nil, errors.New("支付服务拒绝查询，请核对商户配置或等待通知")
	}
	return result, nil
}

func (g *Gateway) Query(ctx context.Context, c Config, o Order) (*Event, error) {
	switch c.Provider {
	case "epay":
		r, err := g.epayCall(ctx, c, "order", o.ID)
		if err != nil {
			return nil, err
		}
		if rawString(r["out_trade_no"]) != o.ID || rawString(r["pid"]) != c.Values["merchant_id"] {
			return nil, ErrInvalidEvent
		}
		e := &Event{OrderID: o.ID, Environment: c.Environment, Status: "pending"}
		if rawString(r["status"]) != "1" {
			return e, nil
		}
		amount, err := decimal.NewFromString(rawString(r["money"]))
		if err != nil {
			return nil, err
		}
		e.Status = "success"
		e.TransactionID = rawString(r["trade_no"])
		e.Currency = "CNY"
		e.Amount = amount
		e.Total = amount
		return e, nil
	case "stripe":
		if o.CheckoutID == "" {
			return nil, ErrUnsupportedQuery
		}
		client := g.stripe(c)
		s, err := client.Get(o.CheckoutID, &stripe.CheckoutSessionParams{Params: stripe.Params{Context: ctx}})
		if err != nil {
			return nil, errors.New("Stripe 查单失败，请稍后刷新")
		}
		if (c.Environment == "prod") != s.Livemode {
			return nil, ErrInvalidEvent
		}
		return stripeEvent(s, c), nil
	case "creem":
		if o.CheckoutID == "" {
			return nil, ErrUnsupportedQuery
		}
		var result creemCheckout
		err := g.request(ctx, "GET", creemBase(c)+"/checkouts?checkout_id="+url.QueryEscape(o.CheckoutID), nil, map[string]string{"x-api-key": c.Secrets["api_key"]}, &result)
		if err != nil {
			return nil, err
		}
		return creemEvent(result, c)
	case "waffo":
		return g.waffoQuery(ctx, c, o)
	case "waffo_pancake":
		return nil, ErrUnsupportedQuery
	}
	return nil, ErrUnsupportedQuery
}

func (g *Gateway) Probe(ctx context.Context, c Config) error {
	switch c.Provider {
	case "epay":
		_, err := g.epayCall(ctx, c, "balance", "")
		return err
	case "stripe":
		var out map[string]any
		return g.request(ctx, "GET", "https://api.stripe.com/v1/account", nil, map[string]string{"Authorization": "Bearer " + c.Secrets["api_key"]}, &out)
	case "creem":
		var out struct {
			Currency    string `json:"currency"`
			BillingType string `json:"billing_type"`
		}
		if err := g.request(ctx, "GET", creemBase(c)+"/products?product_id="+url.QueryEscape(c.Values["product_id"]), nil, map[string]string{"x-api-key": c.Secrets["api_key"]}, &out); err != nil {
			return err
		}
		if !strings.EqualFold(out.Currency, "USD") || out.BillingType != "onetime" {
			return errors.New("请选择 USD 币种的一次性商品")
		}
		return nil
	case "waffo":
		return g.waffoProbe(ctx, c)
	case "waffo_pancake":
		_, err := g.PancakeCatalog(ctx, c)
		return err
	}
	return errors.New("不支持的支付服务")
}
