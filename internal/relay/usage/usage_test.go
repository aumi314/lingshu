package usage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata 里是 2026-10-18 从真实上游抓的响应原文（scripts/capture-fixtures.py）。
func TestFixtures(t *testing.T) {
	cases := []struct {
		file       string
		proto      Protocol
		want       Usage
		responseID string
	}{
		{"deepseek_chat.json", Chat, Usage{Input: 37, Output: 37, Reasoning: 28}, ""},
		{"deepseek_chat.sse", Chat, Usage{Input: 37, Output: 42, Reasoning: 34}, ""},
		{"deepseek_responses.json", Responses, Usage{Input: 37, Output: 62, Reasoning: 53}, "ae83a80e-c19b-45e1-aca9-f7372368c21b"},
		{"deepseek_responses.sse", Responses, Usage{Input: 37, Output: 26, Reasoning: 17}, ""},
		{"zhipu_chat.json", Chat, Usage{Input: 19, Output: 42, Reasoning: 33}, ""},
		{"zhipu_chat.sse", Chat, Usage{Input: 19, Output: 43, Reasoning: 34}, ""},
		{"mimo_chat.json", Chat, Usage{Input: 14, Output: 24, Reasoning: 14}, ""},
		{"mimo_chat.sse", Chat, Usage{Input: 14, Output: 17, Reasoning: 10}, ""},
		{"moonshot_chat.json", Chat, Usage{Input: 92, Output: 48, Reasoning: 26}, ""},
		{"moonshot_chat.sse", Chat, Usage{Input: 92, Output: 110, Reasoning: 88}, ""},
		// StepFun 每块都带累计 usage，只能取最后一块
		{"stepfun_chat.json", Chat, Usage{Input: 18, Output: 86}, ""},
		{"stepfun_chat.sse", Chat, Usage{Input: 18, Output: 57}, ""},
		// 阿里 message_start 里 input_tokens=7 不准，必须以 message_delta 的 68 为准
		{"dashscope_messages.json", Messages, Usage{Input: 68, Output: 30}, ""},
		{"dashscope_messages.sse", Messages, Usage{Input: 68, Output: 41}, ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.file))
			if err != nil {
				t.Fatal(err)
			}
			tr := NewTracker(c.proto)
			if strings.HasSuffix(c.file, ".sse") {
				r := NewSSEReader(bytes.NewReader(raw))
				var forwarded []byte
				for {
					ev, err := r.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					forwarded = append(forwarded, ev.Raw...)
					tr.ObserveEvent(ev.Data)
				}
				if !bytes.Equal(forwarded, raw) {
					t.Error("按事件拼回的字节和原文不一致，透传会改坏内容")
				}
			} else {
				tr.ObserveBody(raw)
			}
			if got := tr.Result(); got != c.want {
				t.Errorf("usage = %+v，期望 %+v", got, c.want)
			}
			if !tr.Terminal {
				t.Error("没有识别出正常结束")
			}
			if c.responseID != "" && tr.ResponseID != c.responseID {
				t.Errorf("ResponseID = %q，期望 %q", tr.ResponseID, c.responseID)
			}
		})
	}
}

func TestStreamResponseIDFromEvents(t *testing.T) {
	raw, _ := os.ReadFile("testdata/deepseek_responses.sse")
	tr := NewTracker(Responses)
	r := NewSSEReader(bytes.NewReader(raw))
	for {
		ev, err := r.Next()
		if err != nil {
			break
		}
		tr.ObserveEvent(ev.Data)
	}
	if tr.ResponseID == "" {
		t.Error("流式 responses 应该能拿到响应 id")
	}
}

func TestCacheNormalization(t *testing.T) {
	chat := NewTracker(Chat)
	chat.ObserveBody([]byte(`{"usage":{"prompt_tokens":100,"completion_tokens":5,
		"prompt_tokens_details":{"cached_tokens":30,"cache_write_tokens":10}}}`))
	if got, want := chat.Result(), (Usage{Input: 60, CacheRead: 30, CacheWrite: 10, Output: 5}); got != want {
		t.Errorf("chat 缓存口径 = %+v，期望 %+v", got, want)
	}

	msg := NewTracker(Messages)
	msg.ObserveBody([]byte(`{"usage":{"input_tokens":60,"output_tokens":5,
		"cache_read_input_tokens":30,"cache_creation_input_tokens":10}}`))
	if got, want := msg.Result(), (Usage{Input: 60, CacheRead: 30, CacheWrite: 10, Output: 5}); got != want {
		t.Errorf("messages 缓存口径 = %+v，期望 %+v", got, want)
	}

	resp := NewTracker(Responses)
	resp.ObserveBody([]byte(`{"usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":40}}}`))
	if got, want := resp.Result(), (Usage{Input: 60, CacheRead: 40, Output: 5}); got != want {
		t.Errorf("responses 缓存口径 = %+v，期望 %+v", got, want)
	}
}

func TestStreamFailureDetection(t *testing.T) {
	msg := NewTracker(Messages)
	msg.ObserveEvent([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"x"}}`))
	if !msg.Failed || msg.Terminal {
		t.Error("messages 的 error 事件应标记为失败")
	}
	resp := NewTracker(Responses)
	resp.ObserveEvent([]byte(`{"type":"response.failed","response":{"id":"r1"}}`))
	if !resp.Failed || resp.Terminal {
		t.Error("response.failed 应标记为失败")
	}
}

func TestIsUsageOnlyChatChunk(t *testing.T) {
	cases := map[string]bool{
		`{"choices":[],"usage":{"prompt_tokens":1}}`:             true,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":1}}`: false, // DeepSeek：usage 跟在最后一个内容块里
		`{"choices":[],"usage":null}`:                            false,
		`{"choices":[{"delta":{"content":"hi"}}]}`:               false,
		`{"usage":{"prompt_tokens":1}}`:                          false,
		`not json`:                                               false,
	}
	for in, want := range cases {
		if got := IsUsageOnlyChatChunk([]byte(in)); got != want {
			t.Errorf("%s => %v，期望 %v", in, got, want)
		}
	}
}

func TestSSEReaderEdgeCases(t *testing.T) {
	in := "\r\n: comment\r\ndata: a\r\ndata: b\r\n\r\ndata:{\"x\":1}"
	r := NewSSEReader(strings.NewReader(in))
	ev, err := r.Next()
	if err != nil || string(ev.Data) != "a\nb" {
		t.Fatalf("多行 data + CRLF：%q %v", ev.Data, err)
	}
	ev, err = r.Next()
	if err != nil || string(ev.Data) != `{"x":1}` {
		t.Fatalf("结尾没有空行的事件：%q %v", ev.Data, err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("应该返回 EOF，实际 %v", err)
	}
}

func TestEstimates(t *testing.T) {
	if got := EstimateText("你好世界"); got != 4 {
		t.Errorf("中文 4 字 = %d，期望 4", got)
	}
	if got := EstimateText("hello world!"); got != 3 {
		t.Errorf("12 个英文字符 = %d，期望 3", got)
	}
	if got := EstimateRequest([]byte(`{"model":"very-long-model-name","messages":[{"role":"user","content":"你好"}]}`)); got != 2+1 {
		t.Errorf("请求估算 = %d，期望 3（role 的 user 算 1，你好算 2，model 不算）", got)
	}

	// 用真实样本检查输出估算的量级：和上游给的 usage 同一个数量级即可
	for _, c := range []struct {
		file  string
		proto Protocol
	}{
		{"deepseek_chat.sse", Chat}, {"dashscope_messages.sse", Messages}, {"deepseek_responses.sse", Responses},
	} {
		raw, _ := os.ReadFile(filepath.Join("testdata", c.file))
		tr := NewTracker(c.proto)
		r := NewSSEReader(bytes.NewReader(raw))
		for {
			ev, err := r.Next()
			if err != nil {
				break
			}
			tr.ObserveEvent(ev.Data)
		}
		real, est := tr.Result().Output, tr.EstimatedOutput()
		if est <= 0 || est > real*3 || est*3 < real {
			t.Errorf("%s: 估算输出 %d，真实 %d，差得太远", c.file, est, real)
		}
	}
}
