package bridge

import (
	"encoding/json"
	"strings"
)

// toolInfo 是一条工具在两边的对应关系。
type toolInfo struct {
	anthropicName string // 上游看到的名字
	clientName    string // 客户端看到的名字
	namespace     string
	custom        bool // 自由文本工具（Responses 的 custom）
}

// toolTable 是本次请求的工具表。
type toolTable struct {
	anthropic   []map[string]any // 上游的 tools 数组
	byAnthropic map[string]toolInfo
	byClient    map[string]toolInfo
	list        []toolInfo
	used        map[string]bool
}

func newToolTable() *toolTable {
	return &toolTable{
		byAnthropic: map[string]toolInfo{},
		byClient:    map[string]toolInfo{},
		used:        map[string]bool{},
	}
}

// collectTools 收工具：请求体里的 tools 和 input 里的 additional_tools 都算。
func (p *preparer) collectTools(items []inputItem) error {
	if raw, ok := p.body["tools"]; ok && !isNullJSON(raw) {
		var defs []map[string]any
		if err := json.Unmarshal(raw, &defs); err != nil {
			return newError(CodeInvalidInput, "tools", "tools 格式不对")
		}
		if err := p.addTools(defs, ""); err != nil {
			return err
		}
	}
	for _, it := range items {
		if it.Type != "additional_tools" {
			continue
		}
		var defs []map[string]any
		if err := json.Unmarshal(it.Tools, &defs); err != nil {
			return newError(CodeInvalidInput, "input", "additional_tools 里的工具列表格式不对")
		}
		if err := p.addTools(defs, ""); err != nil {
			return err
		}
	}
	p.describeTools()
	return nil
}

// addTools 转一批工具定义。namespace 会摊平成带前缀的独立工具。
func (p *preparer) addTools(defs []map[string]any, ns string) error {
	for _, def := range defs {
		switch strField(def, "type") {
		case "function":
			if err := p.addTool(def, ns, false); err != nil {
				return err
			}
		case "custom":
			if err := p.addTool(def, ns, true); err != nil {
				return err
			}
		case "namespace":
			name := strField(def, "name")
			if name == "" {
				return newError(CodeUnsupportedTool, "tools", "namespace 工具缺少 name")
			}
			nested, _ := def["tools"].([]any)
			if nested == nil {
				return newError(CodeUnsupportedTool, "tools."+name, "namespace %s 里的工具列表格式不对", name)
			}
			for _, raw := range nested {
				sub, _ := raw.(map[string]any)
				switch strField(sub, "type") {
				case "function":
					if err := p.addTool(sub, name, false); err != nil {
						return err
					}
				case "custom":
					if err := p.addTool(sub, name, true); err != nil {
						return err
					}
				default:
					return newError(CodeUnsupportedTool, "tools."+name,
						"namespace %s 里的工具类型 %s 不能转成 Messages（只支持 function、custom）", name, strField(sub, "type"))
				}
			}
		case "web_search", "web_search_preview":
			// Codex 默认每个请求都带联网搜索（OpenAI 服务端执行的工具），Messages 上游没有等价物。
			// 去掉只是这一轮不能搜索，对话内容不变；整个请求拒掉的话默认配置的 Codex 完全用不了
			p.diagf("tool_dropped", "tools", "联网搜索工具 %s 在 Messages 上游不可用，已去掉，模型这轮不能联网搜索", strField(def, "type"))
		default:
			return newError(CodeUnsupportedTool, "tools",
				"工具类型 %s 不能转成 Messages（只支持 function、custom、namespace）", strField(def, "type"))
		}
	}
	return nil
}

// addTool 转一条工具定义。
func (p *preparer) addTool(def map[string]any, ns string, custom bool) error {
	name := strField(def, "name")
	if name == "" {
		return newError(CodeUnsupportedTool, "tools", "工具定义缺少 name")
	}
	clientName := name
	if ns != "" {
		clientName = ns + "." + name
	}
	anthropicName := p.tools.uniqueName(sanitizeName(clientName, 64))
	description := strField(def, "description")
	var tool map[string]any
	if custom {
		// Anthropic 没有自由文本工具：把整段输入放进 input 字段。
		tool = map[string]any{
			"name":        anthropicName,
			"description": customToolDescription(description),
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{
						"type":        "string",
						"description": "The complete raw tool input. Preserve it exactly without parsing, rewriting, or explanations.",
					},
				},
				"required": []string{"input"},
			},
		}
	} else {
		schema := mapField(def, "parameters")
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tool = map[string]any{
			"name":         anthropicName,
			"description":  description,
			"input_schema": schema,
		}
	}
	p.tools.anthropic = append(p.tools.anthropic, tool)
	info := toolInfo{anthropicName: anthropicName, clientName: clientName, namespace: ns, custom: custom}
	p.tools.byAnthropic[anthropicName] = info
	p.tools.byClient[clientName] = info
	if ns != "" {
		// 命名空间里的工具再用短名登记一次，客户端回放短名也能对上。
		if _, exists := p.tools.byClient[name]; !exists {
			p.tools.byClient[name] = info
		}
	}
	p.tools.list = append(p.tools.list, info)
	return nil
}

// customToolDescription 给自由文本工具的描述补一句用法说明。
func customToolDescription(description string) string {
	const hint = "Pass the exact raw tool input in the input string field."
	if description == "" {
		return hint
	}
	return hint + "\n\n" + description
}

// uniqueName 保证上游工具名不重复。
func (t *toolTable) uniqueName(name string) string {
	if name == "" {
		name = "tool"
	}
	if !t.used[name] {
		t.used[name] = true
		return name
	}
	for i := 2; ; i++ {
		candidate := name + "_" + itoa(i)
		if !t.used[candidate] {
			t.used[candidate] = true
			return candidate
		}
	}
}

// addHistoryOnly 登记一条只用于回放历史的工具：本次请求没声明它，
// 也不发给上游（上游只能看到本次声明的工具），只是让历史里的调用能对上名字。
func (t *toolTable) addHistoryOnly(clientName string, custom bool) toolInfo {
	anthropicName := t.uniqueName(sanitizeName(clientName, 64))
	info := toolInfo{anthropicName: anthropicName, clientName: clientName, custom: custom}
	t.byClient[clientName] = info
	return info
}

// sanitizeName 把名字收拾成上游允许的字符集，并截到上限。
func sanitizeName(name string, limit int) string {
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
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// itoa 是小整数转字符串，避免为一个数字引入 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
