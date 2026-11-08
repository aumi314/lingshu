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

// 上游先回 200 再在流里报错（2026-11-07 Claude 大海捞针 20 万 token 时实际发生）：记成请求失败。
func TestProbeMessagesStreamError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01x\",\"model\":\"claude-opus-5-5\",\"usage\":{\"input_tokens\":201223,\"output_tokens\":1}}}\n\n"+
			"event: ping\ndata: {\"type\":\"ping\"}\n\n"+
			"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"prompt is too long\"}}\n\n")
	}))
	defer up.Close()
	h := &Handler{Client: up.Client()}
	c := h.ProbeMessages(context.Background(), &store.Channel{BaseURLMessages: up.URL, APIKey: "k"}, "claude-opus-5-5", "s", probe.Request{Prompt: "hi"})
	if !strings.Contains(c.Error, "prompt is too long") || c.Done {
		t.Errorf("流中报错应记成请求失败: %+v", c)
	}
}

// 上游回了 200、开了流，却一个字不回就断开（2026-11-07 kinght 实际发生）：两种检测请求都记成失败。
func TestProbeHungStreamIsError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01x\",\"model\":\"m\",\"usage\":{\"input_tokens\":5}}}\n\n")
			return
		}
		io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	}))
	defer up.Close()
	h := &Handler{Client: up.Client()}
	ch := &store.Channel{BaseURLChat: up.URL, BaseURLMessages: up.URL, APIKey: "k"}
	if c := h.ProbeChat(context.Background(), ch, "m", "s", probe.Request{Prompt: "hi"}); !strings.Contains(c.Error, "没有正常结束") {
		t.Errorf("Chat 断流应记成失败: %+v", c)
	}
	if c := h.ProbeMessages(context.Background(), ch, "m", "s", probe.Request{Prompt: "hi"}); !strings.Contains(c.Error, "没有正常结束") {
		t.Errorf("Messages 断流应记成失败: %+v", c)
	}
}

// 官方 API 环境的 astra 不支持推理强度 none，报错里没有 reasoning 字样（2026-11-08 检测 59）：换成 low 重试。
func TestProbeChatRetriesUnsupportedNone(t *testing.T) {
	var efforts []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		e, _ := body["reasoning_effort"].(string)
		efforts = append(efforts, e)
		if e == "none" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"Unsupported value: 'none' is not supported with the 'gpt-6-astra' model. Supported values are: 'low', 'medium', 'high' and 'xhigh'."}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"mango\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	h := &Handler{Client: up.Client()}
	c := h.ProbeChat(context.Background(), &store.Channel{BaseURLChat: up.URL, APIKey: "k"}, "gpt-6-astra", "s", probe.Request{Prompt: "fruit?", Effort: "none"})
	if c.Error != "" || c.Text != "mango" || strings.Join(efforts, ",") != "none,low" || c.EffortSent != "low" || c.NoneRejectedBy != "400" {
		t.Errorf("应换成 low 重试并成功: %+v，推理强度依次 %v", c, efforts)
	}
}

// 有的中转把 Chat 转成 Responses 后，上游不支持 none 时什么都不回（aresai，2026-11-08 检测 60：0 token、流没有结束标记）：也换 low 重试。
func TestProbeChatRetriesNoneOnEmptyStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		if body["reasoning_effort"] == "none" {
			io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
			return
		}
		io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"mango\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	h := &Handler{Client: up.Client()}
	c := h.ProbeChat(context.Background(), &store.Channel{BaseURLChat: up.URL, APIKey: "k"}, "gpt-6-astra", "s", probe.Request{Prompt: "fruit?", Effort: "none"})
	if c.Error != "" || c.Text != "mango" || c.EffortSent != "low" || c.NoneRejectedBy != "empty" {
		t.Errorf("空流应换成 low 重试并成功: %+v", c)
	}
}
