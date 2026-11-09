package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/payments"
	"github.com/aumi314/lingshu/internal/store"
	"github.com/shopspring/decimal"
)

func (s *Server) registerPayments(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me/payment-methods", s.user(s.paymentMethods))
	mux.HandleFunc("POST /api/me/topups", s.user(s.createTopup))
	mux.HandleFunc("GET /api/me/topups", s.user(s.listTopups))
	mux.HandleFunc("GET /api/me/topups/{order}", s.user(s.getTopup))
	mux.HandleFunc("POST /api/me/topups/{order}/checkout", s.user(s.topupCheckout))
	mux.HandleFunc("POST /api/me/topups/{order}/refresh", s.user(s.refreshTopup))
	mux.HandleFunc("GET /api/admin/payments", s.admin(s.listPaymentConfigs))
	mux.HandleFunc("PUT /api/admin/payments/{provider}", s.admin(s.savePaymentConfig))
	mux.HandleFunc("POST /api/admin/payments/{provider}/probe", s.admin(s.probePayment))
	mux.HandleFunc("POST /api/admin/payments/{provider}/test-order", s.admin(s.createTopup))
	mux.HandleFunc("POST /api/admin/payments/waffo_pancake/catalog", s.admin(s.pancakeCatalog))
	mux.HandleFunc("POST /api/admin/payments/waffo_pancake/create-product", s.admin(s.createPancakeProduct))
	mux.HandleFunc("GET /api/payments/{provider}/{version}/{environment}/notify", s.paymentNotify)
	mux.HandleFunc("POST /api/payments/{provider}/{version}/{environment}/notify", s.paymentNotify)
}

func validProvider(p string) bool {
	for _, v := range payments.Providers {
		if v == p {
			return true
		}
	}
	return false
}

func (s *Server) listPaymentConfigs(w http.ResponseWriter, r *http.Request) {
	configs := []any{}
	for _, provider := range payments.Providers {
		c, err := s.Store.PaymentConfig(r.Context(), provider)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		configs = append(configs, map[string]any{"config": c, "ready": c.Ready(), "notify_url": payments.NotifyURL(s.PublicURL, c)})
	}
	writeJSON(w, 200, map[string]any{"public_url": s.PublicURL, "services": configs})
}

type paymentConfigInput struct {
	payments.Config
	Secrets      map[string]string `json:"secrets"`
	ClearSecrets []string          `json:"clear_secrets"`
}

func (s *Server) readPaymentConfig(w http.ResponseWriter, r *http.Request) (payments.Config, bool) {
	provider := r.PathValue("provider")
	if provider == "" {
		provider = "waffo_pancake"
	}
	if !validProvider(provider) {
		writeError(w, 400, "不支持的支付服务")
		return payments.Config{}, false
	}
	old, err := s.Store.PaymentConfig(r.Context(), provider)
	if err != nil {
		writeStoreError(w, err)
		return old, false
	}
	in := paymentConfigInput{Config: old}
	if !readJSON(w, r, &in) {
		return old, false
	}
	c := in.Config
	c.Provider = provider
	c.VersionID = old.VersionID
	c.CallbackVersionID = old.CallbackVersionID
	c.Secrets = map[string]string{}
	for k, v := range old.Secrets {
		c.Secrets[k] = v
	}
	allowed := map[string]bool{}
	for _, k := range payments.SecretKeys(provider) {
		allowed[k] = true
	}
	for k, v := range in.Secrets {
		if !allowed[k] {
			writeError(w, 400, "未知的支付密钥字段")
			return c, false
		}
		if strings.TrimSpace(v) != "" {
			c.Secrets[k] = strings.TrimSpace(v)
		}
	}
	for _, k := range in.ClearSecrets {
		if !allowed[k] {
			writeError(w, 400, "未知的支付密钥字段")
			return c, false
		}
		delete(c.Secrets, k)
	}
	allowed = map[string]bool{}
	for _, k := range payments.ValueKeys(provider) {
		allowed[k] = true
	}
	for k, v := range c.Values {
		if !allowed[k] {
			writeError(w, 400, "未知的支付配置字段")
			return c, false
		}
		c.Values[k] = strings.TrimSpace(v)
	}
	if c.Currency == "CNY" {
		c.UnitPrice = s.Store.CNYPerUSD(r.Context()) // 跟随系统汇率，不单独配置
	}
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return c, false
	}
	c.ConfiguredSecrets = map[string]bool{}
	for k, v := range c.Secrets {
		c.ConfiguredSecrets[k] = v != ""
	}
	return c, true
}

func (s *Server) savePaymentConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := s.readPaymentConfig(w, r)
	if !ok {
		return
	}
	if c.Enabled && s.PublicURL == "" {
		writeError(w, 400, "请先在部署配置中设置 LINGSHU_PUBLIC_URL")
		return
	}
	c, err := s.Store.SavePaymentConfig(r.Context(), c)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("支付配置已更新", "provider", c.Provider, "version_id", c.VersionID, "enabled", c.Enabled, "operator_id", currentUser(r).ID)
	writeJSON(w, 200, map[string]any{"config": c, "ready": c.Ready(), "notify_url": payments.NotifyURL(s.PublicURL, c)})
}

func (s *Server) probePayment(w http.ResponseWriter, r *http.Request) {
	c, ok := s.readPaymentConfig(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := s.Payments.Probe(ctx, c); err != nil {
		writeError(w, 502, err.Error())
		return
	}
	okResponse := map[string]any{"ok": true, "message": "支付服务验证通过"}
	writeJSON(w, 200, okResponse)
}

func (s *Server) pancakeCatalog(w http.ResponseWriter, r *http.Request) {
	c, ok := s.readPaymentConfig(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	catalog, err := s.Payments.PancakeCatalog(ctx, c)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, catalog)
}

func (s *Server) createPancakeProduct(w http.ResponseWriter, r *http.Request) {
	c, ok := s.readPaymentConfig(w, r)
	if !ok {
		return
	}
	if s.PublicURL == "" {
		writeError(w, 400, "请先配置 LINGSHU_PUBLIC_URL")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := s.Payments.CreatePancakeProduct(ctx, c, s.PublicURL)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error(), "store_id": out.StoreID, "product_id": out.ProductID})
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) paymentsOpen(ctx context.Context) bool {
	v, err := s.Store.Setting(ctx, "payment_enabled")
	return err == nil && v == "true" && s.PublicURL != ""
}

func (s *Server) paymentMethods(w http.ResponseWriter, r *http.Request) {
	redeem, err := s.redeemEnabled(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	methods := []any{}
	open := s.paymentsOpen(r.Context())
	if open {
		for _, p := range payments.Providers {
			c, err := s.Store.PaymentConfig(r.Context(), p)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			if !c.Enabled || !c.Ready() || c.Environment != "prod" {
				continue
			}
			methods = append(methods, map[string]any{"provider": p, "currency": c.Currency, "unit_price": c.UnitPrice, "min_usd": c.MinUSD, "max_usd": c.MaxUSD, "methods": c.Methods})
		}
	}
	writeJSON(w, 200, map[string]any{"enabled": open, "redeem_enabled": redeem, "methods": methods})
}

func (s *Server) createTopup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider   string          `json:"provider"`
		Method     string          `json:"method"`
		CreditUSD  decimal.Decimal `json:"credit_usd"`
		RequestKey string          `json:"request_key"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	test := r.PathValue("provider") != ""
	if test {
		in.Provider = r.PathValue("provider")
	}
	if !validProvider(in.Provider) || len(in.RequestKey) < 16 || len(in.RequestKey) > 100 {
		writeError(w, 400, "支付服务或请求标识无效，请刷新后重试")
		return
	}
	if !test && !s.paymentsOpen(r.Context()) {
		writeError(w, 403, "在线充值尚未开放，请联系管理员")
		return
	}
	if s.PublicURL == "" {
		writeError(w, 400, "尚未配置站点公网地址，请联系管理员")
		return
	}
	c, err := s.Store.PaymentConfig(r.Context(), in.Provider)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if c.VersionID == 0 || !c.Ready() || (!test && (!c.Enabled || c.Environment != "prod")) {
		writeError(w, 400, "此支付服务尚未开放或配置不完整")
		return
	}
	amount, err := payments.Quote(c, in.CreditUSD, in.Method)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	o, err := s.Store.CreateTopup(r.Context(), currentUser(r).ID, c, in.CreditUSD, amount, in.Method, in.RequestKey, test)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if o.Provider != in.Provider || !o.CreditUSD.Equal(in.CreditUSD) || o.Method != in.Method || o.AdminTest != test {
		writeError(w, 409, "该请求标识已用于另一笔订单，请重新确认金额")
		return
	}
	writeJSON(w, 200, o)
}

func (s *Server) ownTopup(w http.ResponseWriter, r *http.Request) (payments.Order, bool) {
	o, err := s.Store.Topup(r.Context(), r.PathValue("order"))
	if err != nil {
		writeStoreError(w, err)
		return o, false
	}
	if o.UserID != currentUser(r).ID {
		writeError(w, 404, "订单不存在")
		return o, false
	}
	return o, true
}

func (s *Server) getTopup(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownTopup(w, r)
	if ok {
		writeJSON(w, 200, o)
	}
}

func (s *Server) listTopups(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size < 1 || size > 100 {
		size = 20
	}
	q := r.URL.Query()
	f := store.TopupFilter{UserID: currentUser(r).ID, Status: q.Get("status")} // 只看自己的；管理员在操作日志里看全站充值
	switch f.Status {
	case "", "pending", "success", "failed", "expired":
	default:
		writeError(w, 400, "订单状态不正确")
		return
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, 400, "时间格式不对: "+v)
				return
			}
			*p.dst = &t
		}
	}
	orders, total, err := s.Store.Topups(r.Context(), f, page, size)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": orders, "total": total, "page": page, "page_size": size})
}

func (s *Server) topupCheckout(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownTopup(w, r)
	if !ok {
		return
	}
	if o.AdminTest && !isAdmin(currentUser(r).Role) {
		writeError(w, 403, "此订单是管理员验收订单")
		return
	}
	if o.Status != "pending" {
		writeError(w, 409, "订单已结束，请刷新查看结果")
		return
	}
	if o.Action != nil {
		writeJSON(w, 200, o.Action)
		return
	}
	c, err := s.Store.PaymentConfigVersion(r.Context(), o.VersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	claimed, err := s.Store.ClaimTopupCheckout(r.Context(), o.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !claimed {
		writeError(w, 409, "支付会话正在创建或等待核对，请刷新订单，不要重复付款")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	a, err := s.Payments.Checkout(ctx, c, o, s.PublicURL)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		writeError(w, 502, "支付服务返回的收银台地址无效，请刷新订单核对")
		return
	}
	// 客户端中途断开也要保存已创建的会话，供重试复用。
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer saveCancel()
	if err := s.Store.SaveTopupCheckout(saveCtx, o.ID, a); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) refreshTopup(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownTopup(w, r)
	if !ok {
		return
	}
	if o.Status == "success" {
		writeJSON(w, 200, map[string]any{"order": o})
		return
	}
	c, err := s.Store.PaymentConfigVersion(r.Context(), o.VersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	e, err := s.Payments.Query(ctx, c, o)
	if errors.Is(err, payments.ErrUnsupportedQuery) {
		writeJSON(w, 200, map[string]any{"order": o, "message": err.Error()})
		return
	}
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	if e != nil {
		if err := s.Store.ApplyTopupEvent(ctx, c.Provider, c.VersionID, *e); err != nil {
			paymentEventError(w, err)
			return
		}
	}
	o, err = s.Store.Topup(ctx, o.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"order": o})
}

func paymentEventError(w http.ResponseWriter, err error) {
	if errors.Is(err, payments.ErrInvalidEvent) {
		writeError(w, 400, "支付结果与订单不匹配，请保留订单号联系管理员")
		return
	}
	writeError(w, 500, "订单入账未完成，请稍后刷新")
}

func (s *Server) paymentNotify(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil || !validProvider(provider) {
		http.NotFound(w, r)
		return
	}
	c, err := s.Store.PaymentConfigVersion(r.Context(), version)
	if err != nil || c.Provider != provider || c.Environment != r.PathValue("environment") {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	e, err := s.Payments.Verify(r.Context(), c, r)
	if err != nil {
		slog.Warn("支付通知验证失败", "provider", provider, "version_id", version)
		payments.Acknowledge(c, w, false)
		return
	}
	if e == nil {
		payments.Acknowledge(c, w, true)
		return
	}
	o, err := s.Store.Topup(r.Context(), e.OrderID)
	if err == nil {
		var original payments.Config
		original, err = s.Store.PaymentConfigVersion(r.Context(), o.VersionID)
		if err == nil && (original.CallbackVersionID != c.CallbackVersionID || original.Provider != c.Provider || original.Environment != c.Environment) {
			err = payments.ErrInvalidEvent
		}
		if err == nil {
			err = s.Store.ApplyTopupEvent(r.Context(), provider, o.VersionID, *e)
		}
	}
	if err != nil {
		slog.Warn("支付通知未入账", "provider", provider, "order_id", e.OrderID)
		payments.Acknowledge(c, w, false)
		return
	}
	slog.Info("支付通知已处理", "provider", provider, "order_id", e.OrderID, "status", e.Status)
	payments.Acknowledge(c, w, true)
}
