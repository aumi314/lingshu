package billing

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

// PriceRule 是模型基础价之外的一条价格规则（阶梯计价、分时段计价）。
// 规则按顺序匹配，第一条条件全部满足的生效；都不满足用基础价。
type PriceRule struct {
	Name string `json:"name"`
	// InputAbove：总输入（输入 + 缓存命中 + 缓存写入）超过这么多 token 时生效，nil 表示不限。
	InputAbove *int64 `json:"input_above,omitempty"`
	// TimeStart / TimeEnd：北京时间每天的时间段 "HH:MM"，含开始不含结束，可以跨零点；都空表示不限。
	TimeStart string `json:"time_start,omitempty"`
	TimeEnd   string `json:"time_end,omitempty"`
	// Days：日期条件。"" 不限；"weekend" 仅周末（北京时间周六、周日）。
	Days string `json:"days,omitempty"`
	// DateFrom / DateTo：生效日期范围（北京时间 "YYYY-MM-DD"，两头都含），空表示不限。
	// 用来提前配好厂商公布的涨价、降价或限时价，到日子自动切换。
	DateFrom string `json:"date_from,omitempty"`
	DateTo   string `json:"date_to,omitempty"`

	Input      decimal.Decimal `json:"input_price"`
	Output     decimal.Decimal `json:"output_price"`
	CacheRead  decimal.Decimal `json:"cache_read_price"`
	CacheWrite decimal.Decimal `json:"cache_write_price"`
}

func (r PriceRule) Prices() Prices {
	return Prices{Input: r.Input, Output: r.Output, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite}
}

// Beijing 是分时段规则用的时区。中国没有夏令时，用固定偏移，不依赖系统时区数据。
var Beijing = time.FixedZone("Asia/Shanghai", 8*3600)

// TotalInput 是阶梯计价用的输入长度：输入 + 缓存命中 + 缓存写入（和 OpenAI、Anthropic 的算法一致）。
func TotalInput(u usage.Usage) int64 { return u.Input + u.CacheRead + u.CacheWrite }

func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 || len(m) != 2 {
		return 0, fmt.Errorf("时间 %q 格式不对，应为 HH:MM", s)
	}
	return hh*60 + mm, nil
}

// IsWeekend 判断请求开始时间是否落在北京时间周六或周日。
func IsWeekend(at time.Time) bool {
	t := at.In(Beijing)
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// matches 判断规则是否适用。规则在保存时已经校验过，这里遇到坏数据就当作不匹配。
func (r PriceRule) matches(totalInput int64, at time.Time) bool {
	if r.InputAbove != nil && totalInput <= *r.InputAbove {
		return false
	}
	if r.DateFrom != "" || r.DateTo != "" {
		day := at.In(Beijing).Format("2006-01-02")
		if (r.DateFrom != "" && day < r.DateFrom) || (r.DateTo != "" && day > r.DateTo) {
			return false
		}
	}
	switch r.Days {
	case "weekend":
		if !IsWeekend(at) {
			return false
		}
	}
	if r.TimeStart != "" || r.TimeEnd != "" {
		start, err1 := parseClock(r.TimeStart)
		end, err2 := parseClock(r.TimeEnd)
		if err1 != nil || err2 != nil {
			return false
		}
		t := at.In(Beijing)
		now := t.Hour()*60 + t.Minute()
		if start < end {
			return now >= start && now < end
		}
		return now >= start || now < end // 跨零点，例如 22:00–06:00
	}
	return true
}

// Select 返回这次请求适用的单价，以及命中的规则名（用基础价时为空）。
func Select(base Prices, rules []PriceRule, u usage.Usage, at time.Time) (Prices, string) {
	total := TotalInput(u)
	for _, r := range rules {
		if r.matches(total, at) {
			return r.Prices(), r.Name
		}
	}
	return base, ""
}

// Normalize 校验规则并补全默认名称，返回给用户看的错误。
func Normalize(rules []PriceRule) ([]PriceRule, error) {
	out := make([]PriceRule, 0, len(rules))
	for i, r := range rules {
		n := i + 1
		r.Name = strings.TrimSpace(r.Name)
		r.TimeStart, r.TimeEnd = strings.TrimSpace(r.TimeStart), strings.TrimSpace(r.TimeEnd)
		if r.InputAbove != nil && *r.InputAbove < 0 {
			return nil, fmt.Errorf("第 %d 条规则：输入长度不能是负数", n)
		}
		hasTime := r.TimeStart != "" || r.TimeEnd != ""
		if hasTime {
			start, err := parseClock(r.TimeStart)
			if err != nil {
				return nil, fmt.Errorf("第 %d 条规则：开始%v", n, err)
			}
			end, err := parseClock(r.TimeEnd)
			if err != nil {
				return nil, fmt.Errorf("第 %d 条规则：结束%v", n, err)
			}
			if start == end {
				return nil, fmt.Errorf("第 %d 条规则：开始和结束时间不能相同", n)
			}
		}
		if r.Days != "" && r.Days != "weekend" {
			return nil, fmt.Errorf("第 %d 条规则：日期条件只能是 weekend", n)
		}
		r.DateFrom, r.DateTo = strings.TrimSpace(r.DateFrom), strings.TrimSpace(r.DateTo)
		for _, d := range []string{r.DateFrom, r.DateTo} {
			if d == "" {
				continue
			}
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return nil, fmt.Errorf("第 %d 条规则：日期 %q 格式不对，应为 2026-12-01 这样", n, d)
			}
		}
		if r.DateFrom != "" && r.DateTo != "" && r.DateFrom > r.DateTo {
			return nil, fmt.Errorf("第 %d 条规则：开始日期晚于结束日期", n)
		}
		hasDate := r.DateFrom != "" || r.DateTo != ""
		if r.InputAbove == nil && !hasTime && r.Days == "" && !hasDate {
			return nil, fmt.Errorf("第 %d 条规则：至少要有一个条件（输入长度、生效日期、周末或时间段）", n)
		}
		for _, p := range []decimal.Decimal{r.Input, r.Output, r.CacheRead, r.CacheWrite} {
			if p.IsNegative() {
				return nil, fmt.Errorf("第 %d 条规则：价格不能是负数", n)
			}
		}
		if r.Name == "" {
			r.Name = DescribeCondition(r)
		}
		out = append(out, r)
	}
	if len(out) > 20 {
		return nil, errors.New("一个模型最多 20 条价格规则")
	}
	return out, nil
}

// DescribeCondition 用中文描述规则的条件，例如"输入超过 272K · 周末 · 北京时间 09:00–12:00"。
func DescribeCondition(r PriceRule) string {
	var parts []string
	switch {
	case r.DateFrom != "" && r.DateTo != "":
		parts = append(parts, r.DateFrom+" 至 "+r.DateTo)
	case r.DateFrom != "":
		parts = append(parts, r.DateFrom+" 起")
	case r.DateTo != "":
		parts = append(parts, "截至 "+r.DateTo)
	}
	if r.InputAbove != nil {
		parts = append(parts, "输入超过 "+compactTokens(*r.InputAbove))
	}
	switch r.Days {
	case "weekend":
		parts = append(parts, "周末")
	}
	if r.TimeStart != "" {
		parts = append(parts, "北京时间 "+r.TimeStart+"–"+r.TimeEnd)
	}
	return strings.Join(parts, " · ")
}

func compactTokens(n int64) string {
	switch {
	case n >= 1_000_000 && n%100_000 == 0:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', -1, 64) + "M"
	case n >= 1000 && n%1000 == 0:
		return strconv.FormatInt(n/1000, 10) + "K"
	}
	return strconv.FormatInt(n, 10)
}
