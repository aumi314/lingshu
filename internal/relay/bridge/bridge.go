// Package bridge 把 OpenAI Responses 客户端的请求转成 Anthropic Messages 上游请求，
// 再把上游 Messages 的响应转回 Responses 的事件和响应体。
//
// 对外接口：
//
//	Prepare(raw []byte, upstreamModel string) (*Request, error) // 请求侧转换，上游一律 stream:true
//	Request{Body []byte; Diagnostics []Diagnostic; Compact bool}
//	NewStream(responseID string) *Stream                        // 响应侧状态机
//	Stream.Bind(req *Request)                                   // 绑定工具表和压缩标记
//	Stream.SetLifecycle(on bool)                                // 是否由本层补发 response.created
//	Stream.Event(data []byte) ([][]byte, error)                 // 吃上游 SSE 的 data，吐 Responses 事件 JSON
//	Stream.Response() ([]byte, error)                           // 非流式客户端要的完整响应体
//	Stream.Close() ([][]byte, error)                             // 上游断流时收尾，出 response.failed
//	Stream.Done() bool / Stream.Failed() bool / Stream.Summary() string
//	SealCompaction(summary string) string                       // 摘要封成版本化、自包含的内容
//	ExpandCompaction(value string) (string, error)              // 展开本层封的摘要
//
// 转换失败一律返回 *Error（errors.As 可取 Code），错误码见下面常量。
// 思考块和压缩摘要是不透明内容，本层用自己的带版本封装保存，见 capsule.go。
// 只认本层生成的封装，别的来源一律显式报错，不猜、不静默丢弃。
package bridge

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Diagnostic 是一次转换的说明，写进调用日志：字段降级、工具改名、effort 映射等。
type Diagnostic struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error 是转换失败。Code 给调用方分流，Field 指到请求里的字段，Message 直接给用户看。
type Error struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error 实现 error，调用方用 errors.As 取到 Code。
func (e *Error) Error() string { return e.Message }

// 转换失败的错误码。
const (
	CodeInvalidJSON           = "invalid_json"
	CodeInvalidInput          = "invalid_input"
	CodeUnsupportedInput      = "unsupported_input_item"
	CodeUnsupportedContent    = "unsupported_content"
	CodeUnsupportedField      = "unsupported_field"
	CodeUnsupportedTool       = "unsupported_tool"
	CodeUnknownTool           = "unknown_tool"
	CodeInvalidArguments      = "invalid_arguments"
	CodeOrphanToolOutput      = "orphan_tool_output"
	CodeMissingToolOutput     = "missing_tool_output"
	CodeForeignEncrypted      = "foreign_encrypted_content"
	CodeForeignCompaction     = "foreign_compaction"
	CodeUnsupportedEffort     = "unsupported_effort"
	CodePreviousNotFound      = "previous_response_not_found"
	CodeEmptySummary          = "empty_compaction_summary"
	CodeTruncatedSummary      = "truncated_compaction_summary"
	CodeTruncatedStream       = "truncated_stream"
	CodeUnknownStopReason     = "unknown_stop_reason"
	CodeUnsupportedStopReason = "unsupported_stop_reason"
)

func newError(code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

// Request 是转换好的上游请求。
type Request struct {
	// Body 是发给上游的 Messages 请求体。
	Body []byte `json:"body"`
	// Diagnostics 是转换过程中的说明。
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Compact 为真表示这次请求带显式压缩触发：上游只出摘要，不执行工具。
	Compact bool `json:"compact"`

	tools *toolTable
	model string
}

// Prepare 把 Responses 请求体转成 Messages 请求体。
// upstreamModel 是路由选出的上游模型名，直接写进请求体。
func Prepare(raw []byte, upstreamModel string) (*Request, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, newError(CodeInvalidJSON, "", "请求体不是合法 JSON: %v", err)
	}
	p := &preparer{body: body, model: upstreamModel, tools: newToolTable()}

	// 本期明确不做的字段：直接报错，不静默忽略。
	for _, field := range []string{"conversation", "prompt", "context_management"} {
		if v, ok := body[field]; ok && !isNullJSON(v) {
			return nil, newError(CodeUnsupportedField, field,
				"Responses 的 %s 暂时不能转成 Messages，请关掉该功能或改用原生 Responses 渠道", field)
		}
	}
	// 后台任务和 JSON 输出模式都没法在 Messages 上等价实现，明确报错。
	if v, ok := body["background"]; ok && !isNullJSON(v) {
		var background bool
		if err := json.Unmarshal(v, &background); err == nil && background {
			return nil, newError(CodeUnsupportedField, "background",
				"后台异步模式不能转成 Messages，请用同步请求")
		}
	}
	if v, ok := body["store"]; ok && !isNullJSON(v) {
		var store bool
		if err := json.Unmarshal(v, &store); err == nil && store {
			return nil, newError(CodeUnsupportedField, "store",
				"服务端保存历史这个功能不支持，请用 store=false 并自己带上完整对话历史")
		}
	}
	if err := checkTextFormat(body); err != nil {
		return nil, err
	}

	// 服务端历史这里恢复不了。store=false 也不能证明历史完整（客户端可能只带增量），
	// 所以只要带 previous_response_id 就明确报错，由调用方先展开再删掉这个字段。
	if v, ok := body["previous_response_id"]; ok && !isNullJSON(v) {
		var prev string
		if err := json.Unmarshal(v, &prev); err == nil && prev != "" {
			return nil, newError(CodePreviousNotFound, "previous_response_id",
				"previous_response_id 指向的服务端历史无法恢复，请带上完整对话历史重试")
		}
	}

	items, err := p.inputItems()
	if err != nil {
		return nil, err
	}
	// instructions 是客户端给的系统提示，放在 system 最前面。
	if raw, ok := body["instructions"]; ok && !isNullJSON(raw) {
		var instructions string
		if err := json.Unmarshal(raw, &instructions); err == nil && strings.TrimSpace(instructions) != "" {
			p.system = append(p.system, map[string]any{"type": "text", "text": instructions})
		}
	}
	// 第一遍收工具：请求体里的 tools 和 input 里的 additional_tools 都要收。
	if err := p.collectTools(items); err != nil {
		return nil, err
	}
	// 第二遍转正文。
	if err := p.buildMessages(items); err != nil {
		return nil, err
	}
	if len(p.messages) == 0 {
		return nil, newError(CodeInvalidInput, "input", "请求里没有可以发给上游的消息")
	}
	// Anthropic 没有 verbosity 这个参数，别让用户以为它生效了。
	if raw, ok := body["text"]; ok && !isNullJSON(raw) {
		var text struct {
			Verbosity string `json:"verbosity"`
		}
		if err := json.Unmarshal(raw, &text); err == nil && text.Verbosity != "" {
			p.diagf("field_ignored", "text.verbosity", "Messages 没有 verbosity 参数，已忽略")
		}
	}
	// 用不上的顶层字段记一条诊断，别让调用方以为它们生效了。
	for field := range body {
		if honoredFields[field] {
			continue
		}
		if note, ok := ignoredFields[field]; ok {
			p.diagf("field_ignored", field, "%s", note)
			continue
		}
		p.diagf("field_ignored", field, "Messages 没有对应字段，这个请求字段已忽略")
	}

	maxTokens, err := p.maxTokens()
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"model":      upstreamModel,
		"max_tokens": maxTokens,
		"stream":     true, // 上游一律流式，响应侧再拼成非流式
	}
	if len(p.system) > 0 {
		out["system"] = p.system
	}
	out["messages"] = p.messages
	if p.compact {
		// 压缩流程：禁用工具执行，只让模型出摘要。
		p.appendMsg("user", []map[string]any{{"type": "text", "text": handoffPrompt}})
		out["messages"] = p.messages
		p.diagf("compaction", "input", "检测到 compaction_trigger：禁用工具执行，追加摘要交接提示")
	} else if len(p.tools.anthropic) > 0 {
		out["tools"] = p.tools.anthropic
		if choice, err := p.toolChoice(); err != nil {
			return nil, err
		} else if choice != nil {
			out["tool_choice"] = choice
		}
	}
	thinking, outputConfig, err := p.thinking()
	if err != nil {
		return nil, err
	}
	if thinking != nil {
		out["thinking"] = thinking
	}
	if outputConfig != nil {
		out["output_config"] = outputConfig
	}

	markCacheBreakpoints(p.system, out["tools"], p.messages)

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, newError(CodeInvalidInput, "", "生成上游请求失败: %v", err)
	}
	return &Request{Body: encoded, Diagnostics: p.diags, Compact: p.compact, tools: p.tools, model: upstreamModel}, nil
}

// markCacheBreakpoints 打提示缓存断点。Anthropic 只缓存请求方标出的前缀（OpenAI 是自动缓存），
// 不标的话长对话每一轮都按全价付输入；做法同 Claude Code，最多 4 处（上游上限）：
// system 末尾、tools 末尾、最后一条消息末尾、倒数第二条 user 消息末尾（上一轮写入的缓存这一轮读得到）。
// 思考块不能带 cache_control，从末尾往前找第一个能带的块。
func markCacheBreakpoints(system []any, tools any, messages []map[string]any) {
	mark := func(block map[string]any) bool {
		switch strField(block, "type") {
		case "thinking", "redacted_thinking":
			return false
		}
		block["cache_control"] = map[string]any{"type": "ephemeral"}
		return true
	}
	markLast := func(blocks []map[string]any) {
		for i := len(blocks) - 1; i >= 0; i-- {
			if mark(blocks[i]) {
				return
			}
		}
	}
	if n := len(system); n > 0 {
		if block, ok := system[n-1].(map[string]any); ok {
			mark(block)
		}
	}
	if list, ok := tools.([]map[string]any); ok && len(list) > 0 {
		mark(list[len(list)-1])
	}
	content := func(m map[string]any) []map[string]any {
		blocks, _ := m["content"].([]map[string]any)
		return blocks
	}
	if n := len(messages); n > 0 {
		markLast(content(messages[n-1]))
		users := 0
		for i := n - 1; i >= 0; i-- {
			if messages[i]["role"] != "user" {
				continue
			}
			if users++; users == 2 {
				markLast(content(messages[i]))
				break
			}
		}
	}
}

// handoffPrompt 是压缩时的英文交接提示：让模型直接写出可以接手的摘要。
const handoffPrompt = "Write a handoff summary of this conversation so far. Another assistant will continue the work " +
	"without seeing any earlier turns. Cover the user's goals and constraints, the decisions already made, the files " +
	"and code that were changed, the commands that were run and what they returned, and the tasks that are still open. " +
	"Be specific and complete, and output only the summary."

// compactionTextPrefix 是展开压缩摘要时加的标记，让模型知道这段是历史摘要。
const compactionTextPrefix = "[Summary of the compacted earlier conversation]\n"

func isNullJSON(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// checkTextFormat 检查 text.format。Anthropic 没有 JSON 输出模式这一层，
// 悄悄丢掉会让模型输出自由文本，调用方却以为拿到了 JSON，所以明确报错。
func checkTextFormat(body map[string]json.RawMessage) error {
	raw, ok := body["text"]
	if !ok || isNullJSON(raw) {
		return nil
	}
	var text struct {
		Format *struct {
			Type string `json:"type"`
		} `json:"format"`
	}
	if err := json.Unmarshal(raw, &text); err != nil {
		return newError(CodeInvalidInput, "text", "text 字段格式不对")
	}
	if text.Format == nil {
		return nil
	}
	if text.Format.Type != "" && text.Format.Type != "text" {
		return newError(CodeUnsupportedField, "text.format",
			"输出格式 %s 不能转成 Messages（这里只支持纯文本），请改用纯文本或改用原生 Responses 渠道", text.Format.Type)
	}
	return nil
}

// randHex 生成随机标识的后缀。
func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(buf)
}

// diagf 记一条转换诊断。
func (p *preparer) diagf(code, field, format string, args ...any) {
	p.diags = append(p.diags, Diagnostic{Code: code, Field: field, Message: fmt.Sprintf(format, args...)})
}

// strField 取字符串字段。
func strField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// mapField 取对象字段。
func mapField(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	c, _ := m[key].(map[string]any)
	return c
}

// intField 取整数字段。
func intField(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

// sliceField 取数组字段。
func sliceField(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	s, _ := m[key].([]any)
	return s
}
