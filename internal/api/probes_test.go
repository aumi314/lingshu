package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/probe"
	"github.com/aumi314/lingshu/internal/relay"
)

type probeUpstream struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	requests []probeUpstreamRequest
	status   int
	delay    time.Duration
}

type probeUpstreamRequest struct {
	path   string
	model  string
	body   map[string]any
	header http.Header
}

var (
	probeOnlyWord = regexp.MustCompile(`single word (\w+)`)
	probeMarker   = regexp.MustCompile(`\[\[END-[0-9a-f]+\]\]`)
	probeExact    = regexp.MustCompile(`Use exactly (\{.*\})`)
)

func newProbeUpstream(t *testing.T) *probeUpstream {
	t.Helper()
	up := &probeUpstream{t: t, status: http.StatusOK}
	up.server = httptest.NewServer(http.HandlerFunc(up.serve))
	t.Cleanup(up.server.Close)
	return up
}

func (up *probeUpstream) url() string { return up.server.URL }

func (up *probeUpstream) setStatus(status int) {
	up.mu.Lock()
	up.status = status
	up.mu.Unlock()
}

func (up *probeUpstream) setDelay(delay time.Duration) {
	up.mu.Lock()
	up.delay = delay
	up.mu.Unlock()
}

func (up *probeUpstream) snapshot() []probeUpstreamRequest {
	up.mu.Lock()
	defer up.mu.Unlock()
	return append([]probeUpstreamRequest(nil), up.requests...)
}

func (up *probeUpstream) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	up.mu.Lock()
	up.requests = append(up.requests, probeUpstreamRequest{path: r.URL.Path, model: probeBodyModel(body), body: body, header: r.Header.Clone()})
	status := up.status
	delay := up.delay
	up.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if status != http.StatusOK {
		http.Error(w, `{"error":{"message":"insufficient balance"}}`, status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(s string) { fmt.Fprintf(w, "data: %s\n\n", s) }
	switch {
	case strings.HasSuffix(r.URL.Path, "/responses"):
		up.serveResponses(w, send, body)
	case strings.HasSuffix(r.URL.Path, "/v1/messages"):
		up.serveMessages(w, send, body)
	default:
		up.serveChat(w, send, body)
	}
}

func probeBodyModel(body map[string]any) string {
	model, _ := body["model"].(string)
	return model
}

func probeBodyString(body map[string]any, key string) string {
	value, _ := body[key].(string)
	return value
}

func probeChatPrompt(body map[string]any) (string, string) {
	system, prompt := "", ""
	if messages, ok := body["messages"].([]any); ok {
		for _, raw := range messages {
			m, _ := raw.(map[string]any)
			switch m["role"] {
			case "system", "developer":
				system += probeJSONText(m["content"])
			case "user":
				prompt = probeJSONText(m["content"])
			}
		}
	}
	return system, prompt
}

func probeJSONText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			parts = append(parts, probeJSONText(item))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
	}
	return ""
}

func probeAnswer(system, prompt string) string {
	if m := probeOnlyWord.FindStringSubmatch(system); m != nil {
		return m[1]
	}
	if m := probeMarker.FindStringSubmatch(system); m != nil {
		return "Hello!\n" + m[0]
	}
	if m := probeExact.FindStringSubmatch(prompt); m != nil {
		return m[1]
	}
	return "47"
}

func (up *probeUpstream) serveChat(w http.ResponseWriter, send func(string), body map[string]any) {
	system, prompt := probeChatPrompt(body)
	answer := probeAnswer(system, prompt)
	if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		tool, _ := tools[0].(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		city := strings.TrimSuffix(strings.TrimPrefix(prompt, "What is the weather in "), " right now? Use the tool.")
		send(fmt.Sprintf(`{"id":"chatcmpl-x","model":"up-sol","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":%q,"arguments":""}}]}}]}`, name))
		send(`{"id":"chatcmpl-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`)
		send(fmt.Sprintf(`{"id":"chatcmpl-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`, `"`+city+`"}`))
	} else {
		text, _ := json.Marshal(map[string]string{"content": answer})
		send(`{"id":"chatcmpl-x","model":"up-sol","choices":[{"index":0,"delta":` + string(text) + `}]}`)
	}
	send(`{"id":"chatcmpl-x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	send(`{"id":"chatcmpl-x","choices":[],"usage":{"prompt_tokens":4158,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4000}}}`)
	send("[DONE]")
}

func (up *probeUpstream) serveResponses(w http.ResponseWriter, send func(string), body map[string]any) {
	system, prompt := "", ""
	if input, ok := body["input"].([]any); ok {
		for _, raw := range input {
			item, _ := raw.(map[string]any)
			if item["role"] == "developer" || item["role"] == "system" {
				system += probeJSONText(item["content"])
			} else {
				prompt = probeJSONText(item["content"])
			}
		}
	}
	answer := probeAnswer(system, prompt)
	output := []any{}
	if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		tool, _ := tools[0].(map[string]any)
		name, _ := tool["name"].(string)
		city := strings.TrimSuffix(strings.TrimPrefix(prompt, "What is the weather in "), " right now? Use the tool.")
		output = append(output, map[string]any{"type": "function_call", "name": name, "arguments": `{"city":"` + city + `"}`})
	} else {
		output = append(output, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": answer}}})
	}
	send(`{"type":"response.created","response":{"id":"resp-x","model":"up-sol"}}`)
	completed, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp-x", "model": "up-sol", "status": "completed", "output": output, "usage": map[string]any{"input_tokens": 4158, "input_tokens_details": map[string]any{"cached_tokens": 4000}, "output_tokens": 5}}})
	send(string(completed))
}

func (up *probeUpstream) serveMessages(w http.ResponseWriter, send func(string), body map[string]any) {
	system, prompt := "", ""
	if blocks, ok := body["system"].([]any); ok {
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			system += probeBodyString(block, "text")
		}
	}
	prompt = probeJSONText(body["messages"])
	answer := probeAnswer(system, prompt)
	if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		tool, _ := tools[0].(map[string]any)
		name := probeBodyString(tool, "name")
		if name == "" {
			name = "get_weather"
		}
		city := strings.TrimSuffix(strings.TrimPrefix(prompt, "What is the weather in "), " right now? Use the tool.")
		send(fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":%q}}`, name))
		send(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`)
		send(fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":%q}}`, `"`+city+`"}`))
	} else {
		send(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		send(fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, answer))
	}
	send(`{"type":"message_start","message":{"id":"msg-x","model":"up-claude","usage":{"input_tokens":4158,"cache_read_input_tokens":4000,"output_tokens":5}}}`)
	send(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
	send(`{"type":"message_stop"}`)
}

// assertFullDisguise：渠道质量测试的每个请求都用服务器上的真实客户端模板（中转认不出检测流量）。
func (up *probeUpstream) assertFullDisguise(t *testing.T, ua string) {
	t.Helper()
	for _, request := range up.snapshot() {
		if got := request.header.Get("User-Agent"); got != ua {
			t.Errorf("%s 质量测试应带模板的 UA %q，实际 %q", request.path, ua, got)
		}
		switch request.path {
		case "/responses":
			if _, ok := request.body["client_metadata"]; !ok {
				t.Errorf("Codex 模板请求缺少 client_metadata")
			}
		case "/v1/messages":
			if !strings.Contains(probeJSONText(request.body["system"]), "private Claude Code system prompt") {
				t.Errorf("Claude 模板请求缺少模板系统提示: %v", request.body["system"])
			}
			assertClaudeSession(t, request)
		}
	}
}

// assertClaudeSession：号池要求 X-Claude-Code-Session-Id 和 metadata.user_id 里的 session_id 一致。
func assertClaudeSession(t *testing.T, request probeUpstreamRequest) {
	t.Helper()
	meta, _ := request.body["metadata"].(map[string]any)
	userID, _ := meta["user_id"].(string)
	var parsed struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal([]byte(userID), &parsed)
	if parsed.SessionID == "" || request.header.Get("X-Claude-Code-Session-Id") != parsed.SessionID {
		t.Errorf("会话头 %q 应等于 metadata 的 session_id %q", request.header.Get("X-Claude-Code-Session-Id"), parsed.SessionID)
	}
}

// assertTransportOnly：模型指纹只用传输层伪装（Codex 客户端标识和会话头，号池按会话路由），不带模板内容，和官方样本的采集条件一致。
func (up *probeUpstream) assertTransportOnly(t *testing.T) {
	t.Helper()
	sessions := map[string]bool{}
	for _, request := range up.snapshot() {
		if got := request.header.Get("User-Agent"); !strings.Contains(got, "codex") {
			t.Errorf("%s 指纹请求应带 Codex 客户端标识，实际 %q", request.path, got)
		}
		if _, ok := request.body["client_metadata"]; ok {
			t.Errorf("%s 指纹请求不应带模板的客户端元数据", request.path)
		}
		sessions[request.header.Get("Session-Id")] = true
	}
	if len(sessions) != probe.Sessions || sessions[""] {
		t.Errorf("指纹请求应分在 %d 个会话里，实际 %v", probe.Sessions, sessions)
	}
}

func seedProbeModel(t *testing.T, e *env, name, upstream string, channelID int64) {
	t.Helper()
	ctx := context.Background()
	var modelID int64
	if err := e.store.Pool.QueryRow(ctx, `INSERT INTO models (name, input_price, output_price, cache_read_price) VALUES ($1, 2, 10, 0.1) RETURNING id`, name).Scan(&modelID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Pool.Exec(ctx, `INSERT INTO channel_models (channel_id, model_id, upstream_name) VALUES ($1, $2, $3)`, channelID, modelID, upstream); err != nil {
		t.Fatal(err)
	}
}

func seedProbeChannel(t *testing.T, e *env, name, key, chatURL, messagesURL, responsesURL string) int64 {
	t.Helper()
	var id int64
	err := e.store.Pool.QueryRow(context.Background(), `
		INSERT INTO channels (name, api_key, base_url_chat, base_url_messages, base_url_responses)
		VALUES ($1, '', $2, $3, $4) RETURNING id`,
		name, chatURL, messagesURL, responsesURL).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Pool.Exec(context.Background(), `INSERT INTO channel_keys (channel_id, api_key) VALUES ($1, $2)`, id, key); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertProbeAuth(t *testing.T, requests []probeUpstreamRequest) {
	t.Helper()
	if len(requests) == 0 {
		t.Fatal("检测没有发出上游请求")
	}
	for _, request := range requests {
		if strings.HasSuffix(request.path, "/v1/messages") {
			if got := request.header.Get("X-Api-Key"); got != "upstream-key" {
				t.Errorf("%s 的上游 x-api-key = %q，want upstream-key", request.path, got)
			}
			continue
		}
		if got := request.header.Get("Authorization"); got != "Bearer upstream-key" {
			t.Errorf("%s 的上游认证头 = %q，want Bearer upstream-key", request.path, got)
		}
	}
}

type probeRunView struct {
	Status        string `json:"status"`
	Verdict       string `json:"verdict"`
	Baseline      string `json:"baseline"`
	UpstreamModel string `json:"upstream_model"`
	Preset        string `json:"preset"`
	Score         *int   `json:"score"`
	Cost          string `json:"cost"`
	Report        struct {
		ProbeMode    string `json:"probe_mode"`
		Protocol     string `json:"protocol"`
		Disguise     bool   `json:"disguise"`
		TemplateHash string `json:"template_hash"`
		StopReason   string `json:"stop_reason"`
		Counts       struct {
			Planned   int `json:"planned"`
			Sent      int `json:"sent"`
			Completed int `json:"completed"`
			Skipped   int `json:"skipped"`
		} `json:"counts"`
		Items []struct {
			Key, Status, Summary string
		} `json:"items"`
		Calls []json.RawMessage `json:"calls"`
	} `json:"report"`
}

func waitProbeRun(t *testing.T, a *client, id int64) probeRunView {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var raw map[string]any
		a.do("GET", fmt.Sprintf("/api/admin/probes/%d", id), nil, &raw)
		if raw["status"] != "running" || time.Now().After(deadline) {
			b, _ := json.Marshal(raw)
			var run probeRunView
			if err := json.Unmarshal(b, &run); err != nil {
				t.Fatalf("解析检测记录失败: %v %s", err, b)
			}
			if run.Status == "running" {
				t.Fatal("检测超时仍在运行")
			}
			return run
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func startProbe(t *testing.T, a *client, payload map[string]any) (int, string, int64) {
	t.Helper()
	var started struct{ ID int64 }
	code, body := a.do("POST", "/api/admin/probes", payload, &started)
	return code, body, started.ID
}

func TestProbeStartValidation(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	up := newProbeUpstream(t)
	chatChannel := seedProbeChannel(t, e, "chat-only", "upstream-key", up.url(), "", "")
	seedProbeModel(t, e, "gpt-6.1-sol", "up-sol", chatChannel)
	responseChannel := seedProbeChannel(t, e, "responses-only", "upstream-key", "", "", up.url())
	seedProbeModel(t, e, "gpt-6-astra", "up-astra", responseChannel)
	blockedChannel := seedProbeChannel(t, e, "blocked", "upstream-key", "", "", up.url())
	seedProbeModel(t, e, "gpt-6-luna", "up-luna", blockedChannel)
	if code, body, _ := startProbe(t, a, map[string]any{"channel_id": responseChannel, "model": "gpt-6-astra", "preset": probe.ModeQuality}); code != http.StatusBadRequest || !strings.Contains(body, "伪装模板") {
		t.Fatalf("服务器没有 Codex 模板时质量测试应拒绝发起: %d %s", code, body)
	}
	e.api.Relay.Disguise = &relay.ProbeDisguise{}
	e.api.Relay.Disguise.Codex.Input = json.RawMessage(`[{"type":"message"}]`)

	for _, preset := range []string{"", "quick", "full", "unknown"} {
		payload := map[string]any{"channel_id": chatChannel, "model": "gpt-6.1-sol", "preset": preset}
		if code, body, _ := startProbe(t, a, payload); code != http.StatusBadRequest || !strings.Contains(body, "quality 或 fingerprint") {
			t.Errorf("旧 preset %q 应拒绝: %d %s", preset, code, body)
		}
	}
	if code, body, _ := startProbe(t, a, map[string]any{"channel_id": chatChannel, "model": "gpt-6.1-sol", "preset": probe.ModeQuality}); code != http.StatusBadRequest || !strings.Contains(body, "Responses") {
		t.Fatalf("质量模式需要 Responses 地址: %d %s", code, body)
	}
	if code, body, _ := startProbe(t, a, map[string]any{"channel_id": responseChannel, "model": "gpt-6-astra", "preset": probe.ModeFingerprint}); code != http.StatusBadRequest || !strings.Contains(body, "Chat") {
		t.Fatalf("指纹模式需要 Chat 地址: %d %s", code, body)
	}

	ctx := context.Background()
	if _, err := e.store.Pool.Exec(ctx, `INSERT INTO probe_runs (channel_id, channel_name, model, upstream_model, baseline, preset, status) VALUES ($1, 'blocked', 'gpt-6-luna', 'up-luna', 'gpt-6-luna', 'quality', 'running')`, blockedChannel); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := startProbe(t, a, map[string]any{"channel_id": blockedChannel, "model": "gpt-6-luna", "preset": probe.ModeQuality}); code != http.StatusTooManyRequests {
		t.Fatalf("同一渠道已有检测应拒绝: %d %s", code, body)
	}
	for range maxRunningProbes {
		if _, err := e.store.Pool.Exec(ctx, `INSERT INTO probe_runs (channel_name, model, upstream_model, baseline, preset, status) VALUES ('idle', 'm', 'm', 'gpt-6-luna', 'quality', 'running')`); err != nil {
			t.Fatal(err)
		}
	}
	if code, body, _ := startProbe(t, a, map[string]any{"channel_id": chatChannel, "model": "gpt-6.1-sol", "preset": probe.ModeFingerprint}); code != http.StatusTooManyRequests {
		t.Fatalf("全局已有检测时应拒绝其他渠道: %d %s", code, body)
	}
}

func TestProbeQualityGPT(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	up := newProbeUpstream(t)
	channelID := seedProbeChannel(t, e, "responses", "upstream-key", "", "", up.url())
	seedProbeModel(t, e, "gpt-6.1-sol", "up-sol", channelID)
	e.api.Relay.Disguise = &relay.ProbeDisguise{}
	e.api.Relay.Disguise.Codex.Input = json.RawMessage(`[{"type":"message"}]`)
	e.api.Relay.Disguise.Codex.Headers = map[string]string{"user-agent": "private-codex-template"}
	code, body, id := startProbe(t, a, map[string]any{"channel_id": channelID, "model": "gpt-6.1-sol", "preset": probe.ModeQuality})
	if code != http.StatusOK {
		t.Fatalf("发起质量测试: %d %s", code, body)
	}
	run := waitProbeRun(t, a, id)
	if run.Status != "done" || run.Report.ProbeMode != probe.ModeQuality || run.Report.Protocol != "responses" || !run.Report.Disguise || run.Report.TemplateHash == "" || run.Score == nil {
		t.Fatalf("质量测试结果不对: %+v", run)
	}
	status := map[string]string{}
	for _, item := range run.Report.Items {
		status[item.Key] = item.Status
	}
	for _, key := range []string{"structured", "instruction", "needle"} {
		if status[key] == "" {
			t.Errorf("缺少检测项 %s: %+v", key, status)
		}
	}
	requests := up.snapshot()
	if len(requests) != len(run.Report.Calls) || len(requests) < 10 {
		t.Fatalf("请求数 %d，报告 %d", len(requests), len(run.Report.Calls))
	}
	assertProbeAuth(t, requests)
	for _, request := range requests {
		if request.path != "/responses" || request.model != "up-sol" {
			t.Fatalf("质量测试应走 Responses: %s %s", request.path, request.model)
		}
	}
	up.assertFullDisguise(t, "private-codex-template")
	var logs int
	e.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM request_logs`).Scan(&logs)
	if logs != 0 {
		t.Errorf("检测请求不应写调用日志: %d", logs)
	}
}

func TestProbeFingerprintGPT(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	up := newProbeUpstream(t)
	channelID := seedProbeChannel(t, e, "chat", "upstream-key", up.url(), "", "")
	seedProbeModel(t, e, "gpt-6.1-sol", "up-sol", channelID)
	code, body, id := startProbe(t, a, map[string]any{"channel_id": channelID, "model": "gpt-6.1-sol", "preset": probe.ModeFingerprint})
	if code != http.StatusOK {
		t.Fatalf("发起指纹辨别: %d %s", code, body)
	}
	run := waitProbeRun(t, a, id)
	if run.Status != "done" || run.Report.ProbeMode != probe.ModeFingerprint || run.Report.Protocol != "chat" || run.Report.Disguise || run.Score != nil {
		t.Fatalf("指纹测试结果不对: %+v", run)
	}
	requests := up.snapshot()
	if len(requests) != 525 || len(run.Report.Calls) != 525 {
		t.Fatalf("指纹请求数 = %d / %d, want 525", len(requests), len(run.Report.Calls))
	}
	assertProbeAuth(t, requests)
	for _, request := range requests {
		if request.path != "/chat/completions" || request.model != "up-sol" {
			t.Fatalf("指纹辨别应走 Chat: %s %s", request.path, request.model)
		}
	}
	up.assertTransportOnly(t)

	var list []map[string]any
	a.do("GET", "/api/admin/probes", nil, &list)
	if len(list) != 1 || list[0]["report"] != nil {
		t.Errorf("列表不应带完整报告: %v", list)
	}
	ctx := context.Background()
	if _, err := e.store.Pool.Exec(ctx, `INSERT INTO probe_runs (channel_name, model, upstream_model, baseline, preset, status) VALUES ('x', 'm', 'm', 'gpt-6.1-sol', 'quick', 'running')`); err != nil {
		t.Fatal(err)
	}
	if err := e.store.FailStaleProbeRuns(ctx); err != nil {
		t.Fatal(err)
	}
	var running int
	e.store.Pool.QueryRow(ctx, `SELECT count(*) FROM probe_runs WHERE status = 'running'`).Scan(&running)
	if running != 0 {
		t.Errorf("重启后不应还有进行中的检测: %d", running)
	}
}

func TestProbeQualityClaude(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	up := newProbeUpstream(t)
	channelID := seedProbeChannel(t, e, "messages", "upstream-key", "", up.url(), "")
	seedProbeModel(t, e, "claude-opus-5-5", "up-claude", channelID)
	e.api.Relay.Disguise = &relay.ProbeDisguise{}
	e.api.Relay.Disguise.Claude.System = []map[string]any{{"type": "text", "text": "private Claude Code system prompt"}}
	e.api.Relay.Disguise.Claude.Headers = map[string]string{"user-agent": "private-claude-template"}
	code, body, id := startProbe(t, a, map[string]any{"channel_id": channelID, "model": "claude-opus-5-5", "preset": probe.ModeQuality})
	if code != http.StatusOK {
		t.Fatalf("发起 Claude 质量测试: %d %s", code, body)
	}
	run := waitProbeRun(t, a, id)
	if run.Status != "done" || run.Report.ProbeMode != probe.ModeQuality || run.Report.Protocol != "messages" || !run.Report.Disguise || run.Report.TemplateHash == "" || run.Score == nil {
		t.Fatalf("Claude 质量测试结果不对: %+v", run)
	}
	requests := up.snapshot()
	if len(requests) != len(run.Report.Calls) || len(requests) < 9 {
		t.Fatalf("Claude 请求数 = %d / %d", len(requests), len(run.Report.Calls))
	}
	assertProbeAuth(t, requests)
	for _, request := range requests {
		if request.path != "/v1/messages" || request.model != "up-claude" {
			t.Fatalf("Claude 质量测试应走 Messages: %s %s", request.path, request.model)
		}
	}
	up.assertFullDisguise(t, "private-claude-template")
}

func TestProbePartialReportOnQuotaError(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	up := newProbeUpstream(t)
	up.setStatus(http.StatusPaymentRequired)
	channelID := seedProbeChannel(t, e, "no-balance", "upstream-key", up.url(), "", "")
	seedProbeModel(t, e, "gpt-6.1-sol", "up-sol", channelID)
	code, body, id := startProbe(t, a, map[string]any{"channel_id": channelID, "model": "gpt-6.1-sol", "preset": probe.ModeFingerprint})
	if code != http.StatusOK {
		t.Fatalf("发起检测: %d %s", code, body)
	}
	run := waitProbeRun(t, a, id)
	if run.Status != "failed" || run.Report.StopReason != "quota" || run.Score != nil {
		t.Fatalf("余额不足应留下部分报告并不评分: %+v", run)
	}
	if run.Report.Counts.Sent == 0 || run.Report.Counts.Sent >= run.Report.Counts.Planned || run.Report.Counts.Completed != 0 || run.Report.Counts.Skipped == 0 {
		t.Fatalf("部分报告计数不对: %+v", run.Report.Counts)
	}
	if len(run.Report.Calls) != run.Report.Counts.Planned {
		t.Fatalf("部分报告调用数 = %d, planned = %d", len(run.Report.Calls), run.Report.Counts.Planned)
	}
}
