package billing

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

// 业务时区可切换：周末和时段按设置的时区判断，描述里写时区名；非法时区保持原来的。
func TestZoneSwitch(t *testing.T) {
	t.Cleanup(func() { SetZone(DefaultZone) })
	// UTC 2026-11-14（周六）20:00 = 北京时间周日 04:00 = 洛杉矶周六 12:00
	at := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	night := PriceRule{Name: "夜间", TimeStart: "00:00", TimeEnd: "06:00", Input: decimal.NewFromInt(9)}
	base := Prices{Input: decimal.NewFromInt(1)}
	if p, _ := Select(base, []PriceRule{night}, usage.Usage{}, at); !p.Input.Equal(decimal.NewFromInt(9)) {
		t.Errorf("北京时间 04:00 应命中夜间价，得到 %s", p.Input)
	}
	if ZoneLabel() != "北京时间" || DescribeCondition(night) != "北京时间 00:00–06:00" {
		t.Errorf("默认时区名: %q / %q", ZoneLabel(), DescribeCondition(night))
	}
	if err := SetZone("America/Los_Angeles"); err != nil {
		t.Fatal(err)
	}
	if p, _ := Select(base, []PriceRule{night}, usage.Usage{}, at); !p.Input.Equal(decimal.NewFromInt(1)) {
		t.Errorf("洛杉矶 12:00 不应命中夜间价，得到 %s", p.Input)
	}
	if !IsWeekend(at) || DescribeCondition(night) != "America/Los_Angeles 00:00–06:00" {
		t.Errorf("洛杉矶周六应是周末；描述 %q", DescribeCondition(night))
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus", "Asia/Shanghai'; --"} {
		if err := SetZone(bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	if ZoneName() != "America/Los_Angeles" {
		t.Errorf("非法时区不应改变当前时区: %s", ZoneName())
	}
}
