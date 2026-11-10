package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

// feed 把上游事件喂给转换器，返回所有 Responses 事件。
func feed(t *testing.T, s *Stream, events ...string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, event := range events {
		evs, err := s.Event([]byte(event))
		if err != nil {
			t.Fatalf("事件处理失败: %v", err)
		}
		for _, raw := range evs {
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("事件不是合法 JSON: %v", err)
			}
			out = append(out, decoded)
		}
	}
	return out
}

// eventTypes 取事件类型序列。
func eventTypes(events []map[string]any) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e["type"].(string))
	}
	return out
}

// findEvent 找第一个指定类型的事件。
func findEvent(t *testing.T, events []map[string]any, kind string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e["type"] == kind {
			return e
		}
	}
	t.Fatalf("没有 %s 事件，实际有 %v", kind, eventTypes(events))
	return nil
}

// findItem 找指定类型的输出项事件。
func findItem(t *testing.T, events []map[string]any, itemType string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e["type"] != "response.output_item.added" {
			continue
		}
		item, _ := e["item"].(map[string]any)
		if item["type"] == itemType {
			return e
		}
	}
	t.Fatalf("没有 %s 输出项，实际有 %v", itemType, eventTypes(events))
	return nil
}

func TestStreamText(t *testing.T) {
	s := NewStream("resp_1")
	s.SetLifecycle(true)
	events := feed(t, s,
		`{"type":"message_start","message":{"id":"msg_up","model":"claude-opus-5-5","usage":{"input_tokens":12,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"第一段"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\n第二段"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`)
	types := eventTypes(events)
	if types[0] != "response.created" || types[1] != "response.in_progress" {
		t.Fatalf("开头应先发 created/in_progress: %v", types)
	}
	if types[len(types)-1] != "response.completed" {
		t.Fatalf("结尾应是 response.completed: %v", types)
	}
	delta := findEvent(t, events, "response.output_text.delta")
	if delta["delta"] != "第一段" {
		t.Errorf("文本增量不对: %v", delta)
	}
	for i, e := range events {
		if e["sequence_number"].(float64) != float64(i) {
			t.Fatalf("第 %d 个事件序号不对: %v", i, e["sequence_number"])
		}
	}
	if !s.Done() || s.Failed() {
		t.Errorf("正常结束不该算失败")
	}
	body, err := s.Response()
	if err != nil {
		t.Fatalf("拼响应体失败: %v", err)
	}
	var response map[string]any
	json.Unmarshal(body, &response)
	if response["status"] != "completed" {
		t.Errorf("状态不对: %v", response["status"])
	}
	output, _ := response["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("输出项数 %d: %v", len(output), output)
	}
	item := output[0].(map[string]any)
	if item["phase"] != "final_answer" {
		t.Errorf("最后一段应是 final_answer: %v", item)
	}
	usage := response["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 12 || usage["output_tokens"].(float64) != 9 {
		t.Errorf("用量不对: %v", usage)
	}
}

func TestStreamMultilineTextEscaping(t *testing.T) {
	s := NewStream("resp_1")
	text := "第一行\n第二行\t带引号 \" 和反斜杠 \\ 还有中文"
	events := feed(t, s,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"第一行\n第二行\t带引号 \" 和反斜杠 \\ 还有中文"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_stop"}`)
	delta := findEvent(t, events, "response.output_text.delta")
	if delta["delta"] != text {
		t.Errorf("多行文本转义后变了: %q", delta["delta"])
	}
	done := findEvent(t, events, "response.output_text.done")
	if done["text"] != text {
		t.Errorf("done 里的正文不对: %q", done["text"])
	}
}

func TestStreamToolCallAndPhases(t *testing.T) {
	req, err := Prepare([]byte(`{"tools":[{"type":"function","name":"exec","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"跑一下"}]}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	s := NewStream("resp_2")
	s.Bind(req)
	events := feed(t, s,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"我先跑一下"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_abc","name":"exec","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":\"ls\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"跑完了"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`)
	// 工具之前的正文是 commentary，之后的是 final_answer。
	var phases []string
	for _, e := range events {
		if e["type"] == "response.output_item.done" {
			item := e["item"].(map[string]any)
			if item["type"] == "message" {
				phases = append(phases, item["phase"].(string))
			}
		}
	}
	if len(phases) != 2 || phases[0] != "commentary" || phases[1] != "final_answer" {
		t.Fatalf("阶段不对: %v", phases)
	}
	call := findEvent(t, events, "response.function_call_arguments.done")
	if call["arguments"] != `{"cmd":"ls"}` {
		t.Errorf("工具参数不对: %v", call)
	}
	added := findItem(t, events, "function_call")
	item := added["item"].(map[string]any)
	if item["type"] != "function_call" || item["name"] != "exec" {
		t.Fatalf("工具项不对: %v", item)
	}
	if item["call_id"] != "toolu_abc" {
		t.Errorf("调用 ID 应该原样保留: %v", item)
	}
}

func TestStreamCustomToolCall(t *testing.T) {
	req, err := Prepare([]byte(`{"tools":[{"type":"custom","name":"apply_patch","description":"打补丁"}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"打补丁"}]}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	s := NewStream("resp_3")
	s.Bind(req)
	events := feed(t, s,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"apply_patch","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"*** Begin Patch\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`)
	done := findEvent(t, events, "response.output_item.done")
	item := done["item"].(map[string]any)
	if item["type"] != "custom_tool_call" {
		t.Fatalf("自由文本工具应还原成 custom_tool_call: %v", item)
	}
	if item["input"] != "*** Begin Patch" {
		t.Errorf("自由文本输入不对: %v", item)
	}
}

func TestStreamThinkingCapsule(t *testing.T) {
	s := NewStream("resp_4")
	events := feed(t, s,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先想想"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_stop"}`)
	done := findEvent(t, events, "response.output_item.done")
	item := done["item"].(map[string]any)
	if item["type"] != "reasoning" {
		t.Fatalf("思考块应还原成 reasoning 项: %v", item)
	}
	summary, _ := item["summary"].([]any)
	if len(summary) != 1 || summary[0].(map[string]any)["text"] != "先想想" {
		t.Errorf("思考摘要不对: %v", summary)
	}
	seal, _ := item["encrypted_content"].(string)
	blocks, err := decodeCapsule(seal, "claude-opus-5-5")
	if err != nil {
		t.Fatalf("封装解不开: %v", err)
	}
	if blocks[0]["type"] != "thinking" || blocks[0]["thinking"] != "先想想" || blocks[0]["signature"] != "sig-1" {
		t.Errorf("封装内容不对: %v", blocks)
	}
}

func TestStreamRedactedThinking(t *testing.T) {
	s := NewStream("resp_5")
	events := feed(t, s,
		`{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"opaque-blob"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_stop"}`)
	done := findEvent(t, events, "response.output_item.done")
	item := done["item"].(map[string]any)
	seal, _ := item["encrypted_content"].(string)
	blocks, err := decodeCapsule(seal, "claude-opus-5-5")
	if err != nil {
		t.Fatalf("封装解不开: %v", err)
	}
	if blocks[0]["type"] != "redacted_thinking" || blocks[0]["data"] != "opaque-blob" {
		t.Errorf("隐藏思考没原样保留: %v", blocks)
	}
}

func TestStreamTerminalStates(t *testing.T) {
	cases := []struct {
		reason string
		event  string
		status string
	}{
		{"end_turn", "response.completed", "completed"},
		{"stop_sequence", "response.completed", "completed"},
		{"tool_use", "response.completed", "completed"},
		{"refusal", "response.completed", "completed"},
		{"max_tokens", "response.incomplete", "incomplete"},
	}
	for _, c := range cases {
		s := NewStream("resp_x")
		events := feed(t, s,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"内容"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"`+c.reason+`"}}`,
			`{"type":"message_stop"}`)
		last := events[len(events)-1]
		if last["type"] != c.event {
			t.Errorf("stop_reason=%s 应出 %s，实际 %s", c.reason, c.event, last["type"])
		}
		response := last["response"].(map[string]any)
		if response["status"] != c.status {
			t.Errorf("stop_reason=%s 状态应为 %s，实际 %v", c.reason, c.status, response["status"])
		}
		if c.reason == "max_tokens" {
			details := response["incomplete_details"].(map[string]any)
			if details["reason"] != "max_output_tokens" {
				t.Errorf("上限截断的原因不对: %v", details)
			}
		}
	}
}

// TestStreamTerminalFailureStates 覆盖不能当成成功的收尾：
// pause_turn 上游要续写，本层不支持；停止原因缺失或看不懂，同样不能伪造完成。
func TestStreamTerminalFailureStates(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		code   string
	}{
		{"pause_turn", "pause_turn", CodeUnsupportedStopReason},
		{"unknown", "something_else", CodeUnknownStopReason},
		{"empty", "", CodeUnknownStopReason},
	}
	for _, c := range cases {
		s := NewStream("resp_y")
		feed(t, s,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"内容"}}`,
			`{"type":"content_block_stop","index":0}`)
		if c.reason != "" {
			feed(t, s, `{"type":"message_delta","delta":{"stop_reason":"`+c.reason+`"}}`)
		}
		_, err := s.Event([]byte(`{"type":"message_stop"}`))
		if err == nil {
			t.Fatalf("%s 应该报错", c.name)
		}
		if code := errorCode(t, err); code != c.code {
			t.Errorf("%s 错误码 %s，期望 %s", c.name, code, c.code)
		}
		if !s.Failed() {
			t.Errorf("%s 应算失败", c.name)
		}
		body, err := s.Response()
		if err != nil {
			t.Fatalf("%s 拼响应体失败: %v", c.name, err)
		}
		var response map[string]any
		json.Unmarshal(body, &response)
		if response["status"] != "failed" {
			t.Errorf("%s 状态应为 failed，实际 %v", c.name, response["status"])
		}
		if response["error"] == nil {
			t.Errorf("%s 的响应体应带 error", c.name)
		}
	}
}

func TestStreamErrorAndTruncation(t *testing.T) {
	s := NewStream("resp_6")
	events := feed(t, s, `{"type":"error","error":{"type":"overloaded_error","message":"上游过载"}}`)
	last := events[len(events)-1]
	if last["type"] != "response.failed" {
		t.Fatalf("流内错误应出 response.failed: %v", last["type"])
	}
	if !s.Done() || !s.Failed() {
		t.Error("流内错误应算失败")
	}

	cut := NewStream("resp_7")
	feed(t, cut,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"半截"}}`)
	if cut.Done() {
		t.Error("没收到终态不该算完成")
	}
	closed, err := cut.Close()
	if err == nil {
		t.Error("断流应该报错")
	}
	if len(closed) != 1 || !strings.Contains(string(closed[0]), "response.failed") {
		t.Fatalf("断流应出 response.failed: %v", closed)
	}
	if !cut.Failed() {
		t.Error("断流应算失败")
	}
}

func TestStreamRejectsOutOfOrder(t *testing.T) {
	s := NewStream("resp_8")
	if _, err := s.Event([]byte(`{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"x"}}`)); err == nil {
		t.Error("没有开始过的内容块应该报错")
	}
	if _, err := s.Event([]byte(`不是 JSON`)); err == nil {
		t.Error("坏 JSON 应该报错")
	}
}

func TestStreamRejectsUnknownTool(t *testing.T) {
	req, err := Prepare([]byte(`{"tools":[{"type":"function","name":"exec","parameters":{"type":"object","properties":{}}}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"跑一下"}]}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	s := NewStream("resp_9")
	s.Bind(req)
	_, err = s.Event([]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"unknown","input":{}}}`))
	if err == nil {
		t.Fatal("上游调用没声明的工具应该报错")
	}
	if code := errorCode(t, err); code != CodeUnknownTool {
		t.Errorf("错误码 %s，期望 %s", code, CodeUnknownTool)
	}
}

func TestStreamCompactionCapsule(t *testing.T) {
	req, err := Prepare([]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"长对话"}]},{"type":"compaction_trigger"}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	s := NewStream("resp_10")
	s.Bind(req)
	events := feed(t, s,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"之前的摘要内容"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_stop"}`)
	compaction := findItem(t, events, "compaction")
	item := compaction["item"].(map[string]any)
	if item["type"] != "compaction" {
		t.Fatalf("压缩请求应产出 compaction 项: %v", item)
	}
	seal, _ := item["encrypted_content"].(string)
	summary, err := ExpandCompaction(seal)
	if err != nil || summary != "之前的摘要内容" {
		t.Errorf("压缩封装不对: %q %v", summary, err)
	}
	body, err := s.Response()
	if err != nil {
		t.Fatalf("拼响应体失败: %v", err)
	}
	var response map[string]any
	json.Unmarshal(body, &response)
	output, _ := response["output"].([]any)
	if len(output) != 1 || output[0].(map[string]any)["type"] != "compaction" {
		t.Fatalf("压缩请求的最终响应只应留 compaction 项: %v", output)
	}
}

func TestStreamCompactionEmptySummary(t *testing.T) {
	req, err := Prepare([]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"长对话"}]},{"type":"compaction_trigger"}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	s := NewStream("resp_11")
	s.Bind(req)
	_, err = s.Event([]byte(`{"type":"message_stop"}`))
	if err == nil {
		t.Fatal("空摘要应该报错")
	}
	if code := errorCode(t, err); code != CodeEmptySummary {
		t.Errorf("错误码 %s，期望 %s", code, CodeEmptySummary)
	}
	if !s.Failed() {
		t.Error("空摘要应算失败")
	}
}

func TestStreamCompactionTruncated(t *testing.T) {
	req, err := Prepare([]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"长对话"}]},{"type":"compaction_trigger"}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	s := NewStream("resp_12")
	s.Bind(req)
	_, err = s.Event([]byte(`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`))
	if err != nil {
		t.Fatalf("事件处理失败: %v", err)
	}
	_, err = s.Event([]byte(`{"type":"message_stop"}`))
	if err == nil {
		t.Fatal("摘要被截断应该报错")
	}
	if code := errorCode(t, err); code != CodeTruncatedSummary {
		t.Errorf("错误码 %s，期望 %s", code, CodeTruncatedSummary)
	}
}

func TestParallelToolsKeepOutputOrder(t *testing.T) {
	q, err := Prepare([]byte(`{"input":"test","tools":[{"type":"function","name":"a"},{"type":"function","name":"b"}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	st := NewStream("resp_parallel")
	st.Bind(q)
	feed(t, st,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"a","id":"toolu_a","input":{}}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"b","id":"toolu_b","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`)
	raw, err := st.Response()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Output []map[string]any `json:"output"`
	}
	json.Unmarshal(raw, &v)
	if v.Output[0]["call_id"] != "toolu_a" || v.Output[1]["call_id"] != "toolu_b" {
		t.Fatalf("%s", raw)
	}
}

func TestNamespaceCallRoundTrip(t *testing.T) {
	q, err := Prepare([]byte(`{"input":"test","tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"exec_command"}]}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	st := NewStream("resp_namespace")
	st.Bind(q)
	feed(t, st,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"functions_exec_command","id":"toolu_ns","input":{"cmd":"pwd"}}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`)
	raw, _ := st.Response()
	var v struct {
		Output []map[string]any `json:"output"`
	}
	json.Unmarshal(raw, &v)
	item := v.Output[0]
	if item["name"] != "exec_command" || item["namespace"] != "functions" {
		t.Fatalf("%s", raw)
	}
	body, _ := json.Marshal(map[string]any{"tools": []any{map[string]any{"type": "namespace", "name": "functions", "tools": []any{map[string]any{"type": "function", "name": "exec_command"}}}}, "input": []any{item, map[string]any{"type": "function_call_output", "call_id": "toolu_ns", "output": "/workspace"}}})
	replay, err := Prepare(body, "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(replay.Body), `"name":"functions_exec_command"`) {
		t.Fatalf("%s", replay.Body)
	}
}
