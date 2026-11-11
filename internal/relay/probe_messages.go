package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/probe"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

// 检测请求伪装成 Claude Code（2026-11-07 从 hvoy 的抓包核对过的形态）：Claude 号池类中转只接受像 Claude Code 的请求，不伪装测不到真实链路。
const (
	probeClaudeUA   = "claude-cli/2.1.319.781 (external, cli)"
	probeClaudeBeta = "claude-code-20250219,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24"
)

// ProbeMessages 向渠道发一次 Claude 检测请求（Messages 协议、流式），和 ProbeChat 一样直接用渠道自己的地址、key 和代理：
// 不走路由、不计费、不写调用日志、不影响渠道健康状态。req.Effort 非空时开 adaptive thinking 并设推理强度。
// session 放进 metadata.user_id 的 session_id：号池按会话路由，一个检测会话像一段 Claude Code 对话。
func (h *Handler) ProbeMessages(ctx context.Context, ch *store.Channel, upstreamModel, session string, req probe.Request) probe.Call {
	var c probe.Call
	if ch.BaseURLMessages == "" {
		c.Error = "这个渠道没有配置 Messages 协议的地址"
		return c
	}
	d := h.Disguise
	var system []map[string]any
	if d.hasClaude() { // 完整伪装：真实 Claude Code 的系统提示，检测自己的系统指令接在最后
		system = d.claudeSystem(upstreamModel, req.System)
	} else {
		system = []map[string]any{
			{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.319.781; cc_entrypoint=cli;"},
			{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
		}
		if req.System != "" {
			system = append(system, map[string]any{"type": "text", "text": req.System})
		}
	}
	var content any = d.directPrompt(req.Prompt, len(req.Tools) > 0, d.hasClaude() && len(d.Claude.Tools) > 0)
	if req.Document != "" {
		content = []map[string]any{
			{"type": "document", "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": req.Document}},
			{"type": "text", "text": content},
		}
	}
	device := make([]byte, 32)
	rand.Read(device)
	userID, _ := json.Marshal(map[string]string{"device_id": hex.EncodeToString(device), "account_uuid": NewProbeSession(), "session_id": session})
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096 // Messages 协议必须给 max_tokens
		if d.hasClaude() && d.Claude.MaxTokens > 0 {
			maxTokens = d.Claude.MaxTokens
		}
	}
	body := map[string]any{"model": upstreamModel, "max_tokens": maxTokens, "stream": true, "system": system,
		"messages": []map[string]any{{"role": "user", "content": content}}, "metadata": map[string]any{"user_id": string(userID)}}
	if req.Effort != "" {
		body["thinking"] = map[string]any{"type": "adaptive"}
		body["output_config"] = map[string]any{"effort": req.Effort}
	}
	var tools []map[string]any
	if d.hasClaude() {
		tools = append(tools, d.Claude.Tools...)
	}
	if tools = append(tools, req.Tools...); len(tools) > 0 {
		body["tools"] = tools
	}
	raw, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ch.BaseURLMessages, "/")+upstreamPaths[usage.Messages], bytes.NewReader(raw))
	if err != nil {
		c.Error = err.Error()
		return c
	}
	hd := httpReq.Header
	hd.Set("Content-Type", "application/json")
	if d.hasClaude() { // 真实 Claude Code 的请求头（SDK 标识、beta 列表等），会话编号按检测会话、请求编号每次新生成
		for k, v := range d.Claude.Headers {
			hd.Set(k, v)
		}
		hd.Set("X-Claude-Code-Session-Id", session)
		hd.Set("X-Client-Request-Id", NewProbeSession())
	} else {
		hd.Set("Accept", "text/event-stream")
		hd.Set("User-Agent", probeClaudeUA)
		hd.Set("X-App", "cli")
		hd.Set("Anthropic-Beta", probeClaudeBeta)
	}
	setUpstreamAuth(hd, usage.Messages, ch.APIKey)

	start := time.Now()
	resp, err := h.clientFor(ch.ProxyURL).Do(httpReq)
	if err != nil {
		c.Error = "连接失败: " + err.Error()
		return c
	}
	defer resp.Body.Close()
	c.HTTPStatus = resp.StatusCode
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		c.Error = fmt.Sprintf("HTTP %d %s", resp.StatusCode, truncate(string(redactKey(b, ch.APIKey)), 300))
		return c
	}

	tracker := usage.NewTracker(usage.Messages)
	var text strings.Builder
	type block struct {
		kind, name, signature string
		input                 strings.Builder
	}
	var blocks []*block
	at := func(i int) *block {
		for len(blocks) <= i {
			blocks = append(blocks, &block{})
		}
		return blocks[i]
	}
	parse := func(data []byte) {
		var ev struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				ID    string                     `json:"id"`
				Model string                     `json:"model"`
				Usage map[string]json.RawMessage `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Signature string `json:"signature"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokensDetails struct {
					ThinkingTokens int64 `json:"thinking_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Type == "" {
			return
		}
		if n := len(c.EventTypes); n == 0 || c.EventTypes[n-1] != ev.Type {
			c.EventTypes = append(c.EventTypes, ev.Type)
		}
		switch ev.Type {
		case "message_start":
			c.ID, c.Model = ev.Message.ID, ev.Message.Model
			for k := range ev.Message.Usage {
				c.UsageFields = append(c.UsageFields, k)
			}
			sort.Strings(c.UsageFields)
		case "content_block_start":
			b := at(ev.Index)
			b.kind, b.name, b.signature = ev.ContentBlock.Type, ev.ContentBlock.Name, ev.ContentBlock.Signature
		case "content_block_delta":
			if c.FirstTokenMS == 0 {
				c.FirstTokenMS = max(int(time.Since(start).Milliseconds()), 1)
			}
			b := at(ev.Index)
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
			case "signature_delta":
				b.signature += ev.Delta.Signature
			case "input_json_delta":
				b.input.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				c.FinishReason = ev.Delta.StopReason
			}
			if t := ev.Usage.OutputTokensDetails.ThinkingTokens; t > 0 {
				c.ReasoningTokens = t
			}
		case "message_stop":
			c.Done = true
		case "error": // 已经回了 200 再在流里报错（上下文超长、过载等）：算请求失败，不当成模型答错
			c.Error = truncate("上游在流中报错: "+ev.Error.Type+" "+ev.Error.Message, 300)
		}
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		reader := usage.NewSSEReader(resp.Body)
		for {
			ev, err := reader.Next()
			if err != nil {
				break
			}
			c.Events++
			tracker.ObserveEvent(ev.Data)
			parse(ev.Data)
		}
	} else { // 要了流式却回了整段 JSON：只取用量，事件序列为空，协议规范项会指出
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		tracker.ObserveBody(b)
	}
	c.TotalMS = int(time.Since(start).Milliseconds())
	c.Text = text.String()
	for _, b := range blocks {
		switch b.kind {
		case "thinking":
			if b.signature != "" {
				c.SignedThinking++
			}
		case "tool_use":
			c.ToolCalls = append(c.ToolCalls, probe.ToolCall{Name: b.name, Arguments: b.input.String()})
		}
	}
	if tracker.Seen() {
		u := tracker.Result()
		c.UsageReported = true
		c.InputTokens = u.Input + u.CacheRead + u.CacheWrite
		c.CachedTokens = u.CacheRead
		c.OutputTokens = u.Output
	}
	if ctx.Err() != nil && c.Error == "" && !c.Done {
		c.Error = "超时或被取消"
	}
	if c.Error == "" && !c.Done && c.FinishReason == "" { // 同 ProbeChat：上游中途断开记成失败
		c.Error = "流没有正常结束（没有 message_stop）"
	}
	return c
}
