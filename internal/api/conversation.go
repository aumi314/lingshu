package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

// 对话留存的查看（docs/decisions.md「对话留存」）：只有管理员能看，按调用日志取回还原后的请求和回复原文。

func (s *Server) logConversation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	v, err := s.Store.LoadCapture(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"captured": false})
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	request := json.RawMessage(v.Request)
	if !json.Valid(v.Request) {
		request, _ = json.Marshal(string(v.Request))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"captured": true, "session": v.Session, "request": request,
		"response": string(v.Response), "response_truncated": v.ResponseTruncated,
		"reply_text": replyText(v.Protocol, v.Response),
	})
}

// replyText 从客户端收到的回复里拼出模型说的正文（流式按增量拼，整段 JSON 取内容），给页面直接看；工具调用和推理不在里面。
func replyText(protocol string, body []byte) string {
	var out strings.Builder
	take := func(data []byte) {
		var ev struct {
			Type    string `json:"type"`
			Delta   json.RawMessage
			Choices []struct {
				Delta   struct{ Content string } `json:"delta"`
				Message struct{ Content string } `json:"message"`
			} `json:"choices"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Output []struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return
		}
		switch protocol {
		case "chat":
			for _, c := range ev.Choices {
				out.WriteString(c.Delta.Content)
				out.WriteString(c.Message.Content)
			}
		case "messages":
			if ev.Type == "content_block_delta" {
				var d struct{ Type, Text string }
				if json.Unmarshal(ev.Delta, &d) == nil && d.Type == "text_delta" {
					out.WriteString(d.Text)
				}
			}
			if ev.Type == "message" {
				for _, c := range ev.Content {
					if c.Type == "text" {
						out.WriteString(c.Text)
					}
				}
			}
		case "responses":
			if ev.Type == "response.output_text.delta" {
				var d string
				if json.Unmarshal(ev.Delta, &d) == nil {
					out.WriteString(d)
				}
			}
			if ev.Type == "" {
				for _, o := range ev.Output {
					for _, c := range o.Content {
						if c.Type == "output_text" {
							out.WriteString(c.Text)
						}
					}
				}
			}
		}
	}
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("{")) {
		take(trimmed)
		return out.String()
	}
	reader := usage.NewSSEReader(bytes.NewReader(body))
	for {
		ev, err := reader.Next()
		if err != nil {
			break
		}
		take(ev.Data)
	}
	return out.String()
}
