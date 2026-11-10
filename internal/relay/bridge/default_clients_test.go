package bridge

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 默认配置的 Codex 0.160 请求（testdata，去掉了系统提示原文和标识）：带 web_search、namespace 工具和 developer 消息，必须能转。
// 客户端升级后按 docs/research/responses-messages.md 重新抓一份替换。
func TestPrepareCodexDefaultRequest(t *testing.T) {
	raw, err := os.ReadFile("testdata/codex-0.160-default.json")
	if err != nil {
		t.Fatal(err)
	}
	req, err := Prepare(raw, "claude-opus-5-5")
	if err != nil {
		t.Fatalf("默认配置的 Codex 请求应能转成 Messages: %v", err)
	}
	var body struct {
		Tools []map[string]any `json:"tools"`
	}
	json.Unmarshal(req.Body, &body)
	for _, tool := range body.Tools {
		if strings.Contains(tool["name"].(string), "web_search") {
			t.Errorf("web_search 不应发给上游: %v", tool)
		}
	}
	if len(body.Tools) != 4 {
		t.Errorf("应保留 2 个函数和 namespace 里的 2 个工具，实际 %d", len(body.Tools))
	}
	if !hasDiag(req.Diagnostics, "tool_dropped") {
		t.Errorf("去掉联网搜索要记诊断: %+v", req.Diagnostics)
	}
}

func TestPrepareDropsWebSearchHistory(t *testing.T) {
	req, err := Prepare([]byte(`{"input":[{"type":"message","role":"user","content":"查天气"},
		{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"weather"}},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"晴"}]},
		{"type":"message","role":"user","content":"谢谢"}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("历史里的联网搜索记录应去掉而不是报错: %v", err)
	}
	if !hasDiag(req.Diagnostics, "history_dropped") {
		t.Errorf("去掉搜索记录要记诊断: %+v", req.Diagnostics)
	}
}

// 提示缓存断点：system 末尾、tools 末尾、最后一条消息末尾、倒数第二条 user 消息末尾，共 4 处；思考块不打。
func TestPrepareCacheBreakpoints(t *testing.T) {
	seal := sealCapsule(capsule{Model: "claude-opus-5-5", Blocks: []map[string]any{{"type": "thinking", "thinking": "想", "signature": "sig"}}})
	req, err := Prepare([]byte(`{"instructions":"sys","tools":[{"type":"function","name":"a"},{"type":"function","name":"b"}],
		"input":[{"type":"message","role":"user","content":"第一问"},
		{"type":"reasoning","encrypted_content":`+string(mustJSON(seal))+`,"summary":[]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第一答"}]},
		{"type":"message","role":"user","content":"第二问"}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		System   []map[string]any `json:"system"`
		Tools    []map[string]any `json:"tools"`
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatal(err)
	}
	marked := func(b map[string]any) bool { return b["cache_control"] != nil }
	count := 0
	for _, b := range body.System {
		if marked(b) {
			count++
		}
	}
	for _, b := range body.Tools {
		if marked(b) {
			count++
		}
	}
	for _, m := range body.Messages {
		for _, b := range m.Content {
			if marked(b) {
				count++
				if b["type"] == "thinking" {
					t.Error("思考块不能打缓存断点")
				}
			}
		}
	}
	if count != 4 || !marked(body.System[len(body.System)-1]) || !marked(body.Tools[1]) || marked(body.Tools[0]) {
		t.Errorf("应有 4 处断点（system 末尾、tools 末尾、最后一条消息、倒数第二条 user），实际 %d: %s", count, req.Body)
	}
	last := body.Messages[len(body.Messages)-1]
	first := body.Messages[0]
	if last.Role != "user" || !marked(last.Content[len(last.Content)-1]) || !marked(first.Content[len(first.Content)-1]) {
		t.Errorf("最后一条消息和倒数第二条 user 消息末尾都应打断点: %s", req.Body)
	}
}

func hasDiag(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
