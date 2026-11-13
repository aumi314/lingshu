package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

// ProbeResult 是一次渠道冒烟测试的结果。
type ProbeResult struct {
	Protocol      string `json:"protocol"`
	Model         string `json:"model"`
	OK            bool   `json:"ok"`
	HTTPStatus    int    `json:"http_status"`
	Error         string `json:"error,omitempty"`
	FirstByteMS   int    `json:"first_byte_ms"`
	TotalMS       int    `json:"total_ms"`
	UsageReported bool   `json:"usage_reported"` // 响应里有没有完整 usage（没有的话计费只能估算）
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
}

// Probe 用最小请求测试渠道的某个协议和模型，走和正式请求相同的用量解析逻辑。
// 聊天协议测流式，System One 测原生非流式决策。
func (h *Handler) Probe(ctx context.Context, ch *store.Channel, proto usage.Protocol, upstreamModel string) ProbeResult {
	res := ProbeResult{Protocol: string(proto), Model: upstreamModel}
	base := map[usage.Protocol]string{usage.Chat: ch.BaseURLChat, usage.Messages: ch.BaseURLMessages, usage.Responses: ch.BaseURLResponses, usage.SystemOne: ch.BaseURLSystemOne}[proto]
	if base == "" {
		res.Error = "这个渠道没有配置该协议的地址"
		return res
	}
	prompt := "Reply with exactly: ok"
	var body map[string]any
	switch proto {
	case usage.Chat:
		body = map[string]any{"model": upstreamModel, "stream": true, "max_tokens": 256,
			"stream_options": map[string]any{"include_usage": true},
			"messages":       []map[string]string{{"role": "user", "content": prompt}}}
	case usage.Messages:
		body = map[string]any{"model": upstreamModel, "stream": true, "max_tokens": 256,
			"messages": []map[string]string{{"role": "user", "content": prompt}}}
	case usage.Responses:
		body = map[string]any{"model": upstreamModel, "stream": true, "max_output_tokens": 256, "input": prompt}
	case usage.SystemOne:
		body = map[string]any{"model": upstreamModel, "state": "Help! My payouts have been failing for 3 days.",
			"questions": map[string]any{"is_urgent": map[string]string{"type": "noul", "instructions": "Does this convey urgency?"}}}
	}
	raw, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+upstreamPaths[proto], bytes.NewReader(raw))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lingshu-probe/1")
	setUpstreamAuth(req.Header, proto, ch.APIKey)
	var openCode *openCodeRequest
	if ch.Template == openCodeTemplate && proto == usage.Chat && strings.HasSuffix(upstreamModel, "-free") {
		prepared, state, err := prepareOpenCode(req, raw, ch.ID)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		req.Body = io.NopCloser(bytes.NewReader(prepared))
		req.ContentLength = int64(len(prepared))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(prepared)), nil }
		openCode = state
	}

	start := time.Now()
	client := h.clientFor(ch.ProxyURL)
	if ch.Template == openCodeTemplate {
		client = h.openCodeClientFor(ch.ProxyURL)
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Error = "连接失败: " + err.Error()
		return res
	}
	defer resp.Body.Close()
	res.HTTPStatus = resp.StatusCode
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		res.Error = truncate(string(redactKey(b, ch.APIKey)), 500)
		res.TotalMS = int(time.Since(start).Milliseconds())
		return res
	}

	tr := usage.NewTracker(proto)
	if proto == usage.SystemOne {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		res.TotalMS = int(time.Since(start).Milliseconds())
		res.FirstByteMS = max(res.TotalMS, 1)
		var answer struct {
			Answers map[string]struct {
				Type string   `json:"type"`
				Noul *float64 `json:"noul"`
			} `json:"answers"`
		}
		if err != nil || json.Unmarshal(b, &answer) != nil {
			res.Error = "上游没有返回完整的 System One JSON 响应"
			return res
		}
		a := answer.Answers["is_urgent"]
		if a.Type != "noul" || a.Noul == nil || *a.Noul < 0 || *a.Noul > 1 {
			res.Error = "上游没有返回有效的决策结果"
			return res
		}
		tr.ObserveBody(b)
		u := tr.Result()
		res.OK, res.UsageReported = true, tr.Seen()
		res.InputTokens, res.OutputTokens = u.Input, u.Output
		return res
	}
	reader := usage.NewSSEReader(resp.Body)
	for {
		ev, err := reader.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				res.Error = "读取流失败: " + err.Error()
			}
			break
		}
		if res.FirstByteMS == 0 {
			res.FirstByteMS = max(int(time.Since(start).Milliseconds()), 1)
		}
		if openCode != nil {
			if err := openCode.sanitizeEvent(ev.Data); err != nil {
				res.Error = err.Error()
				break
			}
		}
		tr.ObserveEvent(ev.Data)
	}
	res.TotalMS = int(time.Since(start).Milliseconds())
	u := tr.Result()
	res.UsageReported = tr.Seen()
	res.InputTokens, res.OutputTokens = u.Input+u.CacheRead+u.CacheWrite, u.Output
	switch {
	case res.Error != "":
	case tr.Failed:
		res.Error = "上游在流中报告错误"
	case !tr.Terminal:
		res.Error = "流在正常结束前断开"
	default:
		res.OK = true
	}
	return res
}

// FetchModels 从上游的模型列表接口拉取模型名。上游没有这个接口时返回错误，管理员手动填写即可。
func (h *Handler) FetchModels(ctx context.Context, ch *store.Channel) ([]string, error) {
	type attempt struct{ url, proto string }
	var tries []attempt
	if ch.BaseURLSystemOne != "" {
		tries = append(tries, attempt{strings.TrimRight(ch.BaseURLSystemOne, "/") + "/models", "systemone"})
	}
	if IsRootTemplate(ch.Template) {
		if ch.BaseURLChat != "" {
			tries = append(tries, attempt{strings.TrimRight(ch.BaseURLChat, "/") + "/models", "openai"})
		}
	} else {
		for _, b := range []string{ch.BaseURLChat, ch.BaseURLResponses} {
			if b != "" {
				tries = append(tries, attempt{strings.TrimRight(b, "/") + "/models", "openai"})
			}
		}
		if ch.BaseURLMessages != "" {
			tries = append(tries, attempt{strings.TrimRight(ch.BaseURLMessages, "/") + "/v1/models", "anthropic"})
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var lastErr error = errors.New("这个渠道没有配置任何地址")
	for _, t := range tries {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
		if t.proto == "anthropic" {
			setUpstreamAuth(req.Header, usage.Messages, ch.APIKey)
		} else {
			setUpstreamAuth(req.Header, usage.Chat, ch.APIKey)
		}
		client := h.clientFor(ch.ProxyURL)
		if ch.Template == openCodeTemplate {
			client = h.openCodeClientFor(ch.ProxyURL)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if json.Unmarshal(b, &list) != nil {
			lastErr = errors.New("返回的不是模型列表")
			continue
		}
		names := make([]string, 0, len(list.Data))
		if t.proto == "systemone" {
			for _, m := range list.Models {
				if m.Name != "" {
					names = append(names, m.Name)
				}
			}
		} else {
			for _, m := range list.Data {
				if m.ID != "" {
					names = append(names, m.ID)
				}
			}
		}
		if len(names) == 0 {
			lastErr = errors.New("返回的不是模型列表，或模型列表为空")
			continue
		}
		sort.Strings(names)
		return names, nil
	}
	return nil, fmt.Errorf("上游没有可用的模型列表接口（%v），请手动填写模型名", lastErr)
}
