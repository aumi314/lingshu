package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aumi314/lingshu/internal/relay/bridge"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/router"
	"github.com/aumi314/lingshu/internal/store"
)

func hasCompactionTrigger(raw json.RawMessage) bool {
	var items []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return false
	}
	for _, item := range items {
		if item.Type == "compaction_trigger" {
			return true
		}
	}
	return false
}

func withCompactionTrigger(body []byte) []byte {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	if !hasCompactionTrigger(fields["input"]) {
		var items []json.RawMessage
		raw := fields["input"]
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &items) != nil {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return body
			}
			item, _ := json.Marshal(map[string]any{"type": "message", "role": "user", "content": text})
			items = append(items, item)
		}
		items = append(items, json.RawMessage(`{"type":"compaction_trigger"}`))
		fields["input"], _ = json.Marshal(items)
	}
	b, _ := json.Marshal(fields)
	return b
}

func responsesErrorBody(status int, message string) []byte {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"type": openAIErrorType(status), "code": "upstream_error", "message": message}})
	return b
}

func upstreamErrorMessage(b []byte) string {
	var v struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &v) == nil && v.Error.Message != "" {
		return v.Error.Message
	}
	return truncate(string(b), 1000)
}

// 转接按原始 Messages 事件取用量；输出侧的 Responses usage 只服务客户端。
func (h *Handler) relayResponsesBridge(w http.ResponseWriter, r *http.Request, c *call, route store.Route, resp *http.Response, streaming bool, q *bridge.Request, compactEndpoint bool) *failure {
	id := "resp_lingshu_" + strings.TrimPrefix(c.log.RequestID, "req_")
	c.log.UpstreamResponseID = id
	state := bridge.NewStream(id)
	state.Bind(q)
	state.SetLifecycle(true)
	reader := usage.NewSSEReader(resp.Body)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRequestBody+1))
		if err != nil || len(raw) > maxRequestBody {
			return &failure{status: 502, reason: "读取 Messages JSON 失败", msg: "上游响应读取失败或超过大小限制"}
		}
		// 同一状态机处理不遵守 stream:true 的 JSON 上游；保留 JSON 字符串转义。
		// 用量和结束标记由下面逐个事件交给 tracker，这里不再整段记一次（否则按文字估算的输出会算两遍）
		encoded, err := jsonBodyEvents(raw)
		if err != nil {
			return &failure{status: 502, reason: err.Error(), msg: err.Error()}
		}
		reader = usage.NewSSEReader(strings.NewReader(encoded))
	}
	committed := false
	rc := http.NewResponseController(w)
	emit := func(events [][]byte) error {
		if !streaming || q.Compact || len(events) == 0 {
			return nil
		}
		if !committed {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			c.log.HTTPStatus = http.StatusOK
			committed = true
		}
		for _, data := range events {
			// 事件边界独立编码，JSON 字符串内的反斜杠保持原样。
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return err
			}
			if err := rc.Flush(); err != nil {
				return err
			}
			c.markFirstByte()
		}
		return nil
	}
	var streamErr error
	for {
		ev, err := reader.Next()
		if err != nil {
			streamErr = err
			break
		}
		c.tracker.ObserveEvent(ev.Data)
		data := ev.Data
		if c.tracker.Failed {
			data = redactKey(data, route.APIKey)
		}
		events, err := state.Event(data)
		if err != nil {
			streamErr = err
			if committed && len(events) > 0 {
				if writeErr := emit(events); writeErr != nil {
					c.log.Status = "client_cancelled"
					return nil
				}
			}
			break
		}
		if state.Failed() && !committed {
			streamErr = errors.New(upstreamErrorMessage(ev.Data))
			break
		}
		if len(events) > 0 {
			if err := emit(events); err != nil {
				c.log.Status = "client_cancelled"
				return nil
			}
		}
		if state.Done() {
			break
		}
	}
	if r.Context().Err() != nil {
		c.log.Status = "client_cancelled"
		return nil
	}
	if (streamErr != nil && !errors.Is(streamErr, io.EOF)) || !state.Done() || state.Failed() || !c.tracker.Terminal || c.tracker.Failed {
		message := "上游流在正常结束前断开"
		if streamErr != nil && !errors.Is(streamErr, io.EOF) {
			message = "Messages 转接失败: " + streamErr.Error()
		}
		message = string(redactKey([]byte(message), route.APIKey))
		h.Health.Record(route.ChannelID, router.Failure)
		if !committed {
			return &failure{status: 502, reason: message, msg: message}
		}
		c.log.Status, c.log.Error = "upstream_error", message
		if !state.Failed() {
			events, _ := state.Close()
			_ = emit(events)
		}
		return nil
	}
	result, err := state.Response()
	if err != nil {
		h.Health.Record(route.ChannelID, router.Failure)
		if !committed {
			return &failure{status: 502, reason: err.Error(), msg: err.Error()}
		}
		c.log.Status, c.log.Error = "upstream_error", err.Error()
		return nil
	}
	if compactEndpoint {
		var v map[string]json.RawMessage
		_ = json.Unmarshal(result, &v)
		v["object"] = json.RawMessage(`"response.compaction"`)
		delete(v, "status")
		result, _ = json.Marshal(v)
	}
	if q.Compact && streaming {
		events, err := compactResponseEvents(result)
		if err != nil {
			return &failure{status: 502, reason: err.Error(), msg: err.Error()}
		}
		// 摘要成功后才把压缩条目交给客户端，失败时原历史仍可重试。
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		for _, event := range events {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
				c.log.Status = "client_cancelled"
				return nil
			}
			if err := rc.Flush(); err != nil {
				c.log.Status = "client_cancelled"
				return nil
			}
		}
		c.markFirstByte()
	} else if !streaming {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(result); err != nil {
			c.log.Status = "client_cancelled"
			return nil
		}
		c.markFirstByte()
	}
	c.log.HTTPStatus, c.log.Status = http.StatusOK, "success"
	h.Health.Record(route.ChannelID, router.Success)
	h.Keys.OK(route.KeyID)
	return nil
}

func compactResponseEvents(result []byte) ([][]byte, error) {
	var response map[string]any
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	output, _ := response["output"].([]any)
	if len(output) == 0 {
		return nil, errors.New("压缩没有返回摘要条目")
	}
	events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": response["id"], "object": "response", "status": "in_progress", "output": []any{}}}}
	for i, item := range output {
		events = append(events, map[string]any{"type": "response.output_item.added", "output_index": i, "item": item}, map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
	}
	events = append(events, map[string]any{"type": "response.completed", "response": response})
	var out [][]byte
	for i, event := range events {
		event["sequence_number"] = i
		b, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func rejectBridge(w http.ResponseWriter, c *call, err error) {
	c.log.Status, c.log.HTTPStatus, c.log.Error = "rejected", http.StatusBadRequest, err.Error()
	var detail *bridge.Error
	if errors.As(err, &detail) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": detail.Code, "param": detail.Field, "message": detail.Message}})
		return
	}
	writeError(w, c.proto, http.StatusBadRequest, err.Error())
}

func jsonBodyEvents(raw []byte) (string, error) {
	var msg struct {
		Type       string           `json:"type"`
		Content    []map[string]any `json:"content"`
		StopReason string           `json:"stop_reason"`
		Usage      json.RawMessage  `json:"usage"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return "", err
	}
	if msg.Type != "message" {
		return "", errors.New("上游没有返回 Messages 响应")
	}
	var buf strings.Builder
	emit := func(v any) { b, _ := json.Marshal(v); fmt.Fprintf(&buf, "data: %s\n\n", b) }
	var full map[string]any
	_ = json.Unmarshal(raw, &full)
	full["content"] = []any{}
	emit(map[string]any{"type": "message_start", "message": full})
	for i, block := range msg.Content {
		emit(map[string]any{"type": "content_block_start", "index": i, "content_block": block})
		emit(map[string]any{"type": "content_block_stop", "index": i})
	}
	emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": msg.StopReason}, "usage": msg.Usage})
	emit(map[string]any{"type": "message_stop"})
	return buf.String(), nil
}
