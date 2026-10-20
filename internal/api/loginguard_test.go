package api

import (
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeClock(g *loginGuard) *time.Time {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	return &now
}

// solve 和前端一样暴力找 nonce。
func solve(token string) string {
	for i := 0; ; i++ {
		n := strconv.Itoa(i)
		if leadingZeroBits(sha256.Sum256([]byte(token+":"+n))) >= powDifficulty {
			return n
		}
	}
}

func TestGuardCaptchaThenLock(t *testing.T) {
	g := newLoginGuard()
	now := fakeClock(g)
	if g.needCaptcha("bob", "1.1.1.1") {
		t.Fatal("没失败过不应要求验证")
	}
	g.fail("bob", "1.1.1.1")
	if g.needCaptcha("bob", "1.1.1.1") {
		t.Error("失败 1 次不应要求验证")
	}
	if !g.fail("Bob", "1.1.1.1") {
		t.Error("同一用户名（不分大小写）失败 2 次应要求验证")
	}
	if g.needCaptcha("alice", "2.2.2.2") {
		t.Error("别的用户和 IP 不受影响")
	}
	for range 3 {
		g.fail("bob", "1.1.1.1")
	}
	if g.lockedFor("bob", "9.9.9.9") <= 0 {
		t.Error("失败 5 次应锁定用户名，换 IP 也不行")
	}
	*now = now.Add(guardWindow + time.Second)
	if g.lockedFor("bob", "1.1.1.1") > 0 {
		t.Error("锁定时间过后应解锁")
	}
	if !g.needCaptcha("bob", "1.1.1.1") {
		t.Error("刚解锁仍应要求验证")
	}
	g.succeed("bob")
	if g.needCaptcha("bob", "3.3.3.3") {
		t.Error("登录成功后清掉用户名的记录")
	}
	*now = now.Add(2 * guardWindow)
	if g.needCaptcha("bob", "1.1.1.1") {
		t.Error("窗口过后 IP 记录也过期")
	}
}

func TestGuardIPLock(t *testing.T) {
	g := newLoginGuard()
	fakeClock(g)
	for i := range ipLockAfter {
		g.fail("user"+strconv.Itoa(i), "1.1.1.1")
	}
	if g.lockedFor("someone-new", "1.1.1.1") <= 0 {
		t.Error("同一 IP 换着用户名猜也应被锁")
	}
}

func TestPow(t *testing.T) {
	g := newLoginGuard()
	now := fakeClock(g)
	tok := g.newChallenge()
	nonce := solve(tok)
	if g.checkPow(tok, "0") && nonce != "0" {
		t.Error("错误答案不应通过")
	}
	if !g.checkPow(tok, nonce) {
		t.Fatal("正确答案应通过")
	}
	if g.checkPow(tok, nonce) {
		t.Error("同一道题不能用两次")
	}
	forged := strings.Replace(g.newChallenge(), ".", "x.", 1)
	if g.checkPow(forged, solve(forged)) {
		t.Error("签名不对的题目不应通过")
	}
	other := newLoginGuard()
	fakeClock(other)
	if t2 := other.newChallenge(); g.checkPow(t2, solve(t2)) {
		t.Error("别的密钥签的题目不应通过")
	}
	tok = g.newChallenge()
	nonce = solve(tok)
	*now = now.Add(powTTL + time.Second)
	if g.checkPow(tok, nonce) {
		t.Error("过期的题目不应通过")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Real-IP", "8.8.8.8")
	if ip := clientIP(r); ip != "8.8.8.8" {
		t.Errorf("Nginx 转发来的请求应取 X-Real-IP，得到 %s", ip)
	}
	r.RemoteAddr = "5.5.5.5:5000"
	if ip := clientIP(r); ip != "5.5.5.5" {
		t.Errorf("直连时不信 X-Real-IP，得到 %s", ip)
	}
}

func TestLoginCaptchaFlow(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	wrong := map[string]string{"username": "bob", "password": "wrong"}
	c.do("POST", "/api/auth/login", wrong, nil)
	code, body := c.do("POST", "/api/auth/login", wrong, nil)
	if code != 401 || !strings.Contains(body, `"captcha":true`) {
		t.Fatalf("失败 2 次后应提示下次要验证: %d %s", code, body)
	}
	right := map[string]string{"username": "bob", "password": "password-1"}
	if code, body := c.do("POST", "/api/auth/login", right, nil); code != 428 {
		t.Fatalf("不带验证应返回 428，实际 %d %s", code, body)
	}
	var ch struct {
		Token      string `json:"token"`
		Difficulty int    `json:"difficulty"`
	}
	c.do("POST", "/api/auth/challenge", nil, &ch)
	if ch.Difficulty != powDifficulty {
		t.Fatalf("challenge = %+v", ch)
	}
	right["captcha_token"], right["captcha_nonce"] = ch.Token, solve(ch.Token)
	if code, body := c.do("POST", "/api/auth/login", right, nil); code != 200 {
		t.Fatalf("带正确验证应能登录: %d %s", code, body)
	}

	// 锁定后正确密码也进不去
	for range userLockAfter {
		_, raw := c.do("POST", "/api/auth/challenge", nil, nil)
		json.Unmarshal([]byte(raw), &ch)
		wrong["captcha_token"], wrong["captcha_nonce"] = ch.Token, solve(ch.Token)
		c.do("POST", "/api/auth/login", wrong, nil)
	}
	delete(right, "captcha_token")
	if code, body := c.do("POST", "/api/auth/login", right, nil); code != 429 || !strings.Contains(body, "分钟后再试") {
		t.Errorf("锁定后应返回 429: %d %s", code, body)
	}
}
