package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aumi314/lingshu/internal/probe"
	"github.com/aumi314/lingshu/internal/store"
)

// 事件按 2026-11-07 在 kinght claude0.8 上抓到的 claude-opus-5-5 响应构造（编号、usage 字段、thinking 签名、工具调用分片）。
const probeMessagesSSE = `event: message_start
data: {"type":"message_start","message":{"model":"claude-opus-5-5","id":"msg_011CgXIcPjXkQ1kNXWHg5Oqj","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":446,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":1,"service_tier":"standard","inference_geo":"us"}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EqQBCkYIBxgCKkDx"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Hobart\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":446,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":389,"output_tokens_details":{"thinking_tokens":377}}}

event: message_stop
data: {"type":"message_stop"}

`

func TestProbeMessagesDisguiseAndParse(t *testing.T) {
	var got map[string]any
	var hd http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("路径 %s", r.URL.Path)
		}
		hd = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, probeMessagesSSE)
	}))
	defer up.Close()
	h := &Handler{Client: up.Client()}
	ch := &store.Channel{BaseURLMessages: up.URL, APIKey: "sk-test"}
	c := h.ProbeMessages(context.Background(), ch, "claude-opus-5-5", "sess-1", probe.Request{
		Prompt: "What is the weather in Hobart right now? Use the tool.", System: "Only reply vvv", Effort: "high", Document: probe.MinimalPDF("x"),
		Tools: []map[string]any{{"name": "get_weather"}},
	})

	// 请求伪装成 Claude Code，会话编号进 metadata.user_id
	if hd.Get("x-api-key") != "sk-test" || !strings.HasPrefix(hd.Get("User-Agent"), "claude-cli/") || hd.Get("X-App") != "cli" || !strings.Contains(hd.Get("Anthropic-Beta"), "claude-code-") {
		t.Errorf("请求头 %v", hd)
	}
	sys, _ := got["system"].([]any)
	if len(sys) != 3 || !strings.Contains(sys[1].(map[string]any)["text"].(string), "Claude Code") || sys[2].(map[string]any)["text"] != "Only reply vvv" {
		t.Errorf("system %v", got["system"])
	}
	var uid map[string]string
	json.Unmarshal([]byte(got["metadata"].(map[string]any)["user_id"].(string)), &uid)
	if uid["session_id"] != "sess-1" || len(uid["device_id"]) != 64 {
		t.Errorf("metadata.user_id %v", uid)
	}
	if got["thinking"].(map[string]any)["type"] != "adaptive" || got["output_config"].(map[string]any)["effort"] != "high" || got["max_tokens"] == nil {
		t.Errorf("推理参数 %v %v %v", got["thinking"], got["output_config"], got["max_tokens"])
	}
	msg := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if msg[0].(map[string]any)["type"] != "document" || msg[1].(map[string]any)["type"] != "text" {
		t.Errorf("PDF 应作为 document 块放在文字前面: %v", msg)
	}

	// 响应解析
	if c.Error != "" || c.ID != "msg_011CgXIcPjXkQ1kNXWHg5Oqj" || c.Model != "claude-opus-5-5" || c.FinishReason != "tool_use" || !c.Done {
		t.Errorf("基本字段 %+v", c)
	}
	if strings.Join(c.EventTypes, ",") != "message_start,content_block_start,ping,content_block_delta,content_block_stop,content_block_start,content_block_delta,content_block_stop,message_delta,message_stop" {
		t.Errorf("事件序列 %v", c.EventTypes)
	}
	if c.SignedThinking != 1 || c.ReasoningTokens != 377 || c.OutputTokens != 389 || c.InputTokens != 446 || !c.UsageReported {
		t.Errorf("推理和用量 %+v", c)
	}
	if strings.Join(c.UsageFields, ",") != "cache_creation,cache_creation_input_tokens,cache_read_input_tokens,inference_geo,input_tokens,output_tokens,service_tier" {
		t.Errorf("usage 字段 %v", c.UsageFields)
	}
	if len(c.ToolCalls) != 1 || c.ToolCalls[0].Name != "get_weather" || c.ToolCalls[0].Arguments != `{"city":"Hobart"}` {
		t.Errorf("工具调用 %+v", c.ToolCalls)
	}
}
