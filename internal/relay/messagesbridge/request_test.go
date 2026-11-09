package messagesbridge

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("上游请求体不是合法 JSON: %v", err)
	}
	return m
}

func decodeItems(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("上游请求体里没有 input 数组")
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("input 条目不是对象: %v", item)
		}
		items = append(items, obj)
	}
	return items
}

func wantErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望出错，实际通过")
	}
	var convErr *Error
	if !errors.As(err, &convErr) {
		t.Fatalf("错误不是转换错误: %v", err)
	}
	if convErr.Code != code {
		t.Fatalf("错误码不对：期望 %s，实际 %s（%s）", code, convErr.Code, convErr.Message)
	}
}

func hasDiagnostic(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestPrepareTextRequest(t *testing.T) {
	raw := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 1024,
		"system": "你是一个助手",
		"messages": [{"role": "user", "content": "你好"}]
	}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Fatalf("Request.Model 应保留客户端模型名，实际 %s", req.Model)
	}
	body := decodeBody(t, req.Body)
	if body["model"] != "gpt-5.1" {
		t.Fatalf("上游模型名不对: %v", body["model"])
	}
	if body["instructions"] != "你是一个助手" {
		t.Fatalf("system 没转成 instructions: %v", body["instructions"])
	}
	if body["stream"] != true || body["store"] != false {
		t.Fatalf("上游固定 stream:true、store:false，实际 stream=%v store=%v", body["stream"], body["store"])
	}
	include, _ := body["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include 不对: %v", body["include"])
	}
	if body["max_output_tokens"] != float64(1024) {
		t.Fatalf("max_tokens 没转成 max_output_tokens: %v", body["max_output_tokens"])
	}
	items := decodeItems(t, body)
	if len(items) != 1 || items[0]["type"] != "message" || items[0]["role"] != "user" || items[0]["content"].([]any)[0].(map[string]any)["text"] != "你好" {
		t.Fatalf("用户文本没转成 input_text: %v", items)
	}
}

func TestPrepareKeepsSystemTextExact(t *testing.T) {
	raw := []byte(`{
		"model": "m",
		"max_tokens": 100,
		"system": "  第一行\n\n第二行  ",
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	if body["instructions"] != "  第一行\n\n第二行  " {
		t.Fatalf("system 原文被改了: %q", body["instructions"])
	}
}

func TestPrepareSystemBlocks(t *testing.T) {
	raw := []byte(`{
		"model": "m",
		"max_tokens": 100,
		"system": [{"type": "text", "text": "第一段"}, {"type": "text", "text": "第二段", "cache_control": {"type": "ephemeral"}}],
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	if body["instructions"] != "第一段\n\n第二段" {
		t.Fatalf("system 文本块没合成 instructions: %v", body["instructions"])
	}
	if !hasDiagnostic(req.Diagnostics, "cache_not_guaranteed") {
		t.Fatalf("缓存提示应该记一条诊断，实际 %v", req.Diagnostics)
	}
}

func TestPrepareToolRoundTrip(t *testing.T) {
	raw := []byte(`{
		"model": "m",
		"max_tokens": 100,
		"tools": [{"name": "get_weather", "description": "查天气", "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}],
		"messages": [
			{"role": "user", "content": "北京天气"},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_01", "name": "get_weather", "input": {"city": "北京"}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_01", "content": "晴 25 度"}]}
		]
	}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("工具数量不对: %v", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "get_weather" {
		t.Fatalf("工具声明没转对: %v", tool)
	}
	if tool["strict"] != false {
		t.Fatalf("strict 应该是 false: %v", tool["strict"])
	}
	params := tool["parameters"].(map[string]any)
	if params["type"] != "object" || params["required"] == nil {
		t.Fatalf("input_schema 没转成 parameters: %v", params)
	}

	items := decodeItems(t, body)
	if len(items) != 3 {
		t.Fatalf("input 条目数量不对: %v", items)
	}
	call := items[1]
	if call["type"] != "function_call" || call["call_id"] != "toolu_01" || call["name"] != "get_weather" {
		t.Fatalf("工具调用没转对: %v", call)
	}
	if call["arguments"] != `{"city":"北京"}` {
		t.Fatalf("工具参数不对: %v", call["arguments"])
	}
	result := items[2]
	if result["type"] != "function_call_output" || result["call_id"] != "toolu_01" {
		t.Fatalf("工具结果没转对: %v", result)
	}
	if result["output"] != "晴 25 度" {
		t.Fatalf("工具结果内容不对: %v", result["output"])
	}
	if req.ToolNames["get_weather"] != "get_weather" {
		t.Fatalf("ToolNames 不对: %v", req.ToolNames)
	}
}

func TestPrepareToolChoiceMapping(t *testing.T) {
	cases := []struct {
		choice string
		want   any
	}{
		{`{"type":"auto"}`, "auto"},
		{`{"type":"any"}`, "required"},
		{`{"type":"none"}`, "none"},
		{`{"type":"tool","name":"f"}`, map[string]any{"type": "function", "name": "f"}},
	}
	for _, c := range cases {
		raw := []byte(`{"model":"m","max_tokens":100,"tools":[{"name":"f","input_schema":{"type":"object"}}],"tool_choice":` + c.choice +
			`,"messages":[{"role":"user","content":"hi"}]}`)
		req, err := Prepare(raw, "gpt-5.1")
		if err != nil {
			t.Fatalf("tool_choice %s 转换失败: %v", c.choice, err)
		}
		body := decodeBody(t, req.Body)
		got := body["tool_choice"]
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(c.want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("tool_choice %s 转错: %s", c.choice, gotJSON)
		}
	}

	raw := []byte(`{"model":"m","max_tokens":100,"tools":[{"name":"f","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"auto","disable_parallel_tool_use":true},"messages":[{"role":"user","content":"hi"}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	if body["parallel_tool_calls"] != false {
		t.Fatalf("disable_parallel_tool_use 没转成 parallel_tool_calls=false: %v", body["parallel_tool_calls"])
	}
}

func TestPrepareRejectsBrokenToolCalls(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		code string
	}{
		{
			"调用了没声明的工具",
			`{"model":"m","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"nope","input":{}}]}]}`,
			CodeUnknownTool,
		},
		{
			"孤立工具结果",
			`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"}]}]}`,
			CodeOrphanToolOutput,
		},
		{
			"工具调用没有结果",
			`{"model":"m","max_tokens":100,"tools":[{"name":"f","input_schema":{"type":"object"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":{}}]}]}`,
			CodeMissingToolOutput,
		},
		{
			"工具参数不是对象",
			`{"model":"m","max_tokens":100,"tools":[{"name":"f","input_schema":{"type":"object"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":["a"]}]}]}`,
			CodeInvalidArguments,
		},
		{
			"工具调用 id 重复",
			`{"model":"m","max_tokens":100,"tools":[{"name":"f","input_schema":{"type":"object"}}],"messages":[
				{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"}]},
				{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"y"}]}]}`,
			CodeInvalidArguments,
		},
	}
	for _, c := range cases {
		req, err := Prepare([]byte(c.raw), "gpt-5.1")
		wantErrorCode(t, err, c.code)
		if req != nil {
			t.Fatalf("%s 应该失败却返回了请求", c.name)
		}
	}
}

func TestPrepareRejectsUnsupportedFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"stop_sequences", `{"model":"m","max_tokens":100,"stop_sequences":["x"],"messages":[{"role":"user","content":"hi"}]}`},
		{"top_k", `{"model":"m","max_tokens":100,"top_k":5,"messages":[{"role":"user","content":"hi"}]}`},
		{"未知字段", `{"model":"m","max_tokens":100,"container":{"id":"c"},"messages":[{"role":"user","content":"hi"}]}`},
		{"缺 max_tokens", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`},
		{"缺 messages", `{"model":"m","max_tokens":100}`},
	}
	for _, c := range cases {
		if _, err := Prepare([]byte(c.raw), "gpt-5.1"); err == nil {
			t.Fatalf("%s 应该报错却通过了", c.name)
		}
	}
}

func TestPrepareImagesAndDocuments(t *testing.T) {
	raw := []byte(`{
		"model": "m",
		"max_tokens": 100,
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "看图"},
			{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "aGk="}},
			{"type": "document", "title": "报告.pdf", "source": {"type": "base64", "media_type": "application/pdf", "data": "cGRm"}}
		]}]
	}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	items := decodeItems(t, decodeBody(t, req.Body))
	if len(items) != 1 || items[0]["role"] != "user" {
		t.Fatalf("invalid message: %v", items)
	}
	parts := items[0]["content"].([]any)
	items = nil
	for _, part := range parts {
		items = append(items, part.(map[string]any))
	}
	if len(items) != 3 {
		t.Fatalf("input 条目数量不对: %v", items)
	}
	if items[1]["type"] != "input_image" || items[1]["image_url"] != "data:image/png;base64,aGk=" {
		t.Fatalf("图片没转成 data URL: %v", items[1])
	}
	if items[2]["type"] != "input_file" || items[2]["filename"] != "报告.pdf" {
		t.Fatalf("文档没转成 input_file: %v", items[2])
	}
	if items[2]["file_data"] != "data:application/pdf;base64,cGRm" {
		t.Fatalf("文档内容不对: %v", items[2])
	}
}

func TestPrepareToolResultWithImage(t *testing.T) {
	raw := []byte(`{
		"model": "m",
		"max_tokens": 100,
		"tools": [{"name": "screenshot", "input_schema": {"type": "object"}}],
		"messages": [
			{"role": "assistant", "content": [{"type": "tool_use", "id": "t1", "name": "screenshot", "input": {}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t1", "content": [
				{"type": "text", "text": "截图如下"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "aGk="}}
			]}]}
		]
	}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	items := decodeItems(t, decodeBody(t, req.Body))
	result := items[1]
	parts, ok := result["output"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("带图片的工具结果应该转成条目数组: %v", result["output"])
	}
	if parts[0].(map[string]any)["type"] != "input_text" {
		t.Fatalf("文字块类型不对: %v", parts[0])
	}
	if parts[1].(map[string]any)["type"] != "input_image" {
		t.Fatalf("图片块类型不对: %v", parts[1])
	}
}

func TestPrepareThinkingMapping(t *testing.T) {
	cases := []struct {
		name  string
		extra string
		want  string
	}{
		{"关思考", `"thinking":{"type":"disabled"}`, "none"},
		{"小预算", `"thinking":{"type":"enabled","budget_tokens":2000}`, "low"},
		{"中预算", `"thinking":{"type":"enabled","budget_tokens":8000}`, "medium"},
		{"大预算", `"thinking":{"type":"enabled","budget_tokens":32000}`, "high"},
		{"自适应带 effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"}`, "medium"},
		{"自适应不带 effort", `"thinking":{"type":"adaptive"}`, "high"},
		{"只给 effort", `"output_config":{"effort":"low"}`, "low"},
	}
	for _, c := range cases {
		raw := []byte(`{"model":"m","max_tokens":100,` + c.extra + `,"messages":[{"role":"user","content":"hi"}]}`)
		req, err := Prepare(raw, "gpt-5.1")
		if err != nil {
			t.Fatalf("%s 转换失败: %v", c.name, err)
		}
		body := decodeBody(t, req.Body)
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != c.want {
			t.Fatalf("%s 的 effort 不对：期望 %s，实际 %v", c.name, c.want, body["reasoning"])
		}
		if !hasDiagnostic(req.Diagnostics, "thinking_mapped") {
			t.Fatalf("%s 应该记一条思考换算诊断", c.name)
		}
	}
}

func TestPrepareKeepsSamplingWithReasoning(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"temperature":0.5,"top_p":0.9,
		"thinking":{"type":"enabled","budget_tokens":8000},"messages":[{"role":"user","content":"hi"}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	if body["temperature"] != 0.5 || body["top_p"] != 0.9 {
		t.Fatalf("采样参数不该被本层丢掉: %v", body)
	}
	if !hasDiagnostic(req.Diagnostics, "sampling_kept") {
		t.Fatalf("开思考时带着采样参数应该记一条诊断")
	}
}

func TestPrepareKeepsSamplingWithoutReasoning(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"temperature":0.5,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	if body["temperature"] != 0.5 {
		t.Fatalf("temperature 不该被丢掉: %v", body)
	}
}

func TestPrepareRejectsUnsupportedServerTool(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,
		"tools":[{"type":"web_search_20250305","name":"web_search"}],
		"messages":[{"role":"user","content":"hi"}]}`)
	_, err := Prepare(raw, "gpt-5.1")
	wantErrorCode(t, err, CodeUnsupportedTool)
}

func TestPrepareRenamesInvalidToolName(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"tools":[{"name":"my.tool","input_schema":{"type":"object"}}],
		"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"my.tool","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"}]}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	tools := body["tools"].([]any)
	if tools[0].(map[string]any)["name"] != "my_tool" {
		t.Fatalf("工具名没收拾: %v", tools[0])
	}
	if req.ToolNames["my_tool"] != "my.tool" {
		t.Fatalf("ToolNames 应该记下原名: %v", req.ToolNames)
	}
	if !hasDiagnostic(req.Diagnostics, "tool_renamed") {
		t.Fatalf("改名应该记诊断")
	}
}

func TestPrepareRejectsForeignSignature(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"messages":[
		{"role":"assistant","content":[{"type":"thinking","thinking":"想了点","signature":"ErUBCkYIBBgCKkBa1234"}]},
		{"role":"user","content":"继续"}]}`)
	_, err := Prepare(raw, "gpt-5.1")
	wantErrorCode(t, err, CodeForeignEncrypted)
}

func TestCapsuleRoundTrip(t *testing.T) {
	item := json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"先看文件"}],"encrypted_content":"ENC"}`)
	sealed := SealReasoning(item)
	if !strings.HasPrefix(sealed, "lingshu.responses.v1:") {
		t.Fatalf("封装前缀不对: %s", sealed)
	}
	expanded, err := ExpandReasoning(sealed)
	if err != nil {
		t.Fatalf("展开失败: %v", err)
	}
	var got, want map[string]any
	json.Unmarshal(expanded, &got)
	json.Unmarshal(item, &want)
	if got["encrypted_content"] != "ENC" {
		t.Fatalf("加密内容没原样保留: %v", got)
	}
	if len(got["summary"].([]any)) != 1 || want["summary"].([]any)[0].(map[string]any)["text"] != "先看文件" {
		t.Fatalf("摘要没原样保留: %v", got)
	}
	if _, err := ExpandReasoning("ErUBCkYIBBgCKkBa1234"); err == nil {
		t.Fatalf("外来内容应该报错")
	}
	if SealReasoning(json.RawMessage(`{"type":"message"}`)) != "" {
		t.Fatalf("不是 reasoning 条目不该封出内容")
	}
}

func TestPrepareReplaysSealedReasoning(t *testing.T) {
	item := json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"先看文件"}],"encrypted_content":"ENC"}`)
	sealed := SealReasoning(item)
	raw := []byte(`{"model":"m","max_tokens":100,"messages":[
		{"role":"assistant","content":[{"type":"thinking","thinking":"想了点","signature":"` + sealed + `"}]},
		{"role":"user","content":"继续"}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	items := decodeItems(t, decodeBody(t, req.Body))
	if len(items) != 2 {
		t.Fatalf("input 条目数量不对: %v", items)
	}
	reasoning := items[0]
	if reasoning["type"] != "reasoning" || reasoning["encrypted_content"] != "ENC" {
		t.Fatalf("reasoning 条目没原样回灌: %v", reasoning)
	}
	if reasoning["id"] != "rs_1" {
		t.Fatalf("reasoning id 丢了: %v", reasoning)
	}
}

func TestPrepareMapsMetadataAndServiceTier(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"metadata":{"user_id":"u1"},"service_tier":"auto",
		"messages":[{"role":"user","content":"hi"}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	metadata := body["metadata"].(map[string]any)
	if metadata["user_id"] != "u1" {
		t.Fatalf("metadata 没转对: %v", metadata)
	}
	if body["service_tier"] != "auto" {
		t.Fatalf("档位没转对: %v", body["service_tier"])
	}
}

func TestPrepareDropsAnthropicOnlyServiceTier(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"service_tier":"standard",
		"messages":[{"role":"user","content":"hi"}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	body := decodeBody(t, req.Body)
	if body["service_tier"] != "default" {
		t.Fatalf("standard 档位应换成 default: %v", body)
	}
	if !hasDiagnostic(req.Diagnostics, "field_ignored") {
		t.Fatalf("换档位应该记诊断")
	}
}

func TestPrepareRejectsForeignServiceTier(t *testing.T) {
	for _, tier := range []string{"priority", "fast", "flex"} {
		raw := []byte(`{"model":"m","max_tokens":100,"service_tier":"` + tier + `",
			"messages":[{"role":"user","content":"hi"}]}`)
		_, err := Prepare(raw, "gpt-5.1")
		wantErrorCode(t, err, CodeUnsupportedField)
	}
}

func TestPrepareRejectsMalformedThinking(t *testing.T) {
	cases := []string{
		`"thinking":"enabled"`,
		`"thinking":{"budget_tokens":8000}`,
		`"thinking":{"type":"enabled","budget_tokens":"很多"}`,
		`"thinking":{"type":"enabled","budget_tokens":8000,"foo":1}`,
	}
	for _, c := range cases {
		raw := []byte(`{"model":"m","max_tokens":100,` + c + `,"messages":[{"role":"user","content":"hi"}]}`)
		if _, err := Prepare(raw, "gpt-5.1"); err == nil {
			t.Fatalf("%s 应该报错却通过了", c)
		}
	}
}

func TestPrepareRejectsMalformedOutputConfig(t *testing.T) {
	for _, c := range []string{
		`"output_config":{"effort":""}`,
		`"output_config":{"effort":5}`,
		`"output_config":{"effort":"max","format":{"type":"json"}}`,
		`"output_config":{"effort":"turbo"}`,
	} {
		raw := []byte(`{"model":"m","max_tokens":100,` + c + `,"messages":[{"role":"user","content":"hi"}]}`)
		if _, err := Prepare(raw, "gpt-5.1"); err == nil {
			t.Fatalf("%s 应该报错却通过了", c)
		}
	}
}

func TestPrepareRequestsReasoningSummary(t *testing.T) {
	cases := []struct {
		extra   string
		wantSum bool
	}{
		{`"thinking":{"type":"enabled","budget_tokens":8000}`, true},
		{`"thinking":{"type":"adaptive"}`, true},
		{`"thinking":{"type":"disabled"}`, false},
		{`"output_config":{"effort":"none"}`, false},
	}
	for _, c := range cases {
		raw := []byte(`{"model":"m","max_tokens":100,` + c.extra + `,"messages":[{"role":"user","content":"hi"}]}`)
		req, err := Prepare(raw, "gpt-5.1")
		if err != nil {
			t.Fatalf("%s 转换失败: %v", c.extra, err)
		}
		reasoning := decodeBody(t, req.Body)["reasoning"].(map[string]any)
		_, hasSummary := reasoning["summary"]
		if hasSummary != c.wantSum {
			t.Fatalf("%s 的 summary 不对：%v", c.extra, reasoning)
		}
		if c.wantSum && reasoning["summary"] != "auto" {
			t.Fatalf("%s 的 summary 应该是 auto: %v", c.extra, reasoning)
		}
	}
}

func TestPrepareHandlesTopLevelCacheControl(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"cache_control":{"type":"ephemeral"},
		"messages":[{"role":"user","content":"hi"}]}`)
	req, err := Prepare(raw, "gpt-5.1")
	if err != nil {
		t.Fatalf("顶层缓存提示不该报错: %v", err)
	}
	if !hasDiagnostic(req.Diagnostics, "cache_not_guaranteed") {
		t.Fatalf("顶层缓存提示应该记一条诊断")
	}
}

func TestPrepareRejectsContextManagement(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"context_management":{"edits":[]},
		"messages":[{"role":"user","content":"hi"}]}`)
	_, err := Prepare(raw, "gpt-5.1")
	wantErrorCode(t, err, CodeUnsupportedField)
}

func TestPrepareRejectsUnknownMetadataKey(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":100,"metadata":{"team":"x"},
		"messages":[{"role":"user","content":"hi"}]}`)
	_, err := Prepare(raw, "gpt-5.1")
	wantErrorCode(t, err, CodeUnsupportedField)
}
