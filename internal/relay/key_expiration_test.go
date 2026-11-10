package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIKeyExpiration(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.upstream = func(http.ResponseWriter, *http.Request) { t.Error("expired key reached upstream") }
	setExpiry := func(exp *time.Time) {
		t.Helper()
		if _, err := e.store.Pool.Exec(ctx, `UPDATE api_keys SET expires_at=$1`, exp); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", e.gateway.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+e.key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	for _, exp := range []*time.Time{nil, func() *time.Time { v := time.Now().Add(time.Hour); return &v }()} {
		setExpiry(exp)
		for _, path := range []string{"/v1/models", "/v1/usage"} {
			if code := get(path); code != 200 {
				t.Fatalf("valid expiry %v %s: %d", exp, path, code)
			}
		}
	}
	past := time.Now().Add(-time.Second)
	setExpiry(&past)
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses", "/v1/systemone"} {
		resp := e.post(path, `{"model":"std-chat","messages":[{"role":"user","content":"hi"}]}`, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 401 || !strings.Contains(string(body), "API key 已过期") {
			t.Fatalf("expired %s: %d %s", path, resp.StatusCode, body)
		}
	}
	for _, path := range []string{"/v1/models", "/v1/usage"} {
		if code := get(path); code != 401 {
			t.Fatalf("expired %s: %d", path, code)
		}
	}
	var balance, reserved, used string
	var logs int
	if err := e.store.Pool.QueryRow(ctx, `SELECT balance::text,reserved::text,(SELECT quota_used::text FROM api_keys LIMIT 1),(SELECT count(*) FROM request_logs) FROM users WHERE id=1`).Scan(&balance, &reserved, &used, &logs); err != nil {
		t.Fatal(err)
	}
	if balance != "10.0000000000" || reserved != "0.0000000000" || used != "0.0000000000" || logs != 0 {
		t.Fatalf("rejected key billed: balance=%s reserved=%s used=%s logs=%d", balance, reserved, used, logs)
	}
	// 重新设置或清空后下一次请求恢复，鉴权不缓存旧的有效期。
	future := time.Now().Add(time.Hour)
	for _, exp := range []*time.Time{&future, nil} {
		setExpiry(exp)
		if code := get("/v1/usage"); code != 200 {
			t.Fatalf("restore expiry %v: %d", exp, code)
		}
	}
}

func TestAPIKeyExpirationBetweenCalls(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var calls atomic.Int64
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}
	if _, err := e.store.Pool.Exec(ctx, `UPDATE api_keys SET expires_at=now()+interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","messages":[]}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || calls.Load() != 1 {
		t.Fatalf("future expiry rejected valid call: %d calls=%d", resp.StatusCode, calls.Load())
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	var before, after string
	if err := e.store.Pool.QueryRow(ctx, `SELECT balance::text FROM users WHERE id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == "10.0000000000" {
		t.Fatal("successful call was not billed")
	}
	if _, err := e.store.Pool.Exec(ctx, `UPDATE api_keys SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	resp = e.post("/v1/chat/completions", `{"model":"std-chat","messages":[]}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 401 || calls.Load() != 1 {
		t.Fatalf("expired call: %d calls=%d", resp.StatusCode, calls.Load())
	}
	if err := e.store.Pool.QueryRow(ctx, `SELECT balance::text FROM users WHERE id=1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("expired request changed balance: %s -> %s", before, after)
	}
	// WebSocket 升级前就拒绝过期 key。
	req := httptest.NewRequest("GET", "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	recorder := httptest.NewRecorder()
	e.handler.ServeWebSocket()(recorder, req)
	if recorder.Code != 401 || !strings.Contains(recorder.Body.String(), "API key 已过期") {
		t.Fatalf("expired WebSocket handshake: %d %s", recorder.Code, recorder.Body.String())
	}
}
