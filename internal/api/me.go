package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/store"
)

// myModels 返回用户能用的分组，以及每个分组下的模型和实际单价（已乘倍率）。
func (s *Server) myModels(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Store.AccessibleGroups(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ids := make([]int64, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	models, err := s.Store.PricedModels(r.Context(), ids)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// 调用设置里关掉的转接方向不再列出（Responses 入口靠 Messages 渠道 / Messages 入口靠 Responses 渠道）
	limits, err := s.Store.CallLimits(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for i := range models {
		kept, bridged := []string{}, []string{}
		for _, p := range models[i].BridgedProtocols {
			if (p == "responses" && !limits.BridgeResponsesToMessages) || (p == "messages" && !limits.BridgeMessagesToResponses) {
				continue
			}
			bridged = append(bridged, p)
		}
		for _, p := range models[i].Protocols {
			if slices.Contains(models[i].BridgedProtocols, p) && !slices.Contains(bridged, p) {
				continue
			}
			kept = append(kept, p)
		}
		models[i].Protocols, models[i].BridgedProtocols = kept, bridged
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "models": models})
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Store.ListAPIKeys(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// canUseGroup 检查用户能不能用这个分组。
func (s *Server) canUseGroup(r *http.Request, groupID int64) (bool, error) {
	groups, err := s.Store.AccessibleGroups(r.Context(), currentUser(r).ID)
	if err != nil {
		return false, err
	}
	for _, g := range groups {
		if g.ID == groupID {
			return true, nil
		}
	}
	return false, nil
}

// parseQuota 解析限额：null 或空字符串表示不限；否则必须是正数。
func parseQuota(raw json.RawMessage) (*decimal.Decimal, string) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil, ""
	}
	var d decimal.Decimal
	if err := json.Unmarshal(raw, &d); err != nil || !d.IsPositive() {
		return nil, "限额要填大于 0 的金额，不限额就留空"
	}
	return &d, ""
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string          `json:"name"`
		GroupID    int64           `json:"group_id"`
		QuotaLimit json.RawMessage `json:"quota_limit"`
		ExpiresAt  *time.Time      `json:"expires_at"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "给 key 起个名字，方便以后区分用途")
		return
	}
	if allowed, err := s.canUseGroup(r, in.GroupID); err != nil {
		writeStoreError(w, err)
		return
	} else if !allowed {
		writeError(w, http.StatusBadRequest, "你不能使用这个分组")
		return
	}
	quota, msg := parseQuota(in.QuotaLimit)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, store.ErrKeyExpiration.Error())
		return
	}
	u := currentUser(r)
	key, hash, prefix := auth.NewAPIKey()
	id, err := s.Store.CreateAPIKey(r.Context(), u.ID, in.Name, hash, prefix, in.GroupID, quota, in.ExpiresAt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// 完整 key 只在这里返回一次
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "key": key})
}

func (s *Server) updateKey(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	var in map[string]json.RawMessage
	if !readJSON(w, r, &in) {
		return
	}
	u := currentUser(r)
	var p store.APIKeyPatch
	if raw, has := in["name"]; has {
		var name string
		json.Unmarshal(raw, &name)
		if name = strings.TrimSpace(name); name == "" {
			writeError(w, http.StatusBadRequest, "名称不能为空")
			return
		}
		p.Name = &name
	}
	if raw, has := in["enabled"]; has {
		var enabled bool
		json.Unmarshal(raw, &enabled)
		p.Enabled = &enabled
	}
	if raw, has := in["group_id"]; has {
		var gid int64
		json.Unmarshal(raw, &gid)
		if allowed, err := s.canUseGroup(r, gid); err != nil {
			writeStoreError(w, err)
			return
		} else if !allowed {
			writeError(w, http.StatusBadRequest, "你不能使用这个分组")
			return
		}
		p.GroupID = &gid
	}
	if raw, has := in["quota_limit"]; has {
		quota, msg := parseQuota(raw)
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		p.SetQuota, p.Quota = true, quota
	}
	if raw, has := in["expires_at"]; has {
		if err := json.Unmarshal(raw, &p.ExpiresAt); err != nil {
			writeError(w, http.StatusBadRequest, "过期时间格式不正确，请使用带时区的日期时间")
			return
		}
		p.SetExpiresAt = true
	}
	if err := s.Store.UpdateAPIKey(r.Context(), u.ID, id, p); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if err := s.Store.DeleteAPIKey(r.Context(), currentUser(r).ID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// parseLogFilter 解析日志筛选参数：model、status、channel_id、user_id、target（操作日志对象类型）、from、to（RFC3339）、page、page_size。
func parseLogFilter(r *http.Request) (store.LogFilter, string) {
	q := r.URL.Query()
	f := store.LogFilter{Type: q.Get("type"), Model: q.Get("model"), Status: q.Get("status")}
	switch f.Type {
	case "", "all", "request", "operation", "topup", "adjustment":
	default:
		return f, "日志类型不正确"
	}
	if f.Type == "operation" {
		switch f.Target = q.Get("target"); f.Target {
		case "", "user", "api_key", "balance", "group", "user_group", "model", "channel", "settings", "payment", "registration_code", "redeem_code", "topup":
		default:
			return f, "对象类型不正确"
		}
	}
	if f.Type != "" && f.Type != "request" {
		f.Model, f.Status = "", ""
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return f, "时间格式不对: " + v
			}
			*p.dst = &t
		}
	}
	f.ChannelID, _ = strconv.ParseInt(q.Get("channel_id"), 10, 64)
	if f.Type != "" && f.Type != "request" {
		f.ChannelID = 0
	}
	f.UserID, _ = strconv.ParseInt(q.Get("user_id"), 10, 64)
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	f.Limit, f.Offset = pageSize, (page-1)*pageSize
	return f, ""
}

func (s *Server) myLogs(w http.ResponseWriter, r *http.Request) {
	f, msg := parseLogFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	f.UserID, f.ChannelID = currentUser(r).ID, 0 // 用户只能看自己的，不按渠道筛
	s.writeLogs(w, r, f, false)
}

func (s *Server) writeLogs(w http.ResponseWriter, r *http.Request, f store.LogFilter, admin bool) {
	if f.Type == "operation" {
		logs, sum, total, err := s.Store.ListOperations(r.Context(), f, !admin)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "summary": sum, "total": total})
		return
	}
	if f.Type != "" {
		logs, sum, total, err := s.Store.ListActivityLogs(r.Context(), f)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if !admin {
			for i := range logs {
				if logs[i].LogEntry != nil {
					logs[i].ChannelName, logs[i].UpstreamModel = "", ""
				}
			}
		}
		models, err := s.Store.LogModels(r.Context(), f.UserID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "summary": sum, "total": total, "models": models})
		return
	}
	logs, sum, err := s.Store.ListLogs(r.Context(), f)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !admin {
		// 渠道和上游模型是运维信息，用户端不展示
		for i := range logs {
			logs[i].ChannelName, logs[i].UpstreamModel = "", ""
		}
	}
	models, err := s.Store.LogModels(r.Context(), f.UserID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if logs == nil {
		logs = []store.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "summary": sum, "total": sum.Requests, "models": models})
}
