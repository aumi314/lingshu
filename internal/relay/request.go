package relay

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

// request 保留客户端请求体的全部字段，只改动网关必须改的部分。解析后不再修改，重试时每次重新生成。
type request struct {
	fields             map[string]json.RawMessage
	body               []byte
	model              string
	stream             bool
	previousResponseID string // responses 协议：接着哪次响应继续
	effort             string // 客户端要求的推理强度，只用于记日志
	maxOutput          int64  // 客户端写的最大输出 token 数，0 表示没写；用于预扣
}

func parseRequest(body []byte) (*request, error) {
	req := &request{body: body}
	if err := json.Unmarshal(body, &req.fields); err != nil {
		return nil, errors.New("请求体不是合法的 JSON 对象")
	}
	if err := json.Unmarshal(req.fields["model"], &req.model); err != nil || req.model == "" {
		return nil, errors.New("请求体缺少 model 字段")
	}
	if raw, ok := req.fields["stream"]; ok {
		json.Unmarshal(raw, &req.stream)
	}
	if raw, ok := req.fields["previous_response_id"]; ok {
		json.Unmarshal(raw, &req.previousResponseID)
	}
	req.effort = reasoningEffort(req.fields)
	// chat 用 max_tokens 或 max_completion_tokens，messages 用 max_tokens，responses 用 max_output_tokens
	for _, k := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		var n int64
		if json.Unmarshal(req.fields[k], &n) == nil && n > req.maxOutput {
			req.maxOutput = n
		}
	}
	return req, nil
}

// reasoningEffort 取客户端要求的推理强度：chat 的 reasoning_effort、responses 的 reasoning.effort、
// messages 的 output_config.effort；messages 只传 thinking 时，记预算（budget:N）或 thinking 类型。
func reasoningEffort(fields map[string]json.RawMessage) string {
	var effort string
	if json.Unmarshal(fields["reasoning_effort"], &effort) == nil && effort != "" {
		return effort
	}
	var nested struct {
		Effort string `json:"effort"`
	}
	for _, key := range []string{"reasoning", "output_config"} {
		if json.Unmarshal(fields[key], &nested) == nil && nested.Effort != "" {
			return nested.Effort
		}
	}
	var thinking struct {
		Type   string `json:"type"`
		Budget int    `json:"budget_tokens"`
	}
	if json.Unmarshal(fields["thinking"], &thinking) == nil {
		if thinking.Type == "enabled" && thinking.Budget > 0 {
			return "budget:" + strconv.Itoa(thinking.Budget)
		}
		return thinking.Type
	}
	return ""
}

// bodyFor 生成发给某个渠道的请求体：model 换成上游名；chat 流式请求补上 include_usage。
// 返回是否替客户端加了 include_usage（加了的话，usage 块不转发给客户端）。
func (req *request) bodyFor(proto usage.Protocol, upstreamModel string) (body []byte, injected bool) {
	fields := make(map[string]json.RawMessage, len(req.fields)+1)
	for k, v := range req.fields {
		fields[k] = v
	}
	changed := false
	if upstreamModel != req.model {
		fields["model"], _ = json.Marshal(upstreamModel)
		changed = true
	}
	if proto == usage.Chat && req.stream {
		opts := map[string]json.RawMessage{}
		if raw, ok := fields["stream_options"]; ok {
			json.Unmarshal(raw, &opts)
		}
		var include bool
		json.Unmarshal(opts["include_usage"], &include)
		if !include {
			opts["include_usage"] = json.RawMessage("true")
			fields["stream_options"], _ = json.Marshal(opts)
			changed, injected = true, true
		}
	}
	if !changed {
		return req.body, false
	}
	body, _ = json.Marshal(fields)
	return body, injected
}

// 不转发给上游的请求头：鉴权、连接相关、可能泄露客户端信息的。
var droppedRequestHeaders = map[string]bool{
	"Authorization": true, "X-Api-Key": true, "Host": true, "Content-Length": true,
	"Connection": true, "Keep-Alive": true, "Proxy-Connection": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
	"Accept-Encoding": true, // 让 Go 自己协商压缩并解压，否则没法从响应里读 usage
	"Cookie":          true, "Forwarded": true, "X-Forwarded-For": true, "X-Forwarded-Host": true,
	"X-Forwarded-Proto": true, "X-Real-Ip": true,
}

func copyRequestHeaders(dst, src http.Header) {
	for k, vs := range src {
		if droppedRequestHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		dst[k] = append([]string(nil), vs...)
	}
	if dst.Get("Content-Type") == "" {
		dst.Set("Content-Type", "application/json")
	}
}

func setUpstreamAuth(h http.Header, proto usage.Protocol, apiKey string) {
	if proto == usage.Messages {
		h.Set("x-api-key", apiKey)
		if h.Get("anthropic-version") == "" {
			h.Set("anthropic-version", "2023-06-01")
		}
		return
	}
	h.Set("Authorization", "Bearer "+apiKey)
}

// 不回传给客户端的响应头。Content-Length / Content-Encoding 因为 Go 已解压、可能删块而失效。
var droppedResponseHeaders = map[string]bool{
	"Content-Length": true, "Content-Encoding": true, "Connection": true, "Keep-Alive": true,
	"Transfer-Encoding": true, "Trailer": true, "Upgrade": true, "Set-Cookie": true,
}

func copyResponseHeaders(dst, src http.Header) {
	for k, vs := range src {
		if droppedResponseHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		dst[k] = append([]string(nil), vs...)
	}
}

// writeError 按入口协议的格式返回错误，客户端 SDK 才能正常解析。
func writeError(w http.ResponseWriter, proto usage.Protocol, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var body any
	if proto == usage.Messages {
		body = map[string]any{"type": "error", "error": map[string]string{"type": anthropicErrorType(status), "message": msg}}
	} else {
		body = map[string]any{"error": map[string]any{"message": msg, "type": openAIErrorType(status), "code": nil}}
	}
	json.NewEncoder(w).Encode(body)
}

func anthropicErrorType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "invalid_request_error"
}

func openAIErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	}
	return "invalid_request_error"
}
