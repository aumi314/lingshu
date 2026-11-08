package router

import (
	"net/http"
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

// 单把 key：429 按 Retry-After 冷却（有上限、没给按默认），失效 30 分钟后自动再试，成功清零。
func TestKeyHealth(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	k := NewKeyHealth()
	k.now = func() time.Time { return now }
	k.Fail(1, http.StatusTooManyRequests, 0, "限流")
	if k.Usable(1) || k.Status(1).State != "cooling" || !k.Usable(2) {
		t.Fatal("429 后这把 key 应冷却，别的 key 不受影响")
	}
	now = now.Add(KeyLimitDefault)
	if !k.Usable(1) || k.Status(1).State != "ok" {
		t.Fatal("没给 Retry-After 时默认冷却到期后恢复")
	}
	k.Fail(1, http.StatusTooManyRequests, time.Hour, "限流")
	now = now.Add(KeyLimitMax)
	if !k.Usable(1) {
		t.Fatal("Retry-After 再长也最多冷却 KeyLimitMax")
	}
	k.Fail(2, http.StatusUnauthorized, 0, "key 无效")
	if k.Usable(2) || k.Status(2).State != "invalid" {
		t.Fatal("401 标为失效")
	}
	now = now.Add(KeyInvalidRecheck)
	if !k.Usable(2) {
		t.Fatal("失效 30 分钟后自动再试")
	}
	k.Fail(3, http.StatusForbidden, 0, "欠费")
	k.OK(3)
	if !k.Usable(3) || k.Status(3).State != "ok" {
		t.Fatal("成功后清掉失效标记")
	}
	if !KeyFailure(429) || !KeyFailure(401) || !KeyFailure(402) || !KeyFailure(403) || KeyFailure(500) || KeyFailure(404) || KeyFailure(400) {
		t.Fatal("只有限流、失效、欠费算 key 自己的问题")
	}
}
