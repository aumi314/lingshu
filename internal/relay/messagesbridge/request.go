// Package messagesbridge 把 Anthropic Messages 客户端的请求转成 OpenAI Responses 上游请求。
//
// 对外接口：
//
//	Prepare(raw []byte, upstreamModel string) (*Request, error) // 请求侧转换，上游一律 stream:true
//	Request{Body []byte; Diagnostics []Diagnostic; Model string; ToolNames map[string]string}
//	SealReasoning(item json.RawMessage) string                 // reasoning 条目封成版本化、自包含的内容
//	ExpandReasoning(value string) (json.RawMessage, error)      // 展开本层封的 reasoning 条目
//
// 上游统一 stream:true、store:false、include reasoning.encrypted_content，响应侧再拼成 Messages 的流或整段响应。
// 转换失败一律返回 *Error（errors.As 可取 Code）。
// 原生 Anthropic 签名不是本层的封装，回灌时明确报错，不当成上游状态猜。
package messagesbridge

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aumi314/lingshu/internal/relay/bridge"
)

// Diagnostic 是一次转换的说明，写进调用日志：字段降级、工具改名、思考档位换算等。
// 和另一层的 bridge 共用同一套结构，日志字段一致。
type Diagnostic = bridge.Diagnostic

// Error 是转换失败。Code 给调用方分流，Field 指到请求里的字段，Message 直接给用户看。
// 和另一层的 bridge 共用同一套错误码，上层按同一套码处理。
type Error = bridge.Error

// 转换失败的错误码，和 bridge 共用。
const (
	CodeInvalidJSON        = bridge.CodeInvalidJSON
	CodeInvalidInput       = bridge.CodeInvalidInput
	CodeUnsupportedField   = bridge.CodeUnsupportedField
	CodeUnsupportedContent = bridge.CodeUnsupportedContent
	CodeUnsupportedTool    = bridge.CodeUnsupportedTool
	CodeUnknownTool        = bridge.CodeUnknownTool
	CodeInvalidArguments   = bridge.CodeInvalidArguments
	CodeOrphanToolOutput   = bridge.CodeOrphanToolOutput
	CodeMissingToolOutput  = bridge.CodeMissingToolOutput
	CodeForeignEncrypted   = bridge.CodeForeignEncrypted
	CodeUnsupportedEffort  = bridge.CodeUnsupportedEffort
)

// Request 是转换好的上游请求。
type Request struct {
	// Body 是发给上游的 Responses 请求体。
	Body []byte `json:"body"`
	// Diagnostics 是转换过程中的说明。
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Model 是客户端原本写的模型名，日志按它记客户端用的是哪个模型。
	Model string `json:"model"`
	// ToolNames 是 Responses 函数名到客户端 Messages 工具名的对应表，
	// 响应侧把 function_call 转回 tool_use 时按它还原名字。
	ToolNames map[string]string `json:"tool_names"`
}

// knownFields 是本层认识的 Messages 请求字段。
// 不在表里的字段一律报错，不静默忽略：客户端以为生效了，其实没有，比报错更糟。
var knownFields = map[string]bool{
	"model": true, "max_tokens": true, "messages": true, "system": true,
	"tools": true, "tool_choice": true, "stream": true, "temperature": true,
	"top_p": true, "top_k": true, "stop_sequences": true, "metadata": true,
	"thinking": true, "output_config": true, "service_tier": true,
}

// reasoningEfforts 是 OpenAI 的推理强度档位。
var reasoningEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true, "high": true,
	"xhigh": true, "max": true,
}

// preparer 攒一次转换的状态。
type preparer struct {
	body          map[string]json.RawMessage
	clientModel   string
	upstreamModel string
	diags         []Diagnostic
	instructions  string
	items         []map[string]any
	tools         []map[string]any
	toolChoice    any
	parallel      *bool
	// effort 是这次请求实际发出去的推理档位，采样参数要不要提示看它。
	effort string
	// toolNames 是 Responses 名字到客户端名字；upstreamNames 反向。
	toolNames     map[string]string
	upstreamNames map[string]string
	// pending 是已经出现、还没等到结果的 tool_use：id -> 工具名。
	pending map[string]string
	// seenCalls 是已经出现过的 tool_use id，用来发现重复。
	seenCalls map[string]bool
}

// Prepare 把 Messages 请求体转成 Responses 请求体。
// upstreamModel 是路由选出的上游模型名，直接写进请求体；
// Request.Model 保留客户端原来写的模型名。
func Prepare(raw []byte, upstreamModel string) (*Request, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, newError(CodeInvalidJSON, "", "请求体不是合法 JSON: %v", err)
	}
	p := &preparer{
		body:          body,
		upstreamModel: upstreamModel,
		toolNames:     map[string]string{},
		upstreamNames: map[string]string{},
		pending:       map[string]string{},
		seenCalls:     map[string]bool{},
	}
	for field := range body {
		if field == "cache_control" {
			// 顶层缓存提示：Responses 上游没有对应字段，丢掉并在诊断里说明。
			p.diagf("cache_not_guaranteed", field,
				"Responses 上游没有顶层缓存提示字段，这段缓存标记已去掉，上游是否命中缓存不保证")
			continue
		}
		if !knownFields[field] {
			return nil, newError(CodeUnsupportedField, field,
				"Messages 的 %s 不能转成 Responses 上游，请去掉该字段或改用原生 Messages 渠道", field)
		}
	}

	clientModel, err := p.stringField("model")
	if err != nil || clientModel == "" {
		return nil, newError(CodeInvalidInput, "model", "请求体缺少 model 字段")
	}
	p.clientModel = clientModel

	maxTokens, err := p.maxTokens()
	if err != nil {
		return nil, err
	}
	if err := p.collectTools(); err != nil {
		return nil, err
	}
	if err := p.mapToolChoice(); err != nil {
		return nil, err
	}
	if err := p.buildMessages(); err != nil {
		return nil, err
	}
	if err := p.mapSystem(); err != nil {
		return nil, err
	}

	out := map[string]any{
		"model":             upstreamModel,
		"input":             p.items,
		"max_output_tokens": maxTokens,
		"stream":            true, // 上游一律流式，响应侧再拼成非流式
		"store":             false,
		"include":           []string{"reasoning.encrypted_content"},
	}
	if p.instructions != "" {
		out["instructions"] = p.instructions
	}
	if len(p.tools) > 0 {
		out["tools"] = p.tools
	}
	if p.toolChoice != nil {
		out["tool_choice"] = p.toolChoice
	}
	if p.parallel != nil {
		out["parallel_tool_calls"] = *p.parallel
	}
	reasoning, err := p.reasoning()
	if err != nil {
		return nil, err
	}
	if reasoning != nil {
		out["reasoning"] = reasoning
	}
	if err := p.mapSampling(out); err != nil {
		return nil, err
	}
	if err := p.mapMetadata(out); err != nil {
		return nil, err
	}
	if err := p.mapServiceTier(out); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, newError(CodeInvalidInput, "", "生成上游请求失败: %v", err)
	}
	return &Request{
		Body:        encoded,
		Diagnostics: p.diags,
		Model:       p.clientModel,
		ToolNames:   p.toolNames,
	}, nil
}

// newError 造一个转换失败。
func newError(code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

// diagf 记一条转换诊断。
func (p *preparer) diagf(code, field, format string, args ...any) {
	p.diags = append(p.diags, Diagnostic{Code: code, Field: field, Message: fmt.Sprintf(format, args...)})
}

// stringField 取字符串字段。
func (p *preparer) stringField(key string) (string, error) {
	raw, ok := p.body[key]
	if !ok || isNullJSON(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", newError(CodeInvalidInput, key, "%s 字段格式不对", key)
	}
	return s, nil
}

// maxTokens 取 max_tokens，转成上游的 max_output_tokens。
func (p *preparer) maxTokens() (int64, error) {
	raw, ok := p.body["max_tokens"]
	if !ok || isNullJSON(raw) {
		return 0, newError(CodeInvalidInput, "max_tokens", "请求体缺少 max_tokens 字段")
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, newError(CodeInvalidInput, "max_tokens", "max_tokens 格式不对")
	}
	if n <= 0 {
		return 0, newError(CodeInvalidInput, "max_tokens", "max_tokens 必须大于 0")
	}
	return n, nil
}

// mapSystem 把 system 合成 Responses 的 instructions（上游只收一段字符串）。
func (p *preparer) mapSystem() error {
	raw, ok := p.body["system"]
	if !ok || isNullJSON(raw) {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		// 原样保留：system 里的空格和换行都是提示词的一部分，不能收拾。
		p.instructions = text
		return nil
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return newError(CodeInvalidInput, "system", "system 格式不对")
	}
	var parts []string
	for i, b := range blocks {
		if b.Type != "text" {
			return newError(CodeUnsupportedContent, fmt.Sprintf("system[%d]", i),
				"system 里的 %s 内容块不能转成 Responses 的 instructions", b.Type)
		}
		if len(b.CacheControl) > 0 && !isNullJSON(b.CacheControl) {
			p.diagf("cache_not_guaranteed", fmt.Sprintf("system[%d]", i),
				"Responses 上游没有缓存提示字段，这段缓存标记已去掉，上游是否命中缓存不保证")
		}
		parts = append(parts, b.Text)
	}
	p.instructions = strings.Join(parts, "\n\n")
	return nil
}

// collectTools 转工具声明：input_schema 变成 parameters。
func (p *preparer) collectTools() error {
	raw, ok := p.body["tools"]
	if !ok || isNullJSON(raw) {
		return nil
	}
	var defs []json.RawMessage
	if err := json.Unmarshal(raw, &defs); err != nil {
		return newError(CodeInvalidInput, "tools", "tools 格式不对")
	}
	for i, def := range defs {
		if err := p.addTool(def, fmt.Sprintf("tools[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// addTool 转一条工具声明。
func (p *preparer) addTool(def json.RawMessage, field string) error {
	var tool struct {
		Type         string          `json:"type"`
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		InputSchema  json.RawMessage `json:"input_schema"`
		CacheControl json.RawMessage `json:"cache_control"`
		DeferLoading json.RawMessage `json:"defer_loading"`
	}
	if err := json.Unmarshal(def, &tool); err != nil {
		return newError(CodeInvalidInput, field, "工具定义格式不对")
	}
	if tool.Type != "" && tool.Type != "custom" {
		// Anthropic 的服务端工具（网页搜索、代码执行等）在 Responses 上游没有对应实现。
		return newError(CodeUnsupportedTool, field,
			"工具 %s 是 Anthropic 的服务端工具，不能转成 Responses 上游，请改用客户端工具或原生 Messages 渠道",
			tool.Name)
	}
	if tool.Name == "" {
		return newError(CodeUnsupportedTool, field, "工具定义缺少 name")
	}
	if len(tool.CacheControl) > 0 && !isNullJSON(tool.CacheControl) {
		p.diagf("cache_not_guaranteed", field,
			"Responses 上游没有缓存提示字段，工具 %s 上的缓存标记已去掉，上游是否命中缓存不保证", tool.Name)
	}
	if len(tool.DeferLoading) > 0 && !isNullJSON(tool.DeferLoading) {
		p.diagf("field_ignored", field, "Responses 上游没有延迟加载工具的字段，工具 %s 的 defer_loading 已忽略", tool.Name)
	}
	parameters := map[string]any{"type": "object", "properties": map[string]any{}}
	if len(tool.InputSchema) > 0 && !isNullJSON(tool.InputSchema) {
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return newError(CodeInvalidInput, field+".input_schema", "工具 %s 的 input_schema 不是 JSON 对象", tool.Name)
		}
		if err := json.Unmarshal(tool.InputSchema, &parameters); err != nil {
			return newError(CodeInvalidInput, field+".input_schema", "工具 %s 的 input_schema 格式不对", tool.Name)
		}
	}
	upstreamName := tool.Name
	if !validToolName(upstreamName) {
		upstreamName = sanitizeToolName(tool.Name)
		if _, exists := p.toolNames[upstreamName]; exists {
			return newError(CodeUnsupportedTool, field, "工具名 %s 收拾后和别的工具重名", tool.Name)
		}
		p.diagf("tool_renamed", field,
			"工具名 %s 不符合 Responses 的命名要求，上游改用 %s", tool.Name, upstreamName)
	}
	if _, exists := p.toolNames[upstreamName]; exists {
		return newError(CodeUnsupportedTool, field, "重复工具名 %s", tool.Name)
	}
	p.tools = append(p.tools, map[string]any{
		"type":        "function",
		"name":        upstreamName,
		"description": tool.Description,
		"parameters":  parameters,
		"strict":      false,
	})
	p.toolNames[upstreamName] = tool.Name
	p.upstreamNames[tool.Name] = upstreamName
	return nil
}

// mapToolChoice 转工具选择。
func (p *preparer) mapToolChoice() error {
	raw, ok := p.body["tool_choice"]
	if !ok || isNullJSON(raw) {
		return nil
	}
	var choice struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return newError(CodeInvalidInput, "tool_choice", "tool_choice 格式不对")
	}
	switch choice.Type {
	case "auto":
		p.toolChoice = "auto"
	case "any":
		p.toolChoice = "required"
	case "none":
		p.toolChoice = "none"
	case "tool":
		if choice.Name == "" {
			return newError(CodeUnknownTool, "tool_choice", "tool_choice 指定了工具但没写名字")
		}
		upstreamName, ok := p.upstreamNames[choice.Name]
		if !ok {
			return newError(CodeUnknownTool, "tool_choice", "tool_choice 里的工具 %s 没有在 tools 里声明", choice.Name)
		}
		p.toolChoice = map[string]any{"type": "function", "name": upstreamName}
	case "":
		return newError(CodeInvalidInput, "tool_choice", "tool_choice 缺少 type")
	default:
		return newError(CodeUnsupportedField, "tool_choice",
			"tool_choice 的 %s 不能转成 Responses 上游（支持 auto、any、tool、none）", choice.Type)
	}
	if choice.DisableParallelToolUse != nil {
		parallel := !*choice.DisableParallelToolUse
		p.parallel = &parallel
	}
	return nil
}

// buildMessages 把 messages 变成 Responses 的 input 条目，顺序保持不变。
func (p *preparer) buildMessages() error {
	raw, ok := p.body["messages"]
	if !ok || isNullJSON(raw) {
		return newError(CodeInvalidInput, "messages", "请求体缺少 messages 字段")
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return newError(CodeInvalidInput, "messages", "messages 格式不对")
	}
	if len(messages) == 0 {
		return newError(CodeInvalidInput, "messages", "messages 是空的")
	}
	for i, rawMsg := range messages {
		var msg struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			return newError(CodeInvalidInput, fmt.Sprintf("messages[%d]", i), "消息格式不对")
		}
		switch msg.Role {
		case "user":
			if err := p.buildUser(i, msg.Content); err != nil {
				return err
			}
		case "assistant":
			if err := p.buildAssistant(i, msg.Content); err != nil {
				return err
			}
		default:
			return newError(CodeUnsupportedField, fmt.Sprintf("messages[%d].role", i),
				"角色 %s 不能转成 Responses 上游（只支持 user、assistant）", msg.Role)
		}
	}
	if len(p.pending) > 0 {
		var missing []string
		for id, name := range p.pending {
			missing = append(missing, fmt.Sprintf("%s(%s)", id, name))
		}
		return newError(CodeMissingToolOutput, "messages",
			"工具调用 %s 没有对应的工具结果，Responses 上游要求每个调用都有结果",
			strings.Join(missing, ", "))
	}
	return nil
}

// buildUser 转用户消息：文本、图片、文档、工具结果。
func (p *preparer) buildUser(idx int, content json.RawMessage) error {
	return p.buildContent(idx, "user", content)
}

func (p *preparer) buildAssistant(idx int, content json.RawMessage) error {
	return p.buildContent(idx, "assistant", content)
}

// 正文属于 message.content，工具和 reasoning 是独立 input 条目；按原序分段。
func (p *preparer) buildContent(idx int, role string, content json.RawMessage) error {
	blocks, err := p.contentBlocks(idx, content)
	if err != nil {
		return err
	}
	var parts []map[string]any
	flush := func() {
		if len(parts) != 0 {
			var content any = parts
			if role == "assistant" {
				// 官方多轮示例使用文本简式；避免 assistant 的 input_text 校验差异。
				var texts []string
				for _, part := range parts {
					texts = append(texts, part["text"].(string))
				}
				content = strings.Join(texts, "\n\n")
			}
			p.items = append(p.items, map[string]any{"type": "message", "role": role, "content": content})
			parts = nil
		}
	}
	for i, b := range blocks {
		field := fmt.Sprintf("messages[%d].content[%d]", idx, i)
		var item map[string]any
		var err error
		if len(b.CacheControl) > 0 && !isNullJSON(b.CacheControl) {
			p.diagf("cache_not_guaranteed", field+".cache_control", "缓存提示不转发，缓存命中以 Responses 上游返回的用量为准")
		}
		switch {
		case b.Type == "text":
			parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
			continue
		case role == "user" && b.Type == "image":
			item, err = p.imageItem(b, field)
			if err != nil {
				return err
			}
			parts = append(parts, item)
			continue
		case role == "user" && b.Type == "document":
			item, err = p.documentItem(b, field)
			if err != nil {
				return err
			}
			parts = append(parts, item)
			continue
		case role == "user" && b.Type == "tool_result":
			item, err = p.toolResultItem(b, field)
		case role == "assistant" && b.Type == "tool_use":
			item, err = p.toolCallItem(b, field)
		case role == "assistant" && (b.Type == "thinking" || b.Type == "redacted_thinking"):
			item, err = p.reasoningItem(b, field)
		default:
			return newError(CodeUnsupportedContent, field, "%s 消息里的 %s 内容块不能转成 Responses", role, b.Type)
		}
		if err != nil {
			return err
		}
		flush()
		p.items = append(p.items, item)
	}
	flush()
	return nil
}

// contentBlocks 把消息内容拆成内容块。字符串内容当成一段文本。
func (p *preparer) contentBlocks(idx int, content json.RawMessage) ([]block, error) {
	field := fmt.Sprintf("messages[%d].content", idx)
	if len(content) == 0 || isNullJSON(content) {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return []block{{Type: "text", Text: text}}, nil
	}
	var blocks []block
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, newError(CodeInvalidInput, field, "消息内容格式不对")
	}
	return blocks, nil
}

// imageItem 转图片块。base64 拼成 data URL，url 直接用。
func (p *preparer) imageItem(b block, field string) (map[string]any, error) {
	if b.Source == nil {
		return nil, newError(CodeInvalidInput, field, "图片块缺少 source")
	}
	switch b.Source.Type {
	case "base64":
		if b.Source.Data == "" {
			return nil, newError(CodeInvalidInput, field, "图片块缺少 data")
		}
		mediaType := b.Source.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		return map[string]any{
			"type":      "input_image",
			"image_url": "data:" + mediaType + ";base64," + b.Source.Data,
		}, nil
	case "url":
		if b.Source.URL == "" {
			return nil, newError(CodeInvalidInput, field, "图片块缺少 url")
		}
		return map[string]any{"type": "input_image", "image_url": b.Source.URL}, nil
	default:
		return nil, newError(CodeUnsupportedContent, field,
			"图片来源 %s 不能转成 Responses 上游（支持 base64、url）", b.Source.Type)
	}
}

// documentItem 转文档块。
func (p *preparer) documentItem(b block, field string) (map[string]any, error) {
	if len(b.Citations) > 0 && !isNullJSON(b.Citations) {
		var config struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(b.Citations, &config) != nil {
			return nil, newError(CodeInvalidInput, field+".citations", "文档 citations 配置格式不对")
		}
		if config.Enabled {
			return nil, newError(CodeUnsupportedContent, field+".citations", "文档引用暂不支持转接，请关闭 citations 或使用原生 Messages 渠道")
		}
	}
	if len(b.Context) > 0 && !isNullJSON(b.Context) {
		var context string
		if json.Unmarshal(b.Context, &context) != nil {
			return nil, newError(CodeInvalidInput, field+".context", "文档 context 必须是字符串")
		}
		if context != "" {
			return nil, newError(CodeUnsupportedContent, field+".context", "文档 context 暂不支持转接，请改用普通文本或原生 Messages 渠道")
		}
	}
	if b.Source == nil {
		return nil, newError(CodeInvalidInput, field, "文档块缺少 source")
	}
	switch b.Source.Type {
	case "base64":
		if b.Source.Data == "" {
			return nil, newError(CodeInvalidInput, field, "文档块缺少 data")
		}
		mediaType := b.Source.MediaType
		if mediaType == "" {
			mediaType = "application/pdf"
		}
		return map[string]any{
			"type":      "input_file",
			"filename":  documentName(b, mediaType),
			"file_data": "data:" + mediaType + ";base64," + b.Source.Data,
		}, nil
	case "text":
		if b.Source.Data == "" {
			return nil, newError(CodeInvalidInput, field, "文档块缺少 data")
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(b.Source.Data))
		return map[string]any{
			"type":      "input_file",
			"filename":  documentName(b, "text/plain"),
			"file_data": "data:text/plain;base64," + encoded,
		}, nil
	case "url":
		if b.Source.URL == "" {
			return nil, newError(CodeInvalidInput, field, "文档块缺少 url")
		}
		return map[string]any{
			"type":     "input_file",
			"filename": documentName(b, ""),
			"file_url": b.Source.URL,
		}, nil
	default:
		return nil, newError(CodeUnsupportedContent, field,
			"文档来源 %s 不能转成 Responses 上游（支持 base64、text、url）", b.Source.Type)
	}
}

// documentName 给上游文件起名：优先用标题，否则按媒体类型猜一个。
func documentName(b block, mediaType string) string {
	if b.Title != "" {
		return b.Title
	}
	switch mediaType {
	case "application/pdf":
		return "document.pdf"
	case "text/plain":
		return "document.txt"
	default:
		return "document"
	}
}

// toolCallItem 转工具调用：id 变成 call_id，参数变成 JSON 字符串。
func (p *preparer) toolCallItem(b block, field string) (map[string]any, error) {
	if b.ID == "" {
		return nil, newError(CodeInvalidInput, field, "工具调用缺少 id")
	}
	if b.Name == "" {
		return nil, newError(CodeInvalidInput, field, "工具调用缺少 name")
	}
	if p.seenCalls[b.ID] {
		return nil, newError(CodeInvalidArguments, field, "工具调用 id %s 重复", b.ID)
	}
	upstreamName, ok := p.upstreamNames[b.Name]
	if !ok {
		return nil, newError(CodeUnknownTool, field,
			"工具调用里的工具 %s 没有在本次请求的 tools 里声明", b.Name)
	}
	if len(b.Input) == 0 || isNullJSON(b.Input) {
		return nil, newError(CodeInvalidArguments, field, "工具调用 %s 的参数不是 JSON 对象", b.ID)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(b.Input, &input); err != nil {
		return nil, newError(CodeInvalidArguments, field,
			"工具调用 %s 的参数不是 JSON 对象，Responses 上游只接受对象", b.ID)
	}
	arguments, err := json.Marshal(input)
	if err != nil {
		return nil, newError(CodeInvalidArguments, field, "工具调用 %s 的参数序列化失败", b.ID)
	}
	p.pending[b.ID] = b.Name
	p.seenCalls[b.ID] = true
	return map[string]any{
		"type":      "function_call",
		"call_id":   b.ID,
		"name":      upstreamName,
		"arguments": string(arguments),
	}, nil
}

// toolResultItem 转工具结果：tool_use_id 变成 call_id。
func (p *preparer) toolResultItem(b block, field string) (map[string]any, error) {
	if b.ToolUseID == "" {
		return nil, newError(CodeInvalidInput, field, "工具结果缺少 tool_use_id")
	}
	name, ok := p.pending[b.ToolUseID]
	if !ok {
		return nil, newError(CodeOrphanToolOutput, field,
			"工具结果 %s 没有对应的工具调用", b.ToolUseID)
	}
	delete(p.pending, b.ToolUseID)

	output, err := p.toolResultOutput(b, field)
	if err != nil {
		return nil, err
	}
	if b.IsError {
		p.diagf("tool_error_mapped", field, "工具 %s 的 is_error 转为显式失败标记", name)
		switch value := output.(type) {
		case string:
			output = "[Tool execution failed]\n" + value
		case []map[string]any:
			output = append([]map[string]any{{"type": "input_text", "text": "[Tool execution failed]"}}, value...)
		}
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": b.ToolUseID,
		"output":  output,
	}, nil
}

// toolResultOutput 转工具结果的内容：纯文本合成一段字符串，带图片就用条目数组。
func (p *preparer) toolResultOutput(b block, field string) (any, error) {
	if len(b.Content) == 0 || isNullJSON(b.Content) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(b.Content, &text); err == nil {
		return text, nil
	}
	var blocks []block
	if err := json.Unmarshal(b.Content, &blocks); err != nil {
		return nil, newError(CodeInvalidInput, field, "工具结果内容格式不对")
	}
	var texts []string
	var parts []map[string]any
	for i, sub := range blocks {
		subField := fmt.Sprintf("%s.content[%d]", field, i)
		switch sub.Type {
		case "text":
			texts = append(texts, sub.Text)
			parts = append(parts, map[string]any{"type": "input_text", "text": sub.Text})
		case "image":
			item, err := p.imageItem(sub, subField)
			if err != nil {
				return nil, err
			}
			parts = append(parts, item)
		default:
			return nil, newError(CodeUnsupportedContent, subField,
				"工具结果里的 %s 内容块不能转成 Responses 上游", sub.Type)
		}
	}
	if len(texts) == len(blocks) {
		// 全是文字：合成一段字符串，上游兼容性最好。
		return strings.Join(texts, "\n"), nil
	}
	return parts, nil
}

// reasoningItem 转思考块：展开本层的封装，原样回灌 reasoning 条目。
func (p *preparer) reasoningItem(b block, field string) (map[string]any, error) {
	value := b.Signature
	if b.Type == "redacted_thinking" {
		value = b.Data
	}
	if value == "" {
		return nil, newError(CodeForeignEncrypted, field,
			"思考块缺少不透明内容，无法回灌给 Responses 上游")
	}
	item, err := ExpandReasoning(value)
	if err != nil {
		return nil, newError(CodeForeignEncrypted, field,
			"这段思考内容不是本转接生成的（原生 Anthropic 签名无法回灌到 Responses 上游）")
	}
	var obj map[string]any
	if err := json.Unmarshal(item, &obj); err != nil {
		return nil, newError(CodeForeignEncrypted, field, "推理内容格式不对")
	}
	p.diagf("reasoning_replayed", field, "已把本转接封存的 reasoning 条目原样回灌给上游")
	return obj, nil
}

// reasoning 把 Anthropic 的思考设置换成 Responses 的 reasoning。
func (p *preparer) reasoning() (map[string]any, error) {
	thinkingType, budget, hasThinking, err := p.thinking()
	if err != nil {
		return nil, err
	}
	effort, hasEffort, err := p.outputConfigEffort()
	if err != nil {
		return nil, err
	}
	switch {
	case !hasThinking && !hasEffort:
		return nil, nil
	case hasThinking && thinkingType == "disabled":
		if hasEffort && effort != "none" {
			return nil, newError(CodeUnsupportedEffort, "output_config.effort", "thinking.disabled 与非 none 的 effort 冲突，请统一思考设置")
		}
		p.diagf("thinking_mapped", "thinking",
			"思考已关闭，按 Responses 的 reasoning.effort=none 发出（上游型号接不接受这一档由上游决定）")
		p.effort = "none"
		return map[string]any{"effort": "none"}, nil
	case hasThinking && thinkingType == "enabled":
		if budget <= 0 {
			return nil, newError(CodeInvalidInput, "thinking.budget_tokens",
				"thinking.enabled 必须带 budget_tokens")
		}
		mapped := effortForBudget(budget)
		if hasEffort {
			mapped = effort
			p.diagf("thinking_mapped", "output_config.effort",
				"显式 output_config.effort=%s 优先于 thinking 预算换算", effort)
		} else {
			p.diagf("thinking_mapped", "thinking.budget_tokens",
				"Anthropic 思考预算 %d token 换算成 Responses 的 reasoning.effort=%s（阈值 4096/16384；两家的预算和档位不等价，实际思考量由上游模型决定）",
				budget, mapped)
		}
		p.effort = mapped
		reasoning := map[string]any{"effort": mapped}
		if mapped != "none" {
			reasoning["summary"] = "auto"
		}
		return reasoning, nil
	case hasThinking && thinkingType == "adaptive":
		if !hasEffort {
			effort = "high"
			p.diagf("thinking_mapped", "thinking",
				"thinking=adaptive 没带 output_config.effort，Responses 没有自适应档位，按 high 发出")
		} else {
			p.diagf("thinking_mapped", "thinking",
				"thinking=adaptive 按 output_config.effort=%s 发出", effort)
		}
		p.effort = effort
		reasoning := map[string]any{"effort": effort}
		if effort != "none" {
			reasoning["summary"] = "auto"
		}
		return reasoning, nil
	case hasThinking:
		return nil, newError(CodeUnsupportedField, "thinking",
			"thinking 的 %s 不能转成 Responses 上游（支持 enabled、disabled、adaptive）", thinkingType)
	default:
		p.diagf("thinking_mapped", "output_config.effort",
			"按 output_config.effort=%s 转成 Responses 的 reasoning.effort", effort)
		p.effort = effort
		reasoning := map[string]any{"effort": effort}
		if effort != "none" {
			reasoning["summary"] = "auto"
		}
		return reasoning, nil
	}
}

// thinking 读 thinking 字段：类型和预算。
func (p *preparer) thinking() (typ string, budget int, ok bool, err error) {
	raw, exists := p.body["thinking"]
	if !exists || isNullJSON(raw) {
		return "", 0, false, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", 0, false, newError(CodeInvalidInput, "thinking", "thinking 格式不对")
	}
	for key := range config {
		switch key {
		case "type", "budget_tokens":
		case "display":
			// 展示方式只影响客户端怎么显示，转接层不管。
			p.diagf("field_ignored", "thinking.display", "Responses 上游没有思考展示设置，这段 display 已忽略")
		default:
			return "", 0, false, newError(CodeUnsupportedField, "thinking."+key,
				"thinking 的 %s 不能转成 Responses 上游（支持 type、budget_tokens）", key)
		}
	}
	var thinkingType string
	if err := json.Unmarshal(config["type"], &thinkingType); err != nil || thinkingType == "" {
		return "", 0, false, newError(CodeInvalidInput, "thinking.type", "thinking 缺少 type 字段")
	}
	if raw, exists := config["budget_tokens"]; exists {
		if err := json.Unmarshal(raw, &budget); err != nil {
			return "", 0, false, newError(CodeInvalidInput, "thinking.budget_tokens", "thinking.budget_tokens 格式不对")
		}
	}
	return thinkingType, budget, true, nil
}

// outputConfigEffort 读 output_config.effort。output_config 里别的字段没法表达，报错。
func (p *preparer) outputConfigEffort() (string, bool, error) {
	raw, exists := p.body["output_config"]
	if !exists || isNullJSON(raw) {
		return "", false, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", false, newError(CodeInvalidInput, "output_config", "output_config 格式不对")
	}
	for key := range config {
		if key != "effort" {
			return "", false, newError(CodeUnsupportedField, "output_config."+key,
				"output_config 的 %s 不能转成 Responses 上游（只支持 effort）", key)
		}
	}
	var effort string
	if err := json.Unmarshal(config["effort"], &effort); err != nil {
		return "", false, newError(CodeInvalidInput, "output_config.effort", "output_config.effort 格式不对")
	}
	if effort == "" {
		return "", false, newError(CodeInvalidInput, "output_config.effort", "output_config.effort 是空的")
	}
	if !reasoningEfforts[effort] {
		return "", false, newError(CodeUnsupportedEffort, "output_config.effort",
			"推理强度 %s 不是 Responses 支持的档位（支持 none、minimal、low、medium、high、xhigh、max）", effort)
	}
	return effort, true, nil
}

// effortForBudget 把 Anthropic 的思考预算换算成 Responses 的推理档位。
// 两家的预算不等价：预算只决定"想多久"，档位决定"想多深"，所以这里只做档位近似，
// 实际思考量仍由上游模型决定，诊断里写清楚。
func effortForBudget(budget int) string {
	switch {
	case budget < 4096:
		return "low"
	case budget < 16384:
		return "medium"
	default:
		return "high"
	}
}

// mapSampling 保留两种协议共有的采样参数，由具体上游型号验证支持范围。
func (p *preparer) mapSampling(out map[string]any) error {
	if raw, ok := p.body["stop_sequences"]; ok && !isNullJSON(raw) {
		var stops []string
		if err := json.Unmarshal(raw, &stops); err != nil {
			return newError(CodeInvalidInput, "stop_sequences", "stop_sequences 格式不对")
		}
		if len(stops) > 0 {
			return newError(CodeUnsupportedField, "stop_sequences",
				"Responses 上游没有 stop_sequences，请去掉该字段或改用原生 Messages 渠道")
		}
	}
	if raw, ok := p.body["top_k"]; ok && !isNullJSON(raw) {
		var topK int64
		if err := json.Unmarshal(raw, &topK); err != nil {
			return newError(CodeInvalidInput, "top_k", "top_k 格式不对")
		}
		if topK != 0 {
			return newError(CodeUnsupportedField, "top_k",
				"Responses 上游没有 top_k，请去掉该字段或改用原生 Messages 渠道")
		}
	}
	for _, key := range []string{"temperature", "top_p"} {
		raw, ok := p.body[key]
		if !ok || isNullJSON(raw) {
			continue
		}
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return newError(CodeInvalidInput, key, "%s 格式不对", key)
		}
		// 原样透传：支不支持由上游型号和上游实现决定，本层不替上游猜。
		// 有的推理型号会拒绝 reasoning 搭配采样参数，那种情况让上游自己报 400 更准。
		if p.effort != "" && p.effort != "none" {
			p.diagf("sampling_kept", key,
				"这次请求开了思考（effort=%s），采样参数 %s 仍按客户端原值发出；上游型号若不接受会返回 400",
				p.effort, key)
		}
		out[key] = value
	}
	return nil
}

// mapMetadata 转 metadata。Anthropic 只有 user_id，Responses 的 metadata 是键值对。
func (p *preparer) mapMetadata(out map[string]any) error {
	raw, ok := p.body["metadata"]
	if !ok || isNullJSON(raw) {
		return nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return newError(CodeInvalidInput, "metadata", "metadata 格式不对")
	}
	converted := map[string]any{}
	for key, value := range metadata {
		if key != "user_id" {
			return newError(CodeUnsupportedField, "metadata."+key,
				"metadata 的 %s 不能转成 Responses 上游（只支持 user_id）", key)
		}
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return newError(CodeInvalidInput, "metadata.user_id", "metadata.user_id 必须是字符串")
		}
		converted[key] = s
	}
	if len(converted) > 0 {
		out["metadata"] = converted
	}
	return nil
}

// mapServiceTier 转档位。
// Anthropic 的档位和 OpenAI 的不是一回事：只有 auto 两边通用，
// standard 换成 OpenAI 的 default，其余（fast、priority 等）直接报错，
// 免得把灵枢的档位语义和上游的档位语义搅在一起。
func (p *preparer) mapServiceTier(out map[string]any) error {
	tier, err := p.stringField("service_tier")
	if err != nil || tier == "" {
		return err
	}
	switch tier {
	case "auto":
		out["service_tier"] = tier
		return nil
	case "standard":
		p.diagf("field_ignored", "service_tier",
			"Anthropic 的 standard 档位按 Responses 上游的 default 档位处理")
		out["service_tier"] = "default"
		return nil
	default:
		return newError(CodeUnsupportedField, "service_tier",
			"档位 %s 不能转成 Responses 上游（支持 auto、standard），请去掉该字段或改用原生 Messages 渠道", tier)
	}
}

// validToolName 判断名字能不能直接给上游用。
func validToolName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// sanitizeToolName 把名字收拾成上游接受的字符集。
func sanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "tool"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// isNullJSON 判断是不是 JSON 的 null。
func isNullJSON(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// block 是 Anthropic 消息里的一种内容块。
type block struct {
	Type string `json:"type"`
	Text string `json:"text"`

	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`

	// thinking / redacted_thinking
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
	Data      string `json:"data"`

	// image / document
	Source       *source         `json:"source"`
	Title        string          `json:"title"`
	Citations    json.RawMessage `json:"citations"`
	Context      json.RawMessage `json:"context"`
	CacheControl json.RawMessage `json:"cache_control"`
}

// source 是图片或文档的来源。
type source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
}
