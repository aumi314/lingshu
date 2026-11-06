package relay

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/aumi314/lingshu/internal/store"
)

// limiter 在进程内按用户限制并发数和每分钟请求数（单进程部署，规则见 docs/decisions.md「预扣费与调用限制」）。
type limiter struct {
	mu     sync.Mutex
	active map[int64]int
	recent map[int64][]time.Time // 最近一分钟内放行的请求时间

	cached   store.CallLimits
	cachedAt time.Time
}

func newLimiter() *limiter {
	return &limiter{active: map[int64]int{}, recent: map[int64][]time.Time{}}
}

// limits 读系统设置里的限额，缓存 10 秒，避免每个请求多查一次库。
func (l *limiter) limits(ctx context.Context, s *store.Store) store.CallLimits {
	l.mu.Lock()
	if time.Since(l.cachedAt) < 10*time.Second {
		c := l.cached
		l.mu.Unlock()
		return c
	}
	l.mu.Unlock()
	c, err := s.CallLimits(ctx)
	if err != nil {
		c = store.DefaultCallLimits
	}
	l.mu.Lock()
	l.cached, l.cachedAt = c, time.Now()
	l.mu.Unlock()
	return c
}

// acquire 尝试为用户占一个名额。被拒时返回给用户的原因和建议多少秒后重试；成功时返回释放函数。
func (l *limiter) acquire(userID int64, c store.CallLimits, now time.Time) (release func(), reason string, retryAfter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c.Concurrency > 0 && int64(l.active[userID]) >= c.Concurrency {
		return nil, "同时进行的请求太多：你已有 " + strconv.Itoa(l.active[userID]) + " 个请求在处理，上限 " + strconv.FormatInt(c.Concurrency, 10) + " 个，等前面的完成后再试", 2
	}
	if c.RPM > 0 {
		window := l.recent[userID][:0]
		for _, t := range l.recent[userID] {
			if now.Sub(t) < time.Minute {
				window = append(window, t)
			}
		}
		l.recent[userID] = window
		if int64(len(window)) >= c.RPM {
			wait := int(time.Minute-now.Sub(window[0]))/int(time.Second) + 1
			return nil, "请求太频繁：每分钟最多 " + strconv.FormatInt(c.RPM, 10) + " 个请求，约 " + strconv.Itoa(wait) + " 秒后可以继续", wait
		}
		l.recent[userID] = append(window, now)
	}
	l.active[userID]++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.active[userID]--; l.active[userID] <= 0 {
			delete(l.active, userID)
		}
	}, "", 0
}
