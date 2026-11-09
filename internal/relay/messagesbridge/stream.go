package messagesbridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Responses 可交错生成多个条目，Messages 按内容块顺序输出。
// 只等待排在前面的块；当前文字增量直接交给客户端。
type Stream struct {
	id, upstreamID        string
	req                   *Request
	items                 map[int]*responseItem
	next                  int
	content               []map[string]any
	started, done, failed bool
	refusal, tools        bool
	stop                  string
	used                  map[string]any
	size                  int
}
type responseItem struct {
	id, kind, callID, name string
	parts                  []*responsePart
	done                   bool
	final                  json.RawMessage
	signature              string
}
type responsePart struct {
	kind, text   string
	sent         int
	index        int
	done, closed bool
}
type responseEvent struct {
	Type         string          `json:"type"`
	OutputIndex  int             `json:"output_index"`
	ContentIndex int             `json:"content_index"`
	SummaryIndex int             `json:"summary_index"`
	ItemID       string          `json:"item_id"`
	Delta        string          `json:"delta"`
	Text         string          `json:"text"`
	Arguments    string          `json:"arguments"`
	Refusal      string          `json:"refusal"`
	Item         json.RawMessage `json:"item"`
	Part         json.RawMessage `json:"part"`
	Response     json.RawMessage `json:"response"`
}
type outputItem struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Role      string            `json:"role"`
	CallID    string            `json:"call_id"`
	Name      string            `json:"name"`
	Arguments string            `json:"arguments"`
	Content   []json.RawMessage `json:"content"`
	Summary   []json.RawMessage `json:"summary"`
	Encrypted string            `json:"encrypted_content"`
}
type outputPart struct {
	Type        string            `json:"type"`
	Text        string            `json:"text"`
	Refusal     string            `json:"refusal"`
	Annotations []json.RawMessage `json:"annotations"`
}

func NewStream(id string, q *Request) *Stream {
	return &Stream{id: id, req: q, items: map[int]*responseItem{}, content: []map[string]any{}, used: map[string]any{"input_tokens": int64(0), "output_tokens": int64(0), "cache_read_input_tokens": int64(0), "cache_creation_input_tokens": int64(0)}}
}
func (s *Stream) Done() bool         { return s.done }
func (s *Stream) Failed() bool       { return s.failed }
func (s *Stream) UpstreamID() string { return s.upstreamID }
func encodeEvent(v any) []byte       { b, _ := json.Marshal(v); return b }
func (s *Stream) fail(format string, args ...any) ([][]byte, error) {
	s.failed = true
	return nil, fmt.Errorf(format, args...)
}

func (s *Stream) Event(data []byte) ([][]byte, error) {
	if s.done || s.failed {
		return s.fail("上游在终态后继续发送事件")
	}
	s.size += len(data)
	if s.size > 32<<20 {
		return s.fail("Responses 响应超过 32 MiB 转换上限")
	}
	var e responseEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return s.fail("无效 Responses 事件: %v", err)
	}
	switch e.Type {
	case "response.created", "response.in_progress":
		if err := s.identify(e.Response); err != nil {
			return s.fail("%v", err)
		}
		return nil, nil
	case "response.completed", "response.incomplete":
		return s.finish(e.Response, e.Type)
	case "response.failed", "error":
		var v struct {
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
			Response struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		_ = json.Unmarshal(data, &v)
		msg := v.Message
		if msg == "" {
			msg = v.Error.Message
		}
		if msg == "" {
			msg = v.Response.Error.Message
		}
		if msg == "" {
			msg = "上游生成失败"
		}
		return s.fail("%s", msg)
	case "response.output_item.added":
		if _, ok := s.items[e.OutputIndex]; ok {
			return s.fail("重复 output_index %d", e.OutputIndex)
		}
		if err := s.putItem(e.OutputIndex, e.Item, false); err != nil {
			return s.fail("%v", err)
		}
	case "response.output_item.done":
		if err := s.putItem(e.OutputIndex, e.Item, true); err != nil {
			return s.fail("%v", err)
		}
	case "response.content_part.added", "response.content_part.done":
		it, err := s.itemFor(e, "message")
		if err != nil {
			return s.fail("%v", err)
		}
		if err = s.putPart(it, e.ContentIndex, e.Part, e.Type == "response.content_part.done"); err != nil {
			return s.fail("%v", err)
		}
	case "response.output_text.delta", "response.refusal.delta":
		it, err := s.itemFor(e, "message")
		if err != nil {
			return s.fail("%v", err)
		}
		kind := "output_text"
		if e.Type == "response.refusal.delta" {
			kind = "refusal"
			s.refusal = true
		}
		p, err := ensurePart(it, e.ContentIndex, kind)
		if err != nil {
			return s.fail("%v", err)
		}
		if p.done {
			return s.fail("已结束内容块继续输出")
		}
		p.text += e.Delta
	case "response.output_text.done", "response.refusal.done":
		it, err := s.itemFor(e, "message")
		if err != nil {
			return s.fail("%v", err)
		}
		kind, text := "output_text", e.Text
		if e.Type == "response.refusal.done" {
			kind, text = "refusal", e.Refusal
			s.refusal = true
		}
		p, err := ensurePart(it, e.ContentIndex, kind)
		if err != nil {
			return s.fail("%v", err)
		}
		if err = completeText(p, text); err != nil {
			return s.fail("%v", err)
		}
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		it, err := s.itemFor(e, "function_call")
		if err != nil {
			return s.fail("%v", err)
		}
		p := it.parts[0]
		if e.Type == "response.function_call_arguments.delta" {
			if p.done {
				return s.fail("已结束工具继续输出参数")
			}
			p.text += e.Delta
		} else {
			if err = completeText(p, e.Arguments); err != nil {
				return s.fail("%v", err)
			}
		}
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		it, err := s.itemFor(e, "reasoning")
		if err != nil {
			return s.fail("%v", err)
		}
		if err = s.putPart(it, e.SummaryIndex, e.Part, e.Type == "response.reasoning_summary_part.done"); err != nil {
			return s.fail("%v", err)
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
		it, err := s.itemFor(e, "reasoning")
		if err != nil {
			return s.fail("%v", err)
		}
		p, err := ensurePart(it, e.SummaryIndex, "summary_text")
		if err != nil {
			return s.fail("%v", err)
		}
		if e.Type == "response.reasoning_summary_text.delta" {
			if p.done {
				return s.fail("已结束思考摘要继续输出")
			}
			p.text += e.Delta
		} else {
			if err = completeText(p, e.Text); err != nil {
				return s.fail("%v", err)
			}
		}
	default:
		return s.fail("Responses 事件 %s 暂不支持转成 Messages", e.Type)
	}
	return s.drain()
}

func (s *Stream) identify(raw []byte) error {
	var r struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &r) != nil || r.ID == "" {
		return fmt.Errorf("Responses 响应缺少 id")
	}
	if s.upstreamID != "" && s.upstreamID != r.ID {
		return fmt.Errorf("Responses 响应 id 中途改变")
	}
	s.upstreamID = r.ID
	return nil
}
func (s *Stream) itemFor(e responseEvent, kind string) (*responseItem, error) {
	it := s.items[e.OutputIndex]
	if it == nil || it.kind != kind {
		return nil, fmt.Errorf("output_index %d 的事件类型不匹配", e.OutputIndex)
	}
	if e.ItemID != "" && e.ItemID != it.id {
		return nil, fmt.Errorf("output_index %d 的 item_id 不匹配", e.OutputIndex)
	}
	if it.done {
		return nil, fmt.Errorf("已结束的 output_index %d 又出现增量", e.OutputIndex)
	}
	return it, nil
}
func ensurePart(it *responseItem, index int, kind string) (*responsePart, error) {
	if index < 0 || index > len(it.parts) {
		return nil, fmt.Errorf("内容块索引 %d 不连续", index)
	}
	if index == len(it.parts) {
		it.parts = append(it.parts, &responsePart{kind: kind, index: -1})
	}
	p := it.parts[index]
	if p.kind != kind {
		return nil, fmt.Errorf("内容块类型中途改变")
	}
	return p, nil
}
func completeText(p *responsePart, text string) error {
	if p.closed && text != p.text {
		return fmt.Errorf("已结束内容块的终态文本改变")
	}
	if !strings.HasPrefix(text, p.text) {
		return fmt.Errorf("终态内容和已输出增量不一致")
	}
	p.text = text
	p.done = true
	return nil
}
func (s *Stream) putPart(it *responseItem, index int, raw []byte, done bool) error {
	var v outputPart
	if json.Unmarshal(raw, &v) != nil {
		return fmt.Errorf("无效内容块")
	}
	if len(v.Annotations) > 0 {
		return fmt.Errorf("带引用注解的输出暂不支持转成 Messages")
	}
	if it.kind == "reasoning" {
		if v.Type != "summary_text" {
			return fmt.Errorf("不支持的思考内容 %s", v.Type)
		}
	} else if v.Type != "output_text" && v.Type != "refusal" {
		return fmt.Errorf("不支持的输出内容 %s", v.Type)
	}
	p, err := ensurePart(it, index, v.Type)
	if err != nil {
		return err
	}
	text := v.Text
	if v.Type == "refusal" {
		text = v.Refusal
		s.refusal = true
	}
	if done {
		return completeText(p, text)
	}
	if p.text != "" && p.text != text {
		return fmt.Errorf("重复内容块起始事件")
	}
	p.text = text
	return nil
}
func (s *Stream) putItem(index int, raw []byte, done bool) error {
	if index < 0 || index > len(s.items) {
		return fmt.Errorf("output_index %d 不连续", index)
	}
	var v outputItem
	if json.Unmarshal(raw, &v) != nil || v.ID == "" {
		return fmt.Errorf("无效输出条目或缺少 id")
	}
	if v.Type != "message" && v.Type != "function_call" && v.Type != "reasoning" {
		return fmt.Errorf("输出条目 %s 暂不支持转接", v.Type)
	}
	it := s.items[index]
	if it == nil {
		for _, existing := range s.items {
			if existing.id == v.ID {
				return fmt.Errorf("输出条目 id 重复")
			}
		}
		it = &responseItem{id: v.ID, kind: v.Type, callID: v.CallID, name: v.Name}
		s.items[index] = it
	} else if it.id != v.ID || it.kind != v.Type || it.callID != v.CallID || it.name != v.Name {
		return fmt.Errorf("输出条目身份中途改变")
	}
	if it.done {
		var old, now any
		_ = json.Unmarshal(it.final, &old)
		_ = json.Unmarshal(raw, &now)
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(now)
		if string(a) != string(b) {
			return fmt.Errorf("重复终态输出不一致")
		}
		return nil
	}
	switch v.Type {
	case "message":
		if v.Role != "" && v.Role != "assistant" {
			return fmt.Errorf("输出消息角色不是 assistant")
		}
		for i, p := range v.Content {
			if err := s.putPart(it, i, p, done); err != nil {
				return err
			}
		}
		if done && len(v.Content) != len(it.parts) {
			return fmt.Errorf("完整输出缺少已发送内容块")
		}
	case "function_call":
		if v.CallID == "" || v.Name == "" {
			return fmt.Errorf("工具调用缺少 call_id 或 name")
		}
		if _, ok := s.req.ToolNames[v.Name]; !ok {
			return fmt.Errorf("上游调用了未声明的工具 %s", v.Name)
		}
		for i, existing := range s.items {
			if i != index && existing.callID == v.CallID {
				return fmt.Errorf("工具 call_id 重复")
			}
		}
		p, err := ensurePart(it, 0, "function_call")
		if err != nil {
			return err
		}
		if done {
			if err = completeText(p, v.Arguments); err != nil {
				return err
			}
			var args map[string]json.RawMessage
			if json.Unmarshal([]byte(p.text), &args) != nil || args == nil {
				return fmt.Errorf("工具 %s 参数不是有效 JSON 对象", v.Name)
			}
		} else if v.Arguments != "" {
			p.text = v.Arguments
		}
		s.tools = true
	case "reasoning":
		for i, p := range v.Summary {
			if err := s.putPart(it, i, p, done); err != nil {
				return err
			}
		}
		if done {
			if len(v.Summary) != len(it.parts) {
				return fmt.Errorf("完整 reasoning 缺少摘要块")
			}
			it.signature = sealReasoning(raw, s.upstreamID)
			if it.signature == "" {
				return fmt.Errorf("上游 reasoning 缺少可回放的 encrypted_content")
			}
		}
	}
	if done {
		it.done = true
		it.final = append([]byte(nil), raw...)
	}
	return nil
}

func (s *Stream) start(events *[][]byte) {
	if s.started {
		return
	}
	s.started = true
	*events = append(*events, encodeEvent(map[string]any{"type": "message_start", "message": map[string]any{"id": s.id, "type": "message", "role": "assistant", "model": s.req.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": s.used}}))
}
func (s *Stream) drain() ([][]byte, error) {
	var events [][]byte
	for {
		it := s.items[s.next]
		if it == nil {
			break
		}
		if it.kind == "reasoning" {
			// 签名只取最终 reasoning 条目，避免把未完成密文交给下一轮。
			if !it.done {
				break
			}
			s.start(&events)
			var texts []string
			for _, p := range it.parts {
				texts = append(texts, p.text)
			}
			text := strings.Join(texts, "\n\n")
			index := len(s.content)
			var block map[string]any
			if text == "" {
				block = map[string]any{"type": "redacted_thinking", "data": it.signature}
				events = append(events, encodeEvent(map[string]any{"type": "content_block_start", "index": index, "content_block": block}))
			} else {
				block = map[string]any{"type": "thinking", "thinking": text, "signature": it.signature}
				events = append(events, encodeEvent(map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "thinking", "thinking": ""}}), encodeEvent(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "thinking_delta", "thinking": text}}), encodeEvent(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "signature_delta", "signature": it.signature}}))
			}
			events = append(events, encodeEvent(map[string]any{"type": "content_block_stop", "index": index}))
			s.content = append(s.content, block)
			s.next++
			continue
		}
		allClosed := true
		for _, p := range it.parts {
			if p.closed {
				continue
			}
			if p.index < 0 {
				s.start(&events)
				p.index = len(s.content)
				block := map[string]any{"type": "text", "text": ""}
				if it.kind == "function_call" {
					block = map[string]any{"type": "tool_use", "id": it.callID, "name": s.req.ToolNames[it.name], "input": map[string]any{}}
				}
				s.content = append(s.content, block)
				events = append(events, encodeEvent(map[string]any{"type": "content_block_start", "index": p.index, "content_block": block}))
			}
			if p.sent < len(p.text) {
				delta := map[string]any{"type": "text_delta", "text": p.text[p.sent:]}
				if it.kind == "function_call" {
					delta = map[string]any{"type": "input_json_delta", "partial_json": p.text[p.sent:]}
				}
				events = append(events, encodeEvent(map[string]any{"type": "content_block_delta", "index": p.index, "delta": delta}))
				p.sent = len(p.text)
			}
			if !p.done || it.kind == "function_call" && !it.done {
				allClosed = false
				break
			}
			if it.kind == "function_call" {
				var args map[string]any
				_ = json.Unmarshal([]byte(p.text), &args)
				s.content[p.index]["input"] = args
			} else {
				s.content[p.index]["text"] = p.text
			}
			p.closed = true
			events = append(events, encodeEvent(map[string]any{"type": "content_block_stop", "index": p.index}))
		}
		if !it.done || !allClosed {
			break
		}
		s.next++
	}
	return events, nil
}

func (s *Stream) finish(raw []byte, eventType string) ([][]byte, error) {
	if err := s.identify(raw); err != nil {
		return s.fail("%v", err)
	}
	var v struct {
		Status     string            `json:"status"`
		Output     []json.RawMessage `json:"output"`
		Incomplete struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage struct {
			Input   int64 `json:"input_tokens"`
			Output  int64 `json:"output_tokens"`
			Details struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return s.fail("终态响应不是有效 JSON")
	}
	s.stop = "end_turn"
	switch v.Status {
	case "completed":
		if eventType != "response.completed" {
			return s.fail("终态事件和状态不一致")
		}
	case "incomplete":
		if eventType != "response.incomplete" {
			return s.fail("终态事件和状态不一致")
		}
		switch v.Incomplete.Reason {
		case "max_output_tokens":
			s.stop = "max_tokens"
		case "content_filter":
			s.stop = "refusal"
		default:
			return s.fail("不支持的截断原因 %s", v.Incomplete.Reason)
		}
	default:
		return s.fail("上游返回非成功终态 %s", v.Status)
	}
	if len(v.Output) < len(s.items) {
		return s.fail("终态响应丢失输出条目")
	}
	for i, item := range v.Output {
		if err := s.putItem(i, item, true); err != nil {
			return s.fail("%v", err)
		}
	}
	if v.Usage.Input < 0 || v.Usage.Output < 0 || v.Usage.Details.Cached < 0 || v.Usage.Details.Cached > v.Usage.Input {
		return s.fail("上游 token 用量无效")
	}
	s.used = map[string]any{"input_tokens": v.Usage.Input - v.Usage.Details.Cached, "cache_read_input_tokens": v.Usage.Details.Cached, "cache_creation_input_tokens": int64(0), "output_tokens": v.Usage.Output}
	events, err := s.drain()
	if err != nil {
		return s.fail("%v", err)
	}
	if s.next != len(s.items) {
		return s.fail("响应包含未结束的输出块")
	}
	s.start(&events)
	if s.stop == "end_turn" {
		if s.tools {
			s.stop = "tool_use"
		} else if s.refusal {
			s.stop = "refusal"
		}
	}
	events = append(events, encodeEvent(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": s.stop, "stop_sequence": nil}, "usage": s.used}), encodeEvent(map[string]any{"type": "message_stop"}))
	s.done = true
	return events, nil
}
func (s *Stream) ObserveBody(raw []byte) ([][]byte, error) {
	if len(raw) > 32<<20 {
		return s.fail("Responses JSON 超过转换上限")
	}
	var v struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return s.fail("上游响应不是 JSON")
	}
	return s.finish(raw, "response."+v.Status)
}
func (s *Stream) Response() ([]byte, error) {
	if !s.done || s.failed {
		return nil, fmt.Errorf("Messages 响应尚未正常完成")
	}
	return json.Marshal(map[string]any{"id": s.id, "type": "message", "role": "assistant", "model": s.req.Model, "content": s.content, "stop_reason": s.stop, "stop_sequence": nil, "usage": s.used})
}
func (s *Stream) Close() ([][]byte, error) { return s.fail("Responses 上游流在结束前断开") }
