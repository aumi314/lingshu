package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

func TestOpenCodeRequestPreservesCallerFields(t *testing.T) {
	raw := []byte(`{"model":"deepseek-v4.1-flash-free","messages":[{"role":"system","content":"Follow the user's instructions."},{"role":"user","content":"hi"}],"max_tokens":17,"temperature":0.3,"reasoning_effort":"high","custom":{"keep":true},"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}],"tool_choice":"none","stream_options":{"custom":true}}`)
	req := httptest.NewRequest("POST", "https://opencode.ai/inference/openai/v1/chat/completions", nil)
	req.Header.Set("Authorization", "lingshu-key:9")
	req.Header.Set("X-Opencode-Session", "conversation-a")
	prepared, state, err := prepareOpenCode(req, raw, 30)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	json.Unmarshal(raw, &before)
	json.Unmarshal(prepared, &after)
	for _, key := range []string{"model", "max_tokens", "temperature", "reasoning_effort", "custom", "tool_choice"} {
		if !bytes.Equal(before[key], after[key]) {
			t.Errorf("客户端参数 %s 被覆盖: %s", key, after[key])
		}
	}
	var messages []json.RawMessage
	json.Unmarshal(after["messages"], &messages)
	if len(messages) != 3 || !bytes.Contains(messages[1], []byte("Follow the user's instructions.")) {
		t.Fatalf("原始提示词没有保留: %s", after["messages"])
	}
	if state.stubs["read"] || len(state.stubs) != 4 || string(after["stream"]) != "true" {
		t.Fatalf("占位工具或流式参数错误: %+v %s", state, prepared)
	}
	var tools []map[string]json.RawMessage
	json.Unmarshal(after["tools"], &tools)
	var original []map[string]json.RawMessage
	json.Unmarshal(before["tools"], &original)
	if len(tools) != 5 || !bytes.Equal(tools[0]["function"], original[0]["function"]) {
		t.Fatal("客户端工具的定义被改写")
	}
	if req.Header.Get("X-Opencode-Session") == "conversation-a" || strings.Contains(req.Header.Get("X-Opencode-Session"), "lingshu-key") {
		t.Fatal("会话没有隔离或泄露调用 key")
	}
	other := req.Clone(context.Background())
	other.Header.Set("X-Opencode-Session", "conversation-a")
	prepareOpenCode(other, raw, 30)
	if other.Header.Get("X-Opencode-Session") != req.Header.Get("X-Opencode-Session") {
		t.Fatal("同一会话没有稳定 ID")
	}
	other.Header.Set("X-Opencode-Session", "conversation-a")
	other.Header.Set("Authorization", "lingshu-key:10")
	prepareOpenCode(other, raw, 30)
	if other.Header.Get("X-Opencode-Session") == req.Header.Get("X-Opencode-Session") {
		t.Fatal("不同成员共用了会话")
	}
}

func TestNativeOpenCodeGateway(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "body", true: "stream"}[stream], func(t *testing.T) {
			e := newEnv(t)
			_, err := e.store.Pool.Exec(context.Background(), `UPDATE channels SET template = 'opencode-zen'; UPDATE channel_models SET upstream_name = 'deepseek-v4.1-flash-free'`)
			if err != nil {
				t.Fatal(err)
			}
			e.upstream = func(w http.ResponseWriter, r *http.Request) {
				var b map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&b)
				if string(b["stream"]) != "true" || !bytes.Contains(b["tools"], []byte("bash")) || !bytes.Contains(b["messages"], []byte("/workspace")) {
					t.Errorf("没有使用原生 OpenCode 请求: %v", b)
				}
				if r.Header.Get("Authorization") != "Bearer "+upstreamKey || r.Header.Get("X-Opencode-Session") == "" {
					t.Error("鉴权或会话缺失")
				}
				sse(w,
					`{"id":"x","model":"deepseek-v4.1-flash-free","choices":[{"index":0,"delta":{"content":"OK","reasoning_content":"thinking"},"finish_reason":null}]}`,
					`{"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
					`{"id":"x","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}}`,
					`[DONE]`)
			}
			body := `{"model":"std-chat","messages":[{"role":"user","content":"hi"}],"stream":` + map[bool]string{false: "false", true: "true"}[stream] + `}`
			resp := e.post("/v1/chat/completions", body, nil)
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || !bytes.Contains(got, []byte("OK")) {
				t.Fatalf("%d %s", resp.StatusCode, got)
			}
			if stream != strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("响应协议与客户端不符: %s", resp.Header.Get("Content-Type"))
			}
			l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
			if l.Status != "success" || l.InputTokens != 8 || l.CacheReadTokens != 4 || l.OutputTokens != 5 || l.Stream != stream {
				t.Fatalf("结算日志错误: %+v", l)
			}
		})
	}
}

func TestOpenCodeProbeUsesNativeRequest(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Opencode-Session") == "" {
			t.Error("探针缺少会话")
		}
		b, _ := io.ReadAll(r.Body)
		if !bytes.Contains(b, []byte("/workspace")) || !bytes.Contains(b, []byte(`"tools"`)) {
			t.Error("探针没有使用原生适配")
		}
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`, `[DONE]`)
	}))
	defer up.Close()
	h := NewHandler(nil)
	got := h.Probe(context.Background(), &store.Channel{ID: 30, Template: openCodeTemplate, BaseURLChat: up.URL, APIKey: "upstream-key"}, usage.Chat, "deepseek-v4.1-flash-free")
	if !got.OK || !got.UsageReported || got.OutputTokens != 2 {
		t.Fatalf("probe = %+v", got)
	}
}

func TestOpenCodeRejectsSplitStubName(t *testing.T) {
	state := &openCodeRequest{stubs: map[string]bool{"bash": true}}
	if err := state.sanitizeEvent([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"ba"}}]}}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := state.sanitizeEvent([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"sh"}}]}}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := state.sanitizeEvent([]byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)); err == nil {
		t.Fatal("分片工具名绕过占位工具检查")
	}
}

func TestOpenCodePreservesToolNameWithStubPrefix(t *testing.T) {
	state := &openCodeRequest{stubs: map[string]bool{"read": true}}
	for _, event := range []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"read"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"more","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	} {
		if err := state.sanitizeEvent([]byte(event)); err != nil {
			t.Fatalf("真实工具 readmore 被误判: %v", err)
		}
	}
}

func TestOpenCodeChecksStubAtDone(t *testing.T) {
	state := &openCodeRequest{stubs: map[string]bool{"read": true}}
	_ = state.sanitizeEvent([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"read"}}]}}]}`))
	if err := state.sanitizeEvent([]byte(`[DONE]`)); err == nil {
		t.Fatal("DONE 前没有检查未结束的占位工具调用")
	}
}

func TestOpenCodeLimitResponsePreservesRetryAfterAndDoesNotCharge(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE channels SET template = 'opencode-zen'; UPDATE channel_models SET upstream_name = 'iceman-free'`)
	const upstreamBody = `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached"}}`
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		status(http.StatusTooManyRequests, upstreamBody)(w, r)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","messages":[{"role":"user","content":"hi"}]}`, nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "3600" || string(body) != upstreamBody {
		t.Fatalf("限流响应未保留: %d %v %s", resp.StatusCode, resp.Header, body)
	}
	if log := e.logRow(resp.Header.Get("X-Lingshu-Request-Id")); log.status != "upstream_error" || !log.cost.IsZero() {
		t.Fatalf("限流请求被计费: %+v", log)
	}
}

func TestOpenCodeFragmentedToolResponse(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, name := range []string{"readmore", "read"} {
			t.Run(map[bool]string{false: "body", true: "stream"}[stream]+"/"+name, func(t *testing.T) {
				e := newEnv(t)
				e.exec(`UPDATE channels SET template = 'opencode-zen'; UPDATE channel_models SET upstream_name = 'iceman-free'`)
				e.upstream = func(w http.ResponseWriter, r *http.Request) {
					events := []string{`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"read"}}]}}]}`}
					if name == "readmore" {
						events = append(events, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"more","arguments":"{}"}}]}}]}`)
					}
					events = append(events,
						`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
						`[DONE]`)
					sse(w, events...)
				}
				payload, _ := json.Marshal(map[string]any{
					"model": "std-chat", "stream": stream,
					"messages": []map[string]string{{"role": "user", "content": "Use readmore."}},
					"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "readmore", "parameters": map[string]any{"type": "object"}}}},
				})
				resp := e.post("/v1/chat/completions", string(payload), nil)
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				log := e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))
				if name == "readmore" {
					if resp.StatusCode != 200 || log.status != "success" || !bytes.Contains(body, []byte("tool_calls")) || bytes.Contains(body, []byte("upstream_error")) {
						t.Fatalf("真实工具响应错误: %d %s %+v", resp.StatusCode, body, log)
					}
				} else if log.status != "upstream_error" || !log.cost.IsZero() || !bytes.Contains(body, []byte("error")) {
					t.Fatalf("占位工具调用没有报错或被计费: %d %s %+v", resp.StatusCode, body, log)
				}
			})
		}
	}
}

func TestOpenCodeNonStreamTruncationIsNotSuccess(t *testing.T) {
	e := newEnv(t)
	_, err := e.store.Pool.Exec(context.Background(), `UPDATE channels SET template = 'opencode-zen'; UPDATE channel_models SET upstream_name = 'deepseek-v4.1-flash-free'`)
	if err != nil {
		t.Fatal(err)
	}
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"index":0,"delta":{"content":"partial"}}],"usage":{"prompt_tokens":20,"completion_tokens":3}}`)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","messages":[{"role":"user","content":"hi"}]}`, nil)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway || bytes.Contains(raw, []byte(`"content":"partial"`)) {
		t.Fatalf("截断响应被当作成功: %d %s", resp.StatusCode, raw)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "upstream_error" {
		t.Fatalf("status = %s", l.Status)
	}
}

func TestOpenCodeNonStreamCancellationStopsUpstream(t *testing.T) {
	e := newEnv(t)
	_, err := e.store.Pool.Exec(context.Background(), `UPDATE channels SET template = 'opencode-zen'; UPDATE channel_models SET upstream_name = 'deepseek-v4.1-flash-free'`)
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", e.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"std-chat","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+e.key)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("上游没有收到请求")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("客户端取消后上游仍在生成")
	}
	<-done
}
