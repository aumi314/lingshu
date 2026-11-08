package bridge

// usageInfo 是上游 Messages 的用量。
type usageInfo struct {
	inputTokens  int64
	outputTokens int64
	cacheRead    int64
	cacheWrite   int64
	reasoning    int64
}

// merge 吸收一条 usage。字段出现就覆盖，没出现就保留旧值，
// 跟上游原始字段保持一致（后到的字段赢，哪怕值更小或者是 0）。
func (u *usageInfo) merge(raw any) {
	m, _ := raw.(map[string]any)
	if m == nil {
		return
	}
	if v, ok := m["input_tokens"]; ok {
		u.inputTokens = int64Field(map[string]any{"input_tokens": v}, "input_tokens")
	}
	if v, ok := m["output_tokens"]; ok {
		u.outputTokens = int64Field(map[string]any{"output_tokens": v}, "output_tokens")
	}
	if v, ok := m["cache_read_input_tokens"]; ok {
		u.cacheRead = int64Field(map[string]any{"cache_read_input_tokens": v}, "cache_read_input_tokens")
	}
	if v, ok := m["cache_creation_input_tokens"]; ok {
		u.cacheWrite = int64Field(map[string]any{"cache_creation_input_tokens": v}, "cache_creation_input_tokens")
	}
	if details, ok := m["output_tokens_details"].(map[string]any); ok {
		if v, ok := details["thinking_tokens"]; ok {
			u.reasoning = int64Field(map[string]any{"thinking_tokens": v}, "thinking_tokens")
		}
	}
}

// responses 转成 Responses 的 usage。Anthropic 的 input_tokens 不含缓存部分，
// Responses 的 input_tokens 是整段输入，所以要把缓存加回来。
func (u usageInfo) responses() map[string]any {
	input := u.inputTokens + u.cacheRead + u.cacheWrite
	return map[string]any{
		"input_tokens":          input,
		"input_tokens_details":  map[string]any{"cached_tokens": u.cacheRead},
		"output_tokens":         u.outputTokens,
		"output_tokens_details": map[string]any{"reasoning_tokens": u.reasoning},
		"total_tokens":          input + u.outputTokens,
	}
}

// int64Field 取整数字段。
func int64Field(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// maxInt64 取大值。
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
