// Package router 负责渠道健康状态和重试决策。状态只放内存（MVP 单实例部署）。
package router

import (
	"net/http"
	"sync"
	"time"
)

const (
	FailureThreshold = 3               // 连续失败几次进入冷却
	CooldownDuration = 5 * time.Minute // 冷却多久
	MaxAttempts      = 3               // 一个请求最多试几个渠道
	RetryBudget      = 60 * time.Second
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

// Order 把冷却中的渠道挪到最后，其余保持原顺序（数据库已按优先级排好、同档随机）。
// 冷却中的渠道不直接剔除：所有渠道都在冷却时仍然要试，否则故障恢复后还要白等 5 分钟。
func Order[T any](h *Health, routes []T, id func(T) int64) []T {
	out := make([]T, 0, len(routes))
	var cooling []T
	for _, r := range routes {
		if h.Cooling(id(r)) {
			cooling = append(cooling, r)
		} else {
			out = append(out, r)
		}
	}
	return append(out, cooling...)
}
