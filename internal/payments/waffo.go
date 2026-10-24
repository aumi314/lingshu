package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/shopspring/decimal"
	waffo "github.com/waffo-com/waffo-go"
	wconfig "github.com/waffo-com/waffo-go/config"
	"github.com/waffo-com/waffo-go/core"
	wnet "github.com/waffo-com/waffo-go/net"
	"github.com/waffo-com/waffo-go/types/merchant"
	worder "github.com/waffo-com/waffo-go/types/order"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

type waffoTransport struct{ client *http.Client }

func (t waffoTransport) Send(ctx context.Context, r *wnet.HttpRequest) (*wnet.HttpResponse, error) {
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, errors.New("Waffo 网络请求失败")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	return wnet.NewHttpResponse(resp.StatusCode, headers, body), nil
}

func (g *Gateway) waffo(c Config) (*waffo.Waffo, error) {
	env := wconfig.Production
	if c.Environment == "test" {
		env = wconfig.Sandbox
	}
	cfg, err := wconfig.NewConfigBuilder().APIKey(c.Secrets["api_key"]).PrivateKey(c.Secrets["private_key"]).WaffoPublicKey(c.Values["public_key"]).MerchantID(c.Values["merchant_id"]).Environment(env).CustomTransport(waffoTransport{g.HTTP}).Build()
	if err != nil {
		return nil, errors.New("Waffo 凭据或证书格式不正确")
	}
	return waffo.New(cfg), nil
}

func (g *Gateway) waffoCheckout(ctx context.Context, c Config, o Order, base string) (*Action, error) {
	client, err := g.waffo(c)
	if err != nil {
		return nil, err
	}
	res, err := client.Order().Create(ctx, &worder.CreateOrderParams{PaymentRequestID: o.ID, MerchantOrderID: o.ID, OrderCurrency: o.Currency, OrderAmount: o.Amount.StringFixed(2), OrderDescription: "Lingshu API wallet top-up", NotifyURL: NotifyURL(base, c), SuccessRedirectURL: ReturnURL(base, o.ID), FailedRedirectURL: ReturnURL(base, o.ID), CancelRedirectURL: ReturnURL(base, o.ID), UserInfo: &worder.UserInfo{UserID: BuyerID(o), UserTerminal: "WEB"}, PaymentInfo: &worder.PaymentInfo{ProductName: "ONE_TIME_PAYMENT"}, GoodsInfo: &worder.GoodsInfo{GoodsURL: ReturnURL(base, o.ID)}}, nil)
	if err != nil || res == nil || !res.IsSuccess() || res.GetData() == nil {
		return nil, errors.New("Waffo 创建订单失败，请核对配置并刷新订单")
	}
	d := res.GetData()
	if d.FetchRedirectURL() == "" {
		return nil, errors.New("Waffo 未返回收银台地址，请刷新订单")
	}
	return &Action{Type: "redirect", URL: d.FetchRedirectURL(), CheckoutID: d.AcquiringOrderID}, nil
}

func waffoEvent(id, transaction, status, currency, amount string, c Config) (*Event, error) {
	e := &Event{OrderID: id, TransactionID: transaction, Status: "pending", Environment: c.Environment, Currency: currency}
	if status == core.OrderStatusOrderClose {
		e.Status = "failed"
	}
	if status == core.OrderStatusPaySuccess {
		d, err := decimal.NewFromString(amount)
		if err != nil {
			return nil, ErrInvalidEvent
		}
		e.Status = "success"
		e.Amount = d
		e.Total = d
	}
	return e, nil
}

func (g *Gateway) waffoVerify(c Config, body []byte, signature string) (*Event, error) {
	client, err := g.waffo(c)
	if err != nil {
		return nil, err
	}
	if !client.Webhook().VerifySignature(string(body), signature) {
		return nil, ErrInvalidEvent
	}
	var e core.PaymentNotification
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, err
	}
	if e.EventType != core.EventPayment {
		return nil, nil
	}
	if e.Result == nil {
		return nil, ErrInvalidEvent
	}
	r := e.Result
	if merchant, ok := r.MerchantInfo["merchantId"].(string); ok && merchant != c.Values["merchant_id"] {
		return nil, ErrInvalidEvent
	}
	return waffoEvent(r.MerchantOrderID, r.AcquiringOrderID, r.OrderStatus, r.OrderCurrency, r.OrderAmount, c)
}

func (g *Gateway) waffoQuery(ctx context.Context, c Config, o Order) (*Event, error) {
	client, err := g.waffo(c)
	if err != nil {
		return nil, err
	}
	res, err := client.Order().Inquiry(ctx, &worder.InquiryOrderParams{PaymentRequestID: o.ID}, nil)
	if err != nil || res == nil || !res.IsSuccess() || res.GetData() == nil {
		return nil, errors.New("Waffo 查单失败，请稍后刷新")
	}
	r := res.GetData()
	return waffoEvent(r.MerchantOrderID, r.AcquiringOrderID, r.OrderStatus, r.OrderCurrency, r.OrderAmount, c)
}

func (g *Gateway) waffoProbe(ctx context.Context, c Config) error {
	client, err := g.waffo(c)
	if err != nil {
		return err
	}
	res, err := client.MerchantConfig().Inquiry(ctx, &merchant.InquiryMerchantConfigParams{}, nil)
	if err != nil || res == nil || !res.IsSuccess() {
		return errors.New("Waffo 商户验证失败，请核对凭据和环境")
	}
	return nil
}

// Waffo 的通知应答也需要 SDK 签名；其他服务按各自规范返回成功标记。
func Acknowledge(c Config, w http.ResponseWriter, success bool) {
	if c.Provider == "epay" {
		if success {
			io.WriteString(w, "success")
		} else {
			w.WriteHeader(400)
			io.WriteString(w, "fail")
		}
		return
	}
	if c.Provider == "waffo" {
		client, err := NewGateway().waffo(c)
		if err == nil {
			body, sig := client.Webhook().BuildSuccessResponse()
			if !success {
				body, sig = client.Webhook().BuildFailedResponse("retry")
			}
			w.Header().Set("X-SIGNATURE", sig)
			w.Header().Set("Content-Type", "application/json")
			if !success {
				w.WriteHeader(400)
			}
			io.WriteString(w, body)
			return
		}
	}
	if !success {
		w.WriteHeader(400)
		io.WriteString(w, "retry")
		return
	}
	io.WriteString(w, "OK")
}

func (g *Gateway) pancake(c Config) (*pancake.Client, error) {
	client := *g.HTTP
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = pancakeTransport{base}
	return pancake.New(pancake.Config{MerchantID: c.Values["merchant_id"], PrivateKey: c.Secrets["private_key"], Environment: pancake.Environment(c.Environment), HTTPClient: &client})
}

// SDK 的部分入口只检查 JSON errors 字段；HTTP 失败同样必须中止。
type pancakeTransport struct{ base http.RoundTripper }

func (t pancakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err == nil && resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, errors.New("Pancake 请求失败，请核对凭据或稍后重试")
	}
	return resp, err
}

func (g *Gateway) pancakeCheckout(ctx context.Context, c Config, o Order) (*Action, error) {
	client, err := g.pancake(c)
	if err != nil {
		return nil, errors.New("Pancake 凭据格式不正确")
	}
	expiry := 45 * 60
	res, err := client.Checkout.Authenticated.Create(ctx, pancake.AuthenticatedCheckoutParams{CreateCheckoutSessionParams: pancake.CreateCheckoutSessionParams{ProductID: c.Values["product_id"], Currency: o.Currency, PriceSnapshot: &pancake.PriceSnapshot{Amount: o.Amount.StringFixed(2), TaxCategory: pancake.TaxCategory("saas")}, OrderMerchantExternalID: pancake.Ptr(o.ID), ExpiresInSeconds: &expiry}, BuyerIdentity: BuyerID(o)}, pancake.WithIdempotencyKey("lingshu-"+o.ID))
	if err != nil || res == nil || res.SessionID == "" || res.Token == "" || res.CheckoutURL == "" {
		return nil, errors.New("Pancake 创建支付会话失败，请核对商品及环境并刷新订单")
	}
	return &Action{Type: "redirect", URL: res.CheckoutURL, CheckoutID: res.SessionID}, nil
}

func (g *Gateway) pancakeVerify(c Config, body []byte, signature string) (*Event, error) {
	e, err := pancake.VerifyWebhookTyped[pancake.WebhookEventData](string(body), signature, g.PancakeVerifyOptions)
	if err != nil || string(e.Mode) != c.Environment || e.StoreID != c.Values["store_id"] {
		return nil, ErrInvalidEvent
	}
	if e.EventType != "order.completed" {
		return nil, nil
	}
	d := e.Data
	if d.OrderMerchantExternalID == nil || d.MerchantProvidedBuyerIdentity == nil {
		return nil, ErrInvalidEvent
	}
	// 身份包括配置版本和用户编号，必须由本地订单核对；不依赖可修改的买家邮箱。
	totalRaw := d.Amount
	if d.ChargedAmount != nil {
		totalRaw = *d.ChargedAmount
	}
	total, err := decimal.NewFromString(totalRaw)
	if err != nil {
		return nil, ErrInvalidEvent
	}
	taxRaw := d.TaxAmount
	if d.ListPrice != nil {
		taxRaw = d.ListPrice.TaxAmount
	}
	if taxRaw == "" {
		taxRaw = "0"
	}
	tax, err := decimal.NewFromString(taxRaw)
	if err != nil {
		return nil, ErrInvalidEvent
	}
	amount := total.Sub(tax)
	if d.ListPrice != nil && d.ListPrice.Subtotal != nil {
		amount, err = decimal.NewFromString(*d.ListPrice.Subtotal)
	} else if d.Subtotal != nil {
		amount, err = decimal.NewFromString(*d.Subtotal)
	}
	if err != nil {
		return nil, ErrInvalidEvent
	}
	return &Event{OrderID: *d.OrderMerchantExternalID, TransactionID: d.OrderID, Status: "success", Environment: string(e.Mode), Currency: d.Currency, Amount: amount, Tax: tax, Total: total, BuyerIdentity: *d.MerchantProvidedBuyerIdentity}, nil
}

type CatalogProduct struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}
type CatalogStore struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	ProdEnabled bool             `json:"prodEnabled"`
	Products    []CatalogProduct `json:"onetimeProducts"`
}
type Catalog struct {
	Stores []CatalogStore `json:"stores"`
}

func (g *Gateway) PancakeCatalog(ctx context.Context, c Config) (*Catalog, error) {
	client, err := g.pancake(c)
	if err != nil {
		return nil, errors.New("Pancake 凭据格式不正确")
	}
	res, err := pancake.GraphQLQuery[Catalog](ctx, client, pancake.GraphQLParams{Query: `query { stores(limit:100) { id name prodEnabled onetimeProducts { id name status } } }`})
	if err != nil || res == nil || len(res.Errors) > 0 {
		return nil, errors.New("Pancake 拉取店铺商品失败，请核对商户凭据")
	}
	return &res.Data, nil
}

type CreatedProduct struct {
	StoreID   string `json:"store_id"`
	ProductID string `json:"product_id"`
}

func (g *Gateway) CreatePancakeProduct(ctx context.Context, c Config, base string) (CreatedProduct, error) {
	out := CreatedProduct{StoreID: c.Values["store_id"]}
	client, err := g.pancake(c)
	if err != nil {
		return out, errors.New("Pancake 凭据格式不正确")
	}
	if out.StoreID == "" {
		res, err := client.Stores.Create(ctx, pancake.CreateStoreParams{Name: "Lingshu"})
		if err != nil {
			return out, errors.New("创建店铺失败，请刷新目录核对是否已创建")
		}
		out.StoreID = res.Store.ID
	}
	res, err := client.OnetimeProducts.Create(ctx, pancake.CreateOnetimeProductParams{StoreID: out.StoreID, Name: "Lingshu API wallet top-up", Prices: pancake.Prices{"USD": {Amount: "1.00", TaxCategory: pancake.TaxCategory("saas")}}, SuccessURL: pancake.Ptr(ReturnURL(base, ""))})
	if err != nil {
		return out, errors.New("店铺已保留，创建商品失败；请使用店铺 ID 重试")
	}
	out.ProductID = res.Product.ID
	if _, err := client.OnetimeProducts.Publish(ctx, pancake.PublishOnetimeProductParams{ID: out.ProductID}); err != nil {
		return out, errors.New("商品已创建，发布失败；请在 Pancake 后台发布该商品")
	}
	return out, nil
}
