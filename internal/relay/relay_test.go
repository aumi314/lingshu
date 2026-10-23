package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

// env 是一套完整的测试环境：测试库 + 模拟上游 + 灵枢网关。
type env struct {
	t        *testing.T
	store    *store.Store
	gateway  *httptest.Server
	upstream http.HandlerFunc // 每个测试自己设置上游行为
	key      string
	handler  *Handler
}

const upstreamKey = "upstream-secret"

func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("LINGSHU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("没有设置 LINGSHU_TEST_DATABASE_URL，跳过网关测试")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	e := &env{t: t, store: s}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.upstream(w, r) }))
	t.Cleanup(up.Close)

	// 两个渠道：chat 渠道（模型 std-chat → up-chat），messages 渠道（模型 std-msg）
	key, hash, prefix := auth.NewAPIKey()
	e.key = key
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, password_hash, balance) VALUES (1, 'alice', 'x', 10)`, nil},
		{`INSERT INTO user_group_members (user_id, user_group_id) VALUES (1, 1)`, nil},
		{`INSERT INTO groups (id, name) VALUES (1, 'default')`, nil},
		// 价格：输入 $1、输出 $2、缓存读 $0.1 / 每百万 token，算起来好核对
		{`INSERT INTO models (id, name, input_price, output_price, cache_read_price) VALUES (1, 'std-chat', 1, 2, 0.1), (2, 'std-msg', 1, 2, 0.1)`, nil},
		{`INSERT INTO channels (id, name, api_key, base_url_chat, base_url_responses) VALUES (1, 'c-chat', $1, $2, $2)`, []any{upstreamKey, up.URL + "/v1"}},
		{`INSERT INTO channels (id, name, api_key, base_url_messages) VALUES (2, 'c-msg', $1, $2)`, []any{upstreamKey, up.URL + "/anthropic"}},
		{`INSERT INTO channel_groups VALUES (1, 1), (2, 1)`, nil},
		{`INSERT INTO channel_models VALUES (1, 1, 'up-chat'), (2, 2, '')`, nil},
		{`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, group_id) VALUES (1, 'k', $1, $2, 1)`, []any{hash, prefix}},
	} {
		if _, err := s.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}

	mux := http.NewServeMux()
	h := NewHandler(s)
	e.handler = h
	mux.Handle("POST /v1/chat/completions", h.Serve(usage.Chat))
	mux.Handle("POST /v1/messages", h.Serve(usage.Messages))
	mux.Handle("POST /v1/responses", h.Serve(usage.Responses))
	mux.Handle("POST /v1/systemone", h.Serve(usage.SystemOne))
	mux.HandleFunc("GET /v1/models", h.ServeModels)
	e.gateway = httptest.NewServer(mux)
	t.Cleanup(e.gateway.Close)
	return e
}

func (e *env) post(path, body string, header map[string]string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.gateway.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.key)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

// waitLog 等网关写完日志（日志在响应结束后才写）。
func (e *env) waitLog(requestID string) store.RequestLog {
	e.t.Helper()
	var l store.RequestLog
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := e.store.Pool.QueryRow(context.Background(), `
			SELECT status, http_status, error, model, upstream_model, stream,
			       input_tokens, cache_read_tokens, output_tokens, reasoning_tokens, upstream_response_id
			FROM request_logs WHERE request_id = $1`, requestID,
		).Scan(&l.Status, &l.HTTPStatus, &l.Error, &l.Model, &l.UpstreamModel, &l.Stream,
			&l.InputTokens, &l.CacheReadTokens, &l.OutputTokens, &l.ReasoningTokens, &l.UpstreamResponseID)
		if err == nil {
			return l
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("等不到请求 %s 的日志", requestID)
	return l
}

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, ev := range events {
		fmt.Fprintf(w, "data: %s\n\n", ev)
		w.(http.Flusher).Flush()
	}
}

func TestChatNonStreamRewritesModelAndRecordsUsage(t *testing.T) {
	e := newEnv(t)
	upstreamBody := `{"id":"x","model":"up-chat","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}}`
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("上游路径 = %s", r.URL.Path)
		}
		if body["model"] != "up-chat" {
			t.Errorf("model 没换成上游名: %v", body["model"])
		}
		if body["custom_field"] != "keep" {
			t.Error("网关不认识的字段被丢了")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+upstreamKey {
			t.Errorf("上游鉴权头 = %q", got)
		}
		if r.Header.Get("User-Agent") != "test-agent/1.0" {
			t.Errorf("User-Agent 没原样转发: %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, upstreamBody)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","messages":[],"custom_field":"keep"}`, map[string]string{"User-Agent": "test-agent/1.0"})
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(got) != upstreamBody {
		t.Fatalf("响应 %d %s", resp.StatusCode, got)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "success" || l.Model != "std-chat" || l.UpstreamModel != "up-chat" || l.Stream {
		t.Errorf("日志 = %+v", l)
	}
	if l.InputTokens != 6 || l.CacheReadTokens != 4 || l.OutputTokens != 5 {
		t.Errorf("usage = in %d cache %d out %d，期望 6/4/5", l.InputTokens, l.CacheReadTokens, l.OutputTokens)
	}
}

func TestChatStreamInjectsAndStripsUsageChunk(t *testing.T) {
	for _, clientWantsUsage := range []bool{false, true} {
		t.Run(fmt.Sprintf("client_include_usage=%v", clientWantsUsage), func(t *testing.T) {
			e := newEnv(t)
			usageChunk := `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`
			e.upstream = func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					StreamOptions struct {
						IncludeUsage bool `json:"include_usage"`
					} `json:"stream_options"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if !body.StreamOptions.IncludeUsage {
					t.Error("上游没收到 include_usage")
				}
				sse(w, `{"choices":[{"delta":{"content":"a"}}]}`, usageChunk, "[DONE]")
			}
			reqBody := `{"model":"std-chat","stream":true}`
			if clientWantsUsage {
				reqBody = `{"model":"std-chat","stream":true,"stream_options":{"include_usage":true}}`
			}
			resp := e.post("/v1/chat/completions", reqBody, nil)
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(got), usageChunk) != clientWantsUsage {
				t.Errorf("客户端%s要 usage，收到的流：\n%s", map[bool]string{true: "", false: "没"}[clientWantsUsage], got)
			}
			if !strings.Contains(string(got), "[DONE]") {
				t.Error("[DONE] 没转发")
			}
			l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
			if l.Status != "success" || l.InputTokens != 7 || l.OutputTokens != 3 || !l.Stream {
				t.Errorf("日志 = %+v", l)
			}
		})
	}
}

func TestMessagesForwardsQueryAndAnthropicAuth(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anthropic/v1/messages" || r.URL.RawQuery != "beta=true" {
			t.Errorf("上游地址 = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("x-api-key") != upstreamKey || r.Header.Get("Authorization") != "" {
			t.Errorf("messages 应该用 x-api-key 鉴权，且不能泄露客户端的 Authorization")
		}
		if r.Header.Get("anthropic-beta") != "tools-2024" {
			t.Error("anthropic-beta 没转发")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n"+
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":50,\"output_tokens\":9,\"cache_read_input_tokens\":20}}\n\n"+
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}
	// Claude Code 用 x-api-key 带灵枢的 key
	req, _ := http.NewRequest(http.MethodPost, e.gateway.URL+"/v1/messages?beta=true", strings.NewReader(`{"model":"std-msg","stream":true}`))
	req.Header.Set("x-api-key", e.key)
	req.Header.Set("anthropic-beta", "tools-2024")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "success" || l.InputTokens != 50 || l.CacheReadTokens != 20 || l.OutputTokens != 9 {
		t.Errorf("日志 = %+v", l)
	}
}

func TestResponsesRecordsResponseID(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("上游路径 = %s", r.URL.Path)
		}
		sse(w, `{"type":"response.created","response":{"id":"resp_1"}}`,
			`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":12,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":2}}}}`)
	}
	resp := e.post("/v1/responses", `{"model":"std-chat","stream":true,"input":"hi"}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "success" || l.UpstreamResponseID != "resp_1" || l.InputTokens != 12 || l.ReasoningTokens != 2 {
		t.Errorf("日志 = %+v", l)
	}
}

func TestRejections(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) { t.Error("不应该请求上游") }

	// 模型只在 chat 渠道上，却从 messages 入口调：提示换入口，错误格式是 Anthropic 的
	resp := e.post("/v1/messages", `{"model":"std-chat"}`, nil)
	var anthropicErr struct {
		Type  string `json:"type"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&anthropicErr)
	resp.Body.Close()
	if resp.StatusCode != 400 || anthropicErr.Type != "error" || !strings.Contains(anthropicErr.Error.Message, "/v1/chat/completions") {
		t.Errorf("换入口提示 = %d %+v", resp.StatusCode, anthropicErr)
	}
	if l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id")); l.Status != "rejected" {
		t.Errorf("日志状态 = %s", l.Status)
	}

	resp = e.post("/v1/chat/completions", `{"model":"nope"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("不存在的模型应返回 404，实际 %d", resp.StatusCode)
	}

	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, map[string]string{"Authorization": "Bearer sk-ls-wrong"})
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("错误的 key 应返回 401，实际 %d", resp.StatusCode)
	}

	// 两个鉴权头都带：x-api-key 是别处的 key，Bearer 才是灵枢的（Claude Code 同时设了两个环境变量时就是这样）
	resp = e.post("/v1/chat/completions", `{"model":"nope"}`, map[string]string{"x-api-key": "sk-ant-other"})
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("Bearer 里的灵枢 key 应该被认出来（模型不存在返回 404），实际 %d", resp.StatusCode)
	}

	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, map[string]string{"Authorization": "Bearer sk-ls-wrong"})
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("错误的 key 应返回 401，实际 %d", resp.StatusCode)
	}
}

func TestUpstreamErrorsPassThrough(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 429 || !strings.Contains(string(got), "rate limited") {
		t.Errorf("上游错误应原样返回，实际 %d %s", resp.StatusCode, got)
	}
	if l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id")); l.Status != "upstream_error" || l.HTTPStatus != 429 {
		t.Errorf("日志 = %+v", l)
	}
}

func TestTruncatedStreamIsUpstreamError(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"a"}}]}`) // 没有 [DONE] 就断了
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","stream":true}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id")); l.Status != "upstream_error" {
		t.Errorf("截断的流应记为 upstream_error，实际 %+v", l)
	}
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	e := newEnv(t)
	upstreamCancelled := make(chan struct{})
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"a"}}]}`)
		select {
		case <-r.Context().Done():
			close(upstreamCancelled)
		case <-time.After(5 * time.Second):
		}
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","stream":true}`, nil)
	bufio.NewReader(resp.Body).ReadString('\n') // 收到第一块就断开
	resp.Body.Close()

	select {
	case <-upstreamCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("客户端断开后，上游请求没有被取消")
	}
	if l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id")); l.Status != "client_cancelled" {
		t.Errorf("日志状态 = %s，期望 client_cancelled", l.Status)
	}
}

func TestModelsListsGroupModels(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodGet, e.gateway.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Data) != 2 || list.Data[0].ID != "std-chat" || list.Data[1].ID != "std-msg" {
		t.Errorf("模型列表 = %+v", list.Data)
	}
}

// ---- 计费 ----

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.store.Pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) decimal(sql string, args ...any) decimal.Decimal {
	e.t.Helper()
	var s string
	if err := e.store.Pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return decimal.RequireFromString(s)
}

func (e *env) balance() decimal.Decimal {
	return e.decimal(`SELECT balance::text FROM users WHERE id = 1`)
}

// cost 等日志写完后返回这次请求的扣费和是否估算。
func (e *env) cost(requestID string) (decimal.Decimal, bool) {
	e.waitLog(requestID)
	var estimated bool
	e.store.Pool.QueryRow(context.Background(), `SELECT usage_estimated FROM request_logs WHERE request_id = $1`, requestID).Scan(&estimated)
	return e.decimal(`SELECT cost::text FROM request_logs WHERE request_id = $1`, requestID), estimated
}

func okUpstream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":500,"prompt_tokens_details":{"cached_tokens":200}}}`)
}

func TestSuccessChargesBalanceAndKeyQuota(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	// (800×1 + 200×0.1 + 500×2) / 1e6 = 0.00182
	want := decimal.RequireFromString("0.00182")
	if got, est := e.cost(resp.Header.Get("X-Lingshu-Request-Id")); !got.Equal(want) || est {
		t.Fatalf("cost = %s（估算 %v），期望 %s", got, est, want)
	}
	if got := e.balance(); !got.Equal(decimal.NewFromInt(10).Sub(want)) {
		t.Errorf("余额 = %s", got)
	}
	if got := e.decimal(`SELECT quota_used::text FROM api_keys`); !got.Equal(want) {
		t.Errorf("key 已用额度 = %s", got)
	}
}

func TestGroupRatioApplied(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE groups SET ratio = 0.5`)
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if got, _ := e.cost(resp.Header.Get("X-Lingshu-Request-Id")); !got.Equal(decimal.RequireFromString("0.00091")) {
		t.Errorf("倍率 0.5 后 cost = %s，期望 0.00091", got)
	}
}

func TestAdmissionRejections(t *testing.T) {
	cases := []struct {
		name       string
		setup      string
		wantStatus int
		wantMsg    string
	}{
		{"余额为 0", `UPDATE users SET balance = 0`, 402, "余额不足"},
		{"透支后余额为负", `UPDATE users SET balance = -0.01`, 402, "余额不足"},
		{"key 额度用完", `UPDATE api_keys SET quota_limit = 1, quota_used = 1`, 402, "额度已用完"},
		{"特殊分组没有授权", `UPDATE groups SET is_public = false`, 403, "分组"},
		{"服务分组已停用", `UPDATE groups SET enabled = false`, 403, "分组"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.exec(c.setup)
			e.upstream = func(w http.ResponseWriter, r *http.Request) { t.Error("不应该请求上游") }
			resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != c.wantStatus || !strings.Contains(string(body), c.wantMsg) {
				t.Errorf("响应 %d %s", resp.StatusCode, body)
			}
			if l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id")); l.Status != "rejected" {
				t.Errorf("日志状态 = %s", l.Status)
			}
		})
	}

	// 授权了特殊分组就能用
	e := newEnv(t)
	e.exec(`UPDATE groups SET is_public = false`)
	e.exec(`INSERT INTO group_user_groups VALUES (1, 1)`)
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("授权后应该能用，实际 %d", resp.StatusCode)
	}
}

func TestOverdraftAllowedOnce(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE users SET balance = 0.001`) // 不够这次请求的 0.00182
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("余额为正时应放行，实际 %d", resp.StatusCode)
	}
	e.cost(resp.Header.Get("X-Lingshu-Request-Id"))
	if got := e.balance(); !got.Equal(decimal.RequireFromString("-0.00082")) {
		t.Errorf("透支后余额 = %s，期望 -0.00082", got)
	}
	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 402 {
		t.Errorf("透支后下一次请求应被拒绝，实际 %d", resp.StatusCode)
	}
}

func TestNoChargeOnUpstreamFailures(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"上游 429": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"slow down"}}`)
		},
		"流被截断": func(w http.ResponseWriter, r *http.Request) {
			sse(w, `{"choices":[{"delta":{"content":"partial answer"}}]}`)
		},
		"流里报错": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"r\",\"usage\":{\"input_tokens\":100,\"output_tokens\":10}}}\n\n")
		},
	}
	for name, up := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.upstream = up
			path := "/v1/chat/completions"
			if name == "流里报错" {
				path = "/v1/responses"
			}
			resp := e.post(path, `{"model":"std-chat","stream":true}`, nil)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if got, _ := e.cost(resp.Header.Get("X-Lingshu-Request-Id")); !got.IsZero() {
				t.Errorf("上游失败不应扣费，实际扣了 %s", got)
			}
			if !e.balance().Equal(decimal.NewFromInt(10)) {
				t.Errorf("余额变了: %s", e.balance())
			}
		})
	}
}

func TestClientDisconnectChargesEstimate(t *testing.T) {
	e := newEnv(t)
	done := make(chan struct{})
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		sse(w, `{"choices":[{"delta":{"content":"`+strings.Repeat("word ", 200)+`"}}]}`) // 约 250 token
		<-r.Context().Done()
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","stream":true,"messages":[{"role":"user","content":"`+strings.Repeat("x", 400)+`"}]}`, nil)
	bufio.NewReader(resp.Body).ReadString('\n')
	resp.Body.Close()
	<-done
	got, estimated := e.cost(resp.Header.Get("X-Lingshu-Request-Id"))
	if !estimated || !got.IsPositive() {
		t.Fatalf("用户断开应按估算扣费，实际 cost=%s 估算=%v", got, estimated)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.InputTokens < 90 || l.OutputTokens < 200 {
		t.Errorf("估算偏离太多: 输入 %d（约 101），输出 %d（约 250）", l.InputTokens, l.OutputTokens)
	}
}

func TestSuccessWithoutUsageIsEstimated(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"你好世界"}}]}`, "[DONE]") // 上游没给 usage
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	got, estimated := e.cost(resp.Header.Get("X-Lingshu-Request-Id"))
	if !estimated || !got.IsPositive() {
		t.Errorf("没有 usage 时应估算扣费，实际 cost=%s 估算=%v", got, estimated)
	}
}

func TestUpstreamCostDeductsAccount(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO upstream_accounts (id, name, balance, cost_factor) VALUES (1, 'acc', 5, 0.5)`)
	e.exec(`UPDATE channels SET upstream_account_id = 1 WHERE id = 1`)
	e.exec(`UPDATE groups SET ratio = 2`) // 分组倍率只影响向用户收费，不影响上游成本
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	id := resp.Header.Get("X-Lingshu-Request-Id")
	e.cost(id)
	// 基础价算出 0.00182，× 系数 0.5
	want := decimal.RequireFromString("0.00091")
	if got := e.decimal(`SELECT upstream_cost::text FROM request_logs WHERE request_id = $1`, id); !got.Equal(want) {
		t.Errorf("upstream_cost = %s，期望 %s", got, want)
	}
	if got := e.decimal(`SELECT balance::text FROM upstream_accounts WHERE id = 1`); !got.Equal(decimal.NewFromInt(5).Sub(want)) {
		t.Errorf("上游账户估算余额 = %s", got)
	}
}

func TestTruncatedStreamStillCostsUpstream(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO upstream_accounts (id, name, balance) VALUES (1, 'acc', 5)`)
	e.exec(`UPDATE channels SET upstream_account_id = 1 WHERE id = 1`)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"partial answer"}}]}`)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","stream":true,"messages":[{"role":"user","content":"hello there"}]}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	id := resp.Header.Get("X-Lingshu-Request-Id")
	if got, _ := e.cost(id); !got.IsZero() {
		t.Errorf("上游断流不向用户收费，实际 %s", got)
	}
	if got := e.decimal(`SELECT upstream_cost::text FROM request_logs WHERE request_id = $1`, id); !got.IsPositive() {
		t.Errorf("上游已经输出了内容，应估算上游成本，实际 %s", got)
	}
}

func TestNoAccountNoDeduction(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO upstream_accounts (id, name, balance) VALUES (1, 'acc', 5)`)
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	e.cost(resp.Header.Get("X-Lingshu-Request-Id"))
	if got := e.decimal(`SELECT balance::text FROM upstream_accounts WHERE id = 1`); !got.Equal(decimal.NewFromInt(5)) {
		t.Errorf("渠道没关联账户，不应扣账户: %s", got)
	}
}

func TestFetchBalance(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/user/balance":
			io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.19"},{"currency":"USD","total_balance":"1.5"}]}`)
		case "/v1/users/me/balance":
			io.WriteString(w, `{"code":0,"data":{"available_balance":71.43,"voucher_balance":13.21,"cash_balance":58.22},"status":true}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	h := NewHandler(nil)
	b, err := h.FetchBalance(context.Background(), "deepseek", up.URL, "sk-test", "")
	if err != nil || !b["CNY"].Equal(decimal.RequireFromString("12.19")) || !b["USD"].Equal(decimal.RequireFromString("1.5")) {
		t.Errorf("deepseek: %v %v", b, err)
	}
	b, err = h.FetchBalance(context.Background(), "moonshot", up.URL+"/v1", "sk-test", "")
	if err != nil || !b["USD"].Equal(decimal.RequireFromString("71.43")) {
		t.Errorf("moonshot（非 .cn 域名记美元）: %v %v", b, err)
	}
	if _, err := h.FetchBalance(context.Background(), "deepseek", up.URL, "sk-wrong", ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("key 不对应报错: %v", err)
	}
	if _, err := h.FetchBalance(context.Background(), "zhipu", up.URL, "sk-test", ""); err == nil {
		t.Error("不支持的厂商应报错")
	}
}

func TestPriceRuleAppliedAtSettlement(t *testing.T) {
	e := newEnv(t)
	// okUpstream 的用量：输入 800 + 缓存命中 200 = 总输入 1000
	e.exec(`UPDATE models SET price_rules = '[{"name":"长上下文","input_above":999,"input_price":"2","output_price":"4","cache_read_price":"0.2","cache_write_price":"0"}]' WHERE name = 'std-chat'`)
	e.exec(`INSERT INTO upstream_accounts (id, name, balance) VALUES (1, 'acc', 5)`)
	e.exec(`UPDATE channels SET upstream_account_id = 1 WHERE id = 1`)
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	id := resp.Header.Get("X-Lingshu-Request-Id")
	// (800×2 + 200×0.2 + 500×4) / 1e6 = 0.00364，是基础价 0.00182 的两倍
	want := decimal.RequireFromString("0.00364")
	if got, _ := e.cost(id); !got.Equal(want) {
		t.Errorf("规则价 cost = %s，期望 %s", got, want)
	}
	var rule, inPrice string
	e.store.Pool.QueryRow(context.Background(), `SELECT price_rule, input_price::text FROM request_logs WHERE request_id = $1`, id).Scan(&rule, &inPrice)
	if rule != "长上下文" || !decimal.RequireFromString(inPrice).Equal(decimal.NewFromInt(2)) {
		t.Errorf("日志应记命中的规则和生效单价: %q %s", rule, inPrice)
	}
	if got := e.decimal(`SELECT upstream_cost::text FROM request_logs WHERE request_id = $1`, id); !got.Equal(want) {
		t.Errorf("上游成本也应按规则价: %s", got)
	}

	// 门槛调高到不触发：回到基础价，规则名为空
	e.exec(`UPDATE models SET price_rules = '[{"name":"长上下文","input_above":1000,"input_price":"2","output_price":"4","cache_read_price":"0.2","cache_write_price":"0"}]' WHERE name = 'std-chat'`)
	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	id = resp.Header.Get("X-Lingshu-Request-Id")
	if got, _ := e.cost(id); !got.Equal(decimal.RequireFromString("0.00182")) {
		t.Errorf("没触发规则应按基础价: %s", got)
	}
	e.store.Pool.QueryRow(context.Background(), `SELECT price_rule FROM request_logs WHERE request_id = $1`, id).Scan(&rule)
	if rule != "" {
		t.Errorf("基础价时规则名应为空: %q", rule)
	}
}

func TestChannelProxy(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	// 一个最简单的 HTTP 正向代理：记下经过的请求，再转给真正的上游
	var proxied int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxied, 1)
		out, _ := http.NewRequest(r.Method, r.URL.String(), r.Body)
		out.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	e.cost(resp.Header.Get("X-Lingshu-Request-Id"))
	if atomic.LoadInt32(&proxied) != 0 {
		t.Fatal("没配代理的渠道不应走代理")
	}

	e.exec(`UPDATE channels SET proxy_url = $1 WHERE id = 1`, proxy.URL)
	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("经代理请求失败: %d", resp.StatusCode)
	}
	e.cost(resp.Header.Get("X-Lingshu-Request-Id"))
	if atomic.LoadInt32(&proxied) != 1 {
		t.Errorf("配了代理的渠道应经过代理，实际经过 %d 次", proxied)
	}

	// 冒烟测试也走渠道代理（冒烟测试发的是流式请求）
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1}}`, `[DONE]`)
	}
	ch, _ := e.store.ChannelSecret(context.Background(), 1)
	if r := e.handler.Probe(context.Background(), ch, usage.Chat, "up-chat"); !r.OK {
		t.Fatalf("经代理冒烟测试失败: %+v", r)
	}
	if atomic.LoadInt32(&proxied) != 2 {
		t.Errorf("冒烟测试应经过代理，累计 %d 次", proxied)
	}
}

func TestValidateProxyURL(t *testing.T) {
	for _, ok := range []string{"", "socks5://127.0.0.1:10808", "socks5h://127.0.0.1:1080", "http://127.0.0.1:8080"} {
		if err := ValidateProxyURL(ok); err != nil {
			t.Errorf("%q 应通过: %v", ok, err)
		}
	}
	for _, bad := range []string{"127.0.0.1:10808", "vless://x@y:1", "socks5://"} {
		if err := ValidateProxyURL(bad); err == nil {
			t.Errorf("%q 应拒绝", bad)
		}
	}
}
