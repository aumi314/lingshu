package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// KeyOwner 是网关鉴权需要的 key 和用户信息。
type KeyOwner struct {
	KeyID       int64
	UserID      int64
	GroupID     int64
	KeyEnabled  bool
	UserEnabled bool
}

// LookupKey 按 key 哈希查找，找不到返回 nil。
func (s *Store) LookupKey(ctx context.Context, keyHash string) (*KeyOwner, error) {
	var o KeyOwner
	err := s.Pool.QueryRow(ctx, `
		SELECT k.id, k.user_id, k.group_id, k.enabled, u.enabled
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = $1`, keyHash,
	).Scan(&o.KeyID, &o.UserID, &o.GroupID, &o.KeyEnabled, &o.UserEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// Route 是一个可用的上游渠道。
type Route struct {
	ChannelID     int64
	ChannelName   string
	BaseURL       string
	APIKey        string
	UpstreamModel string
}

// 协议到渠道地址列的映射；列名只来自这里，不拼接外部输入。
var protocolColumns = map[string]string{
	"chat":      "base_url_chat",
	"messages":  "base_url_messages",
	"responses": "base_url_responses",
}

// FindRoutes 返回分组内支持该协议和模型的渠道，按优先级从高到低，同优先级随机。
func (s *Store) FindRoutes(ctx context.Context, groupID int64, protocol, model string) ([]Route, error) {
	col, ok := protocolColumns[protocol]
	if !ok {
		return nil, fmt.Errorf("未知协议 %q", protocol)
	}
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
		SELECT c.id, c.name, c.%[1]s, c.api_key, COALESCE(NULLIF(cm.upstream_name, ''), m.name)
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id
		JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id = $1
		WHERE m.name = $2 AND m.enabled AND c.enabled AND c.%[1]s <> ''
		ORDER BY c.priority DESC, random()`, col), groupID, model)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Route, error) {
		var rt Route
		err := r.Scan(&rt.ChannelID, &rt.ChannelName, &rt.BaseURL, &rt.APIKey, &rt.UpstreamModel)
		return rt, err
	})
}

// ModelProtocols 返回分组内这个模型能用哪些协议，用来提示用户换入口。
func (s *Store) ModelProtocols(ctx context.Context, groupID int64, model string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT p FROM (
			SELECT CASE WHEN c.base_url_chat <> '' THEN 'chat' END AS p1,
			       CASE WHEN c.base_url_messages <> '' THEN 'messages' END AS p2,
			       CASE WHEN c.base_url_responses <> '' THEN 'responses' END AS p3
			FROM models m
			JOIN channel_models cm ON cm.model_id = m.id
			JOIN channels c ON c.id = cm.channel_id
			JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id = $1
			WHERE m.name = $2 AND m.enabled AND c.enabled
		) t, LATERAL (VALUES (p1), (p2), (p3)) v(p)
		WHERE p IS NOT NULL ORDER BY p`, groupID, model)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// GroupModels 返回分组内可用的模型名，给 /v1/models 用。
func (s *Store) GroupModels(ctx context.Context, groupID int64) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT m.name
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id
		JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id = $1
		WHERE m.enabled AND c.enabled
		ORDER BY m.name`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// RequestLog 对应 request_logs 一行。阶段 1 还不计费，价格和 cost 为 0。
type RequestLog struct {
	RequestID          string
	UserID             int64
	APIKeyID           int64
	GroupID            int64
	Protocol           string
	Model              string
	UpstreamModel      string
	ChannelID          int64 // 0 表示没选到渠道
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
	Attempts           int
	FirstByteMS        int
	TotalMS            int
	UpstreamResponseID string
}

func (s *Store) InsertRequestLog(ctx context.Context, l *RequestLog) error {
	var channelID *int64
	if l.ChannelID != 0 {
		channelID = &l.ChannelID
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO request_logs (
			request_id, user_id, api_key_id, group_id, protocol, model, upstream_model, channel_id,
			stream, status, http_status, error,
			input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens, usage_estimated,
			attempts, first_byte_ms, total_ms, upstream_response_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
		l.RequestID, l.UserID, l.APIKeyID, l.GroupID, l.Protocol, l.Model, l.UpstreamModel, channelID,
		l.Stream, l.Status, l.HTTPStatus, l.Error,
		l.InputTokens, l.CacheReadTokens, l.CacheWriteTokens, l.OutputTokens, l.ReasoningTokens, l.UsageEstimated,
		l.Attempts, l.FirstByteMS, l.TotalMS, l.UpstreamResponseID,
	)
	return err
}

// TouchKey 更新 key 的最后使用时间。
func (s *Store) TouchKey(ctx context.Context, keyID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, keyID)
	return err
}

// CreateAPIKey 保存新 key 的哈希。
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, name, keyHash, keyPrefix string, groupID int64) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, key_hash, key_prefix, group_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		userID, name, keyHash, keyPrefix, groupID,
	).Scan(&id)
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
