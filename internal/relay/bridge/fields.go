package bridge

// honoredFields 是本层真的用上了的请求字段。
var honoredFields = map[string]bool{
	"model": true, "input": true, "instructions": true, "tools": true,
	"tool_choice": true, "parallel_tool_calls": true, "reasoning": true,
	"max_output_tokens": true, "text": true, "stream": true,
}

// ignoredFields 是用不上但无害的字段：丢掉可以，但要在诊断里说清楚。
var ignoredFields = map[string]string{
	"include":                "本层总会带上自己的不透明推理内容（reasoning.encrypted_content），客户端的 include 按这个处理",
	"metadata":               "Messages 没有 metadata 透传，已忽略",
	"prompt_cache_key":       "Messages 的缓存按上游账号算，没有这个字段，已忽略",
	"client_metadata":        "上游用不上，已忽略",
	"service_tier":           "档位由灵枢按渠道定，客户端的 service_tier 不透传",
	"temperature":            "Claude 开思考时只认默认采样参数，已忽略",
	"top_p":                  "Claude 开思考时只认默认采样参数，已忽略",
	"user":                   "上游用不上，已忽略",
	"stream_options":         "上游用不上，已忽略",
	"safety_identifier":      "上游用不上，已忽略",
	"truncation":             "上游用不上，已忽略",
	"max_tool_calls":         "上游用不上，已忽略",
	"logprobs":               "上游用不上，已忽略",
	"top_logprobs":           "上游用不上，已忽略",
	"prompt_cache_retention": "上游用不上，已忽略",
	"store":                  "服务端保存历史这个功能不支持，客户端给的是 false，不影响本次转换",
}
