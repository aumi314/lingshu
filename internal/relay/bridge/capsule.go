package bridge

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const (
	// capsulePrefix 是思考块的封装前缀：带版本、自包含、只认本层生成的。
	capsulePrefix = "lingshu-bridge/v1:"
	// compactionPrefix 是压缩摘要的封装前缀。
	compactionPrefix = "lingshu-bridge/compact/v1:"
	// capsuleSource 写进封装里，出问题时能看出是谁生成的。
	capsuleSource = "lingshu-bridge"
	// capsuleProvider 是上游思考块的来源厂商。
	capsuleProvider = "anthropic"
)

// capsule 是思考块的封装内容。
// 注意：这是自包含的编码，不是加密，别在对外文案里说加密。
type capsule struct {
	Version  int              `json:"v"`
	Source   string           `json:"source"`
	Provider string           `json:"provider"`
	Model    string           `json:"model"`
	Blocks   []map[string]any `json:"blocks"`
}

// sealCapsule 把思考块封成不透明字符串。
func sealCapsule(c capsule) string {
	c.Version = 1
	c.Source = capsuleSource
	c.Provider = capsuleProvider
	encoded, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return capsulePrefix + base64.RawURLEncoding.EncodeToString(encoded)
}

// decodeCapsule 展开思考块封装。外来内容显式报错，不猜。
// model 是本次请求的上游型号：思考块带签名，换型号回放上游会拒，所以型号要对得上。
func decodeCapsule(value string, model string) ([]map[string]any, error) {
	if !strings.HasPrefix(value, capsulePrefix) {
		return nil, newError(CodeForeignEncrypted, "input.encrypted_content",
			"这段不透明的推理内容不是本转接生成的，无法还原成 Messages 的 thinking 块")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, capsulePrefix))
	if err != nil {
		return nil, newError(CodeForeignEncrypted, "input.encrypted_content", "推理内容解码失败")
	}
	var c capsule
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, newError(CodeForeignEncrypted, "input.encrypted_content", "推理内容格式不对")
	}
	if c.Version != 1 || c.Source != capsuleSource || c.Provider != capsuleProvider {
		return nil, newError(CodeForeignEncrypted, "input.encrypted_content", "推理内容的版本或来源不是本转接支持的")
	}
	if !sameModelFamily(c.Model, model) {
		return nil, newError(CodeForeignEncrypted, "input.encrypted_content",
			"推理内容来自模型 %s，当前模型是 %s，签名对不上不能回放", c.Model, model)
	}
	for _, block := range c.Blocks {
		if err := validateCapsuleBlock(block); err != nil {
			return nil, err
		}
	}
	return c.Blocks, nil
}

// sameModelFamily 判断两个模型名是不是同一个型号（允许带日期后缀的差异）。
func sameModelFamily(a, b string) bool {
	left, right := normalizeModel(a), normalizeModel(b)
	if left == "" || right == "" {
		// 有一边没记录型号就比不了，签名对不对交给上游验。
		return true
	}
	return left == right || strings.HasPrefix(left, right+"-") || strings.HasPrefix(right, left+"-")
}

// validateCapsuleBlock 校验封装里的思考块：只认 thinking 和 redacted_thinking，字段不能缺。
func validateCapsuleBlock(block map[string]any) error {
	switch strField(block, "type") {
	case "thinking":
		text, ok := block["thinking"].(string)
		if !ok || text == "" {
			return newError(CodeForeignEncrypted, "input.encrypted_content", "推理内容里的 thinking 块缺内容")
		}
		signature, ok := block["signature"].(string)
		if !ok || signature == "" {
			return newError(CodeForeignEncrypted, "input.encrypted_content", "推理内容里的 thinking 块缺签名")
		}
	case "redacted_thinking":
		data, ok := block["data"].(string)
		if !ok || data == "" {
			return newError(CodeForeignEncrypted, "input.encrypted_content", "推理内容里的 redacted_thinking 块缺内容")
		}
	default:
		return newError(CodeForeignEncrypted, "input.encrypted_content",
			"推理内容里出现不支持的块 %s", strField(block, "type"))
	}
	return nil
}

// SealCompaction 把摘要封成版本化、自包含的内容。
// 客户端把它存进历史，下次回灌时用 ExpandCompaction 展开。
func SealCompaction(summary string) string {
	return compactionPrefix + base64.RawURLEncoding.EncodeToString([]byte(summary))
}

// ExpandCompaction 展开本层封的压缩摘要。别的来源显式报错。
func ExpandCompaction(value string) (string, error) {
	if !strings.HasPrefix(value, compactionPrefix) {
		return "", newError(CodeForeignCompaction, "input.encrypted_content",
			"这段不透明的压缩内容不是本转接生成的，无法展开成对话摘要")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, compactionPrefix))
	if err != nil {
		return "", newError(CodeForeignCompaction, "input.encrypted_content", "压缩内容解码失败")
	}
	return string(raw), nil
}
