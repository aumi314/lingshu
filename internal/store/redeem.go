package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ---- 兑换码充值（2026-11-06 决策）----
// 生成、哈希沿用注册码的做法：明文只在生成时返回，库里存哈希；兑换时核销、加余额、记流水在同一个事务里。

var ErrInvalidRedeemCode = errors.New("兑换码无效、已使用、已撤销或已过期")
var ErrRedeemCodeUsed = errors.New("兑换码已使用，不能撤销")

type RedeemCode struct {
	ID        int64           `json:"id"`
	Prefix    string          `json:"prefix"`
	Amount    decimal.Decimal `json:"amount"`
	Note      string          `json:"note"`
	ExpiresAt *time.Time      `json:"expires_at"`
	CreatedAt time.Time       `json:"created_at"`
	UsedBy    string          `json:"used_by"`
	UsedAt    *time.Time      `json:"used_at"`
	RevokedAt *time.Time      `json:"revoked_at"`
}

var redeemEntity = entity{"redeem_code", "prefix", `
	SELECT jsonb_build_object('prefix', code_prefix, 'amount', trim_scale(amount)::text,
	       'status', CASE WHEN used_at IS NOT NULL THEN 'used' WHEN revoked_at IS NOT NULL THEN 'revoked' ELSE 'active' END)
	FROM redeem_codes WHERE id = $1`, nil}

// CreateRedeemCodes 生成 count 个面额 amount（美元）的兑换码，记一条操作日志。
func (s *Store) CreateRedeemCodes(ctx context.Context, adminID int64, count int, amount decimal.Decimal, note string, expiresAt *time.Time) ([]NewRegistrationCode, error) {
	codes := make([]NewRegistrationCode, count)
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		prefixes := make([]string, count)
		for i := range codes {
			code, prefix, err := randomRegistrationCode()
			if err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `INSERT INTO redeem_codes (code_hash, code_prefix, amount, note, expires_at, created_by)
				VALUES ($1, $2, $3::numeric, $4, $5, $6) RETURNING id`,
				registrationCodeHash(code), prefix, amount.String(), note, expiresAt, adminID).Scan(&codes[i].ID); err != nil {
				return err
			}
			codes[i].Prefix, codes[i].Code = prefix, code
			prefixes[i] = prefix
		}
		to, _ := json.Marshal(prefixes)
		face, _ := json.Marshal(amount.String())
		changes := []Change{{Field: "amount", To: face}, {Field: "prefixes", To: to}}
		if note != "" {
			n, _ := json.Marshal(note)
			changes = append(changes, Change{Field: "note", To: n})
		}
		return record(ctx, tx, operation{action: "redeem_code.create", targetType: "redeem_code", changes: changes})
	})
	if err != nil {
		return nil, translate(err)
	}
	return codes, nil
}

func (s *Store) ListRedeemCodes(ctx context.Context) ([]RedeemCode, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT rc.id, rc.code_prefix, rc.amount::text, rc.note, rc.expires_at, rc.created_at, COALESCE(u.username, ''), rc.used_at, rc.revoked_at
		FROM redeem_codes rc LEFT JOIN users u ON u.id = rc.used_by
		ORDER BY rc.id DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RedeemCode, error) {
		var c RedeemCode
		var amount string
		err := r.Scan(&c.ID, &c.Prefix, &amount, &c.Note, &c.ExpiresAt, &c.CreatedAt, &c.UsedBy, &c.UsedAt, &c.RevokedAt)
		c.Amount = mustDecimal(amount)
		return c, err
	})
}

func (s *Store) RevokeRedeemCode(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, redeemEntity, &id, 0, func() error {
			var used bool
			if err := tx.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM redeem_codes WHERE id = $1 FOR UPDATE`, id).Scan(&used); err != nil {
				return translate(err)
			}
			if used {
				return ErrRedeemCodeUsed
			}
			_, err := tx.Exec(ctx, `UPDATE redeem_codes SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
			return err
		})
	})
}

// Redeem 核销兑换码并给用户加余额，返回到账金额和兑换后余额。核销、加余额、余额流水、操作日志在同一事务里。
func (s *Store) Redeem(ctx context.Context, userID int64, code string) (decimal.Decimal, decimal.Decimal, error) {
	var amount, after decimal.Decimal
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var id int64
		var prefix, face string
		err := tx.QueryRow(ctx, `SELECT id, code_prefix, amount::text FROM redeem_codes
			WHERE code_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())
			FOR UPDATE`, registrationCodeHash(code)).Scan(&id, &prefix, &face)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidRedeemCode
		}
		if err != nil {
			return err
		}
		amount = mustDecimal(face)
		if _, err := tx.Exec(ctx, `UPDATE redeem_codes SET used_by = $2, used_at = now() WHERE id = $1`, id, userID); err != nil {
			return err
		}
		var balance string
		if err := tx.QueryRow(ctx, `UPDATE users SET balance = balance + $2::numeric WHERE id = $1 RETURNING balance::text`, userID, amount.String()).Scan(&balance); err != nil {
			return translate(err)
		}
		after = mustDecimal(balance)
		note := "兑换码 " + prefix
		if _, err := tx.Exec(ctx, `INSERT INTO balance_adjustments (user_id, amount, balance_after, operator_id, note) VALUES ($1, $2::numeric, $3::numeric, $1, $4)`,
			userID, amount.String(), balance, note); err != nil {
			return err
		}
		return recordBalance(ctx, tx, &userID, userID, amount, balance, note, "", "redeem.credit")
	})
	return amount, after, err
}
