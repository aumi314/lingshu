package billing

import (
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/relay/usage"
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
}
