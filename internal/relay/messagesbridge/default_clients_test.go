package messagesbridge

import (
	"encoding/json"
	"os"
	"testing"
)

// 默认配置的 Claude Code 2.1.322 请求（testdata，去掉了系统提示原文和标识）：带 context_management、
// 对话中途的 system 消息和会话编号，必须能转。客户端升级后按 docs/research/responses-messages.md 重新抓一份替换。
func TestPrepareClaudeCodeDefaultRequest(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-code-2.1.322-default.json")
	if err != nil {
		t.Fatal(err)
	}
	req, err := Prepare(raw, "gpt-6.1-sol")
	if err != nil {
		t.Fatalf("默认配置的 Claude Code 请求应能转成 Responses: %v", err)
	}
	var body struct {
		Input []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"input"`
		PromptCacheKey string `json:"prompt_cache_key"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Input) != 2 || body.Input[0].Role != "user" || body.Input[1].Role != "developer" {
		t.Fatalf("中途的 system 消息应在原位置转成 developer 消息: %s", req.Body)
	}
	if body.Input[1].Content[0]["text"] != "# Environment\nPrimary working directory: /work" {
		t.Errorf("developer 消息内容不对: %v", body.Input[1].Content)
	}
	if body.PromptCacheKey != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("prompt_cache_key 应取 metadata.user_id 里的 session_id，实际 %q", body.PromptCacheKey)
	}
	found := false
	for _, d := range req.Diagnostics {
		if d.Code == "field_ignored" && d.Field == "context_management" {
			found = true
		}
	}
	if !found {
		t.Errorf("忽略 context_management 要记诊断: %+v", req.Diagnostics)
	}
}

func TestPrepareNoSessionNoCacheKey(t *testing.T) {
	req, err := Prepare([]byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":"plain-user"}}`), "gpt-6.1-sol")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(req.Body, &body)
	if _, ok := body["prompt_cache_key"]; ok {
		t.Errorf("user_id 里没有会话编号时不写 prompt_cache_key: %s", req.Body)
	}
}
