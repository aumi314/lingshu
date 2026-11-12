// Package billing 负责算钱和准入检查。金额单位 USD，只用 decimal。
package billing

import (
	"time"

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

// Scale 把各项单价乘同一个倍数，用于 GPT 的 Fast 档（价格规则选出单价后再乘，长上下文同样适用）。
func (p Prices) Scale(k decimal.Decimal) Prices {
	return Prices{Input: p.Input.Mul(k), Output: p.Output.Mul(k), CacheRead: p.CacheRead.Mul(k), CacheWrite: p.CacheWrite.Mul(k)}
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
	return AdmitQuota(quotaLimit, quotaUsed)
}

// AdmitQuota 只检查 key 自己的限额（项目 key 的钱走项目池，余额另查）。
func AdmitQuota(quotaLimit *decimal.Decimal, quotaUsed decimal.Decimal) *Rejection {
	if quotaLimit != nil && quotaUsed.GreaterThanOrEqual(*quotaLimit) {
		return &Rejection{Status: 402, Message: "这个 API key 的额度已用完（$" + quotaUsed.StringFixed(4) + " / $" + quotaLimit.StringFixed(4) + "），可以调高限额或换一个 key"}
	}
	return nil
}

// Reserve 是放行前要冻结的额度：按估算输入和最大输出、在当前时刻生效的价格算出最大可能费用。
// 输入全部按未命中缓存算，偏保守；结算时按实际用量多退少补。
func Reserve(inputTokens, maxOutputTokens int64, base Prices, rules []PriceRule, ratio decimal.Decimal, at time.Time) decimal.Decimal {
	u := usage.Usage{Input: inputTokens, Output: maxOutputTokens}
	prices, _ := Select(base, rules, u, at)
	return Cost(u, prices, ratio)
}
