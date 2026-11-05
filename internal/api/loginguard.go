package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/bits"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 登录防护：按用户名和 IP 统计失败次数。失败几次后要求人机验证（工作量证明），
// 失败更多次就锁一段时间。只存在内存里，重启后清零；单实例部署够用。
const (
	guardWindow      = 15 * time.Minute // 失败次数统计窗口，也是锁定时长
	userCaptchaAfter = 2                // 同一用户名失败这么多次后要求人机验证
	userLockAfter    = 5                // 同一用户名失败这么多次后锁定
	ipCaptchaAfter   = 5                // 同一 IP（团队可能共用出口）阈值放宽
	ipLockAfter      = 30
	powDifficulty    = 16 // 哈希前导零的位数；浏览器平均要算 2^16 次，约 0.5–2 秒
	powTTL           = 5 * time.Minute
)

type failRecord struct {
	count       int
	first       time.Time
	lockedUntil time.Time
}

type loginGuard struct {
	mu        sync.Mutex
	fails     map[string]*failRecord // "u:用户名" 或 "ip:地址"
	usedPow   map[string]time.Time   // 已用过的题目，过期前不能再用
	secret    []byte
	lastSweep time.Time
	now       func() time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		fails:   map[string]*failRecord{},
		usedPow: map[string]time.Time{},
		secret:  []byte(rand.Text()),
		now:     time.Now,
	}
}

func userKey(username string) string { return "u:" + strings.ToLower(username) }
func ipKey(ip string) string         { return "ip:" + ip }

// record 返回窗口内仍有效的记录，过期的当作没有。调用方持有锁。
func (g *loginGuard) record(key string) *failRecord {
	r := g.fails[key]
	if r == nil {
		return nil
	}
	now := g.now()
	if now.Sub(r.first) > guardWindow && now.After(r.lockedUntil) {
		delete(g.fails, key)
		return nil
	}
	return r
}

// lockedFor 返回还要锁多久，0 表示没锁。
func (g *loginGuard) lockedFor(username, ip string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	var wait time.Duration
	for _, k := range []string{userKey(username), ipKey(ip)} {
		if r := g.record(k); r != nil {
			if d := r.lockedUntil.Sub(g.now()); d > wait {
				wait = d
			}
		}
	}
	return wait
}

func (g *loginGuard) needCaptcha(username, ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	u, i := g.record(userKey(username)), g.record(ipKey(ip))
	return (u != nil && u.count >= userCaptchaAfter) || (i != nil && i.count >= ipCaptchaAfter)
}

// fail 记一次失败；返回下次登录是否需要人机验证。
func (g *loginGuard) fail(username, ip string) bool {
	g.mu.Lock()
	now := g.now()
	g.sweep(now)
	for _, k := range []string{userKey(username), ipKey(ip)} {
		r := g.record(k)
		if r == nil {
			r = &failRecord{first: now}
			g.fails[k] = r
		}
		r.count++
		lockAfter, captchaAfter := userLockAfter, userCaptchaAfter
		if strings.HasPrefix(k, "ip:") {
			lockAfter, captchaAfter = ipLockAfter, ipCaptchaAfter
		}
		if r.count >= lockAfter {
			// 解锁后仍要人机验证，再错几次又会锁上
			r.lockedUntil = now.Add(guardWindow)
			r.count, r.first = captchaAfter, r.lockedUntil
		}
	}
	g.mu.Unlock()
	return g.needCaptcha(username, ip)
}

// succeed 清掉这个用户名的失败记录。IP 的记录保留，防止用自己的账号登录来给 IP 清零。
func (g *loginGuard) succeed(username string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, userKey(username))
}

// sweep 每分钟最多清理一次过期记录，防止内存一直涨。调用方持有锁。
func (g *loginGuard) sweep(now time.Time) {
	if now.Sub(g.lastSweep) < time.Minute {
		return
	}
	g.lastSweep = now
	for k := range g.fails {
		g.record(k)
	}
	for k, exp := range g.usedPow {
		if now.After(exp) {
			delete(g.usedPow, k)
		}
	}
}

// ---- 工作量证明 ----
// 题目是 "随机串.过期时间.签名"。浏览器找一个数字 nonce，让 sha256(题目 + ":" + nonce)
// 的前 powDifficulty 位都是 0。服务端不用存题目，靠签名确认是自己发的。

func (g *loginGuard) sign(payload string) string {
	m := hmac.New(sha256.New, g.secret)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func (g *loginGuard) newChallenge() string {
	b := make([]byte, 12)
	rand.Read(b)
	payload := base64.RawURLEncoding.EncodeToString(b) + "." + strconv.FormatInt(g.now().Add(powTTL).Unix(), 10)
	return payload + "." + g.sign(payload)
}

// checkPow 验证答案，每道题只能用一次。
func (g *loginGuard) checkPow(challenge, nonce string) bool {
	parts := strings.Split(challenge, ".")
	if len(parts) != 3 || nonce == "" || len(nonce) > 20 {
		return false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(g.sign(payload)), []byte(parts[2])) {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || g.now().Unix() > exp {
		return false
	}
	if leadingZeroBits(sha256.Sum256([]byte(challenge+":"+nonce))) < powDifficulty {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, used := g.usedPow[challenge]; used {
		return false
	}
	g.usedPow[challenge] = time.Unix(exp, 0)
	return true
}

func leadingZeroBits(sum [32]byte) int {
	n := 0
	for _, b := range sum {
		if b != 0 {
			return n + bits.LeadingZeros8(b)
		}
		n += 8
	}
	return n
}
