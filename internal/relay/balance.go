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
var BalanceTemplates = []string{"deepseek", "moonshot", "openrouter"}

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
