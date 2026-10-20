package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/relay"
	"github.com/aumi314/lingshu/internal/store"
)

// 上游账户：估算各厂商账号的余额，纯运维工具，不影响用户余额和扣费。

func (s *Server) listUpstreamAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListUpstreamAccounts(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if list == nil {
		list = []store.UpstreamAccount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": list, "sync_templates": relay.BalanceTemplates})
}

func (s *Server) saveUpstreamAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.UpstreamAccount
		// 新建时可以顺便填当前余额，记一笔校准
		InitialBalance  *decimal.Decimal `json:"initial_balance"`
		InitialCurrency string           `json:"initial_currency"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a := in.UpstreamAccount
	a.ID = 0
	if r.PathValue("id") != "" {
		id, okID := pathID(w, r)
		if !okID {
			return
		}
		a.ID = id
	}
	a.Name, a.Note = strings.TrimSpace(a.Name), strings.TrimSpace(a.Note)
	if len(a.ChannelIDs) > 1 {
		writeError(w, http.StatusBadRequest, "一个上游账户只能对应一个渠道")
		return
	}
	if !a.CostFactor.IsPositive() {
		writeError(w, http.StatusBadRequest, "成本系数要大于 0（和站内价一样就填 1）")
		return
	}
	if a.ChannelIDs == nil {
		a.ChannelIDs = []int64{}
	}
	isNew := a.ID == 0
	if err := s.Store.SaveUpstreamAccount(r.Context(), &a); errors.Is(err, store.ErrChannelHasAccount) {
		writeError(w, http.StatusConflict, err.Error())
		return
	} else if err != nil {
		writeStoreError(w, err)
		return
	}
	if isNew && in.InitialBalance != nil {
		if _, msg := s.addEntry(r, a.ID, "calibrate", *in.InitialBalance, in.InitialCurrency, "创建时填写的余额"); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": a.ID})
}

func (s *Server) deleteUpstreamAccount(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if err := s.Store.DeleteUpstreamAccount(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// toUSD 把输入金额换成美元；只接受 USD 和 CNY。
func (s *Server) toUSD(ctx context.Context, amount decimal.Decimal, currency string) (decimal.Decimal, string, bool) {
	switch strings.ToUpper(currency) {
	case "", "USD":
		return amount, "USD", true
	case "CNY":
		return amount.Div(s.Store.CNYPerUSD(ctx)).Round(10), "CNY", true
	}
	return decimal.Zero, "", false
}

func (s *Server) addEntry(r *http.Request, accountID int64, kind string, amount decimal.Decimal, currency, note string) (*store.AccountEntry, string) {
	usd, cur, ok := s.toUSD(r.Context(), amount, currency)
	if !ok {
		return nil, "币种只能是 USD 或 CNY"
	}
	e, err := s.Store.AddAccountEntry(r.Context(), accountID, kind, usd, amount, cur, currentUser(r).ID, note)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "账户不存在，可能已被删除，请刷新页面"
	}
	if err != nil {
		slog.Error("记上游账户流水失败", "err", err)
		return nil, "服务器出错了，请稍后重试"
	}
	return e, ""
}

// addUpstreamEntry：kind=recharge 记一笔充值；kind=calibrate 填真实余额校准。
func (s *Server) addUpstreamEntry(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	var in struct {
		Kind     string          `json:"kind"`
		Amount   decimal.Decimal `json:"amount"`
		Currency string          `json:"currency"`
		Note     string          `json:"note"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	switch in.Kind {
	case "recharge":
		if !in.Amount.IsPositive() {
			writeError(w, http.StatusBadRequest, "充值金额要大于 0")
			return
		}
	case "calibrate":
	default:
		writeError(w, http.StatusBadRequest, "kind 只能是 recharge 或 calibrate")
		return
	}
	e, msg := s.addEntry(r, id, in.Kind, in.Amount, in.Currency, strings.TrimSpace(in.Note))
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) upstreamEntries(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	list, err := s.Store.AccountEntries(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if list == nil {
		list = []store.AccountEntry{}
	}
	writeJSON(w, http.StatusOK, list)
}

// syncUpstreamAccount 立即用余额接口校准一次（不要求开了自动校准）。
func (s *Server) syncUpstreamAccount(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	n, err := s.SyncBalances(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, "查询余额失败："+err.Error())
		return
	}
	if n == 0 {
		writeError(w, http.StatusBadRequest, "这个账户没有关联支持查余额的渠道（目前只有 DeepSeek、Kimi），只能手动校准")
		return
	}
	ok(w)
}

// SyncBalances 用厂商余额接口校准账户：accountID 为 0 时校准所有开了自动校准的账户。
// 返回成功校准的个数；只有一个账户时，查询失败会返回错误。
func (s *Server) SyncBalances(ctx context.Context, accountID int64) (int, error) {
	targets, err := s.Store.SyncTargets(ctx, accountID, relay.BalanceTemplates)
	if err != nil {
		return 0, err
	}
	n := 0
	var lastErr error
	for _, t := range targets {
		bal, err := s.Relay.FetchBalance(ctx, t.Template, t.BaseURL, t.APIKey, t.ProxyURL)
		if err != nil {
			slog.Warn("查询上游余额失败", "account_id", t.AccountID, "err", err)
			s.Store.SetSyncError(ctx, t.AccountID, err.Error())
			lastErr = err
			continue
		}
		usd, original, currency := s.balanceToUSD(ctx, bal)
		if _, err := s.Store.AddAccountEntry(ctx, t.AccountID, "sync", usd, original, currency, 0, ""); err != nil {
			lastErr = err
			continue
		}
		n++
	}
	if len(targets) == 1 && lastErr != nil {
		return n, lastErr
	}
	return n, nil
}

// balanceToUSD 合计各币种余额；只有一种币种时原样记录原始金额，多种时原始金额记美元合计。
func (s *Server) balanceToUSD(ctx context.Context, b relay.Balance) (usd, original decimal.Decimal, currency string) {
	currencies := make([]string, 0, len(b))
	for c := range b {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	for _, c := range currencies {
		v, _, ok := s.toUSD(ctx, b[c], c)
		if !ok {
			slog.Warn("上游余额币种不认识，忽略", "currency", c)
			continue
		}
		usd = usd.Add(v)
	}
	if len(currencies) == 1 {
		return usd, b[currencies[0]], currencies[0]
	}
	return usd, usd, "USD"
}

// RunBalanceSync 每小时自动校准一次，启动 1 分钟后先跑一次。ctx 结束时退出。
func (s *Server) RunBalanceSync(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := s.SyncBalances(ctx, 0); err != nil {
			slog.Warn("自动校准上游余额出错", "err", err)
		}
		timer.Reset(time.Hour)
	}
}
