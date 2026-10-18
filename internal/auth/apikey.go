// Package auth 负责 API key 和登录会话。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const keyPrefix = "sk-ls-"

// NewAPIKey 生成新 key，返回完整 key（只给用户看一次）、哈希（存库）和展示用前缀。
func NewAPIKey() (key, hash, prefix string) {
	key = keyPrefix + rand.Text() // 26 个 base32 字符，130 位随机
	return key, HashAPIKey(key), key[:len(keyPrefix)+6]
}

func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// KeysFromRequest 返回请求里带的候选 key，兼容 x-api-key（Anthropic）和 Authorization: Bearer（OpenAI）。
// 两个头可能同时存在且值不同：比如用户全局设了 ANTHROPIC_API_KEY，又为灵枢设了 ANTHROPIC_AUTH_TOKEN，
// Claude Code 会两个都发。调用方应逐个尝试，用能匹配上的那个。
func KeysFromRequest(r *http.Request) []string {
	var keys []string
	if v := strings.TrimSpace(r.Header.Get("x-api-key")); v != "" {
		keys = append(keys, v)
	}
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if v = strings.TrimSpace(v); v != "" && (len(keys) == 0 || keys[0] != v) {
			keys = append(keys, v)
		}
	}
	return keys
}
