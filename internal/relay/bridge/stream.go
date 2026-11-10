package bridge

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Stream 把上游 Messages 的事件转成 Responses 事件。
//
// 终态映射：end_turn / stop_sequence / tool_use / refusal 都算这一轮结束（completed），
// max_tokens 算 incomplete，pause_turn、未知停止原因、流内错误和断流算 failed。断流不会伪造 completed。
type Stream struct {
	responseID string
	model      string
	tools      *toolTable
	compact    bool
	lifecycle  bool // 是否由本层补发 response.created / response.in_progress

	seq       int
	outputIdx int
	items     []map[string]any
	usage     usageInfo

	blocks  map[int]*blockState
	pending *textState

	text       strings.Builder
	stopReason string
	status     string
	compaction string
	done       bool
	failed     bool
	createdAt  int64
	terminal   map[string]any // 终态的响应对象，非流式客户端直接用它
}

// blockState 是一个上游内容块的转换状态。
type blockState struct {
	kind      string
	itemID    string
	outputIdx int
	partIdx   int
	text      strings.Builder
	args      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	data      string
	name      string
	namespace string
	callID    string
	custom    bool
	closed    bool
	msg       *textState
}

// textState 是一条助手消息（可能含多个文本段）的状态。
type textState struct {
	itemID    string
	outputIdx int
	parts     []any
}

// NewStream 建一个转换器。responseID 是网关给这次 Responses 响应的 id。
func NewStream(responseID string) *Stream {
	return &Stream{
		responseID: responseID,
		items:      []map[string]any{},
		blocks:     map[int]*blockState{},
		createdAt:  time.Now().Unix(),
	}
}

// Bind 把请求里的工具表和压缩标记交给转换器。
// 工具表决定上游的工具调用还原成 function_call 还是 custom_tool_call。
func (s *Stream) Bind(req *Request) {
	if req == nil {
		return
	}
	s.tools = req.tools
	s.compact = req.Compact
	if s.model == "" {
		s.model = req.model
	}
}

// SetLifecycle 控制要不要由本层补发 response.created / response.in_progress。
// 调用方自己发这两个事件时关掉，避免重复。
func (s *Stream) SetLifecycle(on bool) { s.lifecycle = on }

// Done 表示已经出了终态事件。
func (s *Stream) Done() bool { return s.done }

// Failed 表示终态是失败（流内报错或断流）。
func (s *Stream) Failed() bool { return s.failed }

// Summary 是这一轮模型的正文（压缩模式下就是摘要）。
func (s *Stream) Summary() string { return strings.TrimSpace(s.text.String()) }

// Compaction 是压缩模式下生成的封装内容。
func (s *Stream) Compaction() (string, bool) { return s.compaction, s.compaction != "" }

// Event 吃上游 SSE 的一条 data，返回要发给客户端的 Responses 事件 JSON。
func (s *Stream) Event(data []byte) ([][]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "[DONE]" {
		return nil, nil
	}
	var ev map[string]any
	if err := json.Unmarshal(trimmed, &ev); err != nil {
		// 坏数据不能悄悄吞掉：上游流式内容坏了要看得见。
		return nil, newError(CodeInvalidInput, "", "上游事件不是合法 JSON: %v", err)
	}
	var out [][]byte
	var err error
	switch strField(ev, "type") {
	case "message_start":
		out = s.onMessageStart(ev)
	case "content_block_start":
		out, err = s.onContentBlockStart(ev)
	case "content_block_delta":
		out, err = s.onContentBlockDelta(ev)
	case "content_block_stop":
		out, err = s.onContentBlockStop(ev)
	case "message_delta":
		out = s.onMessageDelta(ev)
	case "message_stop":
		out, err = s.onMessageStop()
	case "error":
		out = s.onError(ev)
	}
	return out, err
}

// Close 是上游流结束时的收尾：没收到终态就是断流，不能伪造完成。
func (s *Stream) Close() ([][]byte, error) {
	if s.done {
		return nil, nil
	}
	obj := s.responseObject("failed")
	obj["error"] = map[string]any{"code": CodeTruncatedStream, "message": "上游响应流中断"}
	s.terminal = obj
	out := [][]byte{s.emit(map[string]any{"type": "response.failed", "response": obj})}
	s.failed = true
	s.done = true
	s.status = "failed"
	return out, newError(CodeTruncatedStream, "", "上游响应流中断")
}

// Response 拼出完整的 Responses 响应体（非流式客户端用）。
func (s *Stream) Response() ([]byte, error) {
	if !s.done {
		return nil, newError(CodeTruncatedStream, "", "上游响应还没结束")
	}
	if s.terminal != nil {
		return json.Marshal(s.terminal)
	}
	return json.Marshal(s.responseObject(s.status))
}

// emit 序列化一个事件并加上序号。
func (s *Stream) emit(ev map[string]any) []byte {
	ev["sequence_number"] = s.seq
	s.seq++
	encoded, err := json.Marshal(ev)
	if err != nil {
		return nil
	}
	return encoded
}

// responseObject 组装 Responses 的响应对象。
func (s *Stream) responseObject(status string) map[string]any {
	output := make([]map[string]any, 0, len(s.items))
	for _, item := range s.items {
		if item != nil {
			output = append(output, item)
		}
	}
	if s.compact {
		// 压缩请求最终只留 compaction 项，正文和思考只走事件流，
		// 免得别家用工具思考的内容跟着摘要一起流出去。
		output = nil
		for _, item := range s.items {
			if strField(item, "type") == "compaction" {
				output = append(output, item)
			}
		}
	}
	return map[string]any{
		"id":                  s.responseID,
		"object":              "response",
		"created_at":          s.createdAt,
		"status":              status,
		"model":               s.model,
		"output":              output,
		"usage":               s.usage.responses(),
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"tools":               []any{},
		"incomplete_details":  nil,
		"error":               nil,
	}
}

// customInput 从自由文本工具的参数里取出原始输入。参数坏了就报错，不猜。
func customInput(arguments string) (string, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return "", newError(CodeInvalidArguments, "", "自由文本工具的参数不是合法 JSON")
	}
	text, ok := parsed["input"].(string)
	if !ok {
		return "", newError(CodeInvalidArguments, "", "自由文本工具的参数里没有 input 字符串")
	}
	return text, nil
}

// putItem 按 output_index 保存，平行工具块即使逆序结束也保持客户端顺序。
func (s *Stream) putItem(index int, item map[string]any) {
	for len(s.items) <= index {
		s.items = append(s.items, nil)
	}
	s.items[index] = item
}
