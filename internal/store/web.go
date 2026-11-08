package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/billing"
)

// 这个文件是管理端和用户端页面用的数据访问。金额字段以 decimal 返回，JSON 里是字符串。

var (
	ErrNotFound  = errors.New("记录不存在")
	ErrNameTaken = errors.New("名称已存在")
	ErrInUse     = errors.New("还有数据在使用它，不能删除")
)

// InputError 是存储层发现的输入问题（比如渠道 key 超过上限），接口层原样作为 400 提示返回。
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func badInput(format string, a ...any) error { return &InputError{Msg: fmt.Sprintf(format, a...)} }

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
	ID           int64           `json:"id"`
	Username     string          `json:"username"`
	Role         string          `json:"role"`
	Balance      decimal.Decimal `json:"balance"`
	Enabled      bool            `json:"enabled"`
	Preferences  json.RawMessage `json:"preferences"`
	UserGroupIDs []int64         `json:"user_group_ids"`
	CreatedAt    time.Time       `json:"created_at"`
	// Note 是管理员写的内部备注。只有 ListUsers 会带出来，不进 /api/me，成员看不到。
	Note string `json:"note,omitempty"`
	// LastUsedAt 是该用户所有 key 最近一次调用的时间，只在管理端列表里带出来。
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

const userColumns = `u.id, u.username, u.role, u.balance::text, u.enabled, u.preferences, u.created_at,
	COALESCE((SELECT array_agg(ugm.user_group_id ORDER BY ugm.user_group_id) FROM user_group_members ugm WHERE ugm.user_id = u.id), '{}')`

func scanUser(row pgx.Row) (*User, error) { return scanUserRow(row, false) }

// scanUserRow 按 userColumns 的顺序扫描；withNote 对应末尾多带的 u.note 和最近调用时间（管理端列表用）。
func scanUserRow(row pgx.Row, withNote bool) (*User, error) {
	var u User
	var balance string
	dest := []any{&u.ID, &u.Username, &u.Role, &balance, &u.Enabled, &u.Preferences, &u.CreatedAt, &u.UserGroupIDs}
	if withNote {
		dest = append(dest, &u.Note, &u.LastUsedAt)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, translate(err)
	}
	u.Balance = mustDecimal(balance)
	return &u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = $1`, id))
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userColumns+`, u.note,
		(SELECT max(k.last_used_at) FROM api_keys k WHERE k.user_id = u.id) FROM users u ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUserRow(rows, true)
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
	Username      *string
	Note          *string
	Role          *string
	Enabled       *bool
	PasswordHash  *string
	UserGroupIDs  *[]int64
	BalanceChange *UserBalanceChange
}

type UserBalanceChange struct {
	Mode       string          `json:"mode"`
	Amount     decimal.Decimal `json:"amount"`
	Note       string          `json:"note"`
	OperatorID int64           `json:"-"`
}

func (s *Store) UpdateUser(ctx context.Context, id int64, p UserPatch) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := track(ctx, tx, userEntity, &id, -1, func() error { return updateUserProfile(ctx, tx, id, p) }); err != nil {
			return err
		}
		if p.BalanceChange != nil {
			change := p.BalanceChange
			var beforeText string
			if err := tx.QueryRow(ctx, `SELECT balance::text FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&beforeText); err != nil {
				return err
			}
			before := mustDecimal(beforeText)
			delta := change.Amount
			if change.Mode == "set" {
				delta = delta.Sub(before)
			}
			if !delta.IsZero() {
				var after string
				if err := tx.QueryRow(ctx, `UPDATE users SET balance=balance+$2::numeric WHERE id=$1 RETURNING balance::text`, id, delta.String()).Scan(&after); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO balance_adjustments(user_id,amount,balance_after,operator_id,note) VALUES($1,$2::numeric,$3::numeric,$4,$5)`, id, delta.String(), after, change.OperatorID, change.Note); err != nil {
					return err
				}
				if err := recordBalance(ctx, tx, nil, id, delta, after, change.Note, "", "balance.adjust"); err != nil {
					return err
				}
			}
		}
		if (p.Enabled != nil && !*p.Enabled) || p.PasswordHash != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// updateUserProfile 改资料和用户分组，余额与会话由 UpdateUser 处理。
func updateUserProfile(ctx context.Context, tx pgx.Tx, id int64, p UserPatch) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users SET username = COALESCE($2, username), role = COALESCE($3, role),
		       enabled = COALESCE($4, enabled), password_hash = COALESCE($5, password_hash),
		       note = COALESCE($6, note)
		WHERE id = $1`, id, p.Username, p.Role, p.Enabled, p.PasswordHash, p.Note)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_username_key" {
			return ErrUsernameTaken
		}
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if p.UserGroupIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM user_group_members WHERE user_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_group_members (user_id, user_group_id) SELECT $1, unnest($2::bigint[])`, id, *p.UserGroupIDs); err != nil {
			return translate(err)
		}
	}
	return nil
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
		if _, err := tx.Exec(ctx, `INSERT INTO balance_adjustments (user_id, amount, balance_after, operator_id, note)
			VALUES ($1, $2::numeric, $3::numeric, $4, $5)`, userID, amount.String(), after, operatorID, note); err != nil {
			return err
		}
		return recordBalance(ctx, tx, &operatorID, userID, amount, after, note, "", "balance.adjust")
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

// SetUserGroups 设置用户所属的用户分组（整体替换）。
func (s *Store) SetUserGroups(ctx context.Context, userID int64, userGroupIDs []int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, userEntity, &userID, -1, func() error {
			if _, err := tx.Exec(ctx, `DELETE FROM user_group_members WHERE user_id = $1`, userID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO user_group_members (user_id, user_group_id) SELECT $1, unnest($2::bigint[])`, userID, userGroupIDs)
			return translate(err)
		})
	})
}

type UserGroup struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

func (s *Store) ListUserGroups(ctx context.Context) ([]UserGroup, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, is_default FROM user_groups ORDER BY is_default DESC, name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UserGroup, error) {
		var g UserGroup
		err := r.Scan(&g.ID, &g.Name, &g.IsDefault)
		return g, err
	})
}

func (s *Store) SaveUserGroup(ctx context.Context, g *UserGroup) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, userGroupEntity, &g.ID, 0, func() error {
			if g.ID == 0 {
				err := tx.QueryRow(ctx, `INSERT INTO user_groups (name) VALUES ($1) RETURNING id, is_default`, g.Name).Scan(&g.ID, &g.IsDefault)
				return translate(err)
			}
			return execOne(ctx, tx, `UPDATE user_groups SET name = $2 WHERE id = $1`, g.ID, g.Name)
		})
	})
}

func (s *Store) DeleteUserGroup(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, userGroupEntity, &id, 0, func() error {
			return execOne(ctx, tx, `DELETE FROM user_groups WHERE id = $1 AND NOT is_default`, id)
		})
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
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Ratio        decimal.Decimal `json:"ratio"`
	Enabled      bool            `json:"enabled"`
	IsPublic     bool            `json:"is_public"`
	UserGroupIDs []int64         `json:"user_group_ids"`
	Description  string          `json:"description"`
	// RetryCount 是渠道级失败（5xx、超时、断流）后最多重试几次，0–4；换 key 不占次数。
	// 保存时 nil 表示不改（旧客户端不带这个字段），新建时默认 2
	RetryCount *int `json:"retry_count"`
}

func scanGroups(rows pgx.Rows, err error) ([]Group, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Group, error) {
		var g Group
		var ratio string
		g.RetryCount = new(int)
		err := r.Scan(&g.ID, &g.Name, &ratio, &g.Enabled, &g.IsPublic, &g.UserGroupIDs, &g.Description, g.RetryCount)
		g.Ratio = mustDecimal(ratio)
		return g, err
	})
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	return scanGroups(s.Pool.Query(ctx, `SELECT g.id, g.name, g.ratio::text, g.enabled, g.is_public,
		COALESCE((SELECT array_agg(gug.user_group_id ORDER BY gug.user_group_id) FROM group_user_groups gug WHERE gug.group_id = g.id), '{}'),
		g.description, g.retry_count FROM groups g ORDER BY g.sort_order, g.id`))
}

// AccessibleGroups 返回用户能用的启用服务分组。
func (s *Store) AccessibleGroups(ctx context.Context, userID int64) ([]Group, error) {
	return scanGroups(s.Pool.Query(ctx, `
		SELECT g.id, g.name, g.ratio::text, g.enabled, g.is_public,
		       COALESCE((SELECT array_agg(gug.user_group_id ORDER BY gug.user_group_id) FROM group_user_groups gug WHERE gug.group_id = g.id), '{}'),
		       g.description, g.retry_count FROM groups g
		WHERE g.enabled AND (g.is_public OR EXISTS (
			SELECT 1 FROM group_user_groups gug
			JOIN user_group_members ugm ON ugm.user_group_id = gug.user_group_id
			WHERE gug.group_id = g.id AND ugm.user_id = $1
		))
		ORDER BY g.sort_order, g.id`, userID))
}

func (s *Store) SaveGroup(ctx context.Context, g *Group) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, groupEntity, &g.ID, 0, func() error { return saveGroup(ctx, tx, g) })
	})
}

func saveGroup(ctx context.Context, tx pgx.Tx, g *Group) error {
	var retries int
	if g.ID == 0 {
		err := tx.QueryRow(ctx, `INSERT INTO groups (name, ratio, enabled, is_public, description, retry_count, sort_order)
			VALUES ($1, $2::numeric, $3, $4, $5, COALESCE($6, 2), (SELECT COALESCE(max(sort_order), 0) + 1 FROM groups)) RETURNING id, retry_count`,
			g.Name, g.Ratio.String(), g.Enabled, g.IsPublic, g.Description, g.RetryCount).Scan(&g.ID, &retries)
		if err != nil {
			return translate(err)
		}
		g.RetryCount = &retries
	} else if err := tx.QueryRow(ctx, `UPDATE groups SET name = $2, ratio = $3::numeric, enabled = $4, is_public = $5, description = $6,
		retry_count = COALESCE($7, retry_count) WHERE id = $1 RETURNING retry_count`,
		g.ID, g.Name, g.Ratio.String(), g.Enabled, g.IsPublic, g.Description, g.RetryCount).Scan(&retries); err != nil {
		return translate(err)
	}
	g.RetryCount = &retries // 返回数据库里的实际值（新建默认 2、不带字段时保留原值）
	if _, err := tx.Exec(ctx, `DELETE FROM group_user_groups WHERE group_id = $1`, g.ID); err != nil {
		return err
	}
	if g.IsPublic || len(g.UserGroupIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO group_user_groups (group_id, user_group_id) SELECT $1, unnest($2::bigint[])`, g.ID, g.UserGroupIDs)
	return translate(err)
}

// ReorderGroups 按 ids 的顺序重排服务分组（只影响显示顺序）。不在 ids 里的分组排到后面，保持原有顺序。
func (s *Store) ReorderGroups(ctx context.Context, ids []int64) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE groups g SET sort_order = o.n FROM (
			SELECT g.id, row_number() OVER (ORDER BY array_position($1::bigint[], g.id) NULLS LAST, g.sort_order, g.id) AS n
			FROM groups g
		) o WHERE o.id = g.id`, ids)
	return err
}

func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, groupEntity, &id, 0, func() error {
			return execOne(ctx, tx, `DELETE FROM groups WHERE id = $1`, id)
		})
	})
}

// ---- 模型 ----

type Model struct {
	ID              int64               `json:"id"`
	Name            string              `json:"name"`
	InputPrice      decimal.Decimal     `json:"input_price"`
	OutputPrice     decimal.Decimal     `json:"output_price"`
	CacheReadPrice  decimal.Decimal     `json:"cache_read_price"`
	CacheWritePrice decimal.Decimal     `json:"cache_write_price"`
	PriceRules      []billing.PriceRule `json:"price_rules"`
	FastMultiplier  *decimal.Decimal    `json:"fast_multiplier"` // GPT Fast 计费倍数，nil 表示不开放
}

func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, input_price::text, output_price::text, cache_read_price::text,
		cache_write_price::text, price_rules, fast_multiplier::text FROM models ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Model, error) {
		var m Model
		var in, out, cr, cw string
		var fast *string
		err := r.Scan(&m.ID, &m.Name, &in, &out, &cr, &cw, &m.PriceRules, &fast)
		m.InputPrice, m.OutputPrice, m.CacheReadPrice, m.CacheWritePrice = mustDecimal(in), mustDecimal(out), mustDecimal(cr), mustDecimal(cw)
		m.FastMultiplier = optDecimal(fast)
		return m, err
	})
}

func (s *Store) SaveModel(ctx context.Context, m *Model) error {
	if m.PriceRules == nil {
		m.PriceRules = []billing.PriceRule{}
	}
	args := []any{m.Name, m.InputPrice.String(), m.OutputPrice.String(), m.CacheReadPrice.String(), m.CacheWritePrice.String(), m.PriceRules, decimalArg(m.FastMultiplier)}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, modelEntity, &m.ID, 0, func() error {
			if m.ID == 0 {
				err := tx.QueryRow(ctx, `INSERT INTO models (name, input_price, output_price, cache_read_price, cache_write_price, price_rules, fast_multiplier)
					VALUES ($1, $2::numeric, $3::numeric, $4::numeric, $5::numeric, $6, $7::numeric) RETURNING id`, args...).Scan(&m.ID)
				return translate(err)
			}
			return execOne(ctx, tx, `UPDATE models SET name = $2, input_price = $3::numeric, output_price = $4::numeric,
				cache_read_price = $5::numeric, cache_write_price = $6::numeric, price_rules = $7, fast_multiplier = $8::numeric WHERE id = $1`, append([]any{m.ID}, args...)...)
		})
	})
}

func (s *Store) DeleteModel(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, modelEntity, &id, 0, func() error {
			return execOne(ctx, tx, `DELETE FROM models WHERE id = $1`, id)
		})
	})
}

// PricedModel 是用户端"模型与价格"页的一行：某个分组下能用的模型，价格已乘倍率。
type PricedModel struct {
	GroupID    int64               `json:"group_id"`
	Name       string              `json:"name"`
	Protocols  []string            `json:"protocols"`
	Input      decimal.Decimal     `json:"input_price"`
	Output     decimal.Decimal     `json:"output_price"`
	CacheRead  decimal.Decimal     `json:"cache_read_price"`
	CacheWrite decimal.Decimal     `json:"cache_write_price"`
	PriceRules []billing.PriceRule `json:"price_rules"` // 价格同样已乘倍率
	// FastMultiplier：开放 GPT Fast 时的计费倍数（乘在上面任一单价上，含规则价），nil 表示不开放
	FastMultiplier *decimal.Decimal `json:"fast_multiplier"`
}

// PricedModels 返回这些分组下可用的模型和协议，价格是分组倍率之后的实际单价。
func (s *Store) PricedModels(ctx context.Context, groupIDs []int64) ([]PricedModel, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT cg.group_id, m.name,
		       array_remove(ARRAY[
		           CASE WHEN bool_or(c.base_url_chat <> '') THEN 'chat' END,
		           CASE WHEN bool_or(c.base_url_messages <> '') THEN 'messages' END,
		           CASE WHEN bool_or(c.base_url_responses <> '') THEN 'responses' END,
		           CASE WHEN bool_or(c.base_url_systemone <> '') THEN 'systemone' END], NULL),
		       (m.input_price * g.ratio)::text, (m.output_price * g.ratio)::text,
		       (m.cache_read_price * g.ratio)::text, (m.cache_write_price * g.ratio)::text,
		       m.price_rules, g.ratio::text, m.fast_multiplier::text
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id AND c.enabled
		JOIN channel_groups cg ON cg.channel_id = c.id
		JOIN groups g ON g.id = cg.group_id
		WHERE cg.group_id = ANY($1)
		GROUP BY cg.group_id, m.id, g.ratio
		ORDER BY cg.group_id, m.name`, groupIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PricedModel, error) {
		var p PricedModel
		var in, out, cr, cw, ratio string
		var fast *string
		err := r.Scan(&p.GroupID, &p.Name, &p.Protocols, &in, &out, &cr, &cw, &p.PriceRules, &ratio, &fast)
		p.FastMultiplier = optDecimal(fast)
		p.Input, p.Output, p.CacheRead, p.CacheWrite = mustDecimal(in), mustDecimal(out), mustDecimal(cr), mustDecimal(cw)
		if err == nil {
			k := mustDecimal(ratio)
			for i, rule := range p.PriceRules {
				rule.Input, rule.Output = rule.Input.Mul(k), rule.Output.Mul(k)
				rule.CacheRead, rule.CacheWrite = rule.CacheRead.Mul(k), rule.CacheWrite.Mul(k)
				p.PriceRules[i] = rule
			}
		}
		return p, err
	})
}

// ---- 系统设置 ----

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	return v, translate(err)
}

// SetSettings 在一个事务里保存多项设置，记一条操作日志列出变化的项。
func (s *Store) SetSettings(ctx context.Context, kv [][2]string) error {
	var none int64
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, settingsEntity, &none, 0, func() error {
			for _, p := range kv {
				if _, err := tx.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
					ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, p[0], p[1]); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// SetStatusSetting 保存运行状态类设置（如实时汇率的拉取状态），不记操作日志。
func (s *Store) SetStatusSetting(ctx context.Context, key, value string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
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

// CallLimits 是调用限制（2026-11-06 决策）：每个用户的并发上限、每分钟请求数上限（0 表示不限），
// 以及预扣时输出最多按多少 token 算（请求写得更少按请求的；0 表示完全按请求写的上限）。没设置或格式不对时用默认值。
type CallLimits struct {
	Concurrency   int64 `json:"user_concurrency"`
	RPM           int64 `json:"user_rpm"`
	ReserveOutput int64 `json:"reserve_output_tokens"`
}

var DefaultCallLimits = CallLimits{Concurrency: 10, RPM: 120, ReserveOutput: 8192}

func (s *Store) CallLimits(ctx context.Context) (CallLimits, error) {
	l := DefaultCallLimits
	rows, err := s.Pool.Query(ctx, `SELECT key, value FROM settings WHERE key IN ('user_concurrency', 'user_rpm', 'reserve_output_tokens')`)
	if err != nil {
		return l, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return l, err
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch k {
		case "user_concurrency":
			l.Concurrency = n
		case "user_rpm":
			l.RPM = n
		case "reserve_output_tokens":
			l.ReserveOutput = n
		}
	}
	return l, rows.Err()
}
