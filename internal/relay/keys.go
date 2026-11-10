package relay

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/store"
)

// 渠道多 key（docs/decisions.md「渠道多 key 与渠道备注」）：同一会话固定用同一把 key，
// 上游的提示词缓存按账号算，换 key 缓存全部失效；坏 key 只影响自己。

// sessionOf 取请求所属的会话编号：Claude Code 写在请求体 metadata.user_id（JSON 字符串）的 session_id 里，
// Codex 带 Session-Id 请求头；都没有返回空（按用户的灵枢 key 固定）。
func sessionOf(r *http.Request, req *request) string {
	var meta struct {
		UserID string `json:"user_id"`
	}
	if json.Unmarshal(req.fields["metadata"], &meta) == nil && meta.UserID != "" {
		var uid struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(meta.UserID), &uid) == nil && uid.SessionID != "" {
			return uid.SessionID
		}
	}
	for _, h := range []string{"Session-Id", "X-Session-Id"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	return ""
}

// keyWeight 是最高随机权重哈希的权重：每把 key 对同一个会话算出一个固定的数，最大的那把就是这个会话的 key。
// 停用某把 key 时，只有原本落在它上面的会话改到下一把，其他会话不动。
func keyWeight(sticky string, keyID int64) uint64 {
	sum := sha256.Sum256([]byte(sticky + "\x00" + strconv.FormatInt(keyID, 10)))
	return binary.BigEndian.Uint64(sum[:8])
}

// orderAttempts 把候选渠道展开成按顺序尝试的“渠道 + key”：
// 渠道按优先级从高到低，同档内按会话固定（渠道亲和），渠道冷却中或所有 key 都不可用的排到最后（全都不可用时仍然要试）；
// 渠道内本会话固定的那把 key 在前，其余可用的按权重排在后面，冷却中、失效的放最后。pinKey 非 0 时这个渠道只用那一把。
func (h *Handler) orderAttempts(routes []store.Route, sticky string, pinKey int64) []store.Route {
	// 渠道亲和：同一优先级内按会话固定渠道（同一套最高随机权重哈希，和 key 分开算）。
	// 上游的提示词缓存按账号存，一段对话在几个同档渠道之间来回跳，每跳一次缓存就落空；
	// 固定的渠道冷却或 key 都不可用时，下面的 ready/later 会把它排到后面，恢复后自动回来
	routes = append([]store.Route(nil), routes...)
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].Priority != routes[j].Priority {
			return routes[i].Priority > routes[j].Priority
		}
		return keyWeight("channel\x00"+sticky, routes[i].ChannelID) > keyWeight("channel\x00"+sticky, routes[j].ChannelID)
	})
	var ready, later []store.Route
	for _, rt := range routes {
		keys := append([]store.RouteKey(nil), rt.Keys...)
		if pinKey != 0 {
			keys = keys[:0]
			for _, k := range rt.Keys {
				if k.ID == pinKey {
					keys = append(keys, k)
				}
			}
		}
		sort.SliceStable(keys, func(i, j int) bool {
			ui, uj := h.Keys.Usable(keys[i].ID), h.Keys.Usable(keys[j].ID)
			if ui != uj {
				return ui
			}
			return keyWeight(sticky, keys[i].ID) > keyWeight(sticky, keys[j].ID)
		})
		usable := len(keys) > 0 && h.Keys.Usable(keys[0].ID)
		var expanded []store.Route
		for _, k := range keys {
			a := rt
			a.KeyID, a.APIKey = k.ID, k.Key
			expanded = append(expanded, a)
		}
		if usable && !h.Health.Cooling(rt.ChannelID) {
			ready = append(ready, expanded...)
		} else {
			later = append(later, expanded...)
		}
	}
	return append(ready, later...)
}

// retryAfter 解析 429 的 Retry-After（秒数或 HTTP 日期），没有或看不懂返回 0（按默认冷却）。
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}
