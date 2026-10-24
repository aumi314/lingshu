package payments

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81/webhook"
	"github.com/waffo-com/waffo-go/utils"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func configAndOrder(p string) (Config, Order) {
	c := DefaultConfig(p, decimal.RequireFromString("6.7"))
	c.VersionID = 1
	for _, k := range SecretKeys(p) {
		c.Secrets[k] = "fixture-secret"
	}
	for _, k := range ValueKeys(p) {
		c.Values[k] = "fixture-value"
	}
	c.Values["gateway"] = "https://pay.example"
	c.Values["merchant_id"] = "100"
	o := Order{ID: "order123", UserID: 1, VersionID: 1, Provider: p, Environment: "prod", Currency: c.Currency, Amount: decimal.NewFromInt(10), CreditUSD: decimal.NewFromInt(10), Method: "alipay"}
	return c, o
}

func TestQuotesAndPaymentValidation(t *testing.T) {
	for _, p := range Providers {
		t.Run(p, func(t *testing.T) {
			c, o := configAndOrder(p)
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			if _, err := Quote(c, decimal.RequireFromString("1.001"), "alipay"); err == nil {
				t.Fatal("fractional cent accepted")
			}
			if _, err := Quote(c, decimal.NewFromInt(1001), "alipay"); err == nil {
				t.Fatal("out of bounds accepted")
			}
			if _, err := Quote(c, decimal.NewFromInt(2), "alipay"); err != nil {
				t.Fatal(err)
			}
			e := Event{OrderID: o.ID, Status: "success", TransactionID: "receipt", Environment: "prod", Currency: o.Currency, Amount: o.Amount, Tax: decimal.NewFromInt(1), Total: decimal.NewFromInt(11), BuyerIdentity: BuyerID(o)}
			if err := ValidateEvent(o, e); err != nil {
				t.Fatal(err)
			}
			e.Currency = "EUR"
			if ValidateEvent(o, e) == nil {
				t.Fatal("wrong currency accepted")
			}
			e.Currency = o.Currency
			e.Amount = decimal.NewFromInt(11)
			if ValidateEvent(o, e) == nil {
				t.Fatal("tax credited as principal")
			}
		})
	}
	epayConfig, _ := configAndOrder("epay")
	epayConfig.Environment = "test"
	if err := epayConfig.Validate(); err == nil {
		t.Fatal("EPay test environment accepted")
	}
	stripeConfig, _ := configAndOrder("stripe")
	stripeConfig.Environment = "test"
	if err := stripeConfig.Validate(); err != nil {
		t.Fatal("Stripe test environment rejected")
	}
	c, _ := configAndOrder("creem")
	c.UnitPrice = decimal.RequireFromString("0.5")
	if _, err := Quote(c, decimal.NewFromInt(1), ""); err == nil {
		t.Fatal("Creem minimum not enforced")
	}
	for _, u := range []string{"http://evil.example", "https://u:p@example.com", "https://example.com?a=b", "https://example.com/#x"} {
		if ValidatePublicURL(u) == nil {
			t.Fatalf("invalid URL accepted %s", u)
		}
	}
}

func TestEPayCheckoutNotifyAndQuery(t *testing.T) {
	c, o := configAndOrder("epay")
	g := NewGateway()
	ctx := context.Background()
	a, err := g.Checkout(ctx, c, o, "https://gateway.example")
	if err != nil || a.Type != "form" || a.Fields["money"] != "10.00" || a.Fields["out_trade_no"] != o.ID {
		t.Fatal("checkout", err)
	}
	if strings.Contains(a.Fields["notify_url"], "pay.example") || !strings.Contains(a.Fields["notify_url"], "/epay/1/prod/notify") {
		t.Fatal("wrong callback")
	}
	params := epay.GenerateParams(map[string]string{"pid": "100", "trade_no": "receipt", "out_trade_no": o.ID, "money": "10.00", "trade_status": "TRADE_SUCCESS", "type": "alipay"}, c.Secrets["key"])
	v := url.Values{}
	for k, x := range params {
		v.Set(k, x)
	}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "https://gateway.example/?"+v.Encode(), nil)
		if method == "POST" {
			r = httptest.NewRequest(method, "https://gateway.example", strings.NewReader(v.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		e, err := g.Verify(ctx, c, r)
		if err != nil || ValidateEvent(o, *e) != nil {
			t.Fatal(method, err)
		}
	}
	v.Set("money", "11")
	if _, err := g.Verify(ctx, c, httptest.NewRequest("GET", "/?"+v.Encode(), nil)); err == nil {
		t.Fatal("tampered payment accepted")
	}
	v.Set("money", "10.00")
	v.Add("money", "10.00")
	if _, err := g.Verify(ctx, c, httptest.NewRequest("GET", "/?"+v.Encode(), nil)); err == nil {
		t.Fatal("ambiguous fields accepted")
	}
	g.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("key") != c.Secrets["key"] {
			t.Error("missing query authentication")
		}
		return response(`{"code":1,"pid":"100","out_trade_no":"order123","status":1,"trade_no":"receipt","money":"10.00"}`), nil
	})
	e, err := g.Query(ctx, c, o)
	if err != nil || ValidateEvent(o, *e) != nil {
		t.Fatal("query", err)
	}
	g.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failure with key=%s", c.Secrets["key"])
	})
	_, err = g.Query(ctx, c, o)
	if err == nil || strings.Contains(err.Error(), c.Secrets["key"]) {
		t.Fatal("network error leaked credentials")
	}
}

func TestStripeCheckoutAsyncNotificationsAndTax(t *testing.T) {
	c, o := configAndOrder("stripe")
	g := NewGateway()
	ctx := context.Background()
	g.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			r.ParseForm()
			if r.Form.Get("line_items[0][price_data][unit_amount]") != "1000" || r.Header.Get("Idempotency-Key") != o.ID || r.Form.Get("client_reference_id") != o.ID {
				t.Error("incorrect Stripe quote or identity")
			}
		}
		return response(`{"id":"cs_receipt","url":"https://checkout.stripe.com/pay/session","livemode":true,"client_reference_id":"order123","currency":"usd","amount_subtotal":1000,"amount_total":1100,"total_details":{"amount_tax":100},"payment_status":"paid"}`), nil
	})
	a, err := g.Checkout(ctx, c, o, "https://gateway.example")
	if err != nil || a.CheckoutID != "cs_receipt" {
		t.Fatal(err)
	}
	o.CheckoutID = a.CheckoutID
	e, err := g.Query(ctx, c, o)
	if err != nil || ValidateEvent(o, *e) != nil {
		t.Fatal("query", err)
	}
	for _, tc := range []struct {
		typ, status, want string
		live              bool
	}{{"checkout.session.completed", "unpaid", "pending", true}, {"checkout.session.async_payment_succeeded", "paid", "success", true}, {"checkout.session.expired", "unpaid", "expired", true}, {"checkout.session.async_payment_failed", "unpaid", "failed", true}, {"checkout.session.completed", "paid", "", false}} {
		payload := []byte(fmt.Sprintf(`{"id":"evt_1","type":%q,"livemode":%t,"data":{"object":{"id":"cs_receipt","client_reference_id":"order123","currency":"usd","amount_subtotal":1000,"amount_total":1100,"total_details":{"amount_tax":100},"payment_status":%q}}}`, tc.typ, tc.live, tc.status))
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: c.Secrets["webhook_secret"]})
		r := httptest.NewRequest("POST", "/", strings.NewReader(string(payload)))
		r.Header.Set("Stripe-Signature", signed.Header)
		e, err := g.Verify(ctx, c, r)
		if tc.want == "" {
			if err == nil {
				t.Fatal("sandbox Stripe event accepted")
			}
			continue
		}
		if err != nil || e.Status != tc.want {
			t.Fatalf("%s: %+v %v", tc.typ, e, err)
		}
		if e.Status == "success" && ValidateEvent(o, *e) != nil {
			t.Fatal("tax mishandled")
		}
		r = httptest.NewRequest("POST", "/", strings.NewReader(string(payload)+" "))
		r.Header.Set("Stripe-Signature", signed.Header)
		if _, err := g.Verify(ctx, c, r); err == nil {
			t.Fatal("tampered Stripe notification accepted")
		}
	}
}

func TestCreemDynamicPriceNotificationsAndQuery(t *testing.T) {
	c, o := configAndOrder("creem")
	g := NewGateway()
	ctx := context.Background()
	checkout := `{"id":"ch_1","mode":"prod","request_id":"order123","status":"completed","order":{"id":"ord_1","status":"paid","currency":"USD","amount":1100,"amount_paid":1100,"sub_total":1000,"tax_amount":100}}`
	g.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			var v map[string]any
			json.NewDecoder(r.Body).Decode(&v)
			if v["custom_price"] != float64(1000) || v["request_id"] != o.ID {
				t.Error("Creem custom price missing")
			}
			return response(`{"id":"ch_1","mode":"prod","checkout_url":"https://checkout.creem.io/session"}`), nil
		}
		return response(checkout), nil
	})
	a, err := g.Checkout(ctx, c, o, "https://gateway.example")
	if err != nil {
		t.Fatal(err)
	}
	o.CheckoutID = a.CheckoutID
	e, err := g.Query(ctx, c, o)
	if err != nil || ValidateEvent(o, *e) != nil {
		t.Fatal("query", err)
	}
	verify := func(body string, config Config) (*Event, error) {
		mac := hmac.New(sha256.New, []byte(c.Secrets["webhook_secret"]))
		mac.Write([]byte(body))
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("creem-signature", hex.EncodeToString(mac.Sum(nil)))
		return g.Verify(ctx, config, r)
	}
	body := `{"eventType":"checkout.completed","object":` + checkout + `}`
	e, err = verify(body, c)
	if err != nil || ValidateEvent(o, *e) != nil {
		t.Fatal("notify", err)
	}
	c.Environment = "test"
	if _, err := verify(body, c); err == nil {
		t.Fatal("prod event accepted in test")
	}
	c.Environment = "prod"
	c.Secrets["webhook_secret"] = "wrong"
	if _, err := g.Verify(ctx, c, httptest.NewRequest("POST", "/", strings.NewReader(body))); err == nil {
		t.Fatal("unsigned event accepted")
	}
}

func rsaFixture(t *testing.T) (*rsa.PrivateKey, string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := x509.MarshalPKCS8PrivateKey(key)
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return key, base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}))
}

func TestWaffoCheckoutSignedNotificationAndResponse(t *testing.T) {
	c, o := configAndOrder("waffo")
	_, priv, pub, _ := rsaFixture(t)
	c.Secrets["private_key"] = priv
	c.Values["public_key"] = pub
	g := NewGateway()
	ctx := context.Background()
	g.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if !utils.Verify(string(raw), r.Header.Get("X-SIGNATURE"), pub) {
			t.Error("request signature invalid")
		}
		body := `{"code":"0","data":{"acquiringOrderId":"receipt","merchantOrderId":"order123","orderStatus":"PAY_SUCCESS","orderCurrency":"USD","orderAmount":"10.00","orderAction":"{\"actionType\":\"WEB\",\"webUrl\":\"https://checkout.waffo.com/session\"}"}}`
		res := response(body)
		res.Header.Set("X-SIGNATURE", utils.MustSign(body, priv))
		return res, nil
	})
	a, err := g.Checkout(ctx, c, o, "https://gateway.example")
	if err != nil || a.CheckoutID != "receipt" {
		t.Fatal("checkout", err)
	}
	e, err := g.Query(ctx, c, o)
	if err != nil || ValidateEvent(o, *e) != nil {
		t.Fatal("query", err)
	}
	body := `{"eventType":"PAYMENT_NOTIFICATION","result":{"acquiringOrderId":"receipt","merchantOrderId":"order123","orderStatus":"PAY_SUCCESS","orderCurrency":"USD","orderAmount":"10.00","merchantInfo":{"merchantId":"100"}}}`
	sig := utils.MustSign(body, priv)
	e, err = g.waffoVerify(c, []byte(body), sig)
	if err != nil || ValidateEvent(o, *e) != nil {
		t.Fatal("notification", err)
	}
	if _, err := g.waffoVerify(c, []byte(body+" "), sig); err == nil {
		t.Fatal("tampered notification")
	}
	w := httptest.NewRecorder()
	Acknowledge(c, w, true)
	if !utils.Verify(w.Body.String(), w.Header().Get("X-SIGNATURE"), pub) {
		t.Fatal("ack unsigned")
	}
}

func TestPancakePriceSnapshotIdentityAndSignature(t *testing.T) {
	c, o := configAndOrder("waffo_pancake")
	key, _, pub, privPEM := rsaFixture(t)
	c.Secrets["private_key"] = privPEM
	c.Values = map[string]string{"merchant_id": "MER_AbCdEfGhIjKlMnOpQrStUv", "store_id": "STO_AbCdEfGhIjKlMnOpQrStUv", "product_id": "PROD_AbCdEfGhIjKlMnOpQrStUv"}
	g := NewGateway()
	g.PancakeVerifyOptions = &pancake.VerifyWebhookOptions{PublicKey: pub}
	ctx := context.Background()
	g.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		var v map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&v)
		if strings.Contains(r.URL.Path, "issue-session-token") {
			var identity string
			json.Unmarshal(v["buyerIdentity"], &identity)
			if identity != BuyerID(o) {
				t.Error("buyer identity missing")
			}
			return response(`{"data":{"token":"fixture-token"}}`), nil
		}
		if r.Header.Get("X-Idempotency-Key") == "" || string(v["orderMerchantExternalId"]) != `"order123"` || !strings.Contains(string(v["priceSnapshot"]), `"10.00"`) {
			t.Error("price snapshot or external identity missing")
		}
		return response(`{"data":{"sessionId":"session","checkoutUrl":"https://checkout.pancake.com/session"}}`), nil
	})
	a, err := g.Checkout(ctx, c, o, "https://gateway.example")
	if err != nil || a.CheckoutID != "session" || !strings.Contains(a.URL, "token=") {
		t.Fatal("checkout", err)
	}
	body := fmt.Sprintf(`{"id":"evt_1","eventType":"order.completed","eventId":"payment1","mode":"prod","storeId":%q,"data":{"orderId":"receipt","orderMerchantExternalId":"order123","merchantProvidedBuyerIdentity":%q,"currency":"USD","chargedAmount":"11.00","listPrice":{"total":"11.00","subtotal":"10.00","taxAmount":"1.00"}}}`, c.Values["store_id"], BuyerID(o))
	ts := fmt.Sprint(time.Now().UnixMilli())
	hash := sha256.Sum256([]byte(ts + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	header := "t=" + ts + ",v1=" + base64.StdEncoding.EncodeToString(sig)
	e, err := g.pancakeVerify(c, []byte(body), header)
	if err != nil || ValidateEvent(o, *e) != nil {
		t.Fatal("notification", err)
	}
	e.BuyerIdentity = "another-user"
	if ValidateEvent(o, *e) == nil {
		t.Fatal("wrong identity accepted")
	}
	c.Environment = "test"
	if _, err := g.pancakeVerify(c, []byte(body), header); err == nil {
		t.Fatal("production webhook accepted in sandbox")
	}
	c.Environment = "prod"
	if _, err := g.pancakeVerify(c, []byte(body+" "), header); err == nil {
		t.Fatal("bad signature accepted")
	}
	if _, err := g.Query(ctx, c, o); err != ErrUnsupportedQuery {
		t.Fatal("unsupported query not explicit")
	}
}

func TestProviderHTTPFailuresDoNotExposeCredentials(t *testing.T) {
	for _, p := range []string{"stripe", "creem", "waffo", "waffo_pancake"} {
		t.Run(p, func(t *testing.T) {
			c, o := configAndOrder(p)
			if p == "waffo" || p == "waffo_pancake" {
				_, priv, pub, pem := rsaFixture(t)
				c.Secrets["private_key"] = priv
				c.Values["public_key"] = pub
				if p == "waffo_pancake" {
					c.Secrets["private_key"] = pem
					c.Values["merchant_id"] = "MER_AbCdEfGhIjKlMnOpQrStUv"
					c.Values["product_id"] = "PROD_AbCdEfGhIjKlMnOpQrStUv"
				}
			}
			g := NewGateway()
			g.HTTP.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				r := response(`{"error":"fixture-secret"}`)
				r.StatusCode = 400
				return r, nil
			})
			_, err := g.Checkout(context.Background(), c, o, "https://gateway.example")
			if err == nil || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("provider failure handling", err)
			}
		})
	}
}
