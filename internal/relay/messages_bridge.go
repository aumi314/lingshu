package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aumi314/lingshu/internal/relay/messagesbridge"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/router"
	"github.com/aumi314/lingshu/internal/store"
)

func messagesErrorBody(status int, message string) []byte {
	kind := "api_error"
	switch status {
	case 400:
		kind = "invalid_request_error"
	case 401:
		kind = "authentication_error"
	case 403:
		kind = "permission_error"
	case 404:
		kind = "not_found_error"
	case 413:
		kind = "request_too_large"
	case 429:
		kind = "rate_limit_error"
	case 503, 529:
		kind = "overloaded_error"
	}
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": message}})
	return body
}

// 用 Responses 原始事件计费；Messages 输出单独转换。
func (h *Handler) relayMessagesBridge(w http.ResponseWriter, r *http.Request, c *call, route store.Route, resp *http.Response, streaming bool, q *messagesbridge.Request) *failure {
	state := messagesbridge.NewStream("msg_lingshu_"+strings.TrimPrefix(c.log.RequestID, "req_"), q)
	committed := false
	rc := http.NewResponseController(w)
	emit := func(events [][]byte) error {
		if !streaming || len(events) == 0 {
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
			var event struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
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
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRequestBody+1))
		if err != nil || len(raw) > maxRequestBody {
			return &failure{status: 502, reason: "读取 Responses JSON 失败", msg: "上游响应读取失败或超过大小限制"}
		}
		c.tracker.ObserveBody(raw)
		events, err := state.ObserveBody(raw)
		streamErr = err
		if err == nil {
			if err = emit(events); err != nil {
				c.log.Status = "client_cancelled"
				return nil
			}
		}
	} else {
		reader := usage.NewSSEReader(resp.Body)
		for {
			event, err := reader.Next()
			if err != nil {
				streamErr = err
				break
			}
			c.tracker.ObserveEvent(event.Data)
			data := event.Data
			if c.tracker.Failed {
				data = redactKey(data, route.APIKey)
			}
			events, err := state.Event(data)
			if err != nil {
				streamErr = err
				break
			}
			if state.Failed() {
				streamErr = errors.New(upstreamErrorMessage(event.Data))
				break
			}
			if err := emit(events); err != nil {
				c.log.Status = "client_cancelled"
				return nil
			}
			if state.Done() {
				break
			}
		}
	}
	c.log.UpstreamResponseID = state.UpstreamID()
	if r.Context().Err() != nil {
		c.log.Status = "client_cancelled"
		return nil
	}
	if (streamErr != nil && !errors.Is(streamErr, io.EOF)) || !state.Done() || state.Failed() || !c.tracker.Terminal || c.tracker.Failed {
		message := "Responses 上游流在正常结束前断开"
		if streamErr != nil && !errors.Is(streamErr, io.EOF) {
			message = "Responses 转接失败: " + streamErr.Error()
		}
		message = string(redactKey([]byte(message), route.APIKey))
		h.Health.Record(route.ChannelID, router.Failure)
		if !committed {
			return &failure{status: 502, reason: message, msg: message}
		}
		c.log.Status, c.log.Error = "upstream_error", message
		_ = emit([][]byte{messagesErrorBody(502, message)})
		return nil
	}
	result, err := state.Response()
	if err != nil {
		h.Health.Record(route.ChannelID, router.Failure)
		if !committed {
			return &failure{status: 502, reason: err.Error(), msg: err.Error()}
		}
		c.log.Status, c.log.Error = "upstream_error", err.Error()
		_ = emit([][]byte{messagesErrorBody(502, err.Error())})
		return nil
	}
	if !streaming {
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
