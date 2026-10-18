package billing

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestCost(t *testing.T) {
	qwenMax := Prices{Input: d("2"), Output: d("6"), CacheRead: d("0.2"), CacheWrite: d("2.5")}
	cases := []struct {
		name  string
		u     usage.Usage
		p     Prices
		ratio string
		want  string
	}{
		// Claude Code 实测的第一次请求：6 输入 + 15825 缓存写入 + 51 输出
		{"缓存写入", usage.Usage{Input: 6, CacheWrite: 15825, Output: 51}, qwenMax, "1", "0.039880500"},
		// 第二次请求：缓存命中 15825
		{"缓存命中", usage.Usage{Input: 6, CacheRead: 15825, CacheWrite: 103, Output: 20}, qwenMax, "1", "0.0035545"},
		{"分组倍率", usage.Usage{Input: 1_000_000, Output: 1_000_000}, qwenMax, "0.5", "4"},
		// 极便宜的 MiMo 缓存命中价也不能被舍入成 0
		{"小额不丢精度", usage.Usage{CacheRead: 1}, Prices{CacheRead: d("0.0036")}, "1", "0.0000000036"},
		{"零用量", usage.Usage{}, qwenMax, "1", "0"},
	}
	for _, c := range cases {
		got := Cost(c.u, c.p, d(c.ratio))
		if !got.Equal(d(c.want)) {
			t.Errorf("%s: cost = %s，期望 %s", c.name, got, c.want)
		}
	}
}

func TestAdmit(t *testing.T) {
	limit := d("1")
	cases := []struct {
		name      string
		balance   string
		limit     *decimal.Decimal
		used      string
		wantAdmit bool
	}{
		{"余额为正", "0.01", nil, "0", true},
		{"余额为 0", "0", nil, "0", false},
		{"透支后余额为负", "-0.5", nil, "0", false},
		{"key 额度未用完", "10", &limit, "0.99", true},
		{"key 额度刚好用完", "10", &limit, "1", false},
	}
	for _, c := range cases {
		r := Admit(d(c.balance), c.limit, d(c.used))
		if (r == nil) != c.wantAdmit {
			t.Errorf("%s: 准入 = %v，期望 %v（%+v）", c.name, r == nil, c.wantAdmit, r)
		}
	}
}
