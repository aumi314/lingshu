package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/probe"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

// probeUserAgent 是检测请求伪装成的客户端（Codex 命令行工具），避免中转认出检测流量、专门转给真模型。
const probeUserAgent = "codex-tui/0.163.0 (Ubuntu 24.4.0; x86_64) WindowsTerminal (codex-tui; 0.163.0)"

// NewProbeSession 生成一个检测会话编号（UUID 格式）；同一会话的请求像同一段 Codex 对话。
func NewProbeSession() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// ProbeChat 向渠道发一次检测请求（Chat 协议、流式），直接用渠道自己的地址、key 和代理：不走路由、不计费、不写调用日志、不影响渠道健康状态。
// 上游不支持请求里的推理强度（比如不支持 none）时，去掉推理强度重试一次。
func (h *Handler) ProbeChat(ctx context.Context, ch *store.Channel, upstreamModel, session string, req probe.Request) probe.Call {
	c := h.probeChatOnce(ctx, ch, upstreamModel, session, req)
	if c.HTTPStatus == http.StatusBadRequest && req.Effort != "" && strings.Contains(strings.ToLower(c.Error), "reasoning") {
		req.Effort = ""
		c = h.probeChatOnce(ctx, ch, upstreamModel, session, req)
	}
	return c
}

func (h *Handler) probeChatOnce(ctx context.Context, ch *store.Channel, upstreamModel, session string, req probe.Request) probe.Call {
	var c probe.Call
	if ch.BaseURLChat == "" {
		c.Error = "这个渠道没有配置 Chat 协议的地址"
		return c
	}
	var msgs []map[string]string
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": req.Prompt})
	body := map[string]any{"model": upstreamModel, "messages": msgs, "stream": true, "stream_options": map[string]any{"include_usage": true}}
	if req.Effort != "" {
		body["reasoning_effort"] = req.Effort
	}
	if req.MaxTokens > 0 {
		body["max_completion_tokens"] = req.MaxTokens
	}
	if req.ResponseFormat != nil {
		body["response_format"] = req.ResponseFormat
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	raw, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ch.BaseURLChat, "/")+upstreamPaths[usage.Chat], bytes.NewReader(raw))
	if err != nil {
		c.Error = err.Error()
		return c
	}
	hd := httpReq.Header
	hd.Set("Content-Type", "application/json")
	hd.Set("Accept", "text/event-stream")
	hd.Set("User-Agent", probeUserAgent)
	hd.Set("Originator", "codex-tui")
	hd.Set("Session-Id", session)
	hd.Set("Thread-Id", session)
	hd.Set("X-Client-Request-Id", session)
	setUpstreamAuth(hd, usage.Chat, ch.APIKey)

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

	tracker := usage.NewTracker(usage.Chat)
	var text strings.Builder
	type partial struct{ name, args strings.Builder }
	var tools []*partial
	parse := func(data []byte) {
		var ev struct {
			ID          string `json:"id"`
			Model       string `json:"model"`
			ServiceTier string `json:"service_tier"`
			Choices     []struct {
				Delta        probeMessage `json:"delta"`   // 流式
				Message      probeMessage `json:"message"` // 整段 JSON
				FinishReason string       `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return
		}
		if ev.ID != "" {
			c.ID = ev.ID
		}
		if ev.Model != "" {
			c.Model = ev.Model
		}
		if ev.ServiceTier != "" {
			c.ServiceTier = ev.ServiceTier
		}
		for _, choice := range ev.Choices {
			for _, m := range []probeMessage{choice.Delta, choice.Message} {
				if (m.Content != "" || len(m.ToolCalls) > 0) && c.FirstTokenMS == 0 {
					c.FirstTokenMS = max(int(time.Since(start).Milliseconds()), 1)
				}
				text.WriteString(m.Content)
				for _, tc := range m.ToolCalls { // 流式里工具调用按 index 分片发，名字和参数要拼起来
					for len(tools) <= tc.Index {
						tools = append(tools, &partial{})
					}
					tools[tc.Index].name.WriteString(tc.Function.Name)
					tools[tc.Index].args.WriteString(tc.Function.Arguments)
				}
			}
			if choice.FinishReason != "" {
				c.FinishReason = choice.FinishReason
			}
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
			if string(ev.Data) == "[DONE]" {
				c.Done = true
				continue
			}
			tracker.ObserveEvent(ev.Data)
			parse(ev.Data)
		}
	} else { // 要了流式却回了整段 JSON：照样解析，Done 记为 false，请求质量里会提示
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		tracker.ObserveBody(b)
		parse(b)
	}
	c.TotalMS = int(time.Since(start).Milliseconds())
	c.Text = text.String()
	for _, t := range tools {
		c.ToolCalls = append(c.ToolCalls, probe.ToolCall{Name: t.name.String(), Arguments: t.args.String()})
	}
	if tracker.Seen() {
		u := tracker.Result()
		c.UsageReported = true
		c.InputTokens = u.Input + u.CacheRead + u.CacheWrite
		c.CachedTokens = u.CacheRead
		c.OutputTokens = u.Output
		c.ReasoningTokens = u.Reasoning
	}
	if ctx.Err() != nil && c.Error == "" && !c.Done {
		c.Error = "超时或被取消"
	}
	return c
}

// probeMessage 是 Chat 响应里的一条消息或一个增量。
type probeMessage struct {
	Content   string `json:"content"`
	ToolCalls []struct {
		Index    int `json:"index"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}
