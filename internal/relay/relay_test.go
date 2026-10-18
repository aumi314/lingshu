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
	"testing"
	"time"

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
		{`INSERT INTO users (id, username, password_hash) VALUES (1, 'alice', 'x')`, nil},
		{`INSERT INTO groups (id, name) VALUES (1, 'default')`, nil},
		{`INSERT INTO models (id, name) VALUES (1, 'std-chat'), (2, 'std-msg')`, nil},
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
	mux.Handle("POST /v1/chat/completions", h.Serve(usage.Chat))
	mux.Handle("POST /v1/messages", h.Serve(usage.Messages))
	mux.Handle("POST /v1/responses", h.Serve(usage.Responses))
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
