package router

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		status  int
		retry   bool
		outcome Outcome
	}{
		{0, true, Failure},
		{401, true, Unhealthy},
		{402, true, Unhealthy},
		{403, true, Unhealthy},
		{404, true, Failure},
		{429, true, Failure},
		{500, true, Failure},
		{503, true, Failure},
		{400, false, Neutral},
		{413, false, Neutral},
		{422, false, Neutral},
		{200, false, Success},
	}
	for _, c := range cases {
		retry, outcome := Classify(c.status)
		if retry != c.retry || outcome != c.outcome {
			t.Errorf("Classify(%d) = %v, %v，期望 %v, %v", c.status, retry, outcome, c.retry, c.outcome)
		}
	}
}

func TestCooldown(t *testing.T) {
	now := time.Unix(0, 0)
	h := NewHealth()
	h.now = func() time.Time { return now }

	h.Record(1, Failure)
	h.Record(1, Failure)
	if h.Cooling(1) {
		t.Fatal("失败 2 次不应冷却")
	}
	h.Record(1, Success)
	h.Record(1, Failure)
	h.Record(1, Failure)
	if h.Cooling(1) {
		t.Fatal("成功一次后失败计数应清零")
	}
	h.Record(1, Failure)
	if !h.Cooling(1) {
		t.Fatal("连续失败 3 次应冷却")
	}
	now = now.Add(CooldownDuration)
	if h.Cooling(1) {
		t.Fatal("冷却时间到了应恢复")
	}

	h.Record(2, Unhealthy)
	if !h.Cooling(2) {
		t.Fatal("401/403 应立刻冷却")
	}
	h.Record(3, Neutral)
	h.Record(3, Neutral)
	h.Record(3, Neutral)
	if h.Cooling(3) {
		t.Fatal("和渠道无关的结果不应影响健康")
	}
}

func TestOrderMovesCoolingToEnd(t *testing.T) {
	h := NewHealth()
	h.Record(1, Unhealthy)
	got := Order(h, []int64{1, 2, 3}, func(id int64) int64 { return id })
	if got[0] != 2 || got[1] != 3 || got[2] != 1 {
		t.Errorf("顺序 = %v，期望 [2 3 1]", got)
	}
	h.Record(2, Unhealthy)
	h.Record(3, Unhealthy)
	if got := Order(h, []int64{1, 2, 3}, func(id int64) int64 { return id }); len(got) != 3 {
		t.Errorf("全部冷却时也要保留所有渠道，实际 %v", got)
	}
}
