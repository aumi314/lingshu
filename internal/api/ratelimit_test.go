package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	return resp
}

// 突发上限之内都放行，超了按令牌桶的速率等。
func TestRateLimiterBurstAndRecovery(t *testing.T) {
	l := newRateLimiter(limitPerMinute, limitBurst)
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := range limitBurst {
		if ok, _ := l.allow("1.1.1.1"); !ok {
			t.Fatalf("第 %d 次请求不该被拒", i+1)
		}
	}
	ok, wait := l.allow("1.1.1.1")
	if ok {
		t.Fatal("超出突发上限应被拒")
	}
	if wait < time.Second || wait > 3*time.Second {
		t.Errorf("等待时间应接近 2 秒，实际 %v", wait)
	}
	if ok, _ := l.allow("1.1.1.1"); ok {
		t.Error("令牌没补回来前不该放行")
	}
	now = now.Add(2 * time.Second)
	if ok, _ := l.allow("1.1.1.1"); !ok {
		t.Error("补回一个令牌后应放行")
	}
}

// 不同 IP 各算各的。
func TestRateLimiterIPIsolation(t *testing.T) {
	l := newRateLimiter(limitPerMinute, limitBurst)
	for range limitBurst {
		l.allow("1.1.1.1")
	}
	if ok, _ := l.allow("1.1.1.1"); ok {
		t.Fatal("1.1.1.1 的配额应已用完")
	}
	if ok, _ := l.allow("2.2.2.2"); !ok {
		t.Error("别的 IP 不该受影响")
	}
}

// 长期不来的 IP 记录要清掉，内存不一直涨。
func TestRateLimiterIdleCleanup(t *testing.T) {
	l := newRateLimiter(limitPerMinute, limitBurst)
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	l.allow("1.1.1.1")
	now = now.Add(limitIdle + time.Minute)
	l.allow("2.2.2.2") // 这次调用顺手清理
	if len(l.buckets) != 1 {
		t.Errorf("过期的 IP 记录应被清掉，实际还剩 %d 个", len(l.buckets))
	}
}

// 登录限流在查库之前生效：超出配额的请求直接 429，不会走到数据库。
func TestLoginRateLimitedBeforeStore(t *testing.T) {
	srv := &Server{guard: newLoginGuard()} // Store 是空的：走到数据库就会 panic
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", srv.login)
	mux.HandleFunc("POST /api/auth/challenge", srv.challenge)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 内容不对的请求也计数，但不会碰数据库。
	for i := range limitBurst {
		resp := postJSON(t, ts.URL+"/api/auth/login", "{")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("第 %d 次请求应返回 400，实际 %d", i+1, resp.StatusCode)
		}
	}
	resp := postJSON(t, ts.URL+"/api/auth/login", `{"username":"bob","password":"x"}`)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("超出配额应返回 429，实际 %d %s", resp.StatusCode, raw)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 应带 Retry-After")
	}
	if !strings.Contains(string(raw), "请求过于频繁") {
		t.Errorf("429 应带上服务端提示，实际 %s", raw)
	}

	// 别的 IP 不受影响。
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/login", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", "8.8.8.8")
	other, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, other.Body)
	other.Body.Close()
	if other.StatusCode != http.StatusBadRequest {
		t.Errorf("别的 IP 应照常处理，实际 %d", other.StatusCode)
	}

	// challenge 用自己的桶：登录被限流不影响它。
	for i := range limitBurst {
		r := postJSON(t, ts.URL+"/api/auth/challenge", "{}")
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次 challenge 应返回 200，实际 %d", i+1, r.StatusCode)
		}
	}
	r := postJSON(t, ts.URL+"/api/auth/challenge", "{}")
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusTooManyRequests {
		t.Errorf("challenge 超出配额应返回 429，实际 %d", r.StatusCode)
	}
}

// 已消费的题目要按期清掉，不然 usedPow 会一直涨。
func TestUsedPowCleanup(t *testing.T) {
	g := newLoginGuard()
	now := fakeClock(g)
	tok := g.newChallenge()
	if !g.checkPow(tok, solve(tok)) {
		t.Fatal("正确答案应通过")
	}
	if len(g.usedPow) != 1 {
		t.Fatalf("已消费的题目应被记下，实际 %d", len(g.usedPow))
	}
	*now = now.Add(powTTL + time.Minute)
	g.newChallenge() // 再出一道题，顺手清理
	if len(g.usedPow) != 0 {
		t.Errorf("过期的已消费题目应被清掉，实际 %d", len(g.usedPow))
	}
}
