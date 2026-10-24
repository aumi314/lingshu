// Package payments 适配外部支付服务；钱包入账由 store 的数据库事务负责。
package payments

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var Providers = []string{"epay", "stripe", "creem", "waffo", "waffo_pancake"}

var ErrUnsupportedQuery = errors.New("此支付服务暂不支持主动查单，请等待支付通知")
var ErrInvalidEvent = errors.New("支付通知与订单不匹配")

type Method struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Config struct {
	VersionID         int64             `json:"version_id"`
	CallbackVersionID int64             `json:"callback_version_id"`
	Provider          string            `json:"provider"`
	Enabled           bool              `json:"enabled"`
	Environment       string            `json:"environment"`
	Currency          string            `json:"currency"`
	UnitPrice         decimal.Decimal   `json:"unit_price"`
	MinUSD            decimal.Decimal   `json:"min_usd"`
	MaxUSD            decimal.Decimal   `json:"max_usd"`
	Values            map[string]string `json:"values"`
	Secrets           map[string]string `json:"-"`
	ConfiguredSecrets map[string]bool   `json:"configured_secrets"`
	Methods           []Method          `json:"methods"`
}

func DefaultConfig(provider string, rate decimal.Decimal) Config {
	c := Config{Provider: provider, Environment: "prod", Currency: "USD", UnitPrice: decimal.NewFromInt(1), MinUSD: decimal.NewFromInt(1), MaxUSD: decimal.NewFromInt(1000), Values: map[string]string{}, Secrets: map[string]string{}, ConfiguredSecrets: map[string]bool{}, Methods: []Method{}}
	if provider == "epay" {
		c.Currency = "CNY"
		c.UnitPrice = rate
		c.Methods = []Method{{ID: "alipay", Name: "支付宝"}}
	}
	return c
}

func SecretKeys(provider string) []string {
	switch provider {
	case "epay":
		return []string{"key"}
	case "stripe", "creem":
		return []string{"api_key", "webhook_secret"}
	case "waffo":
		return []string{"api_key", "private_key"}
	case "waffo_pancake":
		return []string{"private_key"}
	}
	return nil
}

func ValueKeys(provider string) []string {
	switch provider {
	case "epay":
		return []string{"gateway", "merchant_id"}
	case "stripe":
		return []string{}
	case "creem":
		return []string{"product_id"}
	case "waffo":
		return []string{"merchant_id", "public_key"}
	case "waffo_pancake":
		return []string{"merchant_id", "store_id", "product_id"}
	}
	return nil
}

func (c Config) Ready() bool {
	for _, key := range SecretKeys(c.Provider) {
		if strings.TrimSpace(c.Secrets[key]) == "" {
			return false
		}
	}
	for _, key := range ValueKeys(c.Provider) {
		if strings.TrimSpace(c.Values[key]) == "" {
			return false
		}
	}
	return c.Provider != "epay" || len(c.Methods) > 0
}

func (c Config) Validate() error {
	found := false
	for _, p := range Providers {
		if c.Provider == p {
			found = true
		}
	}
	if !found {
		return errors.New("不支持的支付服务")
	}
	if c.Environment != "test" && c.Environment != "prod" {
		return errors.New("支付环境必须为 test 或 prod")
	}
	if (c.Provider == "epay" && c.Currency != "CNY") || (c.Provider != "epay" && c.Currency != "USD") {
		return errors.New("EPay 使用 CNY，其他支付服务使用 USD")
	}
	if !c.UnitPrice.IsPositive() || !c.MinUSD.IsPositive() || c.MaxUSD.LessThan(c.MinUSD) {
		return errors.New("收款单价和最低额度必须大于零，最高额度不能小于最低额度")
	}
	limit := decimal.NewFromInt(10000000000)
	if c.UnitPrice.Exponent() < -10 || !c.UnitPrice.LessThan(limit) || !c.MaxUSD.LessThan(limit) {
		return errors.New("金额超出账本精度：单价最多十位小数，单价和到账额度须小于 10000000000")
	}
	if c.MinUSD.Exponent() < -2 || c.MaxUSD.Exponent() < -2 {
		return errors.New("充值额度最多两位小数")
	}
	if c.Provider == "epay" && c.Values["gateway"] != "" {
		if err := ValidatePublicURL(c.Values["gateway"]); err != nil {
			return fmt.Errorf("支付网关：%w", err)
		}
	}
	seen := map[string]bool{}
	for _, m := range c.Methods {
		if m.ID == "" || m.Name == "" || seen[m.ID] {
			return errors.New("支付方式标识和名称不能为空，标识不能重复")
		}
		seen[m.ID] = true
	}
	if c.Enabled && !c.Ready() {
		return errors.New("支付配置不完整，请补齐凭据和商品信息再启用")
	}
	return nil
}

func ValidatePublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return errors.New("请输入 HTTPS 地址；本地开发允许 localhost HTTP")
	}
	return nil
}

type Order struct {
	ID              string          `json:"id"`
	UserID          int64           `json:"user_id"`
	Provider        string          `json:"provider"`
	VersionID       int64           `json:"version_id"`
	Environment     string          `json:"environment"`
	Method          string          `json:"method"`
	CreditUSD       decimal.Decimal `json:"credit_usd"`
	Amount          decimal.Decimal `json:"amount"`
	Currency        string          `json:"currency"`
	UnitPrice       decimal.Decimal `json:"unit_price"`
	Status          string          `json:"status"`
	CheckoutID      string          `json:"-"`
	TransactionID   string          `json:"transaction_id"`
	Tax             decimal.Decimal `json:"tax"`
	Total           decimal.Decimal `json:"total"`
	AdminTest       bool            `json:"admin_test"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     *time.Time      `json:"completed_at"`
	CheckoutStarted bool            `json:"-"`
	Action          *Action         `json:"-"`
}

type Action struct {
	Type       string            `json:"type"` // redirect 或 form
	URL        string            `json:"url"`
	Fields     map[string]string `json:"fields,omitempty"`
	CheckoutID string            `json:"-"`
}

type Event struct {
	OrderID       string
	Status        string
	TransactionID string
	Currency      string
	Amount        decimal.Decimal // 充值商品金额，不含税
	Tax           decimal.Decimal
	Total         decimal.Decimal
	Environment   string
	BuyerIdentity string
}

func Quote(c Config, amount decimal.Decimal, method string) (decimal.Decimal, error) {
	if amount.Exponent() < -2 || amount.LessThan(c.MinUSD) || amount.GreaterThan(c.MaxUSD) {
		return decimal.Zero, fmt.Errorf("美元到账额度须在 %s–%s 之间，最多两位小数", c.MinUSD, c.MaxUSD)
	}
	if c.Provider == "epay" {
		found := false
		for _, m := range c.Methods {
			if method == m.ID {
				found = true
			}
		}
		if !found {
			return decimal.Zero, errors.New("请选择已配置的支付方式")
		}
	}
	pay := amount.Mul(c.UnitPrice).Round(2)
	if (c.Provider == "creem" || c.Provider == "stripe") && pay.GreaterThan(decimal.RequireFromString("999999.99")) {
		return decimal.Zero, errors.New("此服务单笔实付金额不能超过 999999.99 USD")
	}
	minimum := decimal.RequireFromString("0.01")
	if c.Provider == "creem" {
		minimum = decimal.NewFromInt(1)
	}
	if c.Provider == "stripe" {
		minimum = decimal.RequireFromString("0.50")
	}
	if pay.LessThan(minimum) {
		return decimal.Zero, fmt.Errorf("此服务实付金额不能小于 %s %s", minimum, c.Currency)
	}
	return pay, nil
}

func ValidateEvent(o Order, e Event) error {
	if o.ID != e.OrderID || o.Environment != e.Environment {
		return ErrInvalidEvent
	}
	if o.Provider == "waffo_pancake" && e.Status == "success" && e.BuyerIdentity != BuyerID(o) {
		return ErrInvalidEvent
	}
	if e.Status == "success" && (e.TransactionID == "" || o.Currency != strings.ToUpper(e.Currency) || !o.Amount.Equal(e.Amount) || e.Tax.IsNegative() || !e.Total.Equal(e.Amount.Add(e.Tax))) {
		return ErrInvalidEvent
	}
	return nil
}

func NotifyURL(base string, c Config) string {
	id := c.CallbackVersionID
	if id == 0 {
		id = c.VersionID
	}
	return strings.TrimRight(base, "/") + fmt.Sprintf("/api/payments/%s/%d/%s/notify", c.Provider, id, c.Environment)
}
func ReturnURL(base, id string) string {
	return strings.TrimRight(base, "/") + "/console/wallet?order=" + url.QueryEscape(id)
}
func BuyerID(o Order) string { return fmt.Sprintf("lingshu-%d-user-%d", o.VersionID, o.UserID) }
