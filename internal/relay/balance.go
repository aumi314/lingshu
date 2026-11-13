package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// BalanceTemplates 是能用接口查上游余额的渠道模板。其他厂商没有公开的余额接口。
// sub2api、new-api 是中转站常用的开源面板，用渠道自己的 key 就能查到在那个站上的余额（2026-11-12 实测）。
var BalanceTemplates = []string{"deepseek", "moonshot", "openrouter", "sub2api", "new-api"}

// RootTemplates 是只填域名根地址、自动生成三种协议地址的模板（标准模板和中转面板）。
var RootTemplates = []string{"standard", "sub2api", "new-api"}

// IsRootTemplate 表示这个模板用根地址生成协议地址。
func IsRootTemplate(template string) bool {
	for _, t := range RootTemplates {
		if t == template {
			return true
		}
	}
	return false
}

// CanQueryBalance 表示这个模板能不能用渠道的 key 查余额。
func CanQueryBalance(template string) bool {
	for _, t := range BalanceTemplates {
		if t == template {
			return true
		}
	}
	return false
}

// Balance 是厂商余额接口返回的可用余额，按币种分开（DeepSeek 一个账户可能同时有人民币和美元余额）。
type Balance map[string]decimal.Decimal

// FetchBalance 查上游账户的可用余额。baseURL 用渠道的 chat 地址，只取其中的域名部分。
func (h *Handler) FetchBalance(ctx context.Context, template, baseURL, apiKey, proxy string) (Balance, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("渠道地址不对: %q", baseURL)
	}
	origin := u.Scheme + "://" + u.Host
	var path string
	switch template {
	case "deepseek":
		path = "/user/balance"
	case "moonshot":
		path = "/v1/users/me/balance"
	case "openrouter":
		path = "/api/v1/credits"
	case "sub2api":
		path = "/v1/usage"
	case "new-api":
		return h.newAPIBalance(ctx, origin, apiKey, proxy)
	default:
		return nil, fmt.Errorf("%s 没有余额接口", template)
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := h.clientFor(proxy).Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 %s: %v", u.Host, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(redactKey(raw, apiKey)), 200))
	}

	switch template {
	case "deepseek":
		// {"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.19",...}]}
		var out struct {
			BalanceInfos []struct {
				Currency     string          `json:"currency"`
				TotalBalance decimal.Decimal `json:"total_balance"`
			} `json:"balance_infos"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.BalanceInfos) == 0 {
			return nil, fmt.Errorf("看不懂返回内容: %s", truncate(string(raw), 200))
		}
		b := Balance{}
		for _, info := range out.BalanceInfos {
			cur := strings.ToUpper(info.Currency)
			b[cur] = b[cur].Add(info.TotalBalance)
		}
		return b, nil
	case "sub2api":
		// {"balance":21.5,"remaining":21.5,"unit":"USD","isValid":true,"planName":"钱包余额",...}
		var out struct {
			Balance   *decimal.Decimal `json:"balance"`
			Remaining *decimal.Decimal `json:"remaining"`
			Unit      string           `json:"unit"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || (out.Remaining == nil && out.Balance == nil) {
			return nil, fmt.Errorf("看不懂返回内容（这个站可能不是 sub2api）: %s", truncate(string(redactKey(raw, apiKey)), 200))
		}
		amount := out.Balance
		if out.Remaining != nil {
			amount = out.Remaining
		}
		cur := strings.ToUpper(out.Unit)
		if cur == "" {
			cur = "USD"
		}
		return Balance{cur: *amount}, nil
	case "openrouter":
		// {"data":{"total_credits":10,"total_usage":0.02197344}}，剩余 = 总额 − 已用
		var out struct {
			Data *struct {
				TotalCredits decimal.Decimal `json:"total_credits"`
				TotalUsage   decimal.Decimal `json:"total_usage"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || out.Data == nil {
			return nil, fmt.Errorf("看不懂返回内容: %s", truncate(string(raw), 200))
		}
		return Balance{"USD": out.Data.TotalCredits.Sub(out.Data.TotalUsage)}, nil
	default: // moonshot
		// {"code":0,"data":{"available_balance":71.43,"voucher_balance":13.21,"cash_balance":58.22},"status":true}
		var out struct {
			Data *struct {
				Available decimal.Decimal `json:"available_balance"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || out.Data == nil {
			return nil, fmt.Errorf("看不懂返回内容: %s", truncate(string(raw), 200))
		}
		// 中国站（moonshot.cn）记人民币，国际站（moonshot.ai）记美元
		cur := "USD"
		if strings.HasSuffix(u.Host, ".cn") {
			cur = "CNY"
		}
		return Balance{cur: out.Data.Available}, nil
	}
}

// newAPIBalance 查 new-api（以及 one-api 系）站上的余额，都用渠道自己的 key：
//   - /api/usage/token/ 给出令牌和账户的额度（内部单位，默认 50 万 = 1 美元）：令牌有上限就取令牌剩余，令牌不限额就取账户剩余（新版本才返回 user_quota）；
//   - /v1/dashboard/billing/subscription 和 /usage 是 OpenAI 旧账单格式（美元）：用来反推单位换算，也是老版本拿不到账户余额时的兜底（上限 − 已用）。
//
// 上限是 1 亿时代表“不限额”，没法算出剩余。
func (h *Handler) newAPIBalance(ctx context.Context, origin, apiKey, proxy string) (Balance, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	get := func(path string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := h.clientFor(proxy).Do(req)
		if err != nil {
			return fmt.Errorf("连不上 %s: %v", origin, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(redactKey(raw, apiKey)), 200))
		}
		return json.Unmarshal(raw, out)
	}
	var token struct {
		Code bool `json:"code"`
		Data struct {
			Available      decimal.Decimal `json:"total_available"`
			Used           decimal.Decimal `json:"total_used"`
			UnlimitedQuota bool            `json:"unlimited_quota"`
			UserQuota      *struct {
				Available decimal.Decimal `json:"total_available"`
			} `json:"user_quota"`
		} `json:"data"`
	}
	tokenErr := get("/api/usage/token/", &token)
	var sub struct {
		HardLimit *decimal.Decimal `json:"hard_limit_usd"`
	}
	var usage struct {
		TotalUsage *decimal.Decimal `json:"total_usage"` // 单位是美分
	}
	subErr := get("/v1/dashboard/billing/subscription", &sub)
	usageErr := get("/v1/dashboard/billing/usage?start_date=2000-01-01&end_date="+time.Now().AddDate(0, 0, 1).Format("2006-01-02"), &usage)

	// 内部单位换算：令牌已用（内部单位）÷ 账单接口的已用（美元），拿不到就按默认 50 万
	perUSD := decimal.NewFromInt(500000)
	if tokenErr == nil && usageErr == nil && usage.TotalUsage != nil && usage.TotalUsage.IsPositive() && token.Data.Used.IsPositive() {
		perUSD = token.Data.Used.Div(usage.TotalUsage.Div(decimal.NewFromInt(100))).Round(0)
	}
	if tokenErr == nil && token.Code {
		if !token.Data.UnlimitedQuota {
			return Balance{"USD": token.Data.Available.Div(perUSD).Round(4)}, nil
		}
		if token.Data.UserQuota != nil {
			return Balance{"USD": token.Data.UserQuota.Available.Div(perUSD).Round(4)}, nil
		}
	}
	unlimited := decimal.NewFromInt(100000000)
	if subErr == nil && usageErr == nil && sub.HardLimit != nil && usage.TotalUsage != nil && sub.HardLimit.LessThan(unlimited) {
		return Balance{"USD": sub.HardLimit.Sub(usage.TotalUsage.Div(decimal.NewFromInt(100))).Round(4)}, nil
	}
	if tokenErr != nil && subErr != nil {
		return nil, fmt.Errorf("这个站没有 new-api 的余额接口（%v）", tokenErr)
	}
	return nil, fmt.Errorf("这个 key 不限额，站点也没返回账户余额，查不到剩余")
}
