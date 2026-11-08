package bridge

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// preparer 是请求侧转换的工作区。
type preparer struct {
	body  map[string]json.RawMessage
	model string
	tools *toolTable
	diags []Diagnostic

	system   []any
	messages []map[string]any

	pendingThinking []map[string]any  // 还没挂到助手消息上的思考块
	toolUses        map[string]string // call_id → Anthropic tool_use id
	toolKinds       map[string]bool   // call_id → custom tool
	toolUseOrder    []string          // 工具调用出现的顺序
	answered        map[string]bool   // 已经配好结果的 tool_use id
	compact         bool
}

// inputItem 是 Responses 输入项的公共字段。
type inputItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Namespace        string          `json:"namespace"`
	Arguments        string          `json:"arguments"`
	Input            string          `json:"input"`
	Output           json.RawMessage `json:"output"`
	EncryptedContent string          `json:"encrypted_content"`
	Tools            json.RawMessage `json:"tools"`
}

// inputItems 解析请求里的 input。
func (p *preparer) inputItems() ([]inputItem, error) {
	raw, ok := p.body["input"]
	if !ok || isNullJSON(raw) {
		return nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		encoded, _ := json.Marshal(asString)
		return []inputItem{{Type: "message", Role: "user", Content: encoded}}, nil
	}
	var items []inputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, newError(CodeInvalidInput, "input", "input 既不是字符串也不是数组")
	}
	return items, nil
}

// buildMessages 把输入项转成 Messages 的消息序列。
func (p *preparer) buildMessages(items []inputItem) error {
	p.toolUses = map[string]string{}
	p.toolKinds = map[string]bool{}
	p.answered = map[string]bool{}
	for _, it := range items {
		if it.Type == "" && it.Role != "" {
			// Responses 的消息对象常常不带 type，只有 role。
			it.Type = "message"
		}
		switch it.Type {
		case "message":
			if err := p.addMessage(it); err != nil {
				return err
			}
		case "function_call":
			if err := p.addFunctionCall(it, false); err != nil {
				return err
			}
		case "custom_tool_call":
			if err := p.addFunctionCall(it, true); err != nil {
				return err
			}
		case "function_call_output", "custom_tool_call_output":
			if err := p.addToolOutput(it); err != nil {
				return err
			}
		case "reasoning":
			if err := p.addReasoning(it); err != nil {
				return err
			}
		case "compaction":
			if err := p.addCompaction(it); err != nil {
				return err
			}
		case "compaction_trigger":
			p.compact = true
		case "additional_tools":
			// 工具在第一遍已经收进工具表，这里不用再管。
		default:
			// 不认识的输入项不能悄悄丢：历史少一段，模型会顺着错的前提往下答。
			return newError(CodeUnsupportedInput, "input",
				"输入项 %s 暂时不能转成 Messages，请关掉对应功能或改用原生 Responses 渠道", it.Type)
		}
	}
	if len(p.pendingThinking) > 0 {
		pending := p.pendingThinking
		p.pendingThinking = nil
		p.appendMsg("assistant", pending)
	}
	if err := p.checkToolResults(); err != nil {
		return err
	}
	return nil
}

// checkToolResults 检查工具调用和结果是不是成套。
// Anthropic 要求每个 tool_use 都紧跟一个 tool_result，缺了上游会直接 400；
// 这种历史也不能编一个空结果糊弄过去，直接报错。
func (p *preparer) checkToolResults() error {
	for _, useID := range p.toolUseOrder {
		if !p.answered[useID] {
			return newError(CodeMissingToolOutput, "input",
				"工具调用 %s 没有对应的工具结果，调用和结果必须成套出现", useID)
		}
	}
	return nil
}

// addMessage 转一条消息。developer/system 归到 system，其余进对话。
func (p *preparer) addMessage(it inputItem) error {
	blocks, err := p.contentBlocks(it.Content)
	if err != nil {
		return err
	}
	switch it.Role {
	case "system", "developer":
		for _, b := range blocks {
			if b["type"] != "text" {
				// system 里的非文本内容上游接不住，丢掉就是改指令，明确报错。
				return newError(CodeUnsupportedContent, "input",
					"system/developer 消息里的 %s 内容不能转成 Messages", b["type"])
			}
			p.system = append(p.system, map[string]any{"type": "text", "text": b["text"]})
		}
		return nil
	case "user", "assistant":
		if len(blocks) == 0 {
			p.diagf("empty_message", "input", "%s 消息没有内容，已忽略", it.Role)
			return nil
		}
		p.appendMsg(it.Role, blocks)
		return nil
	default:
		return newError(CodeInvalidInput, "input.role", "不支持的消息角色 %s", it.Role)
	}
}

// contentBlocks 把 Responses 的 content 转成 Anthropic 的内容块。
func (p *preparer) contentBlocks(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 || isNullJSON(raw) {
		return nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return nil, nil
		}
		return []map[string]any{{"type": "text", "text": asString}}, nil
	}
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, newError(CodeInvalidInput, "input", "消息 content 既不是字符串也不是数组")
	}
	blocks := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch strField(part, "type") {
		case "input_text", "output_text", "text", "summary_text":
			text := strField(part, "text")
			if text == "" {
				continue
			}
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		case "refusal":
			text := strField(part, "refusal")
			if text == "" {
				continue
			}
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		case "input_image":
			block, err := p.imageBlock(part)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		case "input_file":
			block, err := p.fileBlock(part)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		default:
			// 不认识的内容块不能悄悄丢：用户的数据会少一截。
			return nil, newError(CodeUnsupportedContent, "input",
				"内容块 %s 不能转成 Messages，请改用纯文本或图片", strField(part, "type"))
		}
	}
	return blocks, nil
}

// imageBlock 把 input_image 转成 Anthropic 的 image 块。
func (p *preparer) imageBlock(part map[string]any) (map[string]any, error) {
	url := strField(part, "image_url")
	if url == "" {
		return nil, newError(CodeUnsupportedContent, "input", "图片缺少 image_url")
	}
	if strings.HasPrefix(url, "data:") {
		meta, data, ok := strings.Cut(url, ",")
		if !ok {
			return nil, newError(CodeUnsupportedContent, "input", "图片 data URL 格式不对")
		}
		mediaType := strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64")
		if mediaType == "" {
			mediaType = "image/png"
		}
		source := map[string]any{"type": "base64", "media_type": mediaType, "data": data}
		return map[string]any{"type": "image", "source": source}, nil
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		source := map[string]any{"type": "url", "url": url}
		return map[string]any{"type": "image", "source": source}, nil
	}
	return nil, newError(CodeUnsupportedContent, "input", "图片地址不是 data URL 也不是 http(s)")
}

// fileBlock 把 input_file 转成 Anthropic 的 document 块。
func (p *preparer) fileBlock(part map[string]any) (map[string]any, error) {
	data := strField(part, "file_data")
	if data == "" {
		return nil, newError(CodeUnsupportedContent, "input", "文件没有 file_data，无法转成 Messages")
	}
	meta, payload, ok := strings.Cut(data, ",")
	if !ok {
		return nil, newError(CodeUnsupportedContent, "input", "文件 data URL 格式不对")
	}
	mediaType := strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64")
	if mediaType == "" {
		mediaType = "application/pdf"
	}
	source := map[string]any{"type": "base64", "media_type": mediaType, "data": payload}
	return map[string]any{"type": "document", "source": source}, nil
}

// addFunctionCall 转一条工具调用。custom 为真时输入是自由文本。
func (p *preparer) addFunctionCall(it inputItem, custom bool) error {
	if it.Name == "" {
		return newError(CodeInvalidInput, "input.name", "工具调用缺少名称")
	}
	if _, exists := p.toolUses[it.CallID]; exists {
		return newError(CodeInvalidInput, "input.call_id", "重复的工具调用 ID %s", it.CallID)
	}
	if it.Namespace != "" {
		it.Name = it.Namespace + "." + it.Name
	}
	info, ok := p.tools.byClient[it.Name]
	if !ok {
		// 这次请求没声明这个工具（Codex 摘要请求会把工具清空），但历史里的调用
		// 仍然是真实发生过的事：按历史项自己补一条只用于回放的工具，不发给上游。
		info = p.tools.addHistoryOnly(it.Name, custom)
		p.diagf("tool_history_only", "tools."+it.Name,
			"历史里的工具 %s 本次没声明，按历史回放，不发给上游执行", it.Name)
	}
	if it.CallID == "" {
		return newError(CodeInvalidInput, "input", "工具调用 %s 缺少 call_id", it.Name)
	}
	if info.custom != custom {
		return newError(CodeInvalidInput, "input.type", "工具 %s 的调用类型与声明不一致", it.Name)
	}
	input := map[string]any{}
	if custom {
		// 自由文本工具：原始输入放在 input 字段里。
		input["input"] = it.Input
	} else {
		if raw := strings.TrimSpace(it.Arguments); raw != "" {
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				return newError(CodeInvalidArguments, "input", "工具 %s 的参数不是合法 JSON 对象", it.Name)
			}
		}
		if input == nil {
			return newError(CodeInvalidArguments, "input.arguments", "工具 %s 的参数必须是 JSON 对象", it.Name)
		}
	}
	block := map[string]any{"type": "tool_use", "id": toolUseID(it.CallID), "name": info.anthropicName, "input": input}
	p.appendMsg("assistant", []map[string]any{block})
	p.toolUses[it.CallID] = toolUseID(it.CallID)
	p.toolKinds[it.CallID] = custom
	p.toolUseOrder = append(p.toolUseOrder, toolUseID(it.CallID))
	return nil
}

// addToolOutput 转一条工具结果。
func (p *preparer) addToolOutput(it inputItem) error {
	useID, ok := p.toolUses[it.CallID]
	if !ok {
		return newError(CodeOrphanToolOutput, "input", "工具结果 %s 没有对应的工具调用", it.CallID)
	}
	if p.toolKinds[it.CallID] != (it.Type == "custom_tool_call_output") {
		return newError(CodeInvalidInput, "input.type", "工具结果 %s 的类型与调用不一致", it.CallID)
	}
	if len(it.Output) == 0 {
		return newError(CodeInvalidInput, "input.output", "工具结果 %s 缺少 output", it.CallID)
	}
	if p.answered[useID] {
		return newError(CodeOrphanToolOutput, "input.call_id", "工具调用 %s 的结果重复", it.CallID)
	}
	blocks, err := p.contentBlocks(it.Output)
	if err != nil {
		return err
	}
	if len(blocks) == 0 {
		blocks = []map[string]any{{"type": "text", "text": ""}}
	}
	result := map[string]any{"type": "tool_result", "tool_use_id": useID, "content": blocks}
	p.appendResult("user", []map[string]any{result})
	p.answered[useID] = true
	return nil
}

// addReasoning 回放思考块。只有本层封的才认，别的来源显式报错。
func (p *preparer) addReasoning(it inputItem) error {
	if it.EncryptedContent == "" {
		p.diagf("reasoning_dropped", "input", "推理项没有加密载荷，已忽略")
		return nil
	}
	blocks, err := decodeCapsule(it.EncryptedContent, p.model)
	if err != nil {
		return err
	}
	p.pendingThinking = append(p.pendingThinking, blocks...)
	return nil
}

// addCompaction 展开压缩摘要，放回对话。
func (p *preparer) addCompaction(it inputItem) error {
	summary, err := ExpandCompaction(it.EncryptedContent)
	if err != nil {
		return err
	}
	if strings.TrimSpace(summary) == "" {
		return newError(CodeForeignCompaction, "input", "压缩摘要是空的")
	}
	p.appendMsg("user", []map[string]any{{"type": "text", "text": compactionTextPrefix + summary}})
	return nil
}

// appendMsg 追加一条消息：同角色的相邻消息合成一条（Anthropic 要求角色交替）。
func (p *preparer) appendMsg(role string, blocks []map[string]any) {
	if len(p.pendingThinking) > 0 {
		if role == "assistant" {
			blocks = append(append([]map[string]any{}, p.pendingThinking...), blocks...)
			p.pendingThinking = nil
		} else {
			// 思考块必须挂在助手消息上，单独补一条。
			pending := p.pendingThinking
			p.pendingThinking = nil
			p.appendMsg("assistant", pending)
		}
	}
	if n := len(p.messages); n > 0 && p.messages[n-1]["role"] == role {
		existing, _ := p.messages[n-1]["content"].([]map[string]any)
		p.messages[n-1]["content"] = append(existing, blocks...)
		return
	}
	p.messages = append(p.messages, map[string]any{"role": role, "content": blocks})
}

// appendResult 追加工具结果：结果块必须排在用户消息最前面。
func (p *preparer) appendResult(role string, blocks []map[string]any) {
	if n := len(p.messages); n > 0 && p.messages[n-1]["role"] == role {
		existing, _ := p.messages[n-1]["content"].([]map[string]any)
		at := 0
		for at < len(existing) && existing[at]["type"] == "tool_result" {
			at++
		}
		content := make([]map[string]any, 0, len(existing)+len(blocks))
		content = append(content, existing[:at]...)
		content = append(content, blocks...)
		content = append(content, existing[at:]...)
		p.messages[n-1]["content"] = content
		return
	}
	p.messages = append(p.messages, map[string]any{"role": role, "content": blocks})
}

// toolUseID 把客户端的 call_id 变成 Anthropic 的 tool_use id。
// 常见形态（call_xxx、toolu_xxx）原样保留；其他字符用 base64 无损编码，避免不同 id 撞成同一个。
func toolUseID(callID string) string {
	if validToolUseID(callID) {
		return callID
	}
	return "toolu_" + base64.RawURLEncoding.EncodeToString([]byte(callID))
}

// validToolUseID 判断 id 能不能原样给上游用（字母、数字、下划线、横线，64 字符内）。
func validToolUseID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// defaultMaxTokens 是客户端没给 max_output_tokens 时的兜底。
const defaultMaxTokens = 8192

// maxTokens 取上游的 max_tokens，客户端没给就用 8192；给了但不像话就报错。
func (p *preparer) maxTokens() (int, error) {
	raw, ok := p.body["max_output_tokens"]
	if !ok {
		return defaultMaxTokens, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, newError(CodeInvalidInput, "max_output_tokens", "max_output_tokens 必须是正整数")
	}
	if n <= 0 {
		return 0, newError(CodeInvalidInput, "max_output_tokens", "max_output_tokens 必须是正整数")
	}
	return n, nil
}

// toolChoice 把 Responses 的 tool_choice 转成 Anthropic 的。
func (p *preparer) toolChoice() (any, error) {
	raw, ok := p.body["tool_choice"]
	if !ok || isNullJSON(raw) {
		return nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		switch asString {
		case "auto":
			return p.autoOrAny("auto"), nil
		case "none":
			return map[string]any{"type": "none"}, nil
		case "required":
			return p.autoOrAny("any"), nil
		default:
			return nil, newError(CodeInvalidInput, "tool_choice", "tool_choice %s 不能转成 Messages", asString)
		}
	}
	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err != nil {
		return nil, newError(CodeInvalidInput, "tool_choice", "tool_choice 格式不对")
	}
	name := strField(asObject, "name")
	if name == "" {
		return nil, newError(CodeInvalidInput, "tool_choice", "tool_choice 缺少工具名")
	}
	info, ok := p.tools.byClient[name]
	if !ok {
		return nil, newError(CodeUnknownTool, "tool_choice", "tool_choice 里的工具 %s 不在工具列表里", name)
	}
	return map[string]any{"type": "tool", "name": info.anthropicName}, nil
}

// autoOrAny 按 parallel_tool_calls 决定要不要禁并行工具调用。
func (p *preparer) autoOrAny(kind string) map[string]any {
	choice := map[string]any{"type": kind}
	if raw, ok := p.body["parallel_tool_calls"]; ok {
		var parallel bool
		if err := json.Unmarshal(raw, &parallel); err == nil && !parallel {
			choice["disable_parallel_tool_use"] = true
		}
	}
	return choice
}

// thinking 把 reasoning.effort 转成 Anthropic 的 thinking 和 output_config。
func (p *preparer) thinking() (map[string]any, map[string]any, error) {
	raw, ok := p.body["reasoning"]
	if !ok || isNullJSON(raw) {
		return nil, nil, nil
	}
	var reasoning struct {
		Effort  string `json:"effort"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &reasoning); err != nil {
		return nil, nil, newError(CodeInvalidInput, "reasoning", "reasoning 格式不对")
	}
	if reasoning.Effort == "" {
		return nil, nil, nil
	}
	plan, err := planEffort(p.model, reasoning.Effort)
	if err != nil {
		return nil, nil, err
	}
	p.diagf("thinking", "reasoning.effort", "%s", describeEffort(plan))
	return plan.thinking, plan.outputConfig, nil
}

// describeTools 把工具表写进诊断，方便排查改名和降级。
func (p *preparer) describeTools() {
	for _, info := range p.tools.list {
		if info.clientName != info.anthropicName {
			p.diagf("tool_renamed", "tools."+info.clientName,
				"工具 %s 在上游改名为 %s", info.clientName, info.anthropicName)
		}
		if info.custom {
			p.diagf("custom_tool_mapped", "tools."+info.clientName,
				"自由文本工具 %s 转成 input 字符串参数", info.clientName)
		}
	}
}
