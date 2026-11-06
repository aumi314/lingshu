package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// openCodeBody 记录旧响应体有没有被关掉。
type openCodeBody struct {
	io.Reader
	closed bool
}

func (b *openCodeBody) Close() error {
	b.closed = true
	return nil
}

func openCodeStream(events ...string) *openCodeBody {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: ")
		b.WriteString(e)
		b.WriteString("\n\n")
	}
	return &openCodeBody{Reader: strings.NewReader(b.String())}
}

func openCodeResponse(body *openCodeBody) *http.Response {
	return &http.Response{
		StatusCode:    200,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:          body,
		ContentLength: -1,
	}
}

type openCodeCompletionBody struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	Model       string `json:"model"`
	ServiceTier string `json:"service_tier"`
	Choices     []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role             string  `json:"role"`
			Content          *string `json:"content"`
			ReasoningContent string  `json:"reasoning_content"`
			Reasoning        string  `json:"reasoning"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

func readOpenCodeCompletion(t *testing.T, resp *http.Response) openCodeCompletionBody {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取合成结果失败: %v", err)
	}
	if resp.ContentLength != int64(len(raw)) {
		t.Fatalf("content length = %d, want %d", resp.ContentLength, len(raw))
	}
	var out openCodeCompletionBody
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析合成结果失败: %v (%s)", err, raw)
	}
	return out
}

func TestCollectOpenCodeChatJoinsFragmentedToolCalls(t *testing.T) {
	body := openCodeStream(
		`{"id":"chatcmpl-7","created":1793953014,"model":"deepseek-v4.1-flash-free","service_tier":"default","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"id":"chatcmpl-7","choices":[{"index":0,"delta":{"content":"先读文件"}}]}`,
		`{"id":"chatcmpl-7","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":"{\"pa"}}]}}]}`,
		`{"id":"chatcmpl-7","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"grep","arguments":"{\"q"}}]}}]}`,
		`{"id":"chatcmpl-7","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a\"}"}},{"index":1,"function":{"arguments":"uery\":\"b\"}"}}]}}]}`,
		`{"id":"chatcmpl-7","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"id":"chatcmpl-7","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`,
		`[DONE]`,
	)
	resp := openCodeResponse(body)
	if err := collectOpenCodeChat(resp, &openCodeRequest{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !body.closed {
		t.Fatal("旧的响应体没有关闭")
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	out := readOpenCodeCompletion(t, resp)
	if out.ID != "chatcmpl-7" || out.Object != "chat.completion" || out.Created != 1793953014 || out.Model != "deepseek-v4.1-flash-free" || out.ServiceTier != "default" {
		t.Fatalf("合成结果的元信息不对: %+v", out)
	}
	if len(out.Choices) != 1 {
		t.Fatalf("choices = %d", len(out.Choices))
	}
	choice := out.Choices[0]
	if choice.FinishReason != "tool_calls" || choice.Message.Role != "assistant" {
		t.Fatalf("choice = %+v", choice)
	}
	if choice.Message.Content == nil || *choice.Message.Content != "先读文件" {
		t.Fatalf("content = %+v", choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", choice.Message.ToolCalls)
	}
	first := choice.Message.ToolCalls[0]
	if first.ID != "call_a" || first.Type != "function" || first.Function.Name != "read" || first.Function.Arguments != `{"path":"a"}` {
		t.Fatalf("第一个工具调用 = %+v", first)
	}
	second := choice.Message.ToolCalls[1]
	if second.ID != "call_b" || second.Function.Name != "grep" || second.Function.Arguments != `{"query":"b"}` {
		t.Fatalf("第二个工具调用 = %+v", second)
	}
	if string(out.Usage) != `{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}` {
		t.Fatalf("usage = %s", out.Usage)
	}
}

func TestCollectOpenCodeChatKeepsUsageOnlyTail(t *testing.T) {
	body := openCodeStream(
		`{"id":"chatcmpl-8","model":"gpt-5.1-codex","choices":[{"index":0,"delta":{"role":"assistant","content":null}}]}`,
		`{"id":"chatcmpl-8","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"chatcmpl-8","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":1}}}`,
		`[DONE]`,
	)
	resp := openCodeResponse(body)
	if err := collectOpenCodeChat(resp, &openCodeRequest{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := readOpenCodeCompletion(t, resp)
	if len(out.Choices) != 1 || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("choices = %+v", out.Choices)
	}
	if out.Choices[0].Message.Content == nil || *out.Choices[0].Message.Content != "" {
		t.Fatalf("content = %+v", out.Choices[0].Message.Content)
	}
	if !strings.Contains(string(out.Usage), `"cached_tokens":1`) || !strings.Contains(string(out.Usage), `"reasoning_tokens":1`) {
		t.Fatalf("usage 尾块没有原样保留: %s", out.Usage)
	}
}

func TestCollectOpenCodeChatRejectsTruncatedStream(t *testing.T) {
	cases := []struct {
		name   string
		events []string
	}{
		{"没有结束标记", []string{`{"choices":[{"index":0,"delta":{"content":"半句"}}]}`}},
		{"没有结束原因", []string{`{"choices":[{"index":0,"delta":{"content":"半句"}}]}`, `[DONE]`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := openCodeStream(tc.events...)
			resp := openCodeResponse(body)
			if err := collectOpenCodeChat(resp, &openCodeRequest{}); err == nil {
				t.Fatal("不完整的流被当成成功")
			}
			if !body.closed {
				t.Fatal("旧的响应体没有关闭")
			}
			if resp.Header.Get("Content-Type") != "text/event-stream" || resp.ContentLength != -1 {
				t.Fatal("失败时不该动响应体")
			}
		})
	}
}

func TestCollectOpenCodeChatRejectsErrorEvent(t *testing.T) {
	body := openCodeStream(`{"error":{"message":"free tier quota exhausted","type":"rate_limit_error"}}`)
	resp := openCodeResponse(body)
	err := collectOpenCodeChat(resp, &openCodeRequest{})
	if err == nil {
		t.Fatal("上游报错被当成成功")
	}
	if strings.Contains(err.Error(), "quota") {
		t.Fatalf("错误信息带出了上游原文: %v", err)
	}
	if !body.closed {
		t.Fatal("旧的响应体没有关闭")
	}
}

func TestCollectOpenCodeChatRejectsStubToolCall(t *testing.T) {
	body := openCodeStream(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"bash","arguments":"{}"}}]}}]}`,
		`[DONE]`,
	)
	resp := openCodeResponse(body)
	err := collectOpenCodeChat(resp, &openCodeRequest{stubs: map[string]bool{"bash": true}})
	if err == nil {
		t.Fatal("调用占位工具的响应被当成成功")
	}
	if !strings.Contains(err.Error(), "bash") {
		t.Fatalf("错误信息没有说是哪个工具: %v", err)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("失败时不该动响应体")
	}
}

func TestCollectOpenCodeChatRejectsNamelessToolCall(t *testing.T) {
	body := openCodeStream(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	)
	resp := openCodeResponse(body)
	if err := collectOpenCodeChat(resp, &openCodeRequest{}); err == nil {
		t.Fatal("不完整的工具调用被静默丢弃")
	}
}
