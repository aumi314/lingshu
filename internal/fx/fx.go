// Package fx 拉取人民币 / 美元的市场汇率（规则见 docs/decisions.md「汇率统一与实时汇率」）。
package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/shopspring/decimal"
)

// URL 是 Frankfurter 的最新汇率接口：多国央行数据综合，免费、无需 key、无配额、不要求署名。
const URL = "https://api.frankfurter.dev/v1/latest?from=USD&to=CNY"

// Source 显示在系统设置里，告诉管理员汇率从哪来。
const Source = "Frankfurter"

// Fetch 返回 1 美元兑人民币（保留 4 位小数）和数据日期。
func Fetch(ctx context.Context, client *http.Client, url string) (decimal.Decimal, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return decimal.Zero, "", err
	}
	req.Header.Set("User-Agent", "lingshu-fx/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("请求汇率失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, "", fmt.Errorf("汇率接口返回 HTTP %d", resp.StatusCode)
	}
	var out struct {
		Date  string                     `json:"date"`
		Rates map[string]json.RawMessage `json:"rates"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return decimal.Zero, "", fmt.Errorf("汇率接口返回的不是有效 JSON")
	}
	raw, ok := out.Rates["CNY"]
	if !ok {
		return decimal.Zero, "", fmt.Errorf("汇率接口没有返回人民币汇率")
	}
	rate, err := decimal.NewFromString(string(raw))
	if err != nil || !rate.IsPositive() {
		return decimal.Zero, "", fmt.Errorf("汇率接口返回的人民币汇率无效: %s", raw)
	}
	return rate.Round(4), out.Date, nil
}

// TooFar 判断新汇率相对当前汇率变动是否超过 10%：超过视为异常，不自动采用。
func TooFar(current, next decimal.Decimal) bool {
	if !current.IsPositive() {
		return false
	}
	return next.Sub(current).Abs().Div(current).GreaterThan(decimal.RequireFromString("0.1"))
}
