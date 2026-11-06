package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/store"
)

// 兑换失败限制：每个用户一小时内最多失败 10 次，防止暴力猜码（码本身是 128 位随机数，这层是兜底）。
const redeemMaxFails = 10

type redeemGuard struct {
	mu    sync.Mutex
	fails map[int64][]time.Time
}

// blocked 返回还要等多久才能再试，0 表示可以试。
func (g *redeemGuard) blocked(userID int64, now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	recent := g.fails[userID][:0]
	for _, t := range g.fails[userID] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	g.fails[userID] = recent
	if len(recent) >= redeemMaxFails {
		return time.Hour - now.Sub(recent[0])
	}
	return 0
}

func (g *redeemGuard) fail(userID int64, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails[userID] = append(g.fails[userID], now)
}

func (s *Server) redeem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Code) == "" {
		writeError(w, http.StatusBadRequest, "请输入兑换码")
		return
	}
	user := currentUser(r)
	if wait := s.redeemGuard.blocked(user.ID, time.Now()); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "兑换失败次数太多，请约 "+strconv.Itoa(int(wait.Minutes())+1)+" 分钟后再试")
		return
	}
	amount, balance, err := s.Store.Redeem(r.Context(), user.ID, in.Code)
	if errors.Is(err, store.ErrInvalidRedeemCode) {
		s.redeemGuard.fail(user.ID, time.Now())
		writeError(w, http.StatusBadRequest, err.Error()+"，请检查是否输入完整")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"amount": amount, "balance": balance})
}

func (s *Server) listRedeemCodes(w http.ResponseWriter, r *http.Request) {
	codes, err := s.Store.ListRedeemCodes(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if codes == nil {
		codes = []store.RedeemCode{}
	}
	writeJSON(w, http.StatusOK, codes)
}

func (s *Server) createRedeemCodes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Count     int             `json:"count"`
		Amount    decimal.Decimal `json:"amount"`
		Currency  string          `json:"currency"` // USD 或 CNY，人民币按系统汇率换成美元存
		Note      string          `json:"note"`
		ExpiresAt *time.Time      `json:"expires_at"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Count < 1 || in.Count > 100 {
		writeError(w, http.StatusBadRequest, "一次生成 1 到 100 个")
		return
	}
	amount := in.Amount
	if in.Currency == "CNY" {
		amount = amount.Div(s.cnyPerUSD(r)).Round(10)
	} else if in.Currency != "" && in.Currency != "USD" {
		writeError(w, http.StatusBadRequest, "币种只能是 USD 或 CNY")
		return
	}
	if !amount.IsPositive() || amount.GreaterThan(decimal.NewFromInt(100000)) {
		writeError(w, http.StatusBadRequest, "面额要大于 0、不超过 $100000")
		return
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "过期时间要晚于现在")
		return
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 200 {
		writeError(w, http.StatusBadRequest, "备注不能超过 200 字")
		return
	}
	codes, err := s.Store.CreateRedeemCodes(r.Context(), currentUser(r).ID, in.Count, amount, note, in.ExpiresAt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, codes)
}

func (s *Server) revokeRedeemCode(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	switch err := s.Store.RevokeRedeemCode(r.Context(), id); {
	case errors.Is(err, store.ErrRedeemCodeUsed):
		writeError(w, http.StatusConflict, "兑换码已使用，不能撤销")
	case err != nil:
		writeStoreError(w, err)
	default:
		ok(w)
	}
}
