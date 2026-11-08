package bridge

import (
	"encoding/json"
	"strings"
)

// onMessageStart 处理上游的第一个事件。
func (s *Stream) onMessageStart(ev map[string]any) [][]byte {
	msg := mapField(ev, "message")
	if s.model == "" {
		s.model = strField(msg, "model")
	}
	s.usage.merge(msg["usage"])
	if !s.lifecycle {
		return nil
	}
	return [][]byte{
		s.emit(map[string]any{"type": "response.created", "response": s.responseObject("in_progress")}),
		s.emit(map[string]any{"type": "response.in_progress", "response": s.responseObject("in_progress")}),
	}
}

// onContentBlockStart 开一个新的内容块。
func (s *Stream) onContentBlockStart(ev map[string]any) ([][]byte, error) {
	index := intField(ev, "index")
	if _, exists := s.blocks[index]; exists {
		return nil, newError(CodeInvalidInput, "", "上游流式事件顺序不对：内容块 %d 重复开始", index)
	}
	block := mapField(ev, "content_block")
	kind := strField(block, "type")
	st := &blockState{kind: kind}
	s.blocks[index] = st
	switch kind {
	case "text":
		out := s.flushPending("commentary")
		out = append(out, s.startText(st)...)
		initial, err := s.initialContent(st, block)
		return append(out, initial...), err
	case "thinking", "redacted_thinking":
		if kind == "redacted_thinking" {
			st.data = strField(block, "data")
		}
		out := s.startThinking(st)
		initial, err := s.initialContent(st, block)
		return append(out, initial...), err
	case "tool_use":
		out := s.flushPending("commentary")
		started, err := s.startToolUse(st, block)
		if err != nil {
			return out, err
		}
		initial, err := s.initialContent(st, block)
		out = append(out, started...)
		out = append(out, initial...)
		return out, err
	}
	return nil, newError(CodeUnsupportedContent, "", "不支持的上游内容块 %s", kind)
}

// initialContent 处理上游把第一段内容放在 content_block_start 里的情况。
func (s *Stream) initialContent(st *blockState, block map[string]any) ([][]byte, error) {
	switch st.kind {
	case "text":
		if text := strField(block, "text"); text != "" {
			return s.pushTextDelta(st, text)
		}
	case "thinking":
		out := [][]byte{}
		if thinking := strField(block, "thinking"); thinking != "" {
			delta, err := s.pushThinkingDelta(st, thinking)
			if err != nil {
				return nil, err
			}
			out = append(out, delta...)
		}
		if signature := strField(block, "signature"); signature != "" {
			st.signature.WriteString(signature)
		}
		return out, nil
	case "tool_use":
		input, ok := block["input"]
		if !ok || input == nil {
			return nil, nil
		}
		arguments, err := json.Marshal(input)
		if err != nil {
			return nil, newError(CodeInvalidArguments, "", "上游工具参数不是合法 JSON")
		}
		if string(arguments) == "{}" || string(arguments) == "null" {
			return nil, nil
		}
		return s.pushArgsDelta(st, string(arguments))
	}
	return nil, nil
}

// startText 开一个文本段。连续文本段合成同一条助手消息。
func (s *Stream) startText(st *blockState) [][]byte {
	if s.pending == nil {
		s.pending = &textState{itemID: "msg_" + randHex(12), outputIdx: s.outputIdx}
		s.outputIdx++
		out := [][]byte{s.emit(map[string]any{
			"type":         "response.output_item.added",
			"output_index": s.pending.outputIdx,
			"item": map[string]any{
				"type": "message", "id": s.pending.itemID, "status": "in_progress",
				"role": "assistant", "content": []any{},
			},
		})}
		st.msg = s.pending
		st.partIdx = len(s.pending.parts)
		return append(out, s.emit(map[string]any{
			"type":          "response.content_part.added",
			"item_id":       s.pending.itemID,
			"output_index":  s.pending.outputIdx,
			"content_index": st.partIdx,
			"part":          map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		}))
	}
	st.msg = s.pending
	st.partIdx = len(s.pending.parts)
	return [][]byte{s.emit(map[string]any{
		"type":          "response.content_part.added",
		"item_id":       s.pending.itemID,
		"output_index":  s.pending.outputIdx,
		"content_index": st.partIdx,
		"part":          map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})}
}

// startThinking 开一个思考块。
func (s *Stream) startThinking(st *blockState) [][]byte {
	st.itemID = "rs_" + randHex(12)
	st.outputIdx = s.outputIdx
	s.outputIdx++
	out := [][]byte{s.emit(map[string]any{
		"type":         "response.output_item.added",
		"output_index": st.outputIdx,
		"item":         map[string]any{"type": "reasoning", "id": st.itemID, "summary": []any{}},
	})}
	if st.kind != "thinking" {
		return out
	}
	return append(out, s.emit(map[string]any{
		"type":          "response.reasoning_summary_part.added",
		"item_id":       st.itemID,
		"output_index":  st.outputIdx,
		"content_index": 0,
		"part":          map[string]any{"type": "summary_text", "text": ""},
	}))
}

// startToolUse 开一个工具调用。上游给的块 id 原样当 call_id，调用 ID 不丢。
func (s *Stream) startToolUse(st *blockState, block map[string]any) ([][]byte, error) {
	name := strField(block, "name")
	info, ok := s.tools.byAnthropic[name]
	if !ok {
		return nil, newError(CodeUnknownTool, "", "上游调用了没有声明的工具 %s", name)
	}
	st.name = info.clientName
	st.namespace = info.namespace
	if st.namespace != "" {
		st.name = strings.TrimPrefix(st.name, st.namespace+".")
	}
	st.custom = info.custom
	st.callID = strField(block, "id")
	if st.callID == "" {
		return nil, newError(CodeInvalidInput, "", "上游工具调用缺少 ID")
	}
	if st.custom {
		st.itemID = "ctc_" + randHex(12)
	} else {
		st.itemID = "fc_" + randHex(12)
	}
	st.outputIdx = s.outputIdx
	s.outputIdx++
	item := map[string]any{
		"type": "function_call", "id": st.itemID, "call_id": st.callID,
		"name": st.name, "arguments": "", "status": "in_progress",
	}
	if st.custom {
		item = map[string]any{
			"type": "custom_tool_call", "id": st.itemID, "call_id": st.callID,
			"name": st.name, "input": "", "status": "in_progress",
		}
	}
	if st.namespace != "" {
		item["namespace"] = st.namespace
	}
	return [][]byte{s.emit(map[string]any{
		"type": "response.output_item.added", "output_index": st.outputIdx, "item": item,
	})}, nil
}

// onContentBlockDelta 处理内容块增量。
func (s *Stream) onContentBlockDelta(ev map[string]any) ([][]byte, error) {
	index := intField(ev, "index")
	st := s.blocks[index]
	if st == nil {
		return nil, newError(CodeInvalidInput, "", "上游流式事件顺序不对：内容块 %d 没有开始过", index)
	}
	if st.closed {
		return nil, newError(CodeInvalidInput, "", "上游流式事件顺序不对：内容块 %d 已经结束", index)
	}
	delta := mapField(ev, "delta")
	switch strField(delta, "type") {
	case "text_delta":
		return s.pushTextDelta(st, strField(delta, "text"))
	case "thinking_delta":
		return s.pushThinkingDelta(st, strField(delta, "thinking"))
	case "signature_delta":
		st.signature.WriteString(strField(delta, "signature"))
	case "input_json_delta":
		return s.pushArgsDelta(st, strField(delta, "partial_json"))
	}
	return nil, nil
}

// pushTextDelta 输出文本增量。
func (s *Stream) pushTextDelta(st *blockState, text string) ([][]byte, error) {
	st.text.WriteString(text)
	s.text.WriteString(text)
	if st.msg == nil {
		return nil, nil
	}
	return [][]byte{s.emit(map[string]any{
		"type":          "response.output_text.delta",
		"item_id":       st.msg.itemID,
		"output_index":  st.msg.outputIdx,
		"content_index": st.partIdx,
		"delta":         text,
	})}, nil
}

// pushThinkingDelta 输出思考摘要增量。
func (s *Stream) pushThinkingDelta(st *blockState, thinking string) ([][]byte, error) {
	st.thinking.WriteString(thinking)
	return [][]byte{s.emit(map[string]any{
		"type":          "response.reasoning_summary_text.delta",
		"item_id":       st.itemID,
		"output_index":  st.outputIdx,
		"content_index": 0,
		"delta":         thinking,
	})}, nil
}

// pushArgsDelta 输出工具参数增量。
func (s *Stream) pushArgsDelta(st *blockState, partial string) ([][]byte, error) {
	st.args.WriteString(partial)
	if st.custom {
		// 自由文本工具的输入是整段文本，拆成 JSON 增量反而会误导客户端。
		return nil, nil
	}
	return [][]byte{s.emit(map[string]any{
		"type":         "response.function_call_arguments.delta",
		"item_id":      st.itemID,
		"output_index": st.outputIdx,
		"delta":        partial,
	})}, nil
}

// onContentBlockStop 收一个内容块。
func (s *Stream) onContentBlockStop(ev map[string]any) ([][]byte, error) {
	index := intField(ev, "index")
	st := s.blocks[index]
	if st == nil {
		return nil, newError(CodeInvalidInput, "", "上游流式事件顺序不对：内容块 %d 没有开始过", index)
	}
	if st.closed {
		return nil, newError(CodeInvalidInput, "", "上游流式事件顺序不对：内容块 %d 重复结束", index)
	}
	st.closed = true
	switch st.kind {
	case "text":
		return s.stopText(st), nil
	case "thinking", "redacted_thinking":
		return s.stopThinking(st), nil
	case "tool_use":
		return s.stopToolUse(st)
	}
	return nil, nil
}

// stopText 收文本段。
func (s *Stream) stopText(st *blockState) [][]byte {
	text := st.text.String()
	part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
	if st.msg != nil {
		st.msg.parts = append(st.msg.parts, part)
	}
	return [][]byte{
		s.emit(map[string]any{
			"type":          "response.output_text.done",
			"item_id":       st.msg.itemID,
			"output_index":  st.msg.outputIdx,
			"content_index": st.partIdx,
			"text":          text,
		}),
		s.emit(map[string]any{
			"type":          "response.content_part.done",
			"item_id":       st.msg.itemID,
			"output_index":  st.msg.outputIdx,
			"content_index": st.partIdx,
			"part":          part,
		}),
	}
}

// stopThinking 收思考块，把思考内容封进 reasoning 项。
func (s *Stream) stopThinking(st *blockState) [][]byte {
	out := [][]byte{}
	if st.kind == "thinking" {
		text := st.thinking.String()
		out = append(out,
			s.emit(map[string]any{
				"type":          "response.reasoning_summary_text.done",
				"item_id":       st.itemID,
				"output_index":  st.outputIdx,
				"content_index": 0,
				"text":          text,
			}),
			s.emit(map[string]any{
				"type":          "response.reasoning_summary_part.done",
				"item_id":       st.itemID,
				"output_index":  st.outputIdx,
				"content_index": 0,
				"part":          map[string]any{"type": "summary_text", "text": text},
			}),
		)
	}
	item := s.reasoningItem(st)
	out = append(out, s.emit(map[string]any{
		"type":         "response.output_item.done",
		"output_index": st.outputIdx,
		"item":         item,
	}))
	s.putItem(st.outputIdx, item)
	return out
}

// stopToolUse 收工具调用。
func (s *Stream) stopToolUse(st *blockState) ([][]byte, error) {
	arguments := st.args.String()
	if st.custom {
		input, err := customInput(arguments)
		if err != nil {
			return nil, err
		}
		item := map[string]any{
			"type": "custom_tool_call", "id": st.itemID, "call_id": st.callID,
			"name": st.name, "input": input, "status": "completed",
		}
		if st.namespace != "" {
			item["namespace"] = st.namespace
		}
		out := [][]byte{s.emit(map[string]any{
			"type":         "response.output_item.done",
			"output_index": st.outputIdx,
			"item":         item,
		})}
		s.putItem(st.outputIdx, item)
		return out, nil
	}
	if arguments == "" {
		arguments = "{}"
	}
	var decoded map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &decoded) != nil || decoded == nil {
		return nil, newError(CodeInvalidArguments, "", "工具 %s 的参数不是合法 JSON 对象", st.name)
	}
	item := map[string]any{
		"type": "function_call", "id": st.itemID, "call_id": st.callID,
		"name": st.name, "arguments": arguments, "status": "completed",
	}
	if st.namespace != "" {
		item["namespace"] = st.namespace
	}
	out := [][]byte{
		s.emit(map[string]any{
			"type":         "response.function_call_arguments.done",
			"item_id":      st.itemID,
			"output_index": st.outputIdx,
			"arguments":    arguments,
		}),
		s.emit(map[string]any{
			"type":         "response.output_item.done",
			"output_index": st.outputIdx,
			"item":         item,
		}),
	}
	s.putItem(st.outputIdx, item)
	return out, nil
}

// reasoningItem 把思考块封成 Responses 的 reasoning 项。
func (s *Stream) reasoningItem(st *blockState) map[string]any {
	summary := []any{}
	blocks := []map[string]any{}
	if st.kind == "redacted_thinking" {
		blocks = append(blocks, map[string]any{"type": "redacted_thinking", "data": st.data})
	} else {
		text := st.thinking.String()
		if text != "" {
			summary = append(summary, map[string]any{"type": "summary_text", "text": text})
		}
		blocks = append(blocks, map[string]any{
			"type": "thinking", "thinking": text, "signature": st.signature.String(),
		})
	}
	return map[string]any{
		"type":              "reasoning",
		"id":                st.itemID,
		"summary":           summary,
		"encrypted_content": sealCapsule(capsule{Model: s.model, Blocks: blocks}),
	}
}

// onMessageDelta 取停止原因和用量。
func (s *Stream) onMessageDelta(ev map[string]any) [][]byte {
	if reason := strField(mapField(ev, "delta"), "stop_reason"); reason != "" {
		s.stopReason = reason
	}
	s.usage.merge(ev["usage"])
	return nil
}

// onMessageStop 收尾：出终态事件。
func (s *Stream) onMessageStop() ([][]byte, error) {
	out := s.flushPending("final_answer")
	if s.compact {
		if s.stopReason == "max_tokens" {
			// 摘要被截断，不能当成成功。
			obj := s.responseObject("failed")
			obj["error"] = map[string]any{"code": CodeTruncatedSummary, "message": "摘要被输出上限截断"}
			s.terminal = obj
			out = append(out, s.emit(map[string]any{"type": "response.failed", "response": obj}))
			s.failed = true
			s.done = true
			s.status = "failed"
			return out, newError(CodeTruncatedSummary, "", "摘要被输出上限截断")
		}
		summary := s.Summary()
		if summary == "" {
			obj := s.responseObject("failed")
			obj["error"] = map[string]any{"code": CodeEmptySummary, "message": "压缩请求没有生成摘要"}
			s.terminal = obj
			out = append(out, s.emit(map[string]any{"type": "response.failed", "response": obj}))
			s.failed = true
			s.done = true
			s.status = "failed"
			return out, newError(CodeEmptySummary, "", "压缩请求没有生成摘要")
		}
		s.compaction = SealCompaction(summary)
		item := map[string]any{
			"type": "compaction", "id": "cmp_" + randHex(12), "encrypted_content": s.compaction,
		}
		out = append(out,
			s.emit(map[string]any{
				"type":         "response.output_item.added",
				"output_index": s.outputIdx,
				"item":         item,
			}),
			s.emit(map[string]any{
				"type":         "response.output_item.done",
				"output_index": s.outputIdx,
				"item":         item,
			}),
		)
		s.putItem(s.outputIdx, item)
		s.outputIdx++
	}
	status := "completed"
	event := "response.completed"
	var incomplete any
	switch s.stopReason {
	case "end_turn", "stop_sequence", "tool_use", "refusal":
		// 正常收尾：模型说完、命中停止序列、交付工具调用或拒绝作答。
	case "max_tokens":
		status = "incomplete"
		event = "response.incomplete"
		incomplete = map[string]any{"reason": "max_output_tokens"}
	case "pause_turn":
		// 上游想接着这一轮继续，本层没有续写的机制，明确报错而不是假装结束。
		obj := s.responseObject("failed")
		obj["error"] = map[string]any{"code": CodeUnsupportedStopReason, "message": "上游要求继续这一轮，本转接暂不支持"}
		s.terminal = obj
		out = append(out, s.emit(map[string]any{"type": "response.failed", "response": obj}))
		s.failed = true
		s.done = true
		s.status = "failed"
		return out, newError(CodeUnsupportedStopReason, "", "上游要求继续这一轮，本转接暂不支持 pause_turn")
	default:
		// 停止原因缺失或看不懂，不能当成成功。
		obj := s.responseObject("failed")
		obj["error"] = map[string]any{"code": CodeUnknownStopReason, "message": "上游没有给出可识别的停止原因"}
		s.terminal = obj
		out = append(out, s.emit(map[string]any{"type": "response.failed", "response": obj}))
		s.failed = true
		s.done = true
		s.status = "failed"
		return out, newError(CodeUnknownStopReason, "", "上游没有给出可识别的停止原因")
	}
	obj := s.responseObject(status)
	if incomplete != nil {
		obj["incomplete_details"] = incomplete
	}
	s.terminal = obj
	out = append(out, s.emit(map[string]any{"type": event, "response": obj}))
	s.done = true
	s.status = status
	return out, nil
}

// onError 处理上游流里的错误。
func (s *Stream) onError(ev map[string]any) [][]byte {
	e := mapField(ev, "error")
	obj := s.responseObject("failed")
	obj["error"] = map[string]any{"code": strField(e, "type"), "message": strField(e, "message")}
	s.terminal = obj
	out := [][]byte{s.emit(map[string]any{"type": "response.failed", "response": obj})}
	s.failed = true
	s.done = true
	s.status = "failed"
	return out
}

// flushPending 收掉还没定 phase 的助手消息。工具之前的是 commentary，之后的是 final_answer。
func (s *Stream) flushPending(phase string) [][]byte {
	if s.pending == nil {
		return nil
	}
	item := map[string]any{
		"type": "message", "id": s.pending.itemID, "status": "completed", "role": "assistant",
		"content": s.pending.parts, "phase": phase,
	}
	out := [][]byte{s.emit(map[string]any{
		"type":         "response.output_item.done",
		"output_index": s.pending.outputIdx,
		"item":         item,
	})}
	if !s.compact {
		// 压缩请求的最终响应只留 compaction 项，正文只走事件流。
		s.putItem(s.pending.outputIdx, item)
	}
	s.pending = nil
	return out
}
