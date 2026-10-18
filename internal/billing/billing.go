// Package billing 负责算钱和准入检查。金额单位 USD，只用 decimal。
package billing

import (
	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

// Prices 是模型标价，单位：USD / 每百万 token。
type Prices struct {
	Input, Output, CacheRead, CacheWrite decimal.Decimal
}

var million = decimal.NewFromInt(1_000_000)

// Cost = Σ(各项 token × 单价) / 1e6 × 分组倍率，保留 10 位小数（和数据库 NUMERIC(20,10) 一致）。
func Cost(u usage.Usage, p Prices, ratio decimal.Decimal) decimal.Decimal {
	sum := p.Input.Mul(decimal.NewFromInt(u.Input)).
		Add(p.Output.Mul(decimal.NewFromInt(u.Output))).
		Add(p.CacheRead.Mul(decimal.NewFromInt(u.CacheRead))).
		Add(p.CacheWrite.Mul(decimal.NewFromInt(u.CacheWrite)))
	return sum.Div(million).Mul(ratio).Round(10)
}

// Rejection 是准入检查不通过的原因，Message 直接展示给用户。
type Rejection struct {
	Status  int
	Message string
}

// Admit 检查余额和 key 限额。允许小额透支：只要请求前余额 > 0 就放行，结算后可以变成负数。
func Admit(balance decimal.Decimal, quotaLimit *decimal.Decimal, quotaUsed decimal.Decimal) *Rejection {
	if !balance.IsPositive() {
		return &Rejection{Status: 402, Message: "账户余额不足（当前 $" + balance.StringFixed(4) + "），请联系管理员充值"}
	}
	if quotaLimit != nil && quotaUsed.GreaterThanOrEqual(*quotaLimit) {
		return &Rejection{Status: 402, Message: "这个 API key 的额度已用完（$" + quotaUsed.StringFixed(4) + " / $" + quotaLimit.StringFixed(4) + "），可以调高限额或换一个 key"}
	}
	return nil
}
