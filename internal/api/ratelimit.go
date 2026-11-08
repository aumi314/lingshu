package api

import (
	"sync"
	"time"
)

// 登录限流：登录和 challenge 各一个令牌桶，按真实客户端 IP 计数。
// 配额在进数据库和 bcrypt 之前就扣掉，所以所有请求都算数（包括密码写错的）。
// 桶在内存里，重启清零；单实例部署够用。
const (
	limitPerMinute = 30               // 每个 IP 每分钟 30 次
	limitBurst     = 10               // 突发上限 10 次
	limitIdle      = 10 * time.Minute // 一个 IP 这么久没来就清掉它的记录
)

type bucket struct {
	tokens float64
	last   time.Time // 上次补充令牌的时间
	seen   time.Time // 上次访问时间，用来清掉长期不来的 IP
}

// rateLimiter 是按 key（客户端 IP）计数的令牌桶。
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // 每秒补充多少令牌
	burst     float64
	lastSweep time.Time
	now       func() time.Time
}

func newRateLimiter(perMinute, burst int) *rateLimiter {
	return &rateLimiter{
		buckets: map[string]*bucket{},
		rate:    float64(perMinute) / 60,
		burst:   float64(burst),
		now:     time.Now,
	}
}

// allow 记一次请求。配额内返回 true；超了返回 false 和还要等多久才有下一个令牌。
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	b.seen = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep 每分钟最多清一次，把长期不来的 IP 的记录删掉，防止内存一直涨。调用方持有锁。
func (l *rateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.seen) > limitIdle {
			delete(l.buckets, k)
		}
	}
}
