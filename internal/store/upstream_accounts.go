package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// UpstreamAccount 是在厂商那里的账号，只用来估算上游余额，和用户余额无关。金额都是 USD。
type UpstreamAccount struct {
	ID         int64            `json:"id"`
	Name       string           `json:"name"`
	Balance    decimal.Decimal  `json:"balance"`
	CostFactor decimal.Decimal  `json:"cost_factor"`
	AlertBelow *decimal.Decimal `json:"alert_below"`
	AutoSync   bool             `json:"auto_sync"`
	SyncedAt   *time.Time       `json:"synced_at"`
	SyncError  string           `json:"sync_error"`
	Note       string           `json:"note"`
	ChannelIDs []int64          `json:"channel_ids"`
	// 最近 7 天：上游成本（估算）和向用户收的钱
	Cost7d    decimal.Decimal `json:"cost_7d"`
	Revenue7d decimal.Decimal `json:"revenue_7d"`
	// 最近一次手动或自动校准的时间
	CalibratedAt *time.Time `json:"calibrated_at"`
}

func (s *Store) ListUpstreamAccounts(ctx context.Context) ([]UpstreamAccount, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT a.id, a.name, a.balance::text, a.cost_factor::text, a.alert_below::text, a.auto_sync, a.synced_at,
		       a.sync_error, a.note,
		       COALESCE((SELECT array_agg(id ORDER BY id) FROM channels WHERE upstream_account_id = a.id), '{}'),
		       COALESCE(u.cost, 0)::text, COALESCE(u.revenue, 0)::text,
		       (SELECT max(created_at) FROM upstream_account_entries e WHERE e.account_id = a.id AND e.kind IN ('calibrate', 'sync'))
		FROM upstream_accounts a
		LEFT JOIN LATERAL (
			SELECT sum(upstream_cost) AS cost, sum(cost) AS revenue FROM request_logs
			WHERE upstream_account_id = a.id AND created_at > now() - interval '7 days'
		) u ON TRUE
		ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UpstreamAccount, error) {
		var a UpstreamAccount
		var balance, factor, cost, revenue string
		var alert *string
		err := r.Scan(&a.ID, &a.Name, &balance, &factor, &alert, &a.AutoSync, &a.SyncedAt, &a.SyncError, &a.Note,
			&a.ChannelIDs, &cost, &revenue, &a.CalibratedAt)
		a.Balance, a.CostFactor, a.AlertBelow = mustDecimal(balance), mustDecimal(factor), optDecimal(alert)
		a.Cost7d, a.Revenue7d = mustDecimal(cost), mustDecimal(revenue)
		return a, err
	})
}

// SaveUpstreamAccount 新建或更新账户的设置和关联渠道。余额不在这里改，只能通过充值、校准。
func (s *Store) SaveUpstreamAccount(ctx context.Context, a *UpstreamAccount) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if a.ID == 0 {
			if err := tx.QueryRow(ctx, `
				INSERT INTO upstream_accounts (name, cost_factor, alert_below, auto_sync, note)
				VALUES ($1, $2::numeric, $3::numeric, $4, $5) RETURNING id`,
				a.Name, a.CostFactor.String(), decimalArg(a.AlertBelow), a.AutoSync, a.Note).Scan(&a.ID); err != nil {
				return translate(err)
			}
		} else if err := execOne(ctx, tx, `
			UPDATE upstream_accounts SET name = $2, cost_factor = $3::numeric, alert_below = $4::numeric, auto_sync = $5, note = $6
			WHERE id = $1`,
			a.ID, a.Name, a.CostFactor.String(), decimalArg(a.AlertBelow), a.AutoSync, a.Note); err != nil {
			return err
		}
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channels WHERE id = ANY($2::bigint[])
			AND upstream_account_id IS NOT NULL AND upstream_account_id <> $1)`, a.ID, a.ChannelIDs).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrChannelHasAccount
		}
		if _, err := tx.Exec(ctx, `UPDATE channels SET upstream_account_id = NULL
			WHERE upstream_account_id = $1 AND NOT (id = ANY($2::bigint[]))`, a.ID, a.ChannelIDs); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE channels SET upstream_account_id = $1 WHERE id = ANY($2::bigint[])`, a.ID, a.ChannelIDs)
		return err
	})
}

func (s *Store) DeleteUpstreamAccount(ctx context.Context, id int64) error {
	return execOne(ctx, s.Pool, `DELETE FROM upstream_accounts WHERE id = $1`, id)
}

// AccountEntry 是一条充值或校准流水。
type AccountEntry struct {
	ID               int64            `json:"id"`
	Kind             string           `json:"kind"` // recharge / calibrate / sync
	Amount           decimal.Decimal  `json:"amount"`
	BalanceBefore    decimal.Decimal  `json:"balance_before"`
	BalanceAfter     decimal.Decimal  `json:"balance_after"`
	OriginalAmount   *decimal.Decimal `json:"original_amount"`
	OriginalCurrency string           `json:"original_currency"`
	Operator         string           `json:"operator"`
	Note             string           `json:"note"`
	CreatedAt        time.Time        `json:"created_at"`
	// 校准类流水：距上一次校准，估算扣了多少（用来看偏差占比）
	EstimatedSpent *decimal.Decimal `json:"estimated_spent,omitempty"`
}

var ErrChannelHasAccount = errors.New("这个渠道已经开启了余额估算")

var ErrBadEntryKind = errors.New("流水类型只能是 recharge、calibrate 或 sync")

// AddAccountEntry 记一笔流水并更新余额。recharge：value 是充值额，余额加上它；
// calibrate / sync：value 是真实余额，余额直接改成它，流水金额记偏差（真实 - 估算）。
// original 是用户输入或接口返回的原始金额（可能是人民币），只做记录。
func (s *Store) AddAccountEntry(ctx context.Context, accountID int64, kind string, value decimal.Decimal,
	original decimal.Decimal, currency string, operatorID int64, note string) (*AccountEntry, error) {
	if kind != "recharge" && kind != "calibrate" && kind != "sync" {
		return nil, ErrBadEntryKind
	}
	e := &AccountEntry{Kind: kind, OriginalAmount: &original, OriginalCurrency: currency, Note: note}
	var opID *int64
	if operatorID != 0 {
		opID = &operatorID
	}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var before string
		if err := tx.QueryRow(ctx, `SELECT balance::text FROM upstream_accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&before); err != nil {
			return translate(err)
		}
		e.BalanceBefore = mustDecimal(before)
		if kind == "recharge" {
			e.Amount, e.BalanceAfter = value, e.BalanceBefore.Add(value)
		} else {
			e.Amount, e.BalanceAfter = value.Sub(e.BalanceBefore), value
		}
		sync := ""
		if kind == "sync" {
			sync = ", synced_at = now(), sync_error = ''"
		}
		if _, err := tx.Exec(ctx, `UPDATE upstream_accounts SET balance = $2::numeric`+sync+` WHERE id = $1`,
			accountID, e.BalanceAfter.String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO upstream_account_entries (account_id, kind, amount, balance_before, balance_after,
				original_amount, original_currency, operator_id, note)
			VALUES ($1, $2, $3::numeric, $4::numeric, $5::numeric, $6::numeric, $7, $8, $9) RETURNING id, created_at`,
			accountID, kind, e.Amount.String(), e.BalanceBefore.String(), e.BalanceAfter.String(),
			original.String(), currency, opID, note).Scan(&e.ID, &e.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}

// AccountEntries 返回最近 200 条流水，新的在前。
func (s *Store) AccountEntries(ctx context.Context, accountID int64) ([]AccountEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH e AS (
			SELECT e.*,
			       max(created_at) FILTER (WHERE kind IN ('calibrate', 'sync'))
			           OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS prev_calibrated
			FROM upstream_account_entries e WHERE account_id = $1
		)
		SELECT e.id, e.kind, e.amount::text, e.balance_before::text, e.balance_after::text,
		       e.original_amount::text, e.original_currency, COALESCE(o.username, ''), e.note, e.created_at,
		       CASE WHEN e.kind IN ('calibrate', 'sync') AND e.prev_calibrated IS NOT NULL THEN (
		           SELECT COALESCE(sum(upstream_cost), 0) FROM request_logs
		           WHERE upstream_account_id = $1 AND created_at > e.prev_calibrated AND created_at <= e.created_at
		       )::text END
		FROM e LEFT JOIN users o ON o.id = e.operator_id
		ORDER BY e.id DESC LIMIT 200`, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AccountEntry, error) {
		var e AccountEntry
		var amount, before, after string
		var original, spent *string
		err := r.Scan(&e.ID, &e.Kind, &amount, &before, &after, &original, &e.OriginalCurrency, &e.Operator, &e.Note,
			&e.CreatedAt, &spent)
		e.Amount, e.BalanceBefore, e.BalanceAfter = mustDecimal(amount), mustDecimal(before), mustDecimal(after)
		e.OriginalAmount, e.EstimatedSpent = optDecimal(original), optDecimal(spent)
		return e, err
	})
}

// SyncTarget 是能用余额接口自动校准的账户，以及用来查询的那个渠道。
type SyncTarget struct {
	AccountID int64
	Template  string
	BaseURL   string
	APIKey    string
}

// SyncTargets 返回开了自动校准的账户；每个账户取一个已启用、模板支持查余额的渠道。
// accountID 为 0 时返回全部，否则只返回这一个（不要求开了自动校准，用于手动"立即同步"）。
func (s *Store) SyncTargets(ctx context.Context, accountID int64, templates []string) ([]SyncTarget, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (a.id) a.id, c.template, c.base_url_chat, c.api_key
		FROM upstream_accounts a
		JOIN channels c ON c.upstream_account_id = a.id AND c.enabled AND c.template = ANY($2::text[]) AND c.base_url_chat <> ''
		WHERE ($1 = 0 AND a.auto_sync) OR a.id = $1
		ORDER BY a.id, c.priority DESC, c.id`, accountID, templates)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SyncTarget, error) {
		var t SyncTarget
		err := r.Scan(&t.AccountID, &t.Template, &t.BaseURL, &t.APIKey)
		return t, err
	})
}

func (s *Store) SetSyncError(ctx context.Context, accountID int64, msg string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE upstream_accounts SET synced_at = now(), sync_error = $2 WHERE id = $1`, accountID, msg)
	return err
}

// CNYPerUSD 是系统设置的汇率，没设置或格式不对时用默认值。
func (s *Store) CNYPerUSD(ctx context.Context) decimal.Decimal {
	v, err := s.Setting(ctx, "cny_per_usd")
	if err != nil {
		return defaultCNYPerUSD
	}
	d, err := decimal.NewFromString(v)
	if err != nil || !d.IsPositive() {
		return defaultCNYPerUSD
	}
	return d
}

var defaultCNYPerUSD = decimal.RequireFromString("6.7")

// Holidays 返回系统设置里的中国法定节假日（"YYYY-MM-DD"），没设置时为空。
func (s *Store) Holidays(ctx context.Context) ([]string, error) {
	v, err := s.Setting(ctx, "cn_holidays")
	if errors.Is(err, ErrNotFound) || (err == nil && v == "") {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, fmt.Errorf("节假日设置格式不对: %w", err)
	}
	return out, nil
}

func (s *Store) SetHolidays(ctx context.Context, dates []string) error {
	raw, _ := json.Marshal(dates)
	return s.SetSetting(ctx, "cn_holidays", string(raw))
}
