package messagesbridge

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const (
	// capsulePrefix 是 Responses reasoning 的封装前缀：带版本、自包含、只认本层生成的。
	capsulePrefix = "lingshu.responses.v1:"
	// capsuleSource 写进封装里，出问题时能看出是谁生成的。
	capsuleSource = "lingshu.responses"
	// capsuleVersion 是当前封装版本。
	capsuleVersion = 1
)

// capsule 是 reasoning 条目的封装内容。
// 注意：这是自包含的编码，不是加密，别在对外文案里说加密。
type capsule struct {
	Version    int             `json:"v"`
	Source     string          `json:"source"`
	Item       json.RawMessage `json:"item"`
	ResponseID string          `json:"response_id,omitempty"`
}

// SealReasoning 把上游返回的 reasoning 条目封成不透明字符串。
// 客户端把它存进 Messages 的 thinking.signature 或 redacted_thinking.data 里，
// 下次回灌时用 ExpandReasoning 展开成原始条目。
// 条目不是带加密内容的 reasoning（或者不是 JSON 对象）时返回空串，
// 由调用方决定是跳过还是报错。
func SealReasoning(item json.RawMessage) string { return sealReasoning(item, "") }
func sealReasoning(item json.RawMessage, responseID string) string {
	if !isReasoningItem(item) {
		return ""
	}
	c := capsule{Version: capsuleVersion, Source: capsuleSource, Item: item, ResponseID: responseID}
	encoded, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return capsulePrefix + base64.RawURLEncoding.EncodeToString(encoded)
}

// ExpandReasoning 展开本层封的 reasoning 条目。
// 不是本层生成的封装（含原生 Anthropic 签名）一律显式报错，不猜、不当普通文本丢掉。
func ExpandReasoning(value string) (json.RawMessage, error) {
	if !strings.HasPrefix(value, capsulePrefix) {
		return nil, newError(CodeForeignEncrypted, "signature",
			"这段不透明的推理内容不是本转接生成的，无法还原成 Responses 的 reasoning 条目")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, capsulePrefix))
	if err != nil {
		return nil, newError(CodeForeignEncrypted, "signature", "推理内容解码失败")
	}
	var c capsule
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, newError(CodeForeignEncrypted, "signature", "推理内容格式不对")
	}
	if c.Version != capsuleVersion || c.Source != capsuleSource {
		return nil, newError(CodeForeignEncrypted, "signature", "推理内容的版本或来源不是本转接支持的")
	}
	if !isReasoningItem(c.Item) {
		return nil, newError(CodeForeignEncrypted, "signature", "推理内容里缺少可回放的 reasoning 条目")
	}
	return c.Item, nil
}

// isReasoningItem 判断是不是带加密内容的 reasoning 条目。
// 只有这种条目能原样回灌给上游：摘要和加密内容都要一模一样。
func isReasoningItem(item json.RawMessage) bool {
	if len(item) == 0 {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(item, &obj); err != nil {
		return false
	}
	var typ, encrypted string
	if err := json.Unmarshal(obj["type"], &typ); err != nil || typ != "reasoning" {
		return false
	}
	if err := json.Unmarshal(obj["encrypted_content"], &encrypted); err != nil {
		return false
	}
	return encrypted != ""
}

// ReplayResponseID 只识别本层封装，用已有日志固定续接的上游渠道和 key。
func ReplayResponseID(raw []byte) (string, error) {
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return "", nil
	}
	var last string
	for _, m := range request.Messages {
		var blocks []struct {
			Type      string `json:"type"`
			Signature string `json:"signature"`
			Data      string `json:"data"`
		}
		if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			value := b.Signature
			if b.Type == "redacted_thinking" {
				value = b.Data
			}
			if !strings.HasPrefix(value, capsulePrefix) {
				continue
			}
			if _, err := ExpandReasoning(value); err != nil {
				return "", err
			}
			decoded, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, capsulePrefix))
			var c capsule
			_ = json.Unmarshal(decoded, &c)
			if c.ResponseID != "" {
				last = c.ResponseID
			}
		}
	}
	return last, nil
}
