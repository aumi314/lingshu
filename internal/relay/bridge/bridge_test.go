package bridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepareBody 跑一次转换，返回上游请求体。
func prepareBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	return prepareBodyWithModel(t, raw, "claude-opus-5-5")
}

// prepareBodyWithModel 指定上游型号跑一次转换，返回上游请求体。
func prepareBodyWithModel(t *testing.T, raw, model string) map[string]any {
	t.Helper()
	req, err := Prepare([]byte(raw), model)
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("上游请求体不是合法 JSON: %v", err)
	}
	return body
}

// errorCode 取转换错误的错误码。
func errorCode(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("错误 %v 不是 *bridge.Error", err)
	}
	return e.Code
}

func TestPrepareInstructionsAndMessages(t *testing.T) {
	body := prepareBody(t, `{"model":"gpt","instructions":"系统提示","input":[
		{"type":"message","role":"developer","content":[{"type":"input_text","text":"开发说明"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"你好"}]}]}`)
	system, _ := body["system"].([]any)
	if len(system) != 2 {
		t.Fatalf("system 块数 %d，期望 2: %v", len(system), body["system"])
	}
	if system[0].(map[string]any)["text"] != "系统提示" {
		t.Errorf("instructions 没进 system: %v", system[0])
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("消息数 %d，期望 1: %v", len(messages), messages)
	}
	content, _ := messages[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "你好" {
		t.Errorf("正文不对: %v", content)
	}
	if body["stream"] != true {
		t.Errorf("上游必须流式")
	}
	if body["max_tokens"].(float64) != 8192 {
		t.Errorf("max_tokens 兜底不对: %v", body["max_tokens"])
	}
}

func TestPrepareMergesAdjacentSameRole(t *testing.T) {
	body := prepareBody(t, `{"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"一"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"二"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"好"}]}]}`)
	messages, _ := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("同角色用户消息应合并，消息数 %d: %v", len(messages), messages)
	}
	content, _ := messages[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("合并后应有 2 个内容块: %v", content)
	}
}

func TestPrepareImagesAndFiles(t *testing.T) {
	body := prepareBody(t, `{"input":[{"type":"message","role":"user","content":[
		{"type":"input_text","text":"看图"},
		{"type":"input_image","image_url":"data:image/png;base64,AAA"},
		{"type":"input_image","image_url":"https://example.com/a.jpg"}]}]}`)
	content, _ := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("内容块数 %d: %v", len(content), content)
	}
	image := content[1].(map[string]any)
	source := image["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != "AAA" {
		t.Errorf("base64 图片转错: %v", image)
	}
	remote := content[2].(map[string]any)["source"].(map[string]any)
	if remote["type"] != "url" {
		t.Errorf("http 图片应转成 url source: %v", remote)
	}
}

func TestPrepareToolDeclarations(t *testing.T) {
	body := prepareBody(t, `{"tools":[
		{"type":"function","name":"exec_command","description":"跑命令","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
		{"type":"custom","name":"apply_patch","description":"打补丁"},
		{"type":"namespace","name":"clock","tools":[{"type":"function","name":"now","description":"现在几点","parameters":{"type":"object","properties":{}}}]}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	tools, _ := body["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("上游工具数 %d，期望 3: %v", len(tools), tools)
	}
	first := tools[0].(map[string]any)
	if first["name"] != "exec_command" {
		t.Errorf("function 工具名不对: %v", first)
	}
	schema, _ := first["input_schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("input_schema 不对: %v", first)
	}
	second := tools[1].(map[string]any)
	if second["name"] != "apply_patch" {
		t.Errorf("custom 工具名不对: %v", second)
	}
	customSchema, _ := second["input_schema"].(map[string]any)
	props, _ := customSchema["properties"].(map[string]any)
	if _, ok := props["input"]; !ok {
		t.Errorf("自由文本工具应映射到 input 字段: %v", customSchema)
	}
	third := tools[2].(map[string]any)
	if third["name"] != "clock_now" {
		t.Errorf("namespace 工具应摊平成带前缀的名字: %v", third)
	}
}

func TestPrepareAdditionalTools(t *testing.T) {
	body := prepareBody(t, `{"input":[
		{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","description":"跑代码"}]}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("additional_tools 里的工具没收进来: %v", body["tools"])
	}
	if tools[0].(map[string]any)["name"] != "functions_exec" {
		t.Errorf("命名空间工具名不对: %v", tools[0])
	}
}

func TestPrepareToolHistoryPairing(t *testing.T) {
	body := prepareBody(t, `{"tools":[{"type":"function","name":"exec","parameters":{"type":"object","properties":{}}}],
		"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"跑一下"}]},
		{"type":"function_call","call_id":"call_abc123","name":"exec","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_abc123","output":"a.txt"}]}`)
	messages, _ := body["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("消息数 %d，期望 3: %v", len(messages), messages)
	}
	assistant := messages[1].(map[string]any)["content"].([]any)
	use := assistant[0].(map[string]any)
	if use["type"] != "tool_use" || use["name"] != "exec" {
		t.Fatalf("工具调用块不对: %v", use)
	}
	input := use["input"].(map[string]any)
	if input["cmd"] != "ls" {
		t.Errorf("工具参数没还原: %v", input)
	}
	result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" {
		t.Fatalf("工具结果块不对: %v", result)
	}
	if result["tool_use_id"] != use["id"] {
		t.Errorf("工具结果和调用没对上: %v vs %v", result["tool_use_id"], use["id"])
	}
}

func TestPrepareToolResultImages(t *testing.T) {
	body := prepareBody(t, `{"tools":[{"type":"function","name":"view","parameters":{"type":"object","properties":{}}}],
		"input":[
		{"type":"function_call","call_id":"call_x1","name":"view","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_x1","output":[{"type":"input_image","image_url":"data:image/png;base64,BBB"}]}]}`)
	result := body["messages"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	blocks, _ := result["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("工具结果内容块数 %d: %v", len(blocks), blocks)
	}
	if blocks[0].(map[string]any)["type"] != "image" {
		t.Errorf("工具结果里的图片没转成 image 块: %v", blocks[0])
	}
}

func TestPrepareRejectsUnsupportedFields(t *testing.T) {
	for _, field := range []string{"conversation", "prompt", "context_management"} {
		_, err := Prepare([]byte(`{"`+field+`":{"id":"x"},"input":"hi"}`), "claude-opus-5-5")
		if err == nil {
			t.Fatalf("%s 应该报错", field)
		}
		if code := errorCode(t, err); code != CodeUnsupportedField {
			t.Errorf("%s 错误码 %s，期望 %s", field, code, CodeUnsupportedField)
		}
	}
}

func TestPrepareRejectsUnsupportedTools(t *testing.T) {
	_, err := Prepare([]byte(`{"tools":[{"type":"file_search"}],"input":"hi"}`), "claude-opus-5-5")
	if err == nil {
		t.Fatal("file_search 应该报错")
	}
	if code := errorCode(t, err); code != CodeUnsupportedTool {
		t.Errorf("错误码 %s，期望 %s", code, CodeUnsupportedTool)
	}
}

func TestPrepareReasoningReplay(t *testing.T) {
	seal := sealCapsule(capsule{Model: "claude-opus-5-5", Blocks: []map[string]any{
		{"type": "thinking", "thinking": "想了一下", "signature": "sig"},
	}})
	body := prepareBody(t, `{"input":[
		{"type":"reasoning","id":"rs_1","encrypted_content":"`+seal+`"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"好"}]}]}`)
	messages, _ := body["messages"].([]any)
	content, _ := messages[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("思考块应排在助手消息最前面: %v", content)
	}
	thinking := content[0].(map[string]any)
	if thinking["type"] != "thinking" || thinking["thinking"] != "想了一下" || thinking["signature"] != "sig" {
		t.Errorf("思考块没还原: %v", thinking)
	}
}

func TestPrepareReasoningEffort(t *testing.T) {
	body := prepareBody(t, `{"reasoning":{"effort":"medium"},"input":"hi"}`)
	if body["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Errorf("medium 应开 adaptive thinking: %v", body["thinking"])
	}
	if body["output_config"].(map[string]any)["effort"] != "medium" {
		t.Errorf("effort 没传过去: %v", body["output_config"])
	}
	// Opus 4.6 官方接受 thinking.disabled，none 就该真的关掉思考。
	body = prepareBodyWithModel(t, `{"reasoning":{"effort":"none"},"input":"hi"}`, "claude-opus-4-6")
	if body["thinking"].(map[string]any)["type"] != "disabled" {
		t.Errorf("none 应关思考: %v", body["thinking"])
	}
	if _, ok := body["output_config"]; ok {
		t.Errorf("none 不该带 output_config: %v", body["output_config"])
	}
	body = prepareBody(t, `{"reasoning":{"summary":"auto"},"input":"hi"}`)
	if _, ok := body["thinking"]; ok {
		t.Errorf("只要摘要不该开思考: %v", body["thinking"])
	}
	_, err := Prepare([]byte(`{"reasoning":{"effort":"turbo"},"input":"hi"}`), "claude-opus-5-5")
	if err == nil || errorCode(t, err) != CodeUnsupportedEffort {
		t.Fatalf("未知强度应该报错，得到 %v", err)
	}
}

// TestPrepareEffortStrictMapping 覆盖 effort 映射的严格边界：
// 官方不支持的档位和不认识的型号都明确报错，不做静默降级。
func TestPrepareEffortStrictMapping(t *testing.T) {
	// Opus 5.5 的思考关不掉（官方文档：始终开启），none 必须报错。
	_, err := Prepare([]byte(`{"reasoning":{"effort":"none"},"input":"hi"}`), "claude-opus-5-5")
	if err == nil {
		t.Fatal("Opus 5.5 的 effort=none 应该报错")
	}
	if code := errorCode(t, err); code != CodeUnsupportedEffort {
		t.Errorf("错误码 %s，期望 %s", code, CodeUnsupportedEffort)
	}

	// Opus 4.6 有 max 没有 xhigh（官方 effort 档位表分开列的）。
	_, err = Prepare([]byte(`{"reasoning":{"effort":"xhigh"},"input":"hi"}`), "claude-opus-4-6")
	if err == nil {
		t.Fatal("Opus 4.6 的 effort=xhigh 应该报错")
	}
	if code := errorCode(t, err); code != CodeUnsupportedEffort {
		t.Errorf("错误码 %s，期望 %s", code, CodeUnsupportedEffort)
	}

	// Sonnet 5.5 官方只认 between_tools，不能用 disabled。
	body := prepareBodyWithModel(t, `{"reasoning":{"effort":"none"},"input":"hi"}`, "claude-sonnet-5-5")
	if body["thinking"].(map[string]any)["type"] != "between_tools" {
		t.Errorf("Sonnet 5.5 的 none 应转成 between_tools: %v", body["thinking"])
	}

	// 型号不认识时，除 none 外一律报错。
	_, err = Prepare([]byte(`{"reasoning":{"effort":"high"},"input":"hi"}`), "some-other-model")
	if err == nil {
		t.Fatal("不认识型号的非 none 强度应该报错")
	}
	if code := errorCode(t, err); code != CodeUnsupportedEffort {
		t.Errorf("错误码 %s，期望 %s", code, CodeUnsupportedEffort)
	}
	// 不认识型号时 none 仍然放行。
	body = prepareBodyWithModel(t, `{"reasoning":{"effort":"none"},"input":"hi"}`, "some-other-model")
	if body["thinking"].(map[string]any)["type"] != "disabled" {
		t.Errorf("不认识型号的 none 应关思考: %v", body["thinking"])
	}
}

func TestPrepareCompactionTrigger(t *testing.T) {
	req, err := Prepare([]byte(`{"tools":[{"type":"function","name":"exec","parameters":{"type":"object"}}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"长对话"}]},
		{"type":"compaction_trigger"}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	if !req.Compact {
		t.Fatal("compaction_trigger 应该标出 Compact")
	}
	var body map[string]any
	json.Unmarshal(req.Body, &body)
	if _, ok := body["tools"]; ok {
		t.Errorf("压缩请求不该带工具: %v", body["tools"])
	}
	if _, ok := body["tool_choice"]; ok {
		t.Errorf("压缩请求不该带 tool_choice")
	}
	messages, _ := body["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	content, _ := last["content"].([]any)
	text := content[len(content)-1].(map[string]any)["text"].(string)
	if !strings.Contains(text, "handoff summary") {
		t.Errorf("压缩请求应追加英文交接提示: %q", text)
	}
}

func TestPrepareCompactionSummaryRoundTrip(t *testing.T) {
	seal := SealCompaction("之前的摘要")
	body := prepareBody(t, `{"input":[
		{"type":"compaction","id":"cmp_1","encrypted_content":"`+seal+`"},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"继续"}]}]}`)
	messages, _ := body["messages"].([]any)
	content, _ := messages[0].(map[string]any)["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "之前的摘要") {
		t.Errorf("压缩摘要没展开: %q", text)
	}
	_, err := Prepare([]byte(`{"input":[{"type":"compaction","id":"cmp_1","encrypted_content":"foreign"}]}`), "claude-opus-5-5")
	if err == nil || errorCode(t, err) != CodeForeignCompaction {
		t.Fatalf("外来压缩内容应该报错，得到 %v", err)
	}
}

func TestPrepareRejectsMissingHistory(t *testing.T) {
	// store=false 也不能证明历史完整，带 previous_response_id 一律报错。
	for _, raw := range []string{
		`{"previous_response_id":"resp_1","input":"hi"}`,
		`{"previous_response_id":"resp_1","store":false,"input":"hi"}`,
	} {
		_, err := Prepare([]byte(raw), "claude-opus-5-5")
		if err == nil || errorCode(t, err) != CodePreviousNotFound {
			t.Fatalf("%s 应该报 previous_response_not_found，得到 %v", raw, err)
		}
	}
}

func TestPrepareCapsuleRoundTrip(t *testing.T) {
	seal := sealCapsule(capsule{Model: "m", Blocks: []map[string]any{
		{"type": "redacted_thinking", "data": "opaque"},
	}})
	blocks, err := decodeCapsule(seal, "m")
	if err != nil {
		t.Fatalf("解封失败: %v", err)
	}
	if len(blocks) != 1 || blocks[0]["type"] != "redacted_thinking" || blocks[0]["data"] != "opaque" {
		t.Errorf("redacted 思考块没还原: %v", blocks)
	}
	if _, err := decodeCapsule("someone-else", "m"); err == nil {
		t.Error("外来封装应该报错")
	}
	summary, err := ExpandCompaction(SealCompaction("摘要\n第二行"))
	if err != nil || summary != "摘要\n第二行" {
		t.Errorf("压缩封装往返不对: %q %v", summary, err)
	}
}

func TestPrepareRejectsMissingToolResult(t *testing.T) {
	// 调用和结果必须成套，缺结果不能编一个空结果糊弄上游。
	_, err := Prepare([]byte(`{"tools":[{"type":"function","name":"exec","parameters":{"type":"object","properties":{}}}],
		"input":[
		{"type":"function_call","call_id":"call_x","name":"exec","arguments":"{}"}]}`), "claude-opus-5-5")
	if err == nil || errorCode(t, err) != CodeMissingToolOutput {
		t.Fatalf("缺结果应该报错，得到 %v", err)
	}
}

func TestPrepareRejectsUnknownContent(t *testing.T) {
	cases := []struct {
		raw  string
		code string
	}{
		{`{"input":[{"type":"message","role":"user","content":[{"type":"input_audio","data":"x"}]}]}`, CodeUnsupportedContent},
		{`{"input":[{"type":"message","role":"system","content":[{"type":"input_image","image_url":"data:image/png;base64,AA"}]}]}`, CodeUnsupportedContent},
		{`{"input":[{"type":"local_shell_call","call_id":"call_1","action":{"command":["ls"]}}]}`, CodeUnsupportedInput},
		{`{"input":[{"type":"item_reference","id":"msg_1"}]}`, CodeUnsupportedInput},
	}
	for _, c := range cases {
		_, err := Prepare([]byte(c.raw), "claude-opus-5-5")
		if err == nil {
			t.Fatalf("%s 应该报错", c.raw)
		}
		if code := errorCode(t, err); code != c.code {
			t.Errorf("%s 错误码 %s，期望 %s", c.raw, code, c.code)
		}
	}
}

func TestPrepareMessageWithoutType(t *testing.T) {
	body := prepareBody(t, `{"input":[{"role":"user","content":"直接给字符串"}]}`)
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("没有 type 的消息应按消息处理: %v", messages)
	}
	content, _ := messages[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "直接给字符串" {
		t.Errorf("正文不对: %v", content)
	}
}

func TestPrepareToolRenameDiagnostic(t *testing.T) {
	req, err := Prepare([]byte(`{"tools":[{"type":"namespace","name":"clock","tools":[{"type":"function","name":"now","parameters":{"type":"object","properties":{}}}]}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`), "claude-opus-5-5")
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	found := false
	for _, d := range req.Diagnostics {
		if d.Code == "tool_renamed" {
			found = true
		}
	}
	if !found {
		t.Errorf("命名空间改名应该有诊断: %v", req.Diagnostics)
	}
}

func TestPrepareRejectsEmptyInput(t *testing.T) {
	_, err := Prepare([]byte(`{"input":[]}`), "claude-opus-5-5")
	if err == nil || errorCode(t, err) != CodeInvalidInput {
		t.Fatalf("空对话应该报错，得到 %v", err)
	}
}

// TestPrepareRealCodexRequests 用仓库里的 Codex 0.160.0 抓包样本，保证转换跟真实请求对得上。
func TestPrepareRealCodexRequests(t *testing.T) {
	dir := filepath.Join("..", "testdata", "codex-0.160.0")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("缺少必需的 Codex 回归样本: %v", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("读样本失败: %v", err)
		}
		var captures []struct {
			Body map[string]any `json:"body"`
		}
		if err := json.Unmarshal(raw, &captures); err != nil {
			t.Fatalf("样本 %s 解析失败: %v", entry.Name(), err)
		}
		for i, capture := range captures {
			body := capture.Body
			// generate=false 是预热，不发上游。
			if generate, ok := body["generate"].(bool); ok && !generate {
				continue
			}
			// 远端压缩样本里的摘要槽位是占位符，换成本层真封的再测。
			if items, ok := body["input"].([]any); ok {
				for _, item := range items {
					compaction, _ := item.(map[string]any)
					if compaction["type"] == "compaction" {
						compaction["encrypted_content"] = SealCompaction("样本摘要")
					}
				}
			}
			// previous_response_id 由调用方先展开再删掉，这里先确认带着它会明确报错。
			if prev, ok := body["previous_response_id"].(string); ok && prev != "" {
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatalf("样本编码失败: %v", err)
				}
				if _, err := Prepare(encoded, "claude-opus-5-5"); err == nil {
					t.Fatalf("样本 %s 第 %d 个请求带 previous_response_id 应该报错", entry.Name(), i)
				} else if code := errorCode(t, err); code != CodePreviousNotFound {
					t.Fatalf("样本 %s 第 %d 个请求错误码 %s", entry.Name(), i, code)
				}
				delete(body, "previous_response_id")
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("样本编码失败: %v", err)
			}
			req, err := Prepare(encoded, "claude-opus-5-5")
			if err != nil {
				t.Fatalf("样本 %s 第 %d 个请求转换失败: %v", entry.Name(), i, err)
			}
			var upstream map[string]any
			if err := json.Unmarshal(req.Body, &upstream); err != nil {
				t.Fatalf("上游请求体不合法: %v", err)
			}
			if upstream["stream"] != true {
				t.Errorf("样本 %s 第 %d 个请求没走流式", entry.Name(), i)
			}
			messages, _ := upstream["messages"].([]any)
			if len(messages) == 0 {
				t.Errorf("样本 %s 第 %d 个请求没有消息", entry.Name(), i)
			}
		}
	}
}

func TestHistoryToolsRejectDuplicateAndPreserveResultOrder(t *testing.T) {
	base := `{"input":[{"type":"function_call","name":"one","call_id":"call_a","arguments":"{}"},{"type":"function_call","name":"two","call_id":"call_b","arguments":"{}"},{"type":"function_call_output","call_id":"call_a","output":"first"},{"type":"function_call_output","call_id":"call_b","output":"second"}]}`
	q, err := Prepare([]byte(base), "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	json.Unmarshal(q.Body, &v)
	blocks := v.Messages[1].Content
	if blocks[0]["tool_use_id"] != "call_a" || blocks[1]["tool_use_id"] != "call_b" {
		t.Fatalf("order=%v", blocks)
	}
	for _, raw := range []string{
		`{"input":[{"type":"function_call","name":"a","call_id":"x","arguments":"{}"},{"type":"function_call","name":"a","call_id":"x","arguments":"{}"}]}`,
		`{"input":[{"type":"function_call","name":"a","call_id":"x","arguments":"{}"},{"type":"function_call_output","call_id":"x","output":"first"},{"type":"function_call_output","call_id":"x","output":"duplicate"}]}`,
		`{"tools":[{"type":"custom","name":"a"}],"input":[{"type":"function_call","name":"a","call_id":"x","arguments":"{}"},{"type":"function_call_output","call_id":"x","output":"wrong type"}]}`,
	} {
		if _, err := Prepare([]byte(raw), "claude-opus-5-5"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
