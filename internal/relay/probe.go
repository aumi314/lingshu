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
	UsageReported bool   `json:"usage_reported"` // 流里有没有 usage（没有的话计费只能估算）
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
}

// Probe 用一个最小的流式请求测试渠道的某个协议和模型，走和正式请求相同的解析逻辑。
func (h *Handler) Probe(ctx context.Context, ch *store.Channel, proto usage.Protocol, upstreamModel string) ProbeResult {
	res := ProbeResult{Protocol: string(proto), Model: upstreamModel}
	base := map[usage.Protocol]string{usage.Chat: ch.BaseURLChat, usage.Messages: ch.BaseURLMessages, usage.Responses: ch.BaseURLResponses}[proto]
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

	start := time.Now()
	resp, err := h.Client.Do(req)
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
	for _, b := range []string{ch.BaseURLChat, ch.BaseURLResponses} {
		if b != "" {
			tries = append(tries, attempt{strings.TrimRight(b, "/") + "/models", "openai"})
		}
	}
	if ch.BaseURLMessages != "" {
		tries = append(tries, attempt{strings.TrimRight(ch.BaseURLMessages, "/") + "/v1/models", "anthropic"})
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
		resp, err := h.Client.Do(req)
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
		}
		if json.Unmarshal(b, &list) != nil || len(list.Data) == 0 {
			lastErr = errors.New("返回的不是模型列表")
			continue
		}
		names := make([]string, 0, len(list.Data))
		for _, m := range list.Data {
			names = append(names, m.ID)
		}
		sort.Strings(names)
		return names, nil
	}
	return nil, fmt.Errorf("上游没有可用的模型列表接口（%v），请手动填写模型名", lastErr)
}
