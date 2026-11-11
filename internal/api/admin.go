package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/billing"
	"github.com/aumi314/lingshu/internal/relay"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

// ---- 用户 ----

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func validRole(role string) bool { return role == "super_admin" || role == "admin" || role == "user" }

// isAdmin：超级管理员和管理员都能进后台，权限相同；超级管理员只多一项任免管理员（2026-11-06 决策）。
func isAdmin(role string) bool { return role == "super_admin" || role == "admin" }

// guardTarget 检查当前管理员能否改这个用户：超级管理员的账号只有超级管理员能改，
// 否则管理员可以重置超级管理员的密码再登录进去给自己升级，任免权就形同虚设。
func (s *Server) guardTarget(w http.ResponseWriter, r *http.Request, id int64) (*store.User, bool) {
	target, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return nil, false
	}
	if target.Role == "super_admin" && currentUser(r).Role != "super_admin" {
		writeError(w, http.StatusForbidden, "只有超级管理员能修改超级管理员的账号")
		return nil, false
	}
	return target, true
}

// userNoteLimit 是管理员内部备注的字数上限，避免无界文本进库。
const userNoteLimit = 500

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" {
		writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	if in.Role == "" {
		in.Role = "user"
	}
	if !validRole(in.Role) {
		writeError(w, http.StatusBadRequest, "角色只能是 super_admin、admin 或 user")
		return
	}
	if in.Role != "user" && currentUser(r).Role != "super_admin" {
		writeError(w, http.StatusForbidden, "只有超级管理员能任命管理员")
		return
	}
	hash, msg := hashPassword(in.Password)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	id, err := s.Store.CreateUser(r.Context(), in.Username, hash, in.Role)
	if err == store.ErrUsernameTaken {
		writeError(w, http.StatusConflict, "用户名已存在")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	var in struct {
		Username      *string  `json:"username"`
		Note          *string  `json:"note"`
		Role          *string  `json:"role"`
		Enabled       *bool    `json:"enabled"`
		Password      *string  `json:"password"`
		UserGroupIDs  *[]int64 `json:"user_group_ids"`
		BalanceChange *struct {
			Mode     string           `json:"mode"`
			Amount   *decimal.Decimal `json:"amount"`
			Currency string           `json:"currency"`
			Note     string           `json:"note"`
		} `json:"balance_adjustment"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	me := currentUser(r)
	if id == me.ID && ((in.Enabled != nil && !*in.Enabled) || (in.Role != nil && !isAdmin(*in.Role))) {
		writeError(w, http.StatusBadRequest, "不能停用自己或取消自己的管理员身份，避免把自己锁在外面")
		return
	}
	target, allowed := s.guardTarget(w, r, id)
	if !allowed {
		return
	}
	if in.Role != nil && *in.Role != target.Role && me.Role != "super_admin" {
		writeError(w, http.StatusForbidden, "只有超级管理员能任免管理员")
		return
	}
	// 至少保留一个启用中的超级管理员
	if target.Role == "super_admin" && target.Enabled && ((in.Role != nil && *in.Role != "super_admin") || (in.Enabled != nil && !*in.Enabled)) {
		n, err := s.Store.CountEnabledSuperAdmins(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if n <= 1 {
			writeError(w, http.StatusBadRequest, "至少要保留一个启用中的超级管理员，先任命另一位再改")
			return
		}
	}
	var p store.UserPatch
	if in.Username != nil {
		username := strings.TrimSpace(*in.Username)
		if username == "" {
			writeError(w, http.StatusBadRequest, "用户名不能为空")
			return
		}
		p.Username = &username
	}
	if in.Note != nil {
		note := strings.TrimSpace(*in.Note)
		if len([]rune(note)) > userNoteLimit {
			writeError(w, http.StatusBadRequest, "备注太长，最多 "+strconv.Itoa(userNoteLimit)+" 个字")
			return
		}
		p.Note = &note
	}
	if in.Role != nil {
		if !validRole(*in.Role) {
			writeError(w, http.StatusBadRequest, "角色只能是 super_admin、admin 或 user")
			return
		}
		p.Role = in.Role
	}
	p.Enabled = in.Enabled
	passwordChanged := in.Password != nil && *in.Password != ""
	if passwordChanged {
		hash, msg := hashPassword(*in.Password)
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		p.PasswordHash = &hash
	}
	p.UserGroupIDs = in.UserGroupIDs
	if in.BalanceChange != nil {
		if in.BalanceChange.Amount == nil {
			writeError(w, http.StatusBadRequest, "额度调整金额不能为空")
			return
		}
		change := store.UserBalanceChange{Mode: in.BalanceChange.Mode, Amount: *in.BalanceChange.Amount, Note: strings.TrimSpace(in.BalanceChange.Note), OperatorID: me.ID}
		if change.Mode != "add" && change.Mode != "set" {
			writeError(w, http.StatusBadRequest, "额度调整方式只能是 add 或 set")
			return
		}
		switch in.BalanceChange.Currency {
		case "", "USD":
		case "CNY":
			change.Amount = change.Amount.Div(s.Store.CNYPerUSD(r.Context())).Round(10)
		default:
			writeError(w, http.StatusBadRequest, "币种只能是 USD 或 CNY")
			return
		}
		p.BalanceChange = &change
	}
	if err := s.Store.UpdateUser(r.Context(), id, p); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// adjustBalance：mode=add 在当前余额上加减 amount；mode=set 直接设为 amount。都会记一条流水。
func (s *Server) adjustBalance(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if _, allowed := s.guardTarget(w, r, id); !allowed {
		return
	}
	var in struct {
		Mode   string          `json:"mode"`
		Amount decimal.Decimal `json:"amount"`
		Note   string          `json:"note"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	delta := in.Amount
	switch in.Mode {
	case "add", "":
	case "set":
		u, err := s.Store.UserByID(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		delta = in.Amount.Sub(u.Balance)
	default:
		writeError(w, http.StatusBadRequest, "mode 只能是 add 或 set")
		return
	}
	if delta.IsZero() {
		writeError(w, http.StatusBadRequest, "余额没有变化")
		return
	}
	after, err := s.Store.AdjustBalance(r.Context(), id, delta, currentUser(r).ID, strings.TrimSpace(in.Note))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balance": after})
}

func (s *Server) balanceHistory(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	list, err := s.Store.BalanceAdjustments(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if list == nil {
		list = []store.BalanceAdjustment{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) setUserGroups(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if _, allowed := s.guardTarget(w, r, id); !allowed {
		return
	}
	var in struct {
		UserGroupIDs []int64 `json:"user_group_ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.UserGroupIDs == nil {
		in.UserGroupIDs = []int64{}
	}
	if err := s.Store.SetUserGroups(r.Context(), id, in.UserGroupIDs); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

func (s *Server) listUserGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Store.ListUserGroups(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) saveUserGroup(w http.ResponseWriter, r *http.Request) {
	var g store.UserGroup
	if !readJSON(w, r, &g) {
		return
	}
	if r.PathValue("id") != "" {
		id, okID := pathID(w, r)
		if !okID {
			return
		}
		g.ID = id
	}
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" {
		writeError(w, http.StatusBadRequest, "用户分组名不能为空")
		return
	}
	if err := s.Store.SaveUserGroup(r.Context(), &g); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteUserGroup(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if err := s.Store.DeleteUserGroup(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// ---- 分组 ----

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Store.ListGroups(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request) {
	var g store.Group
	if !readJSON(w, r, &g) {
		return
	}
	if r.PathValue("id") != "" {
		id, okID := pathID(w, r)
		if !okID {
			return
		}
		g.ID = id
	} else {
		g.ID = 0
	}
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" {
		writeError(w, http.StatusBadRequest, "分组名不能为空")
		return
	}
	if g.RetryCount != nil && (*g.RetryCount < 0 || *g.RetryCount > 4) {
		writeError(w, http.StatusBadRequest, "失败重试次数只能是 0 到 4")
		return
	}
	if g.Ratio.IsNegative() {
		writeError(w, http.StatusBadRequest, "倍率不能是负数")
		return
	}
	if !g.IsPublic && len(g.UserGroupIDs) == 0 {
		writeError(w, http.StatusBadRequest, "指定用户分组时至少选择一个用户分组")
		return
	}
	if err := s.Store.SaveGroup(r.Context(), &g); err != nil {
		var bad *store.InputError
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, bad.Msg)
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if err := s.Store.DeleteGroup(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// ---- 模型 ----

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.Store.ListModels(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) saveModel(w http.ResponseWriter, r *http.Request) {
	var m store.Model
	if !readJSON(w, r, &m) {
		return
	}
	m.ID = 0
	if r.PathValue("id") != "" {
		id, okID := pathID(w, r)
		if !okID {
			return
		}
		m.ID = id
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		writeError(w, http.StatusBadRequest, "模型名不能为空")
		return
	}
	for _, p := range []decimal.Decimal{m.InputPrice, m.OutputPrice, m.CacheReadPrice, m.CacheWritePrice} {
		if p.IsNegative() {
			writeError(w, http.StatusBadRequest, "价格不能是负数")
			return
		}
	}
	if k := m.FastMultiplier; k != nil && (!k.IsPositive() || k.GreaterThan(decimal.NewFromInt(100))) {
		writeError(w, http.StatusBadRequest, "Fast 倍率要大于 0、不超过 100；不开放 Fast 就留空")
		return
	}
	rules, err := billing.Normalize(m.PriceRules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m.PriceRules = rules
	if err := s.Store.SaveModel(r.Context(), &m); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if err := s.Store.DeleteModel(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// ---- 渠道 ----

func (s *Server) channelTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, channelTemplates)
}

type channelView struct {
	*store.Channel
	Cooling bool               `json:"cooling"` // 连续失败被暂时停用中
	Stats   *store.ChannelStat `json:"stats"`   // 最近 24 小时的连接状态，没有任何尝试时为 null
}

// channelStatsWindow 是渠道页「连接状态」的统计窗口。
const channelStatsWindow = 24 * time.Hour

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.Store.ListChannels(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	stats, err := s.Store.ChannelStats(r.Context(), time.Now().Add(-channelStatsWindow))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	keyStats, err := s.Store.ChannelKeyStats(r.Context(), time.Now().Add(-channelStatsWindow))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]channelView, len(channels))
	for i, c := range channels {
		out[i] = channelView{Channel: c, Cooling: s.Relay.Health.Cooling(c.ID)}
		if st, ok := stats[c.ID]; ok {
			out[i].Stats = &st
		}
		// 每把 key 的当前状态（正常 / 冷却中 / 失效）和近 24 小时成功、失败次数
		for j := range c.Keys {
			k := &c.Keys[j]
			k.Status = s.Relay.Keys.Status(k.ID)
			if st, ok := keyStats[k.ID]; ok {
				k.Stats = st
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request) {
	var c store.Channel
	if !readJSON(w, r, &c) {
		return
	}
	c.ID = 0
	if r.PathValue("id") != "" {
		id, okID := pathID(w, r)
		if !okID {
			return
		}
		c.ID = id
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Note = strings.TrimSpace(c.Note)
	c.APIKey = strings.TrimSpace(c.APIKey)
	if c.Keys == nil && c.APIKey != "" {
		c.Keys = []store.ChannelKey{{APIKey: c.APIKey, Enabled: true}} // 旧客户端只传一把 key
	}
	c.BaseURLChat = strings.TrimRight(strings.TrimSpace(c.BaseURLChat), "/")
	c.BaseURLMessages = strings.TrimRight(strings.TrimSpace(c.BaseURLMessages), "/")
	c.BaseURLResponses = strings.TrimRight(strings.TrimSpace(c.BaseURLResponses), "/")
	c.BaseURLSystemOne = strings.TrimRight(strings.TrimSpace(c.BaseURLSystemOne), "/")
	c.ProxyURL = strings.TrimSpace(c.ProxyURL)
	if err := relay.ValidateProxyURL(c.ProxyURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch {
	case c.Name == "":
		writeError(w, http.StatusBadRequest, "渠道名不能为空")
		return
	case c.BaseURLChat == "" && c.BaseURLMessages == "" && c.BaseURLResponses == "" && c.BaseURLSystemOne == "":
		writeError(w, http.StatusBadRequest, "至少填一个协议的地址")
		return
	case c.ID == 0 && len(c.Keys) == 0:
		writeError(w, http.StatusBadRequest, "新建渠道要填 API key")
		return
	case utf8.RuneCountInString(c.Note) > 500:
		writeError(w, http.StatusBadRequest, "渠道备注最多 500 个字")
		return
	}
	for _, u := range []string{c.BaseURLChat, c.BaseURLMessages, c.BaseURLResponses, c.BaseURLSystemOne} {
		if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			writeError(w, http.StatusBadRequest, "地址要以 https:// 开头: "+u)
			return
		}
	}
	if c.GroupIDs == nil {
		c.GroupIDs = []int64{}
	}
	if err := s.Store.SaveChannel(r.Context(), &c); err != nil {
		var bad *store.InputError
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, bad.Msg)
			return
		}
		writeStoreError(w, err)
		return
	}
	// keys_deduped：自动去掉的重复 key 数，页面据此提示
	writeJSON(w, http.StatusOK, map[string]int64{"id": c.ID, "keys_deduped": int64(c.KeysDeduped)})
}

// reorderGroups 保存服务分组的手动顺序（只影响各处的显示顺序）。
func (s *Server) reorderGroups(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.Store.ReorderGroups(r.Context(), in.IDs); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// reorderChannels 保存渠道列表的手动顺序（只影响显示，不影响路由）。
func (s *Server) reorderChannels(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.Store.ReorderChannels(r.Context(), in.IDs); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if err := s.Store.DeleteChannel(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// channelDraft 解析拉取模型、冒烟测试用的渠道配置：可以是还没保存的新渠道；
// 编辑已有渠道时 key 留空表示用已保存的 key。
func (s *Server) channelDraft(ctx context.Context, d *store.Channel) (*store.Channel, string) {
	if d.ID != 0 && strings.TrimSpace(d.APIKey) == "" {
		if d.TestKeyID != 0 { // 冒烟测试指定了某一把 key
			key, err := s.Store.ChannelKeySecret(ctx, d.ID, d.TestKeyID)
			if err != nil {
				return nil, "这把 key 不存在，可能已被删除，请刷新页面"
			}
			d.APIKey = key
		} else {
			saved, err := s.Store.ChannelSecret(ctx, d.ID)
			if err != nil {
				return nil, "渠道不存在"
			}
			d.APIKey = saved.APIKey
		}
	}
	d.APIKey = strings.TrimSpace(d.APIKey)
	if d.APIKey == "" {
		return nil, "先填 API key"
	}
	d.ProxyURL = strings.TrimSpace(d.ProxyURL)
	if err := relay.ValidateProxyURL(d.ProxyURL); err != nil {
		return nil, err.Error()
	}
	return d, ""
}

func (s *Server) fetchModels(w http.ResponseWriter, r *http.Request) {
	var d store.Channel
	if !readJSON(w, r, &d) {
		return
	}
	ch, msg := s.channelDraft(r.Context(), &d)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	names, err := s.Relay.FetchModels(r.Context(), ch)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, names)
}

// probeChannel 对渠道的每个"协议 × 模型"做一次冒烟测试，最多同时 3 个。
func (s *Server) probeChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.Channel
		Targets []struct {
			Protocol string `json:"protocol"`
			Model    string `json:"model"` // 上游模型名
		} `json:"targets"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ch, msg := s.channelDraft(r.Context(), &in.Channel)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if len(in.Targets) == 0 || len(in.Targets) > 20 {
		writeError(w, http.StatusBadRequest, "选 1 到 20 个要测试的模型")
		return
	}
	results := make([]relay.ProbeResult, len(in.Targets))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 3) // 最多同时 3 个：低档位账号（如 Kimi 最低档并发 1）一下子发太多会全被限流
	for i, t := range in.Targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = s.Relay.Probe(r.Context(), ch, usage.Protocol(t.Protocol), t.Model)
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, results)
}

// ---- 日志、设置 ----

func (s *Server) adminLogs(w http.ResponseWriter, r *http.Request) {
	f, msg := parseLogFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	s.writeLogs(w, r, f, true)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	payment, err := s.Store.Setting(r.Context(), "payment_enabled")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	redeem, err := s.redeemEnabled(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	registrationMode, err := s.Store.Setting(r.Context(), "registration_mode")
	if errors.Is(err, store.ErrNotFound) {
		registrationMode = "manual"
	} else if err != nil {
		writeStoreError(w, err)
		return
	}
	registration, err := s.Store.Registration(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	apiBaseURL, err := s.apiBaseURL(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	limits, err := s.Store.CallLimits(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	legal, err := s.Store.LegalDocuments(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"call_limits": limits, "redeem_enabled": redeem,
		"cny_per_usd": s.cnyPerUSD(r), "payment_enabled": payment == "true", "registration_mode": registrationMode, "registration_enabled": registration.Enabled, "registration_code_required": registration.CodeRequired, "registration_email_required": registration.EmailRequired, "api_base_url": apiBaseURL,
		"fx_mode": s.fxMode(r.Context()), "fx_status": s.fxStatus(r.Context()), "announcement": s.announcement(r.Context()),
		"user_agreement": legal.UserAgreement, "privacy_policy": legal.PrivacyPolicy, "legal_version": legal.Version,
	})
}

// maxAnnouncement 是公告的字数上限（概览页顶部一条提示，写客服方式、维护通知这类短文字）。
const maxAnnouncement = 500

// announcement 是管理员在主设置里填的公告，所有登录用户在概览页顶部看到；没填就是空，不显示。
func (s *Server) announcement(ctx context.Context) string {
	v, err := s.Store.Setting(ctx, "announcement")
	if err != nil {
		return ""
	}
	return v
}

func (s *Server) apiBaseURL(ctx context.Context) (string, error) {
	value, err := s.Store.Setting(ctx, "api_base_url")
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if s.PublicURL == "" {
		return "", nil
	}
	return strings.TrimRight(s.PublicURL, "/") + "/v1", nil
}

func (s *Server) getAPIEndpoints(w http.ResponseWriter, r *http.Request) {
	apiBaseURL, err := s.apiBaseURL(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"api_base_url": apiBaseURL, "announcement": s.announcement(r.Context())})
}

func validAPIBaseURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	localHTTP := parsed != nil && (strings.EqualFold(parsed.Hostname(), "localhost") || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && localHTTP)) {
		return "", false
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path != "/v1" {
		return "", false
	}
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), true
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CNYPerUSD                 *decimal.Decimal `json:"cny_per_usd"`
		PaymentEnabled            *bool            `json:"payment_enabled"`
		RedeemEnabled             *bool            `json:"redeem_enabled"`
		RegistrationMode          *string          `json:"registration_mode"`
		RegistrationEnabled       *bool            `json:"registration_enabled"`
		RegistrationCodeRequired  *bool            `json:"registration_code_required"`
		RegistrationEmailRequired *bool            `json:"registration_email_required"`
		APIBaseURL                *string          `json:"api_base_url"`
		FXMode                    *string          `json:"fx_mode"`
		Concurrency               *int64           `json:"user_concurrency"`
		RPM                       *int64           `json:"user_rpm"`
		ReserveOutput             *int64           `json:"reserve_output_tokens"`
		BridgeResponsesToMessages *bool            `json:"bridge_responses_to_messages"`
		BridgeMessagesToResponses *bool            `json:"bridge_messages_to_responses"`
		CaptureConversations      *bool            `json:"capture_conversations"`
		Announcement              *string          `json:"announcement"`
		UserAgreement             *string          `json:"user_agreement"`
		PrivacyPolicy             *string          `json:"privacy_policy"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.CNYPerUSD != nil && !in.CNYPerUSD.IsPositive() {
		writeError(w, http.StatusBadRequest, "汇率要大于 0")
		return
	}
	for _, v := range []*int64{in.Concurrency, in.RPM, in.ReserveOutput} {
		if v != nil && (*v < 0 || *v > 1_000_000) {
			writeError(w, http.StatusBadRequest, "调用限制要填 0 到 1000000 之间的整数，0 表示不限")
			return
		}
	}
	if in.FXMode != nil && *in.FXMode != "manual" && *in.FXMode != "live" {
		writeError(w, http.StatusBadRequest, "汇率模式只能是手动或实时")
		return
	}
	mode := s.fxMode(r.Context())
	if in.FXMode != nil {
		mode = *in.FXMode
	}
	if in.CNYPerUSD != nil && mode == "live" {
		writeError(w, http.StatusBadRequest, "实时模式下汇率自动更新，要手动填写请先切到手动")
		return
	}
	var registrationValues [][2]string
	if in.RegistrationMode != nil || in.RegistrationEnabled != nil || in.RegistrationCodeRequired != nil || in.RegistrationEmailRequired != nil {
		flags, err := s.Store.Registration(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if in.RegistrationMode != nil {
			if in.RegistrationEnabled != nil || in.RegistrationCodeRequired != nil || in.RegistrationEmailRequired != nil {
				writeError(w, 400, "请只提交一种注册配置格式")
				return
			}
			switch *in.RegistrationMode {
			case "manual":
				flags = store.RegistrationFlags{}
			case "code":
				flags = store.RegistrationFlags{Enabled: true, CodeRequired: true}
			case "email":
				flags = store.RegistrationFlags{Enabled: true, EmailRequired: true}
			default:
				writeError(w, 400, "注册方式无效")
				return
			}
		}
		if in.RegistrationEnabled != nil {
			flags.Enabled = *in.RegistrationEnabled
		}
		if in.RegistrationCodeRequired != nil {
			flags.CodeRequired = *in.RegistrationCodeRequired
		}
		if in.RegistrationEmailRequired != nil {
			flags.EmailRequired = *in.RegistrationEmailRequired
		}
		if flags.Enabled && flags.EmailRequired {
			ready, err := s.Store.MailReady(r.Context())
			if err != nil {
				writeStoreError(w, err)
				return
			}
			if !ready {
				writeError(w, 400, "请先启用并检查一个发信账号")
				return
			}
			if currentUser(r).Role != "super_admin" {
				writeError(w, 403, "只有超级管理员能开启邮箱认证")
				return
			}
		}
		mode := "manual"
		if flags.Enabled {
			mode = "open"
			if flags.CodeRequired {
				mode = "code"
			}
			if flags.EmailRequired {
				mode = "email"
			}
			if flags.CodeRequired && flags.EmailRequired {
				mode = "both"
			}
		}
		registrationValues = [][2]string{{"registration_mode", mode}, {"registration_enabled", strconv.FormatBool(flags.Enabled)}, {"registration_code_required", strconv.FormatBool(flags.CodeRequired)}, {"registration_email_required", strconv.FormatBool(flags.EmailRequired)}}
	}
	if in.APIBaseURL != nil {
		value, valid := validAPIBaseURL(*in.APIBaseURL)
		if !valid {
			writeError(w, http.StatusBadRequest, "API 接入地址必须是 http(s)://域名/v1")
			return
		}
		in.APIBaseURL = &value
	}
	if in.Announcement != nil {
		v := strings.TrimSpace(*in.Announcement)
		if utf8.RuneCountInString(v) > maxAnnouncement {
			writeError(w, http.StatusBadRequest, "公告最多 "+strconv.Itoa(maxAnnouncement)+" 个字")
			return
		}
		in.Announcement = &v
	}
	if in.UserAgreement != nil {
		// 协议原文照存，不去空白：从旧站同步过来时不丢内容，版本号也算在原文上。
		if !checkLegalLength(*in.UserAgreement) {
			writeError(w, http.StatusBadRequest, "用户协议最多 "+strconv.Itoa(maxLegalRunes)+" 个字")
			return
		}
	}
	if in.PrivacyPolicy != nil {
		if !checkLegalLength(*in.PrivacyPolicy) {
			writeError(w, http.StatusBadRequest, "隐私政策最多 "+strconv.Itoa(maxLegalRunes)+" 个字")
			return
		}
	}
	var kv [][2]string // 一次保存的多项设置在同一事务里写，操作日志记一条
	if in.FXMode != nil {
		kv = append(kv, [2]string{"fx_mode", *in.FXMode})
	}
	for k, v := range map[string]*int64{"user_concurrency": in.Concurrency, "user_rpm": in.RPM, "reserve_output_tokens": in.ReserveOutput} {
		if v != nil {
			kv = append(kv, [2]string{k, strconv.FormatInt(*v, 10)})
		}
	}
	if in.CNYPerUSD != nil {
		kv = append(kv, [2]string{"cny_per_usd", in.CNYPerUSD.Round(4).String()})
	}
	if in.PaymentEnabled != nil {
		kv = append(kv, [2]string{"payment_enabled", strconv.FormatBool(*in.PaymentEnabled)})
	}
	if in.RedeemEnabled != nil {
		kv = append(kv, [2]string{"redeem_enabled", strconv.FormatBool(*in.RedeemEnabled)})
	}
	for k, v := range map[string]*bool{"bridge_responses_to_messages": in.BridgeResponsesToMessages, "bridge_messages_to_responses": in.BridgeMessagesToResponses, "capture_conversations": in.CaptureConversations} {
		if v != nil {
			kv = append(kv, [2]string{k, strconv.FormatBool(*v)})
		}
	}
	kv = append(kv, registrationValues...)
	if in.APIBaseURL != nil {
		kv = append(kv, [2]string{"api_base_url", *in.APIBaseURL})
	}
	if in.Announcement != nil {
		kv = append(kv, [2]string{"announcement", *in.Announcement})
	}
	if in.UserAgreement != nil {
		kv = append(kv, [2]string{"user_agreement", *in.UserAgreement})
	}
	if in.PrivacyPolicy != nil {
		kv = append(kv, [2]string{"privacy_policy", *in.PrivacyPolicy})
	}
	if err := s.Store.SetSettings(r.Context(), kv); err != nil {
		writeStoreError(w, err)
		return
	}
	// 切到实时后马上拉一次，不用等一小时；失败原因写进状态，页面上能看到
	if in.FXMode != nil && *in.FXMode == "live" {
		if err := s.RefreshFX(r.Context()); err != nil {
			slog.Warn("实时汇率更新失败", "err", err)
		}
	}
	ok(w)
}
