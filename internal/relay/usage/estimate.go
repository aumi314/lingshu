package usage

import (
	"encoding/json"
	"unicode"
)

// EstimateText 粗估一段文字的 token 数：中日韩文字每字 1 个，其他字符每 4 字节 1 个。
// 只在上游没给 usage 时兜底用，结果会标成"估算"。各家分词器都不同，没有必要更精确。
func EstimateText(s string) int64 {
	var cjk, other int64
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			cjk++
		} else {
			other += int64(len(string(r)))
		}
	}
	return cjk + (other+3)/4
}

// EstimateRequest 粗估请求的输入 token：请求体里所有字符串值（model 除外）。
func EstimateRequest(body []byte) int64 {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return EstimateText(string(body))
	}
	var n int64
	walkStrings(v, "", func(key, s string) {
		if key != "model" {
			n += EstimateText(s)
		}
	})
	return n
}

// 流里承载生成内容的字段：正文、推理、工具调用参数。
var outputKeys = map[string]bool{
	"content": true, "text": true, "reasoning_content": true, "thinking": true,
	"partial_json": true, "arguments": true, "delta": true, "refusal": true,
}

// estimateOutput 粗估一个事件（或一个完整响应）里生成内容的 token。
func estimateOutput(data []byte) int64 {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return 0
	}
	var n int64
	walkStrings(v, "", func(key, s string) {
		if outputKeys[key] {
			n += EstimateText(s)
		}
	})
	return n
}

func walkStrings(v any, key string, fn func(key, s string)) {
	switch t := v.(type) {
	case string:
		fn(key, t)
	case map[string]any:
		for k, child := range t {
			walkStrings(child, k, fn)
		}
	case []any:
		for _, child := range t {
			walkStrings(child, key, fn)
		}
	}
}
