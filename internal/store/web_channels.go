package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ---- 渠道 ----

type ChannelModel struct {
	ModelID      int64  `json:"model_id"`
	ModelName    string `json:"model_name"`
	UpstreamName string `json:"upstream_name"` // 空表示和站内标准名相同
}

// Channel 返回给管理端时 APIKey 只显示首尾，完整 key 不出服务器。
type Channel struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	Template         string         `json:"template"`
	APIKey           string         `json:"api_key,omitempty"` // 只用于保存，读取时为空
	APIKeyMasked     string         `json:"api_key_masked"`
	Priority         int            `json:"priority"`
	Enabled          bool           `json:"enabled"`
	BaseURLChat      string         `json:"base_url_chat"`
	BaseURLMessages  string         `json:"base_url_messages"`
	BaseURLResponses string         `json:"base_url_responses"`
	BaseURLSystemOne string         `json:"base_url_systemone"`
	ProxyURL         string         `json:"proxy_url"` // 空表示直连
	GroupIDs         []int64        `json:"group_ids"`
	Models           []ChannelModel `json:"models"`
}

func maskKey(k string) string {
	if len(k) <= 10 {
		return strings.Repeat("*", len(k))
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func (s *Store) ListChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, c.name, c.template, c.api_key, c.priority, c.enabled,
		       c.base_url_chat, c.base_url_messages, c.base_url_responses, c.base_url_systemone, c.proxy_url,
		       COALESCE((SELECT array_agg(group_id ORDER BY group_id) FROM channel_groups WHERE channel_id = c.id), '{}')
		FROM channels c ORDER BY c.sort_order, c.id`)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Channel{}
	var out []*Channel
	for rows.Next() {
		c := &Channel{Models: []ChannelModel{}}
		var key string
		if err := rows.Scan(&c.ID, &c.Name, &c.Template, &key, &c.Priority, &c.Enabled,
			&c.BaseURLChat, &c.BaseURLMessages, &c.BaseURLResponses, &c.BaseURLSystemOne, &c.ProxyURL, &c.GroupIDs); err != nil {
			rows.Close()
			return nil, err
		}
		c.APIKeyMasked = maskKey(key)
		byID[c.ID] = c
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	mrows, err := s.Pool.Query(ctx, `
		SELECT cm.channel_id, m.id, m.name, cm.upstream_name
		FROM channel_models cm JOIN models m ON m.id = cm.model_id ORDER BY m.name`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var chID int64
		var cm ChannelModel
		if err := mrows.Scan(&chID, &cm.ModelID, &cm.ModelName, &cm.UpstreamName); err != nil {
			return nil, err
		}
		if c := byID[chID]; c != nil {
			c.Models = append(c.Models, cm)
		}
	}
	return out, mrows.Err()
}

// ChannelSecret 返回渠道的完整配置（含 key），只给服务器内部用（拉取模型、冒烟测试）。
func (s *Store) ChannelSecret(ctx context.Context, id int64) (*Channel, error) {
	var c Channel
	err := s.Pool.QueryRow(ctx, `SELECT id, name, template, api_key, base_url_chat, base_url_messages, base_url_responses, base_url_systemone, proxy_url
		FROM channels WHERE id = $1`, id).Scan(&c.ID, &c.Name, &c.Template, &c.APIKey, &c.BaseURLChat, &c.BaseURLMessages, &c.BaseURLResponses, &c.BaseURLSystemOne, &c.ProxyURL)
	if err != nil {
		return nil, translate(err)
	}
	return &c, nil
}

// SaveChannel 新建或更新渠道，连同所属分组和模型映射一起整体保存。更新时 APIKey 为空表示不改 key。
func (s *Store) SaveChannel(ctx context.Context, c *Channel) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if c.ID == 0 {
			if c.APIKey == "" {
				return fmt.Errorf("新建渠道必须填写 API key")
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO channels (name, template, api_key, priority, enabled, base_url_chat, base_url_messages, base_url_responses, base_url_systemone, proxy_url, sort_order)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, (SELECT COALESCE(max(sort_order), 0) + 1 FROM channels)) RETURNING id`,
				c.Name, c.Template, c.APIKey, c.Priority, c.Enabled, c.BaseURLChat, c.BaseURLMessages, c.BaseURLResponses, c.BaseURLSystemOne, c.ProxyURL,
			).Scan(&c.ID); err != nil {
				return translate(err)
			}
		} else {
			if err := execOne(ctx, tx, `
				UPDATE channels SET name = $2, template = $3, api_key = COALESCE(NULLIF($4, ''), api_key), priority = $5,
				       enabled = $6, base_url_chat = $7, base_url_messages = $8, base_url_responses = $9, proxy_url = $10, base_url_systemone = $11
				WHERE id = $1`,
				c.ID, c.Name, c.Template, c.APIKey, c.Priority, c.Enabled, c.BaseURLChat, c.BaseURLMessages, c.BaseURLResponses, c.ProxyURL, c.BaseURLSystemOne,
			); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM channel_groups WHERE channel_id = $1`, c.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO channel_groups SELECT $1, unnest($2::bigint[])`, c.ID, c.GroupIDs); err != nil {
			return translate(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM channel_models WHERE channel_id = $1`, c.ID); err != nil {
			return err
		}
		for _, m := range c.Models {
			upstream := m.UpstreamName
			if upstream == m.ModelName {
				upstream = ""
			}
			if _, err := tx.Exec(ctx, `INSERT INTO channel_models (channel_id, model_id, upstream_name) VALUES ($1, $2, $3)`,
				c.ID, m.ModelID, upstream); err != nil {
				return translate(err)
			}
		}
		return nil
	})
}

// ReorderChannels 按 ids 的顺序重排渠道列表（只影响显示）。不在 ids 里的渠道排到后面，保持原有顺序。
func (s *Store) ReorderChannels(ctx context.Context, ids []int64) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE channels c SET sort_order = o.n FROM (
			SELECT c.id, row_number() OVER (ORDER BY array_position($1::bigint[], c.id) NULLS LAST, c.sort_order, c.id) AS n
			FROM channels c
		) o WHERE o.id = c.id`, ids)
	return err
}

// DeleteChannel 删除渠道，连同它的余额估算和流水（请求日志里的上游成本保留）。
func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM upstream_accounts WHERE id = (SELECT upstream_account_id FROM channels WHERE id = $1)`, id); err != nil {
			return err
		}
		return execOne(ctx, tx, `DELETE FROM channels WHERE id = $1`, id)
	})
}

// ---- 用户的 API key ----

type APIKey struct {
	ID         int64            `json:"id"`
	Name       string           `json:"name"`
	Prefix     string           `json:"prefix"`
	GroupID    int64            `json:"group_id"`
	GroupName  string           `json:"group_name"`
	QuotaLimit *decimal.Decimal `json:"quota_limit"`
	QuotaUsed  decimal.Decimal  `json:"quota_used"`
	Enabled    bool             `json:"enabled"`
	LastUsedAt *time.Time       `json:"last_used_at"`
	CreatedAt  time.Time        `json:"created_at"`
}

func (s *Store) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT k.id, k.name, k.key_prefix, k.group_id, g.name, k.quota_limit::text, k.quota_used::text,
		       k.enabled, k.last_used_at, k.created_at
		FROM api_keys k JOIN groups g ON g.id = k.group_id
		WHERE k.user_id = $1 ORDER BY k.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIKey, error) {
		var k APIKey
		var limit *string
		var used string
		err := r.Scan(&k.ID, &k.Name, &k.Prefix, &k.GroupID, &k.GroupName, &limit, &used, &k.Enabled, &k.LastUsedAt, &k.CreatedAt)
		k.QuotaLimit, k.QuotaUsed = optDecimal(limit), mustDecimal(used)
		return k, err
	})
}

// SetAPIKeyQuota 设置 key 的限额，nil 表示不限。
func (s *Store) SetAPIKeyQuota(ctx context.Context, userID, keyID int64, limit *decimal.Decimal) error {
	return execOne(ctx, s.Pool, `UPDATE api_keys SET quota_limit = $3::numeric WHERE id = $2 AND user_id = $1`,
		userID, keyID, decimalArg(limit))
}

type APIKeyPatch struct {
	Name    *string
	Enabled *bool
	GroupID *int64
}

func (s *Store) UpdateAPIKey(ctx context.Context, userID, keyID int64, p APIKeyPatch) error {
	return execOne(ctx, s.Pool, `
		UPDATE api_keys SET name = COALESCE($3, name), enabled = COALESCE($4, enabled), group_id = COALESCE($5, group_id)
		WHERE id = $2 AND user_id = $1`, userID, keyID, p.Name, p.Enabled, p.GroupID)
}

func (s *Store) DeleteAPIKey(ctx context.Context, userID, keyID int64) error {
	return execOne(ctx, s.Pool, `DELETE FROM api_keys WHERE id = $2 AND user_id = $1`, userID, keyID)
}

// ---- 请求日志 ----

type LogFilter struct {
	Type      string
	IDs       []int64
	UserID    int64 // 0 表示全部用户（管理端）
	Model     string
	ChannelID int64
	Status    string
	From, To  *time.Time
	Limit     int
	Offset    int
}

type LogEntry struct {
	ID               int64           `json:"id"`
	RequestID        string          `json:"request_id"`
	Username         string          `json:"username"`
	KeyName          string          `json:"key_name"`
	GroupName        string          `json:"group_name"`
	Protocol         string          `json:"protocol"`
	Model            string          `json:"model"`
	UpstreamModel    string          `json:"upstream_model,omitempty"`
	ChannelName      string          `json:"channel_name,omitempty"`
	Stream           bool            `json:"stream"`
	Status           string          `json:"status"`
	HTTPStatus       int             `json:"http_status"`
	Error            string          `json:"error"`
	InputTokens      int64           `json:"input_tokens"`
	CacheReadTokens  int64           `json:"cache_read_tokens"`
	CacheWriteTokens int64           `json:"cache_write_tokens"`
	OutputTokens     int64           `json:"output_tokens"`
	ReasoningTokens  int64           `json:"reasoning_tokens"`
	UsageEstimated   bool            `json:"usage_estimated"`
	GroupRatio       decimal.Decimal `json:"group_ratio"`
	PriceRule        string          `json:"price_rule"` // 命中的价格规则，空表示基础价
	InputPrice       decimal.Decimal `json:"input_price"`
	OutputPrice      decimal.Decimal `json:"output_price"`
	Cost             decimal.Decimal `json:"cost"`
	Attempts         int             `json:"attempts"`
	FirstByteMS      int             `json:"first_byte_ms"`
	TotalMS          int             `json:"total_ms"`
	CreatedAt        time.Time       `json:"created_at"`
}

type LogSummary struct {
	TopupTotal    decimal.Decimal `json:"topup_total"`
	AdjustmentNet decimal.Decimal `json:"adjustment_net"`
	Requests      int64           `json:"requests"`
	Failed        int64           `json:"failed"`
	InputTokens   int64           `json:"input_tokens"` // 含缓存读写
	OutputTokens  int64           `json:"output_tokens"`
	Cost          decimal.Decimal `json:"cost"`
}

func (f LogFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.UserID != 0 {
		add("l.user_id = $%d", f.UserID)
	}
	if len(f.IDs) > 0 {
		add("l.id = ANY($%d::bigint[])", f.IDs)
	}
	if f.Model != "" {
		add("l.model = $%d", f.Model)
	}
	if f.ChannelID != 0 {
		add("l.channel_id = $%d", f.ChannelID)
	}
	if f.Status != "" {
		add("l.status = $%d", f.Status)
	}
	if f.From != nil {
		add("l.created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("l.created_at < $%d", *f.To)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// ListLogs 返回一页日志和筛选范围内的汇总。
func (s *Store) ListLogs(ctx context.Context, f LogFilter) ([]LogEntry, LogSummary, error) {
	where, args := f.where()
	var sum LogSummary
	var cost string
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE l.status NOT IN ('success', 'client_cancelled')),
		       COALESCE(sum(l.input_tokens + l.cache_read_tokens + l.cache_write_tokens), 0),
		       COALESCE(sum(l.output_tokens), 0), COALESCE(sum(l.cost), 0)::text
		FROM request_logs l `+where, args...).Scan(&sum.Requests, &sum.Failed, &sum.InputTokens, &sum.OutputTokens, &cost); err != nil {
		return nil, sum, err
	}
	sum.Cost = mustDecimal(cost)

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit, f.Offset)
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
		SELECT l.id, l.request_id, u.username, COALESCE(NULLIF(l.key_name, ''), k.name, ''), COALESCE(g.name, ''), l.protocol, l.model,
		       l.upstream_model, COALESCE(NULLIF(l.channel_name, ''), c.name, ''), l.stream, l.status, l.http_status, l.error,
		       l.input_tokens, l.cache_read_tokens, l.cache_write_tokens, l.output_tokens, l.reasoning_tokens,
		       l.usage_estimated, l.group_ratio::text, l.cost::text, l.attempts, l.first_byte_ms, l.total_ms, l.created_at,
		       l.price_rule, l.input_price::text, l.output_price::text
		FROM request_logs l
		JOIN users u ON u.id = l.user_id
		LEFT JOIN api_keys k ON k.id = l.api_key_id
		LEFT JOIN groups g ON g.id = l.group_id
		LEFT JOIN channels c ON c.id = l.channel_id
		%s ORDER BY l.id DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, sum, err
	}
	logs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LogEntry, error) {
		var e LogEntry
		var ratio, cost, inPrice, outPrice string
		err := r.Scan(&e.ID, &e.RequestID, &e.Username, &e.KeyName, &e.GroupName, &e.Protocol, &e.Model,
			&e.UpstreamModel, &e.ChannelName, &e.Stream, &e.Status, &e.HTTPStatus, &e.Error,
			&e.InputTokens, &e.CacheReadTokens, &e.CacheWriteTokens, &e.OutputTokens, &e.ReasoningTokens,
			&e.UsageEstimated, &ratio, &cost, &e.Attempts, &e.FirstByteMS, &e.TotalMS, &e.CreatedAt,
			&e.PriceRule, &inPrice, &outPrice)
		e.GroupRatio, e.Cost = mustDecimal(ratio), mustDecimal(cost)
		e.InputPrice, e.OutputPrice = mustDecimal(inPrice), mustDecimal(outPrice)
		return e, err
	})
	return logs, sum, err
}

// LogModels 返回日志里出现过的模型名，给筛选下拉框用。
func (s *Store) LogModels(ctx context.Context, userID int64) ([]string, error) {
	q, args := `SELECT DISTINCT model FROM request_logs ORDER BY model`, []any{}
	if userID != 0 {
		q, args = `SELECT DISTINCT model FROM request_logs WHERE user_id = $1 ORDER BY model`, []any{userID}
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
