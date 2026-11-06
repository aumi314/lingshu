package billing

import (
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/shopspring/decimal"
)

func i64(n int64) *int64 { return &n }

func TestSelectByInputLength(t *testing.T) {
	base := Prices{Input: d("2"), Output: d("10")}
	rules := []PriceRule{{Name: "长上下文", InputAbove: i64(272_000), Input: d("4"), Output: d("15")}}
	at := time.Now()
	// 刚好 272K 不触发，超过才触发；缓存命中和写入也算进输入长度
	if p, name := Select(base, rules, usage.Usage{Input: 272_000}, at); !p.Input.Equal(d("2")) || name != "" {
		t.Errorf("272K 整不应触发: %v %q", p.Input, name)
	}
	if p, name := Select(base, rules, usage.Usage{Input: 1000, CacheRead: 271_001}, at); !p.Input.Equal(d("4")) || name != "长上下文" {
		t.Errorf("缓存算进输入长度: %v %q", p.Input, name)
	}
}

func TestSelectByTimeWindow(t *testing.T) {
	base := Prices{Input: d("1")}
	rules := []PriceRule{{Name: "夜间", TimeStart: "22:00", TimeEnd: "06:00", Input: d("0.5")}}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 21, h, m, 0, 0, Beijing) }
	for _, c := range []struct {
		h, m int
		want string
	}{{21, 59, "1"}, {22, 0, "0.5"}, {3, 0, "0.5"}, {5, 59, "0.5"}, {6, 0, "1"}} {
		if p, _ := Select(base, rules, usage.Usage{}, at(c.h, c.m)); !p.Input.Equal(d(c.want)) {
			t.Errorf("%02d:%02d 价格 %v，期望 %s", c.h, c.m, p.Input, c.want)
		}
	}
	// 按北京时间判断，和服务器时区无关：UTC 14:30 = 北京 22:30
	if p, _ := Select(base, rules, usage.Usage{}, time.Date(2026, 10, 21, 14, 30, 0, 0, time.UTC)); !p.Input.Equal(d("0.5")) {
		t.Errorf("应按北京时间判断，得到 %v", p.Input)
	}
}

func TestSelectFirstMatchWins(t *testing.T) {
	rules := []PriceRule{
		{Name: "夜间长上下文", InputAbove: i64(100), TimeStart: "00:00", TimeEnd: "08:00", Input: d("3")},
		{Name: "长上下文", InputAbove: i64(100), Input: d("4")},
	}
	night := time.Date(2026, 10, 21, 2, 0, 0, 0, Beijing)
	day := time.Date(2026, 10, 21, 12, 0, 0, 0, Beijing)
	if _, name := Select(Prices{}, rules, usage.Usage{Input: 200}, night); name != "夜间长上下文" {
		t.Errorf("夜里应命中第一条，得到 %q", name)
	}
	if _, name := Select(Prices{}, rules, usage.Usage{Input: 200}, day); name != "长上下文" {
		t.Errorf("白天应命中第二条，得到 %q", name)
	}
}

func TestNormalize(t *testing.T) {
	got, err := Normalize([]PriceRule{{InputAbove: i64(272_000), Input: d("4")}, {TimeStart: "00:30", TimeEnd: "08:30"}})
	if err != nil || got[0].Name != "输入超过 272K" || got[1].Name != "北京时间 00:30–08:30" {
		t.Errorf("自动命名: %+v %v", got, err)
	}
	bad := [][]PriceRule{
		{{Input: d("1")}},                        // 没有条件
		{{TimeStart: "25:00", TimeEnd: "01:00"}}, // 时间不对
		{{TimeStart: "08:00", TimeEnd: "08:00"}}, // 开始等于结束
		{{TimeStart: "08:00"}},                   // 只填一半
		{{InputAbove: i64(-1)}},                  // 负数
		{{InputAbove: i64(1), Output: d("-1")}},  // 负价格
		{{TimeStart: "8:5", TimeEnd: "09:00"}},   // 分钟要两位
	}
	for i, rules := range bad {
		if _, err := Normalize(rules); err == nil {
			t.Errorf("第 %d 组应报错: %+v", i, rules)
		}
	}
	if r, _ := Normalize([]PriceRule{{Days: "weekend"}}); r[0].Name != "周末" {
		t.Errorf("只有周末条件也算有效规则，并自动命名: %+v", r)
	}
	if _, err := Normalize([]PriceRule{{Days: "workday"}}); err == nil {
		t.Error("旧工作日条件应由用户改成时间条件")
	}
	for _, invalid := range []string{"weekday", "workday", "non_workday"} {
		if _, err := Normalize([]PriceRule{{Days: invalid}}); err == nil {
			t.Errorf("日期条件 %s 应被拒绝", invalid)
		}
	}
}

// DeepSeek 的配法：基础价是工作日高峰价，三条低谷规则（周末全天、18:00–09:00、12:00–14:00）
func TestDeepSeekOffPeak(t *testing.T) {
	peak := Prices{Input: d("0.3")}
	low := Prices{Input: d("0.15")}
	rules := []PriceRule{
		{Name: "低谷（周末）", Days: "weekend", Input: low.Input},
		{Name: "低谷（夜间）", TimeStart: "18:00", TimeEnd: "09:00", Input: low.Input},
		{Name: "低谷（午间）", TimeStart: "12:00", TimeEnd: "14:00", Input: low.Input},
	}
	at := func(y, mo, d, h, m int) time.Time { return time.Date(y, time.Month(mo), d, h, m, 0, 0, Beijing) }
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"周三 08:59 夜间低谷", at(2026, 10, 21, 8, 59), "0.15"},
		{"周三 09:00 高峰", at(2026, 10, 21, 9, 0), "0.3"},
		{"周三 11:59 高峰", at(2026, 10, 21, 11, 59), "0.3"},
		{"周三 12:00 午间低谷", at(2026, 10, 21, 12, 0), "0.15"},
		{"周三 14:00 高峰", at(2026, 10, 21, 14, 0), "0.3"},
		{"周三 18:00 低谷", at(2026, 10, 21, 18, 0), "0.15"},
		{"周六 10:00 周末低谷", at(2026, 10, 24, 10, 0), "0.15"},
		{"周日 10:00 周末低谷", at(2026, 10, 25, 10, 0), "0.15"},
		{"法定假日落在周五仍按时段高峰", at(2027, 1, 1, 10, 0), "0.3"},
		{"元旦后周一 10:00 高峰", at(2027, 1, 4, 10, 0), "0.3"},
	}
	for _, c := range cases {
		if p, _ := Select(peak, rules, usage.Usage{}, c.at); !p.Input.Equal(d(c.want)) {
			t.Errorf("%s: %v，期望 %s", c.name, p.Input, c.want)
		}
	}
}

// Gemini 4 Argon：限时价到 2026-11-30，12-01 起翻倍（按北京时间的日期切换）
func TestDateRange(t *testing.T) {
	rules := []PriceRule{{Name: "正式价", DateFrom: "2026-12-01", Input: d("4")}}
	base := Prices{Input: d("2")}
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 30, 23, 59, 0, 0, Beijing), "2"},
		{time.Date(2026, 12, 1, 0, 0, 0, 0, Beijing), "4"},
		{time.Date(2026, 11, 30, 16, 0, 0, 0, time.UTC), "4"}, // UTC 16:00 = 北京 12-01 00:00
		{time.Date(2027, 6, 1, 12, 0, 0, 0, Beijing), "4"},
	} {
		if p, _ := Select(base, rules, usage.Usage{}, c.at); !p.Input.Equal(d(c.want)) {
			t.Errorf("%v: %v，期望 %s", c.at, p.Input, c.want)
		}
	}
	limited := []PriceRule{{DateFrom: "2026-11-11", DateTo: "2026-11-11", Input: d("1")}}
	if p, _ := Select(base, limited, usage.Usage{}, time.Date(2026, 11, 11, 23, 0, 0, 0, Beijing)); !p.Input.Equal(d("1")) {
		t.Error("结束日期当天应包含在内")
	}
	if p, _ := Select(base, limited, usage.Usage{}, time.Date(2026, 11, 12, 0, 0, 0, 0, Beijing)); !p.Input.Equal(d("2")) {
		t.Error("结束日期之后应回到基础价")
	}
	got, err := Normalize([]PriceRule{{DateFrom: "2026-12-01"}, {DateFrom: "2026-11-01", DateTo: "2026-11-30", InputAbove: i64(200_000)}})
	if err != nil || got[0].Name != "2026-12-01 起" || got[1].Name != "2026-11-01 至 2026-11-30 · 输入超过 200K" {
		t.Errorf("自动命名: %+v %v", got, err)
	}
	for _, bad := range [][]PriceRule{{{DateFrom: "2026/12/01"}}, {{DateFrom: "2026-12-02", DateTo: "2026-12-01"}}} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("应报错: %+v", bad)
		}
	}
}

// 预扣金额：按估算输入和最大输出算，价格规则按估算输入长度匹配，乘分组倍率。
func TestReserve(t *testing.T) {
	d := decimal.RequireFromString
	base := Prices{Input: d("1"), Output: d("2"), CacheRead: d("0.1")}
	long := PriceRule{Name: "长上下文", InputAbove: i64(200000), Input: d("2"), Output: d("4")}
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	// 1000 输入 × $1 + 8192 输出 × $2 = 17384 / 1e6，倍率 0.5
	if got := Reserve(1000, 8192, base, []PriceRule{long}, d("0.5"), at); !got.Equal(d("0.008692")) {
		t.Errorf("短输入按基础价: %s", got)
	}
	// 30 万输入命中长上下文规则：300000 × $2 + 1000 × $4
	if got := Reserve(300000, 1000, base, []PriceRule{long}, d("1"), at); !got.Equal(d("0.604")) {
		t.Errorf("长输入按规则价: %s", got)
	}
}
