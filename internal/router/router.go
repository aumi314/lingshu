// Package router 负责渠道健康状态和重试决策。状态只放内存（MVP 单实例部署）。
package router

import (
	"net/http"
	"sync"
	"time"
)

const (
	FailureThreshold = 3                // 连续失败几次进入冷却
	CooldownDuration = 5 * time.Minute  // 冷却多久
	RetryBudget      = 60 * time.Second // 一个请求换渠道、换 key 重试的总时限（重试次数由服务分组设置）
)

// Outcome 是一次尝试对渠道健康的影响。
type Outcome int

const (
	Neutral   Outcome = iota // 和渠道无关（请求本身有问题、用户断开），不影响健康
	Success                  // 成功，清零失败计数
	Failure                  // 渠道失败，累计
	Unhealthy                // 渠道明显坏了（key 失效、欠费），立刻冷却
)

// Classify 根据上游 HTTP 状态码决定是否换渠道重试，以及对渠道健康的影响。
// status 为 0 表示没拿到响应（连不上、超时）。
func Classify(status int) (retry bool, outcome Outcome) {
	switch {
	case status == 0:
		return true, Failure
	case status == http.StatusUnauthorized, status == http.StatusForbidden, status == http.StatusPaymentRequired:
		return true, Unhealthy
	case status == http.StatusTooManyRequests, status >= 500:
		return true, Failure
	case status == http.StatusNotFound:
		// 多半是这个渠道上模型名映射不对，换渠道可能就好了
		return true, Failure
	case status >= 400:
		// 400、413、422 等：请求本身的问题（参数错、上下文超长、内容审核），换渠道结果一样
		return false, Neutral
	}
	return false, Success
}

type health struct {
	fails int
	until time.Time
}

// Health 记录每个渠道的连续失败次数和冷却截止时间。
type Health struct {
	mu    sync.Mutex
	state map[int64]*health
	now   func() time.Time
}

func NewHealth() *Health {
	return &Health{state: map[int64]*health{}, now: time.Now}
}

func (h *Health) Record(channelID int64, o Outcome) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state[channelID]
	if s == nil {
		s = &health{}
		h.state[channelID] = s
	}
	switch o {
	case Success:
		s.fails, s.until = 0, time.Time{}
	case Failure:
		s.fails++
		if s.fails >= FailureThreshold {
			s.until = h.now().Add(CooldownDuration)
		}
	case Unhealthy:
		s.fails = FailureThreshold
		s.until = h.now().Add(CooldownDuration)
	}
}

// Cooling 表示渠道是否在冷却中。
func (h *Health) Cooling(channelID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state[channelID]
	return s != nil && h.now().Before(s.until)
}

// ---- 渠道内的单把 key（docs/decisions.md「渠道多 key 与渠道备注」） ----

const (
	KeyLimitDefault   = 60 * time.Second // 429 没给 Retry-After 时这把 key 冷却多久
	KeyLimitMax       = 10 * time.Minute // Retry-After 的上限：上游给得再长也最多冷却这么久，到点再试
	KeyInvalidRecheck = 30 * time.Minute // 401 / 402 / 403 标为失效，多久后自动再试一次
)

// KeyFailure 判断这个上游状态是不是这把 key 自己的问题（限流、失效、欠费）：是的话同渠道换一把 key 重试，不算渠道失败。
func KeyFailure(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden:
		return true
	}
	return false
}

type keyState struct {
	until   time.Time
	invalid bool
	reason  string
}

// KeyHealth 记录每把 key 的冷却和失效状态，只放内存（单实例部署，重启清零）。
type KeyHealth struct {
	mu    sync.Mutex
	state map[int64]*keyState
	now   func() time.Time
}

func NewKeyHealth() *KeyHealth {
	return &KeyHealth{state: map[int64]*keyState{}, now: time.Now}
}

// Fail 记一次这把 key 自己的失败：429 冷却 retryAfter（0 表示没给，按默认），其余标为失效。
func (k *KeyHealth) Fail(id int64, status int, retryAfter time.Duration, reason string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	s := &keyState{reason: reason}
	if status == http.StatusTooManyRequests {
		if retryAfter <= 0 {
			retryAfter = KeyLimitDefault
		}
		s.until = k.now().Add(min(retryAfter, KeyLimitMax))
	} else {
		s.invalid = true
		s.until = k.now().Add(KeyInvalidRecheck)
	}
	k.state[id] = s
}

// MinCooldown 返回这些 key 里限流冷却中（429）最早恢复的剩余时间；没有一把在限流冷却时 ok 为 false（可能都是失效的）。
func (k *KeyHealth) MinCooldown(ids []int64) (time.Duration, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	best, ok := time.Duration(0), false
	for _, id := range ids {
		s := k.state[id]
		if s == nil || s.invalid || !now.Before(s.until) {
			continue
		}
		if d := s.until.Sub(now); !ok || d < best {
			best, ok = d, true
		}
	}
	return best, ok
}

// OK 这把 key 刚成功过：清掉冷却和失效标记。
func (k *KeyHealth) OK(id int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.state, id)
}

// Usable 表示这把 key 现在可以用（没在冷却，失效的也已到复查时间）。
func (k *KeyHealth) Usable(id int64) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	s := k.state[id]
	return s == nil || !k.now().Before(s.until)
}

// KeyStatus 是给管理端看的单把 key 状态。
type KeyStatus struct {
	State  string     `json:"state"` // ok、cooling（限流冷却中）、invalid（失效，到点自动再试）
	Until  *time.Time `json:"until,omitempty"`
	Reason string     `json:"reason,omitempty"`
}

func (k *KeyHealth) Status(id int64) KeyStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	s := k.state[id]
	if s == nil || !k.now().Before(s.until) {
		return KeyStatus{State: "ok"}
	}
	st := KeyStatus{State: "cooling", Until: &s.until, Reason: s.reason}
	if s.invalid {
		st.State = "invalid"
	}
	return st
}
