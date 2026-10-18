package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// 这个文件是管理端和用户端页面用的数据访问。金额字段以 decimal 返回，JSON 里是字符串。

var (
	ErrNotFound  = errors.New("记录不存在")
	ErrNameTaken = errors.New("名称已存在")
	ErrInUse     = errors.New("还有数据在使用它，不能删除")
)

// translate 把常见的约束错误换成能直接给用户看的错误。
func translate(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrNameTaken
		case "23503":
			return ErrInUse
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func optDecimal(s *string) *decimal.Decimal {
	if s == nil {
		return nil
	}
	d := mustDecimal(*s)
	return &d
}

func decimalArg(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

func execOne(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, sql string, args ...any) error {
	tag, err := q.Exec(ctx, sql, args...)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- 用户 ----

type User struct {
	ID          int64           `json:"id"`
	Username    string          `json:"username"`
	Role        string          `json:"role"`
	Balance     decimal.Decimal `json:"balance"`
	Enabled     bool            `json:"enabled"`
	Preferences json.RawMessage `json:"preferences"`
	GroupIDs    []int64         `json:"group_ids"` // 被授权的特殊分组
	CreatedAt   time.Time       `json:"created_at"`
}

const userColumns = `u.id, u.username, u.role, u.balance::text, u.enabled, u.preferences, u.created_at,
	COALESCE((SELECT array_agg(ug.group_id ORDER BY ug.group_id) FROM user_groups ug WHERE ug.user_id = u.id), '{}')`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	var balance string
	if err := row.Scan(&u.ID, &u.Username, &u.Role, &balance, &u.Enabled, &u.Preferences, &u.CreatedAt, &u.GroupIDs); err != nil {
		return nil, translate(err)
	}
	u.Balance = mustDecimal(balance)
	return &u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = $1`, id))
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userColumns+` FROM users u ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserCredentials 用于登录校验。找不到返回 ErrNotFound。
func (s *Store) UserCredentials(ctx context.Context, username string) (id int64, passwordHash string, enabled bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT id, password_hash, enabled FROM users WHERE username = $1`, username).
		Scan(&id, &passwordHash, &enabled)
	return id, passwordHash, enabled, translate(err)
}

func (s *Store) PasswordHash(ctx context.Context, userID int64) (string, error) {
	var h string
	err := s.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&h)
	return h, translate(err)
}

type UserPatch struct {
	Role         *string
	Enabled      *bool
	PasswordHash *string
}

func (s *Store) UpdateUser(ctx context.Context, id int64, p UserPatch) error {
	return execOne(ctx, s.Pool, `
		UPDATE users SET role = COALESCE($2, role), enabled = COALESCE($3, enabled),
		       password_hash = COALESCE($4, password_hash)
		WHERE id = $1`, id, p.Role, p.Enabled, p.PasswordHash)
}

func (s *Store) SetPreferences(ctx context.Context, userID int64, prefs json.RawMessage) error {
	return execOne(ctx, s.Pool, `UPDATE users SET preferences = $2 WHERE id = $1`, userID, prefs)
}

// AdjustBalance 给用户加减余额（amount 可为负），记一条流水，返回调整后的余额。
func (s *Store) AdjustBalance(ctx context.Context, userID int64, amount decimal.Decimal, operatorID int64, note string) (decimal.Decimal, error) {
	var after string
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `UPDATE users SET balance = balance + $2::numeric WHERE id = $1 RETURNING balance::text`,
			userID, amount.String()).Scan(&after); err != nil {
			return translate(err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO balance_adjustments (user_id, amount, balance_after, operator_id, note)
			VALUES ($1, $2::numeric, $3::numeric, $4, $5)`, userID, amount.String(), after, operatorID, note)
		return err
	})
	if err != nil {
		return decimal.Zero, err
	}
	return mustDecimal(after), nil
}

type BalanceAdjustment struct {
	ID           int64           `json:"id"`
	Amount       decimal.Decimal `json:"amount"`
	BalanceAfter decimal.Decimal `json:"balance_after"`
	Operator     string          `json:"operator"`
	Note         string          `json:"note"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (s *Store) BalanceAdjustments(ctx context.Context, userID int64) ([]BalanceAdjustment, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT b.id, b.amount::text, b.balance_after::text, COALESCE(o.username, ''), b.note, b.created_at
		FROM balance_adjustments b LEFT JOIN users o ON o.id = b.operator_id
		WHERE b.user_id = $1 ORDER BY b.id DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (BalanceAdjustment, error) {
		var b BalanceAdjustment
		var amount, after string
		err := r.Scan(&b.ID, &amount, &after, &b.Operator, &b.Note, &b.CreatedAt)
		b.Amount, b.BalanceAfter = mustDecimal(amount), mustDecimal(after)
		return b, err
	})
}

// SetUserGroups 设置用户被授权的特殊分组（整体替换）。
func (s *Store) SetUserGroups(ctx context.Context, userID int64, groupIDs []int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_groups WHERE user_id = $1`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_groups (user_id, group_id) SELECT $1, unnest($2::bigint[])`, userID, groupIDs)
		return translate(err)
	})
}

// ---- 会话 ----

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expires time.Time) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, tokenHash, userID, expires)
	return err
}

// SessionUser 返回会话对应的用户；会话不存在、已过期或用户已停用时返回 ErrNotFound。
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+`
		FROM sessions se JOIN users u ON u.id = se.user_id
		WHERE se.token_hash = $1 AND se.expires_at > now() AND u.enabled`, tokenHash))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteUserSessions 让用户在所有设备上下线（改密码、停用账号时用）。
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// ---- 分组 ----

type Group struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Ratio       decimal.Decimal `json:"ratio"`
	IsPublic    bool            `json:"is_public"`
	Description string          `json:"description"`
}

func scanGroups(rows pgx.Rows, err error) ([]Group, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Group, error) {
		var g Group
		var ratio string
		err := r.Scan(&g.ID, &g.Name, &ratio, &g.IsPublic, &g.Description)
		g.Ratio = mustDecimal(ratio)
		return g, err
	})
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	return scanGroups(s.Pool.Query(ctx, `SELECT id, name, ratio::text, is_public, description FROM groups ORDER BY id`))
}

// AccessibleGroups 返回用户能用的分组：公开分组 + 被授权的特殊分组。
func (s *Store) AccessibleGroups(ctx context.Context, userID int64) ([]Group, error) {
	return scanGroups(s.Pool.Query(ctx, `
		SELECT g.id, g.name, g.ratio::text, g.is_public, g.description FROM groups g
		WHERE g.is_public OR EXISTS (SELECT 1 FROM user_groups ug WHERE ug.user_id = $1 AND ug.group_id = g.id)
		ORDER BY g.ratio, g.id`, userID))
}

func (s *Store) SaveGroup(ctx context.Context, g *Group) error {
	if g.ID == 0 {
		err := s.Pool.QueryRow(ctx, `INSERT INTO groups (name, ratio, is_public, description) VALUES ($1, $2::numeric, $3, $4) RETURNING id`,
			g.Name, g.Ratio.String(), g.IsPublic, g.Description).Scan(&g.ID)
		return translate(err)
	}
	return execOne(ctx, s.Pool, `UPDATE groups SET name = $2, ratio = $3::numeric, is_public = $4, description = $5 WHERE id = $1`,
		g.ID, g.Name, g.Ratio.String(), g.IsPublic, g.Description)
}

func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	return execOne(ctx, s.Pool, `DELETE FROM groups WHERE id = $1`, id)
}

// ---- 模型 ----

type Model struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	InputPrice      decimal.Decimal `json:"input_price"`
	OutputPrice     decimal.Decimal `json:"output_price"`
	CacheReadPrice  decimal.Decimal `json:"cache_read_price"`
	CacheWritePrice decimal.Decimal `json:"cache_write_price"`
	Enabled         bool            `json:"enabled"`
}

func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, input_price::text, output_price::text, cache_read_price::text,
		cache_write_price::text, enabled FROM models ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Model, error) {
		var m Model
		var in, out, cr, cw string
		err := r.Scan(&m.ID, &m.Name, &in, &out, &cr, &cw, &m.Enabled)
		m.InputPrice, m.OutputPrice, m.CacheReadPrice, m.CacheWritePrice = mustDecimal(in), mustDecimal(out), mustDecimal(cr), mustDecimal(cw)
		return m, err
	})
}

func (s *Store) SaveModel(ctx context.Context, m *Model) error {
	args := []any{m.Name, m.InputPrice.String(), m.OutputPrice.String(), m.CacheReadPrice.String(), m.CacheWritePrice.String(), m.Enabled}
	if m.ID == 0 {
		err := s.Pool.QueryRow(ctx, `INSERT INTO models (name, input_price, output_price, cache_read_price, cache_write_price, enabled)
			VALUES ($1, $2::numeric, $3::numeric, $4::numeric, $5::numeric, $6) RETURNING id`, args...).Scan(&m.ID)
		return translate(err)
	}
	return execOne(ctx, s.Pool, `UPDATE models SET name = $2, input_price = $3::numeric, output_price = $4::numeric,
		cache_read_price = $5::numeric, cache_write_price = $6::numeric, enabled = $7 WHERE id = $1`, append([]any{m.ID}, args...)...)
}

func (s *Store) DeleteModel(ctx context.Context, id int64) error {
	return execOne(ctx, s.Pool, `DELETE FROM models WHERE id = $1`, id)
}

// PricedModel 是用户端"模型与价格"页的一行：某个分组下能用的模型，价格已乘倍率。
type PricedModel struct {
	GroupID    int64           `json:"group_id"`
	Name       string          `json:"name"`
	Protocols  []string        `json:"protocols"`
	Input      decimal.Decimal `json:"input_price"`
	Output     decimal.Decimal `json:"output_price"`
	CacheRead  decimal.Decimal `json:"cache_read_price"`
	CacheWrite decimal.Decimal `json:"cache_write_price"`
}

// PricedModels 返回这些分组下可用的模型和协议，价格是分组倍率之后的实际单价。
func (s *Store) PricedModels(ctx context.Context, groupIDs []int64) ([]PricedModel, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT cg.group_id, m.name,
		       array_remove(ARRAY[
		           CASE WHEN bool_or(c.base_url_chat <> '') THEN 'chat' END,
		           CASE WHEN bool_or(c.base_url_messages <> '') THEN 'messages' END,
		           CASE WHEN bool_or(c.base_url_responses <> '') THEN 'responses' END], NULL),
		       (m.input_price * g.ratio)::text, (m.output_price * g.ratio)::text,
		       (m.cache_read_price * g.ratio)::text, (m.cache_write_price * g.ratio)::text
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id AND c.enabled
		JOIN channel_groups cg ON cg.channel_id = c.id
		JOIN groups g ON g.id = cg.group_id
		WHERE m.enabled AND cg.group_id = ANY($1)
		GROUP BY cg.group_id, m.id, g.ratio
		ORDER BY cg.group_id, m.name`, groupIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PricedModel, error) {
		var p PricedModel
		var in, out, cr, cw string
		err := r.Scan(&p.GroupID, &p.Name, &p.Protocols, &in, &out, &cr, &cw)
		p.Input, p.Output, p.CacheRead, p.CacheWrite = mustDecimal(in), mustDecimal(out), mustDecimal(cr), mustDecimal(cw)
		return p, err
	})
}

// ---- 系统设置 ----

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	return v, translate(err)
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	return err
}
