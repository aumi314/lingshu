package messagesbridge

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func feed(t *testing.T, s *Stream, v any) [][]byte {
	t.Helper()
	b, _ := json.Marshal(v)
	out, err := s.Event(b)
	if err != nil {
		t.Fatalf("event %s: %v", b, err)
	}
	return out
}
func rawItem(id, kind string) map[string]any { return map[string]any{"id": id, "type": kind} }
func doneEvent(items ...any) map[string]any {
	return map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_test", "status": "completed", "output": items, "usage": map[string]any{"input_tokens": 20, "output_tokens": 5, "input_tokens_details": map[string]any{"cached_tokens": 12}}}}
}
func contentOf(t *testing.T, s *Stream) []any {
	t.Helper()
	b, err := s.Response()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m["content"].([]any)
}

func TestStreamParallelToolsAndReplay(t *testing.T) {
	s := NewStream("msg_test", &Request{Model: "m", ToolNames: map[string]string{"read": "Read", "exec": "Bash"}})
	var events [][]byte
	for i, tool := range []string{"read", "exec"} {
		item := rawItem(fmt.Sprint("fc", i), "function_call")
		item["call_id"] = fmt.Sprint("call", i)
		item["name"] = tool
		item["arguments"] = ""
		events = append(events, feed(t, s, map[string]any{"type": "response.output_item.added", "output_index": i, "item": item})...)
	}
	events = append(events, feed(t, s, map[string]any{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc1", "delta": `{"command":"echo 中文"}`})...)
	events = append(events, feed(t, s, map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc0", "delta": `{"path":`})...)
	events = append(events, feed(t, s, map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc0", "delta": `"a\nb"}`})...)
	items := []any{map[string]any{"type": "function_call", "id": "fc0", "call_id": "call0", "name": "read", "arguments": `{"path":"a\nb"}`}, map[string]any{"type": "function_call", "id": "fc1", "call_id": "call1", "name": "exec", "arguments": `{"command":"echo 中文"}`}}
	events = append(events, feed(t, s, map[string]any{"type": "response.output_item.done", "output_index": 1, "item": items[1]})...)
	events = append(events, feed(t, s, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": items[0]})...)
	events = append(events, feed(t, s, doneEvent(items...))...)
	active := -1
	starts, stops := 0, 0
	for _, data := range events {
		var e map[string]any
		json.Unmarshal(data, &e)
		switch e["type"] {
		case "content_block_start":
			if active != -1 {
				t.Fatal("overlapping blocks")
			}
			active = int(e["index"].(float64))
			starts++
		case "content_block_delta":
			if active != int(e["index"].(float64)) {
				t.Fatal("delta outside active block")
			}
		case "content_block_stop":
			if active != int(e["index"].(float64)) {
				t.Fatal("wrong block stop")
			}
			active = -1
			stops++
		}
	}
	if starts != 2 || stops != 2 {
		t.Fatalf("blocks %d/%d", starts, stops)
	}
	content := contentOf(t, s)
	if content[0].(map[string]any)["name"] != "Read" || content[1].(map[string]any)["name"] != "Bash" {
		t.Fatalf("names %v", content)
	}
	replay := map[string]any{"model": "m", "max_tokens": 100, "tools": []any{map[string]any{"name": "Read", "input_schema": map[string]any{"type": "object"}}, map[string]any{"name": "Bash", "input_schema": map[string]any{"type": "object"}}}, "messages": []any{map[string]any{"role": "assistant", "content": content}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call0", "content": "ok"}, map[string]any{"type": "tool_result", "tool_use_id": "call1", "content": "failed", "is_error": true}}}}}
	raw, _ := json.Marshal(replay)
	q, err := Prepare(raw, "up")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(q.Body), "[Tool execution failed]") {
		t.Fatal("tool failure signal lost")
	}
}

func TestStreamReasoningReplayAndFinalCiphertext(t *testing.T) {
	for _, summary := range []string{"", "计划\n中文"} {
		t.Run(fmt.Sprint(len(summary)), func(t *testing.T) {
			s := NewStream("msg_test", &Request{Model: "m"})
			initial := rawItem("rs1", "reasoning")
			initial["summary"] = []any{}
			initial["encrypted_content"] = "incomplete"
			if got := feed(t, s, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": initial}); len(got) != 0 {
				t.Fatal("incomplete encrypted item emitted")
			}
			final := rawItem("rs1", "reasoning")
			final["summary"] = []any{}
			final["encrypted_content"] = "final-ciphertext"
			if summary != "" {
				final["summary"] = []any{map[string]any{"type": "summary_text", "text": summary}}
			}
			feed(t, s, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": final})
			feed(t, s, doneEvent(final))
			block := contentOf(t, s)[0].(map[string]any)
			value, _ := block["signature"].(string)
			if summary == "" {
				value, _ = block["data"].(string)
			}
			restored, err := ExpandReasoning(value)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			json.Unmarshal(restored, &m)
			if !reflect.DeepEqual(m, final) {
				t.Fatalf("reasoning changed %s", restored)
			}
			replay := map[string]any{"model": "m", "max_tokens": 100, "messages": []any{map[string]any{"role": "assistant", "content": []any{block}}, map[string]any{"role": "user", "content": "continue"}}}
			raw, _ := json.Marshal(replay)
			q, err := Prepare(raw, "up")
			if err != nil || !strings.Contains(string(q.Body), "final-ciphertext") {
				t.Fatalf("replay %v", err)
			}
		})
	}
}

func TestStreamTerminalReasonsAndMalformedOutput(t *testing.T) {
	cases := []struct{ status, reason, want string }{{"completed", "", "end_turn"}, {"incomplete", "max_output_tokens", "max_tokens"}, {"incomplete", "content_filter", "refusal"}, {"incomplete", "unknown", ""}, {"failed", "", ""}}
	for _, c := range cases {
		t.Run(c.status+c.reason, func(t *testing.T) {
			s := NewStream("msg", &Request{Model: "m"})
			v := map[string]any{"id": "resp_test", "status": c.status, "output": []any{}, "incomplete_details": map[string]any{"reason": c.reason}}
			b, _ := json.Marshal(v)
			_, err := s.ObserveBody(b)
			if c.want == "" {
				if err == nil {
					t.Fatal("bad status accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := s.Response()
			var got map[string]any
			json.Unmarshal(raw, &got)
			if got["stop_reason"] != c.want {
				t.Fatalf("stop=%v", got)
			}
		})
	}
	for _, args := range []string{"{bad", "null", "[]"} {
		s := NewStream("msg", &Request{ToolNames: map[string]string{"f": "f"}})
		item := map[string]any{"type": "function_call", "id": "fc", "call_id": "c", "name": "f", "arguments": args}
		b, _ := json.Marshal(doneEvent(item))
		if _, err := s.Event(b); err == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	s := NewStream("msg", &Request{})
	if _, err := s.Event([]byte(`{"type":"response.output_text.delta","output_index":0,"delta":"orphan"}`)); err == nil {
		t.Fatal("orphan delta accepted")
	}
}

func TestStreamTextIsImmediateAndEOFIsFailure(t *testing.T) {
	s := NewStream("msg", &Request{Model: "m"})
	feed(t, s, map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_test"}})
	feed(t, s, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "m1", "type": "message", "role": "assistant", "content": []any{}}})
	out := feed(t, s, map[string]any{"type": "response.output_text.delta", "output_index": 0, "item_id": "m1", "content_index": 0, "delta": "即时\n文字"})
	if !strings.Contains(string(out[len(out)-1]), "即时") {
		t.Fatal("text was buffered")
	}
	if _, err := s.Close(); err == nil || !s.Failed() || s.Done() {
		t.Fatal("EOF treated as success")
	}
}
