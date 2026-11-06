package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/billing"
)

// KeyOwner 是网关鉴权和准入需要的 key、用户、分组信息。
type KeyOwner struct {
	KeyID        int64
	KeyName      string
	UserID       int64
	GroupID      int64
	GroupName    string
	KeyEnabled   bool
	UserEnabled  bool
	GroupAllowed bool // 公开分组，或用户被授权了这个特殊分组
	GroupRatio   decimal.Decimal
	Balance      decimal.Decimal
	Reserved     decimal.Decimal  // 进行中请求冻结的额度，可用余额 = Balance - Reserved
	QuotaLimit   *decimal.Decimal // nil 表示不限额
	QuotaUsed    decimal.Decimal
}

// LookupKey 按 key 哈希查找，找不到返回 nil。
func (s *Store) LookupKey(ctx context.Context, keyHash string) (*KeyOwner, error) {
	var o KeyOwner
	var ratio, balance, reserved, used string
	var limit *string
	err := s.Pool.QueryRow(ctx, `
		SELECT k.id, k.name, k.user_id, k.group_id, g.name, k.enabled, u.enabled,
		       g.enabled AND (g.is_public OR EXISTS (
			       SELECT 1 FROM group_user_groups gug
			       JOIN user_group_members ugm ON ugm.user_group_id = gug.user_group_id
			       WHERE gug.group_id = g.id AND ugm.user_id = u.id
		       )),
		       g.ratio::text, u.balance::text, u.reserved::text, k.quota_limit::text, k.quota_used::text
		FROM api_keys k
		JOIN users u ON u.id = k.user_id
		JOIN groups g ON g.id = k.group_id
		WHERE k.key_hash = $1`, keyHash,
	).Scan(&o.KeyID, &o.KeyName, &o.UserID, &o.GroupID, &o.GroupName, &o.KeyEnabled, &o.UserEnabled, &o.GroupAllowed,
		&ratio, &balance, &reserved, &limit, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.GroupRatio, o.Balance, o.Reserved, o.QuotaUsed = mustDecimal(ratio), mustDecimal(balance), mustDecimal(reserved), mustDecimal(used)
	if limit != nil {
		l := mustDecimal(*limit)
		o.QuotaLimit = &l
	}
	return &o, nil
}

// 数据库 NUMERIC 转成的文本一定是合法数字。
func mustDecimal(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

// Route 是一个可用的上游渠道，带上模型的站内标价。
type Route struct {
	ChannelID     int64
	ChannelName   string
	BaseURL       string
	APIKey        string
	ProxyURL      string // 空表示直连
	UpstreamModel string
	Prices        billing.Prices
	PriceRules    []billing.PriceRule
}

// 协议到渠道地址列的映射；列名只来自这里，不拼接外部输入。
var protocolColumns = map[string]string{
	"chat":      "base_url_chat",
	"messages":  "base_url_messages",
	"responses": "base_url_responses",
	"systemone": "base_url_systemone",
}

// FindRoutes 返回分组内支持该协议和模型的渠道，按优先级从高到低，同优先级随机。
func (s *Store) FindRoutes(ctx context.Context, groupID int64, protocol, model string) ([]Route, error) {
	col, ok := protocolColumns[protocol]
	if !ok {
		return nil, fmt.Errorf("未知协议 %q", protocol)
	}
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
		SELECT c.id, c.name, c.%[1]s, c.api_key, c.proxy_url, COALESCE(NULLIF(cm.upstream_name, ''), m.name),
		       m.input_price::text, m.output_price::text, m.cache_read_price::text, m.cache_write_price::text, m.price_rules
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id
		JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id = $1
		WHERE m.name = $2 AND c.enabled AND c.%[1]s <> ''
		ORDER BY c.priority DESC, random()`, col), groupID, model)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Route, error) {
		var rt Route
		var in, out, cr, cw string
		if err := r.Scan(&rt.ChannelID, &rt.ChannelName, &rt.BaseURL, &rt.APIKey, &rt.ProxyURL, &rt.UpstreamModel, &in, &out, &cr, &cw,
			&rt.PriceRules); err != nil {
			return rt, err
		}
		rt.Prices = billing.Prices{Input: mustDecimal(in), Output: mustDecimal(out), CacheRead: mustDecimal(cr), CacheWrite: mustDecimal(cw)}
		return rt, nil
	})
}

// ModelProtocols 返回分组内这个模型能用哪些协议，用来提示用户换入口。
func (s *Store) ModelProtocols(ctx context.Context, groupID int64, model string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT p FROM (
			SELECT CASE WHEN c.base_url_chat <> '' THEN 'chat' END AS p1,
			       CASE WHEN c.base_url_messages <> '' THEN 'messages' END AS p2,
			       CASE WHEN c.base_url_responses <> '' THEN 'responses' END AS p3,
			       CASE WHEN c.base_url_systemone <> '' THEN 'systemone' END AS p4
			FROM models m
			JOIN channel_models cm ON cm.model_id = m.id
			JOIN channels c ON c.id = cm.channel_id
			JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id = $1
			WHERE m.name = $2 AND c.enabled
		) t, LATERAL (VALUES (p1), (p2), (p3), (p4)) v(p)
		WHERE p IS NOT NULL ORDER BY p`, groupID, model)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ChannelForResponse 查 responses 协议的某次响应是哪个渠道处理的，只查这个用户自己的记录。找不到返回 0。
func (s *Store) ChannelForResponse(ctx context.Context, userID int64, responseID string) (int64, error) {
	var id *int64
	err := s.Pool.QueryRow(ctx, `
		SELECT channel_id FROM request_logs
		WHERE upstream_response_id = $1 AND user_id = $2
		ORDER BY id DESC LIMIT 1`, responseID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) || id == nil {
		return 0, nil
	}
	return *id, err
}

// GroupModels 返回分组内可用的模型名，给 /v1/models 用。
func (s *Store) GroupModels(ctx context.Context, groupID int64) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT m.name
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id
		JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id = $1
		WHERE c.enabled
		ORDER BY m.name`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// RequestLog 对应 request_logs 一行。
type RequestLog struct {
	RequestID          string
	UserID             int64
	APIKeyID           int64
	KeyName            string // 名字快照：渠道或 key 被删除后，历史日志仍能看出是谁
	GroupID            int64
	GroupName          string
	ClientIP           string
	ReasoningEffort    string
	Protocol           string
	Model              string
	UpstreamModel      string
	ChannelID          int64 // 0 表示没选到渠道
	ChannelName        string
	Stream             bool
	Status             string
	HTTPStatus         int
	Error              string
	InputTokens        int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	OutputTokens       int64
	ReasoningTokens    int64
	UsageEstimated     bool
	Prices             billing.Prices  // 实际生效的单价快照（命中规则时是规则价）
	PriceRule          string          // 命中的价格规则名，空表示基础价
	GroupRatio         decimal.Decimal // 倍率快照
	Cost               decimal.Decimal // 实际扣费，0 表示不扣
	Attempts           int
	FirstByteMS        int
	TotalMS            int
	UpstreamResponseID string
	Reserved           decimal.Decimal // 放行时冻结的额度，结算时解冻（不写进日志表）
}

// SettleRequest 在一个事务里写日志、扣用户余额、加 key 的已用额度，并更新 key 最后使用时间。
// 一个请求只调用一次。
func (s *Store) SettleRequest(ctx context.Context, l *RequestLog) error {
	var channelID *int64
	if l.ChannelID != 0 {
		channelID = &l.ChannelID
	}
	ratio := l.GroupRatio
	if ratio.IsZero() && l.Cost.IsZero() {
		ratio = decimal.NewFromInt(1) // 没走到计费的请求（如被拒绝），倍率记 1
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO request_logs (
				request_id, user_id, api_key_id, group_id, protocol, model, upstream_model, channel_id,
				stream, status, http_status, error,
				input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens, usage_estimated,
				input_price, output_price, cache_read_price, cache_write_price, group_ratio, cost,
				attempts, first_byte_ms, total_ms, upstream_response_id, channel_name, key_name, price_rule, group_name, client_ip, reasoning_effort
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			          $19::numeric,$20::numeric,$21::numeric,$22::numeric,$23::numeric,$24::numeric,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34)`,
			l.RequestID, l.UserID, l.APIKeyID, l.GroupID, l.Protocol, l.Model, l.UpstreamModel, channelID,
			l.Stream, l.Status, l.HTTPStatus, l.Error,
			l.InputTokens, l.CacheReadTokens, l.CacheWriteTokens, l.OutputTokens, l.ReasoningTokens, l.UsageEstimated,
			l.Prices.Input.String(), l.Prices.Output.String(), l.Prices.CacheRead.String(), l.Prices.CacheWrite.String(),
			ratio.String(), l.Cost.String(),
			l.Attempts, l.FirstByteMS, l.TotalMS, l.UpstreamResponseID, l.ChannelName, l.KeyName, l.PriceRule, l.GroupName, l.ClientIP, l.ReasoningEffort,
		); err != nil {
			return fmt.Errorf("写请求日志: %w", err)
		}
		if l.Cost.IsPositive() || l.Reserved.IsPositive() {
			// 解冻放行时预扣的额度，按实际费用扣余额，多退少补
			if _, err := tx.Exec(ctx, `UPDATE users SET balance = balance - $1::numeric, reserved = GREATEST(reserved - $3::numeric, 0) WHERE id = $2`,
				l.Cost.String(), l.UserID, l.Reserved.String()); err != nil {
				return fmt.Errorf("扣余额: %w", err)
			}
		}
		_, err := tx.Exec(ctx, `UPDATE api_keys SET quota_used = quota_used + $1::numeric, last_used_at = now() WHERE id = $2`,
			l.Cost.String(), l.APIKeyID)
		return err
	})
}

// Reserve 在可用余额（余额 − 已冻结）足够时冻结 amount，返回是否成功和冻结前的可用余额。
// 判断和冻结在一条 UPDATE 里完成，并发请求不会同时用掉同一笔钱。
func (s *Store) Reserve(ctx context.Context, userID int64, amount decimal.Decimal) (bool, decimal.Decimal, error) {
	var available string
	err := s.Pool.QueryRow(ctx, `UPDATE users SET reserved = reserved + $2::numeric
		WHERE id = $1 AND balance - reserved >= $2::numeric RETURNING (balance - reserved + $2::numeric)::text`, userID, amount.String()).Scan(&available)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.Pool.QueryRow(ctx, `SELECT (balance - reserved)::text FROM users WHERE id = $1`, userID).Scan(&available)
		return false, mustDecimal(available), err
	}
	return err == nil, mustDecimal(available), err
}

// ResetReservations 启动时清零所有冻结：单进程部署，上次进程里进行中的请求都已结束。
func (s *Store) ResetReservations(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET reserved = 0 WHERE reserved <> 0`)
	return err
}

// CreateAPIKey 保存新 key 的哈希；quota 为 nil 表示不限额。
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, name, keyHash, keyPrefix string, groupID int64, quota *decimal.Decimal) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, keyEntity, &id, userID, func() error {
			return tx.QueryRow(ctx, `
				INSERT INTO api_keys (user_id, name, key_hash, key_prefix, group_id, quota_limit)
				VALUES ($1, $2, $3, $4, $5, $6::numeric) RETURNING id`,
				userID, name, keyHash, keyPrefix, groupID, decimalArg(quota),
			).Scan(&id)
		})
	})
	return id, err
}

// UserIDByName 找不到返回 0。
func (s *Store) UserIDByName(ctx context.Context, username string) (int64, error) {
	return s.idByName(ctx, `SELECT id FROM users WHERE username = $1`, username)
}

// GroupIDByName 找不到返回 0。
func (s *Store) GroupIDByName(ctx context.Context, name string) (int64, error) {
	return s.idByName(ctx, `SELECT id FROM groups WHERE name = $1`, name)
}

func (s *Store) idByName(ctx context.Context, query, name string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, query, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
