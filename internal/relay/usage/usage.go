// Package usage 从各协议的响应里提取 token 用量，并换算成统一口径。
//
// 统一口径：Input 不含缓存部分；CacheRead、CacheWrite 单独计；Output 含推理 token。
// 流式响应里每个字段取最后出现的值（不累加）：StepFun 每块都带累计值，
// 阿里 messages 的 message_start 里 input_tokens 不准，以 message_delta 为准。
package usage

import (
	"encoding/json"
	"strings"
)

type Protocol string

const (
	Chat      Protocol = "chat"
	Messages  Protocol = "messages"
	Responses Protocol = "responses"
	SystemOne Protocol = "systemone"
)

// Usage 是统一口径的用量。
type Usage struct {
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	Reasoning  int64 // 仅供展示，已包含在 Output 里
}

// Tracker 逐个观察响应体或 SSE 事件，记录各字段最后出现的值。
type Tracker struct {
	proto      Protocol
	fields     map[string]int64
	ResponseID string // responses 协议的响应 id，用于 previous_response_id 粘渠道
	// ServiceTier 是 OpenAI 响应里实际处理用的档位（chat / responses 的 service_tier），取最后出现的值；
	// Fast 请求被上游降回标准时是 "default"，此时按标准价计费
	ServiceTier string
	Terminal    bool  // 是否看到了正常结束的标志
	Failed      bool  // 上游在流里报告了失败
	estOutput   int64 // 已收到的生成内容粗估的 token 数，上游没给 usage 时兜底
}

// EstimatedOutput 返回按已收到内容粗估的输出 token 数。
func (t *Tracker) EstimatedOutput() int64 { return t.estOutput }

func NewTracker(p Protocol) *Tracker {
	return &Tracker{proto: p, fields: map[string]int64{}}
}

// Seen 表示拿到了可计费用量；System One 必须同时有输入和输出字段（显式 0 也有效）。
func (t *Tracker) Seen() bool {
	if t.proto == SystemOne {
		_, input := t.fields["input"]
		_, output := t.fields["output"]
		return input && output
	}
	return len(t.fields) > 0
}

func (t *Tracker) set(name string, v *int64) {
	if v != nil {
		t.fields[name] = *v
	}
}

// 各协议 usage 对象里我们关心的字段，用指针区分"没给"和"给了 0"。
type rawUsage struct {
	// chat
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	PromptDetails    *struct {
		CachedTokens     *int64 `json:"cached_tokens"`
		CacheWriteTokens *int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`

	// messages 和 responses
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`

	// messages
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`

	// responses
	InputDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (t *Tracker) observeUsage(u *rawUsage) {
	if u == nil {
		return
	}
	switch t.proto {
	case Chat:
		t.set("prompt", u.PromptTokens)
		t.set("completion", u.CompletionTokens)
		if u.PromptDetails != nil {
			t.set("cached", u.PromptDetails.CachedTokens)
			t.set("cache_write", u.PromptDetails.CacheWriteTokens)
		}
		if u.CompletionDetails != nil {
			t.set("reasoning", u.CompletionDetails.ReasoningTokens)
		}
	case Messages:
		t.set("input", u.InputTokens)
		t.set("output", u.OutputTokens)
		t.set("cache_read", u.CacheReadInputTokens)
		t.set("cache_write", u.CacheCreationInputTokens)
	case SystemOne:
		t.set("input", u.InputTokens)
		t.set("output", u.OutputTokens)
	case Responses:
		t.set("input", u.InputTokens)
		t.set("output", u.OutputTokens)
		if u.InputDetails != nil {
			t.set("cached", u.InputDetails.CachedTokens)
		}
		if u.OutputDetails != nil {
			t.set("reasoning", u.OutputDetails.ReasoningTokens)
		}
	}
}

// observeTier 只记 OpenAI 协议的档位；messages 的 service_tier 是 Anthropic 的另一套含义，不看。
func (t *Tracker) observeTier(tier string) {
	if tier != "" && (t.proto == Chat || t.proto == Responses) {
		t.ServiceTier = tier
	}
}

// ObserveBody 处理非流式响应的完整 JSON。
func (t *Tracker) ObserveBody(body []byte) {
	var v struct {
		ID          string    `json:"id"`
		Usage       *rawUsage `json:"usage"`
		ServiceTier string    `json:"service_tier"`
	}
	if json.Unmarshal(body, &v) != nil {
		return
	}
	t.observeUsage(v.Usage)
	t.observeTier(v.ServiceTier)
	t.estOutput = estimateOutput(body)
	if t.proto == SystemOne {
		var decision struct {
			Answers map[string]json.RawMessage `json:"answers"`
		}
		if json.Unmarshal(body, &decision) != nil || len(decision.Answers) == 0 {
			t.Failed = true
			return
		}
		answers, _ := json.Marshal(decision.Answers)
		t.estOutput = EstimateText(string(answers))
	}
	if t.proto == Responses && v.ID != "" {
		t.ResponseID = v.ID
	}
	t.Terminal = true
}

// ObserveEvent 处理一个 SSE 事件的 data 内容。
func (t *Tracker) ObserveEvent(data []byte) {
	if t.proto == Chat && string(data) == "[DONE]" {
		t.Terminal = true
		return
	}
	var v struct {
		Type        string    `json:"type"`
		Usage       *rawUsage `json:"usage"`
		ServiceTier string    `json:"service_tier"`
		Message     *struct {
			Usage *rawUsage `json:"usage"`
		} `json:"message"`
		Response *struct {
			ID          string    `json:"id"`
			Usage       *rawUsage `json:"usage"`
			ServiceTier string    `json:"service_tier"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &v) != nil {
		return
	}
	// responses 的 *.done / response.completed 会把全文再发一遍，只统计增量事件
	if t.proto != Responses || strings.HasSuffix(v.Type, ".delta") {
		t.estOutput += estimateOutput(data)
	}
	switch t.proto {
	case Chat:
		t.observeUsage(v.Usage)
		t.observeTier(v.ServiceTier)
	case Messages:
		if v.Message != nil {
			t.observeUsage(v.Message.Usage)
		}
		t.observeUsage(v.Usage)
		switch v.Type {
		case "message_stop":
			t.Terminal = true
		case "error":
			t.Failed = true
		}
	case Responses:
		if v.Response != nil {
			if v.Response.ID != "" {
				t.ResponseID = v.Response.ID
			}
			t.observeUsage(v.Response.Usage)
			t.observeTier(v.Response.ServiceTier)
		}
		switch v.Type {
		case "response.completed", "response.incomplete":
			t.Terminal = true
		case "response.failed", "error":
			t.Failed = true
		}
	}
}

// Result 换算成统一口径。
func (t *Tracker) Result() Usage {
	f := t.fields
	var u Usage
	switch t.proto {
	case Chat:
		u.CacheRead = f["cached"]
		u.CacheWrite = f["cache_write"]
		u.Input = max(f["prompt"]-u.CacheRead-u.CacheWrite, 0)
		u.Output = f["completion"]
		u.Reasoning = f["reasoning"]
	case Messages:
		u.Input = f["input"]
		u.CacheRead = f["cache_read"]
		u.CacheWrite = f["cache_write"]
		u.Output = f["output"]
	case SystemOne:
		u.Input, u.Output = f["input"], f["output"]
	case Responses:
		u.CacheRead = f["cached"]
		u.Input = max(f["input"]-u.CacheRead, 0)
		u.Output = f["output"]
		u.Reasoning = f["reasoning"]
	}
	return u
}

// IsUsageOnlyChatChunk 判断 chat 流里只带 usage、choices 为空的那一块。
// 网关替客户端加了 include_usage 时，要把这一块去掉再转发。
func IsUsageOnlyChatChunk(data []byte) bool {
	var v struct {
		Choices *[]json.RawMessage `json:"choices"`
		Usage   json.RawMessage    `json:"usage"`
	}
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	return v.Choices != nil && len(*v.Choices) == 0 && len(v.Usage) > 0 && string(v.Usage) != "null"
}
