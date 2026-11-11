package relay

import (
	"bytes"
	"context"
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

// ProbeResponses 向渠道发一次 GPT 检测请求，走 Responses 协议（真实 Codex 只走这个接口；走 Chat 再挂 Codex 的 UA 一眼就能认出是检测）。
// 和 ProbeChat 一样直接用渠道自己的地址、key 和代理：不走路由、不计费、不写调用日志、不影响渠道健康状态。
// 有伪装配置时请求和真实 Codex 0.160 同形：input 前缀是工具、开发者说明和环境，会话元数据进请求头和 client_metadata，
// 不传 temperature、max_output_tokens。
func (h *Handler) ProbeResponses(ctx context.Context, ch *store.Channel, upstreamModel, session string, req probe.Request) probe.Call {
	return effortFallback(ctx, req, func(r probe.Request) probe.Call { return h.probeResponsesOnce(ctx, ch, upstreamModel, session, r) })
}

func (h *Handler) probeResponsesOnce(ctx context.Context, ch *store.Channel, upstreamModel, session string, req probe.Request) probe.Call {
	var c probe.Call
	if ch.BaseURLResponses == "" {
		c.Error = "这个渠道没有配置 Responses 协议的地址"
		return c
	}
	d := h.Disguise
	now := time.Now()
	body := map[string]any{"model": upstreamModel, "stream": true, "store": false, "include": []string{"reasoning.encrypted_content"}, "prompt_cache_key": session}
	reasoning := map[string]any{}
	if req.Effort != "" {
		reasoning["effort"] = req.Effort
	}
	text := map[string]any{}
	var turnMeta string
	if d.hasCodex() {
		for k, v := range d.Codex.Body {
			body[k] = v
		}
		if v, ok := d.Codex.Body["text"].(map[string]any); ok {
			for k, x := range v {
				text[k] = x
			}
		}
		if d.Codex.ReasoningContext != "" {
			reasoning["context"] = d.Codex.ReasoningContext
		}
		body["input"] = d.codexInput(req.System, req.Prompt, now)
		var clientMeta map[string]any
		turnMeta, clientMeta = d.codexSessionMeta(session, upstreamModel, req.Effort, now)
		body["client_metadata"] = clientMeta
	} else {
		var input []any
		if req.System != "" {
			input = append(input, map[string]any{"type": "message", "role": "developer", "content": []map[string]any{{"type": "input_text", "text": req.System}}})
		}
		body["input"] = append(input, map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": req.Prompt}}})
		if req.MaxTokens > 0 {
			body["max_output_tokens"] = max(req.MaxTokens, 16) // Responses 协议下限 16
		}
	}
	if len(reasoning) > 0 {
		body["reasoning"] = reasoning
	}
	// 检测题的结构化输出（Chat 的 response_format）换成 Responses 的 text.format
	if rf := req.ResponseFormat; rf != nil {
		if js, ok := rf["json_schema"].(map[string]any); ok {
			text["format"] = map[string]any{"type": "json_schema", "name": js["name"], "strict": js["strict"], "schema": js["schema"]}
		}
	}
	if len(text) > 0 {
		body["text"] = text
	}
	// 检测题的工具（Chat 格式）换成 Responses 的 function 工具
	var tools []any
	for _, t := range req.Tools {
		if f, ok := t["function"].(map[string]any); ok {
			tools = append(tools, map[string]any{"type": "function", "name": f["name"], "description": f["description"], "parameters": f["parameters"]})
		}
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	raw, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ch.BaseURLResponses, "/")+upstreamPaths[usage.Responses], bytes.NewReader(raw))
	if err != nil {
		c.Error = err.Error()
		return c
	}
	hd := httpReq.Header
	hd.Set("Content-Type", "application/json")
	hd.Set("Accept", "text/event-stream")
	if d.hasCodex() {
		for k, v := range d.Codex.Headers {
			hd.Set(k, v)
		}
		hd.Set("X-Codex-Turn-Metadata", turnMeta)
		hd.Set("X-Codex-Window-Id", session+":0")
	} else {
		hd.Set("User-Agent", probeUserAgent)
		hd.Set("Originator", "codex-tui")
	}
	hd.Set("Session-Id", session)
	hd.Set("Thread-Id", session)
	hd.Set("X-Client-Request-Id", session)
	setUpstreamAuth(hd, usage.Responses, ch.APIKey)

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

	tracker := usage.NewTracker(usage.Responses)
	var out strings.Builder
	type item struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	var final []item // response.completed 里的完整输出：工具调用以它为准，正文没收到增量时也从这里取
	parse := func(data []byte) {
		var ev struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				ID          string `json:"id"`
				Model       string `json:"model"`
				Status      string `json:"status"`
				ServiceTier string `json:"service_tier"`
				Output      []item `json:"output"`
				Error       *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return
		}
		if ev.Response.ID != "" {
			c.ID = ev.Response.ID
		}
		if ev.Response.Model != "" {
			c.Model = ev.Response.Model
		}
		if ev.Response.ServiceTier != "" {
			c.ServiceTier = ev.Response.ServiceTier
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" && c.FirstTokenMS == 0 {
				c.FirstTokenMS = max(int(time.Since(start).Milliseconds()), 1)
			}
			out.WriteString(ev.Delta)
		case "response.function_call_arguments.delta":
			if c.FirstTokenMS == 0 {
				c.FirstTokenMS = max(int(time.Since(start).Milliseconds()), 1)
			}
		case "response.completed", "response.incomplete":
			c.Done = ev.Type == "response.completed"
			c.FinishReason = ev.Response.Status
			final = ev.Response.Output
		case "response.failed":
			msg := "上游报告失败"
			if ev.Response.Error != nil {
				msg += ": " + ev.Response.Error.Message
			}
			c.Error = truncate(msg, 300)
		case "error":
			c.Error = truncate("上游在流中报错: "+ev.Message, 300)
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
	} else { // 要了流式却回了整段 JSON：按 response 对象解析，Done 记为 false，请求质量里会提示
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		tracker.ObserveBody(b)
		var r struct {
			ID     string `json:"id"`
			Model  string `json:"model"`
			Status string `json:"status"`
			Output []item `json:"output"`
		}
		if json.Unmarshal(b, &r) == nil {
			c.ID, c.Model, c.FinishReason, final = r.ID, r.Model, r.Status, r.Output
		}
	}
	c.TotalMS = int(time.Since(start).Milliseconds())
	c.Text = out.String()
	for _, it := range final {
		switch it.Type {
		case "function_call":
			c.ToolCalls = append(c.ToolCalls, probe.ToolCall{Name: it.Name, Arguments: it.Arguments})
		case "message":
			if c.Text == "" {
				for _, part := range it.Content {
					c.Text += part.Text
				}
			}
		}
	}
	if tracker.Seen() {
		u := tracker.Result()
		c.UsageReported = true
		c.InputTokens = u.Input + u.CacheRead
		c.CachedTokens = u.CacheRead
		c.OutputTokens = u.Output
		c.ReasoningTokens = u.Reasoning
	}
	if ctx.Err() != nil && c.Error == "" && !c.Done {
		c.Error = "超时或被取消"
	}
	if c.Error == "" && !c.Done && c.FinishReason == "" { // 同 ProbeChat：上游中途断开记成失败
		c.Error = "流没有正常结束（没有 response.completed）"
	}
	return c
}
