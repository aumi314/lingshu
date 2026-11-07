package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

// OpenCode 上游只给流式响应。客户端要非流式时，把上游的流合成一个 chat completion，
// 原地换掉 resp.Body，再交给 relayBody 原样写回去。
// 错误信息只说清楚哪里出了问题，不带上游原文，免得把提示词或密钥写进日志。

// openCodeToolCall 累积同一下标的工具调用，参数按分片顺序拼接。
type openCodeToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

// openCodeChoice 累积一个 choice 的内容。
type openCodeChoice struct {
	Content          strings.Builder
	ReasoningContent strings.Builder
	Reasoning        strings.Builder
	ToolCalls        map[int]*openCodeToolCall
	FinishReason     string
}

func (c *openCodeChoice) toolCall(index int) *openCodeToolCall {
	if c.ToolCalls == nil {
		c.ToolCalls = map[int]*openCodeToolCall{}
	}
	tc, ok := c.ToolCalls[index]
	if !ok {
		tc = &openCodeToolCall{}
		c.ToolCalls[index] = tc
	}
	return tc
}

// toolList 按下标顺序输出工具调用；无名调用由 completion 校验拒绝。
func (c *openCodeChoice) toolList() []openCodeToolCallOut {
	if len(c.ToolCalls) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(c.ToolCalls))
	for i := range c.ToolCalls {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	var out []openCodeToolCallOut
	for _, i := range indexes {
		tc := c.ToolCalls[i]
		if tc.Name == "" {
			continue
		}
		typ := tc.Type
		if typ == "" {
			typ = "function"
		}
		out = append(out, openCodeToolCallOut{
			ID:       tc.ID,
			Type:     typ,
			Function: openCodeFunctionOut{Name: tc.Name, Arguments: tc.Arguments.String()},
		})
	}
	return out
}

type openCodeFunctionOut struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openCodeToolCallOut struct {
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type"`
	Function openCodeFunctionOut `json:"function"`
}

type openCodeMessageOut struct {
	Role             string                `json:"role"`
	Content          *string               `json:"content"`
	ReasoningContent string                `json:"reasoning_content,omitempty"`
	Reasoning        string                `json:"reasoning,omitempty"`
	ToolCalls        []openCodeToolCallOut `json:"tool_calls,omitempty"`
}

type openCodeChoiceOut struct {
	Index        int                `json:"index"`
	Message      openCodeMessageOut `json:"message"`
	FinishReason string             `json:"finish_reason"`
}

type openCodeCompletionOut struct {
	ID          string              `json:"id,omitempty"`
	Object      string              `json:"object"`
	Created     int64               `json:"created,omitempty"`
	Model       string              `json:"model,omitempty"`
	ServiceTier string              `json:"service_tier,omitempty"`
	Choices     []openCodeChoiceOut `json:"choices"`
	Usage       json.RawMessage     `json:"usage,omitempty"`
}

// openCodeChunk 是上游流里的一块，和 OpenAI 的 chat.completion.chunk 对齐。
type openCodeChunk struct {
	ID          string          `json:"id"`
	Created     int64           `json:"created"`
	Model       string          `json:"model"`
	ServiceTier string          `json:"service_tier"`
	Usage       json.RawMessage `json:"usage"`
	Error       json.RawMessage `json:"error"`
	Choices     []struct {
		Index        int             `json:"index"`
		FinishReason string          `json:"finish_reason"`
		Delta        json.RawMessage `json:"delta"`
	} `json:"choices"`
}

// openCodeDelta 是一块里的增量内容；content 可能是 null。
type openCodeDelta struct {
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Reasoning        string          `json:"reasoning"`
	ToolCalls        []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// openCodeCollector 累积整个流的结果。
type openCodeCollector struct {
	id          string
	model       string
	created     int64
	serviceTier string
	usage       json.RawMessage
	done        bool
	choices     map[int]*openCodeChoice
}

func (c *openCodeCollector) choice(index int) *openCodeChoice {
	if c.choices == nil {
		c.choices = map[int]*openCodeChoice{}
	}
	ch, ok := c.choices[index]
	if !ok {
		ch = &openCodeChoice{}
		c.choices[index] = ch
	}
	return ch
}

// add 收一块流式内容。
func (c *openCodeCollector) add(data []byte) error {
	var chunk openCodeChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return errors.New("上游返回了无法解析的内容")
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return errors.New("上游返回了错误事件")
	}
	if chunk.ID != "" {
		c.id = chunk.ID
	}
	if chunk.Model != "" {
		c.model = chunk.Model
	}
	if chunk.Created != 0 {
		c.created = chunk.Created
	}
	if chunk.ServiceTier != "" {
		c.serviceTier = chunk.ServiceTier
	}
	// usage 单独成块，choices 是空数组，照原样带回去，计费才拿得到缓存和推理 token
	if len(chunk.Usage) > 0 && string(chunk.Usage) != "null" {
		c.usage = chunk.Usage
	}
	for _, ch := range chunk.Choices {
		acc := c.choice(ch.Index)
		if ch.FinishReason != "" {
			acc.FinishReason = ch.FinishReason
		}
		if len(ch.Delta) == 0 || string(ch.Delta) == "null" {
			continue
		}
		var delta openCodeDelta
		if err := json.Unmarshal(ch.Delta, &delta); err != nil {
			return errors.New("上游返回了无法解析的内容")
		}
		if text, ok := openCodeText(delta.Content); ok {
			acc.Content.WriteString(text)
		}
		acc.ReasoningContent.WriteString(delta.ReasoningContent)
		acc.Reasoning.WriteString(delta.Reasoning)
		for _, tc := range delta.ToolCalls {
			call := acc.toolCall(tc.Index)
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Type != "" {
				call.Type = tc.Type
			}
			if tc.Function.Name != "" {
				call.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				call.Arguments.WriteString(tc.Function.Arguments)
			}
		}
	}
	return nil
}

// openCodeText 取出增量里的文本；content 为 null 或不是字符串时跳过。
func openCodeText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, true
}

// completion 合成非流式的响应体。流被截断或没给结束原因都算失败。
func (c *openCodeCollector) completion() (*openCodeCompletionOut, error) {
	if len(c.choices) == 0 {
		return nil, errors.New("上游没有返回内容")
	}
	indexes := make([]int, 0, len(c.choices))
	for i := range c.choices {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	choices := make([]openCodeChoiceOut, 0, len(indexes))
	for _, i := range indexes {
		ch := c.choices[i]
		if ch.FinishReason == "" {
			return nil, errors.New("上游没有给出结束原因")
		}
		for _, call := range ch.ToolCalls {
			if call.Name == "" {
				return nil, errors.New("上游返回了不完整的工具调用")
			}
		}
		tools := ch.toolList()
		msg := openCodeMessageOut{Role: "assistant", ToolCalls: tools}
		text := ch.Content.String()
		if text != "" || len(tools) == 0 {
			msg.Content = &text
		}
		msg.ReasoningContent = ch.ReasoningContent.String()
		msg.Reasoning = ch.Reasoning.String()
		choices = append(choices, openCodeChoiceOut{Index: i, Message: msg, FinishReason: ch.FinishReason})
	}
	return &openCodeCompletionOut{
		ID:          c.id,
		Object:      "chat.completion",
		Created:     c.created,
		Model:       c.model,
		ServiceTier: c.serviceTier,
		Choices:     choices,
		Usage:       c.usage,
	}, nil
}

// collectOpenCodeChat 读掉上游的流，原地换成非流式的 chat completion。
// 旧的响应体在这里关掉，调用方拿到的 resp.Body 是可以再读的 JSON。
func collectOpenCodeChat(resp *http.Response, state *openCodeRequest) error {
	if resp == nil || resp.Body == nil {
		return errors.New("上游没有返回内容")
	}
	old := resp.Body
	defer old.Close()

	acc := &openCodeCollector{}
	reader := usage.NewSSEReader(old)
	for {
		ev, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("读取上游流失败")
		}
		if state != nil {
			// 占位工具被模型调用时这里报错，整次响应作废
			if err := state.sanitizeEvent(ev.Data); err != nil {
				return err
			}
		}
		data := bytes.TrimSpace(ev.Data)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			acc.done = true
			break
		}
		if err := acc.add(data); err != nil {
			return err
		}
	}
	if !acc.done {
		return errors.New("上游流没有正常结束")
	}
	completion, err := acc.completion()
	if err != nil {
		return err
	}
	body, err := json.Marshal(completion)
	if err != nil {
		return errors.New("组装上游响应失败")
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Del("Content-Length")
	return nil
}
