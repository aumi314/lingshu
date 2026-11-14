package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/billing"
	"github.com/aumi314/lingshu/internal/relay/usage"
)

// KeyOwner 是网关鉴权和准入需要的 key、用户、分组信息。
type KeyOwner struct {
	KeyID        int64
	KeyName      string
	UserID       int64
	GroupID      int64
	GroupName    string
	ExpiresAt    *time.Time
	KeyEnabled   bool
	UserEnabled  bool
	GroupAllowed bool // 公开分组，或用户被授权了这个特殊分组
	GroupRatio   decimal.Decimal
	GroupRetries int // 渠道级失败后最多重试几次（服务分组设置）
	Balance      decimal.Decimal
	Reserved     decimal.Decimal  // 进行中请求冻结的额度，可用余额 = Balance - Reserved
	QuotaLimit   *decimal.Decimal // nil 表示不限额
	QuotaUsed    decimal.Decimal
	// 项目 Key（docs/decisions.md「项目额度」）：钱走项目池，分组权限看项目开放的分组。ProjectID 为 0 是个人 Key
	ProjectID       int64
	ProjectName     string
	ProjectOwner    string
	ProjectFrozen   bool
	ProjectClosed   bool
	ProjectMember   bool // Key 的主人现在还是项目正式成员
	ProjectBalance  decimal.Decimal
	ProjectReserved decimal.Decimal
}

// LookupKey 按 key 哈希查找，找不到返回 nil。
func (s *Store) LookupKey(ctx context.Context, keyHash string) (*KeyOwner, error) {
	var o KeyOwner
	var ratio, balance, reserved, used, pBalance, pReserved string
	var limit *string
	// 分组权限：个人 Key 看用户自己能不能用；项目 Key 看项目开放了这个分组、且创建人现在仍能用（管理员收回创建人的权限后项目也跟着失效）
	err := s.Pool.QueryRow(ctx, `
		SELECT k.id, k.name, k.user_id, k.group_id, g.name, k.enabled, u.enabled,
		       g.enabled AND (g.is_public OR EXISTS (
			       SELECT 1 FROM group_user_groups gug
			       JOIN user_group_members ugm ON ugm.user_group_id = gug.user_group_id
			       WHERE gug.group_id = g.id AND ugm.user_id = COALESCE(p.owner_id, u.id)
		       )) AND (p.id IS NULL OR EXISTS (SELECT 1 FROM project_groups pg WHERE pg.project_id = p.id AND pg.group_id = g.id)),
		       g.ratio::text, g.retry_count, u.balance::text, u.reserved::text, k.quota_limit::text, k.quota_used::text, k.expires_at,
		       COALESCE(p.id, 0), COALESCE(p.name, ''), COALESCE(po.username, ''), COALESCE(p.frozen, false), p.closed_at IS NOT NULL,
		       EXISTS (SELECT 1 FROM project_members m WHERE m.project_id = p.id AND m.user_id = u.id AND m.status = 'active'),
		       COALESCE(p.balance, 0)::text, COALESCE(p.reserved, 0)::text
		FROM api_keys k
		JOIN users u ON u.id = k.user_id
		JOIN groups g ON g.id = k.group_id
		LEFT JOIN projects p ON p.id = k.project_id
		LEFT JOIN users po ON po.id = p.owner_id
		WHERE k.key_hash = $1`, keyHash,
	).Scan(&o.KeyID, &o.KeyName, &o.UserID, &o.GroupID, &o.GroupName, &o.KeyEnabled, &o.UserEnabled, &o.GroupAllowed,
		&ratio, &o.GroupRetries, &balance, &reserved, &limit, &used, &o.ExpiresAt,
		&o.ProjectID, &o.ProjectName, &o.ProjectOwner, &o.ProjectFrozen, &o.ProjectClosed, &o.ProjectMember, &pBalance, &pReserved)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.GroupRatio, o.Balance, o.Reserved, o.QuotaUsed = mustDecimal(ratio), mustDecimal(balance), mustDecimal(reserved), mustDecimal(used)
	o.ProjectBalance, o.ProjectReserved = mustDecimal(pBalance), mustDecimal(pReserved)
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

// RouteKey 是渠道里一把启用的 key。
type RouteKey struct {
	ID  int64  `json:"id"`
	Key string `json:"key"`
}

// Route 是一个可用的上游渠道，带上模型的站内标价。Keys 是渠道里启用的 key（至少一把）；
// 网关按会话从中选一把，选中的填在 KeyID / APIKey 上再发请求。
type Route struct {
	ChannelID   int64
	ChannelName string
	Priority    int // 渠道优先级，大的先试；同一档内按会话固定（relay/keys.go 的 orderAttempts）
	Template    string
	BaseURL     string
	// UpstreamProtocol 是实际发给上游的协议；Messages 与 Responses 可以互相转接。
	UpstreamProtocol usage.Protocol
	Keys             []RouteKey
	KeyID            int64
	APIKey           string
	ProxyURL         string // 空表示直连
	UpstreamModel    string
	Prices           billing.Prices
	PriceRules       []billing.PriceRule
	// FastMultiplier 是模型开放 GPT Fast 时的计费倍数，nil 表示不开放
	FastMultiplier *decimal.Decimal
}

// 协议到渠道地址列的映射；列名只来自这里，不拼接外部输入。
var protocolColumns = map[string]string{
	"chat":      "base_url_chat",
	"messages":  "base_url_messages",
	"responses": "base_url_responses",
	"systemone": "base_url_systemone",
}

// FindRoutes 返回分组内支持该协议和模型的渠道，按优先级从高到低，同优先级随机。
// Messages 与 Responses 入口互相接受另一协议的渠道，同渠道优先原生地址。
func (s *Store) FindRoutes(ctx context.Context, groupID int64, protocol, model string) ([]Route, error) {
	col, ok := protocolColumns[protocol]
	if !ok {
		return nil, fmt.Errorf("未知协议 %q", protocol)
	}
	baseURL, upstream, filter := "c."+col, "$3::text", "c."+col+" <> ''"
	if protocol == string(usage.Responses) {
		// Responses 客户端可以走只有 Messages 地址的渠道，网关负责转接
		baseURL = "COALESCE(NULLIF(c.base_url_responses, ''), c.base_url_messages)"
		upstream = "CASE WHEN c.base_url_responses <> '' THEN $3::text ELSE 'messages' END"
		filter = "(c.base_url_responses <> '' OR c.base_url_messages <> '')"
	}
	if protocol == string(usage.Messages) {
		baseURL = "COALESCE(NULLIF(c.base_url_messages, ''), c.base_url_responses)"
		upstream = "CASE WHEN c.base_url_messages <> '' THEN $3::text ELSE 'responses' END"
		filter = "(c.base_url_messages <> '' OR c.base_url_responses <> '')"
	}
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
		SELECT c.id, c.name, c.priority, c.template, %[1]s, %[2]s,
		       (SELECT json_agg(json_build_object('id', k.id, 'key', k.api_key) ORDER BY k.sort_order, k.id)
		        FROM channel_keys k WHERE k.channel_id = c.id AND k.enabled),
		       c.proxy_url, COALESCE(NULLIF(cm.upstream_name, ''), m.name),
		       m.input_price::text, m.output_price::text, m.cache_read_price::text, m.cache_write_price::text, m.price_rules,
		       m.fast_multiplier::text
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id
		JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id = $1
		WHERE m.name = $2 AND c.enabled AND %[3]s
		  AND EXISTS (SELECT 1 FROM channel_keys k WHERE k.channel_id = c.id AND k.enabled)
		ORDER BY c.priority DESC, random()`, baseURL, upstream, filter), groupID, model, protocol)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Route, error) {
		var rt Route
		var in, out, cr, cw string
		var fast *string
		var keys []byte
		var upstreamProtocol string
		if err := r.Scan(&rt.ChannelID, &rt.ChannelName, &rt.Priority, &rt.Template, &rt.BaseURL, &upstreamProtocol, &keys, &rt.ProxyURL, &rt.UpstreamModel, &in, &out, &cr, &cw,
			&rt.PriceRules, &fast); err != nil {
			return rt, err
		}
		rt.UpstreamProtocol = usage.Protocol(upstreamProtocol)
		if err := json.Unmarshal(keys, &rt.Keys); err != nil {
			return rt, fmt.Errorf("解析渠道 %d 的 key: %w", rt.ChannelID, err)
		}
		rt.FastMultiplier = optDecimal(fast)
		rt.Prices = billing.Prices{Input: mustDecimal(in), Output: mustDecimal(out), CacheRead: mustDecimal(cr), CacheWrite: mustDecimal(cw)}
		return rt, nil
	})
}

// ModelProtocols 返回分组内这个模型原生提供哪些协议，用来提示用户换入口；能转接的入口由网关按转接开关补上。
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
// 同时返回当时用的 key（旧日志没有记 key，返回 0，表示同渠道哪把都行）。
func (s *Store) ChannelForResponse(ctx context.Context, userID int64, responseID string) (int64, int64, error) {
	var id, keyID *int64
	err := s.Pool.QueryRow(ctx, `
		SELECT channel_id, channel_key_id FROM request_logs
		WHERE upstream_response_id = $1 AND user_id = $2
		ORDER BY id DESC LIMIT 1`, responseID, userID).Scan(&id, &keyID)
	if errors.Is(err, pgx.ErrNoRows) || id == nil {
		return 0, 0, nil
	}
	if keyID == nil {
		return *id, 0, err
	}
	return *id, *keyID, err
}

// ModelSource 是集合里提供某个模型的成员分组。
type ModelSource struct {
	GroupID   int64
	GroupName string
	Ratio     decimal.Decimal
}

// ModelSource 找出集合里提供这个模型的成员：按分组排序取第一个启用、有启用渠道映射这个模型的成员。
// 普通分组、或集合里没有成员提供这个模型时返回 nil（按分组自己处理，后面会报“模型不存在”）。
func (s *Store) ModelSource(ctx context.Context, groupID int64, model string) (*ModelSource, error) {
	var src ModelSource
	var ratio string
	err := s.Pool.QueryRow(ctx, `
		SELECT g.id, g.name, g.ratio::text
		FROM collection_groups x
		JOIN groups g ON g.id = x.group_id AND g.enabled
		WHERE x.collection_id = $1 AND EXISTS (
			SELECT 1 FROM channel_groups cg
			JOIN channels c ON c.id = cg.channel_id AND c.enabled
			JOIN channel_models cm ON cm.channel_id = c.id
			JOIN models m ON m.id = cm.model_id
			WHERE cg.group_id = g.id AND m.name = $2)
		ORDER BY g.sort_order, g.id LIMIT 1`, groupID, model).Scan(&src.GroupID, &src.GroupName, &ratio)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	src.Ratio = mustDecimal(ratio)
	return &src, err
}

// routeGroupsSQL 是分组实际用到渠道的分组：普通分组是它自己，集合是它启用的成员
const routeGroupsSQL = `(SELECT id FROM groups WHERE id = $1 AND NOT is_collection
		UNION ALL SELECT x.group_id FROM collection_groups x JOIN groups g ON g.id = x.group_id AND g.enabled WHERE x.collection_id = $1)`

// GroupModels 返回分组内可用的模型名，给 /v1/models 用；集合返回各成员模型的合集。
func (s *Store) GroupModels(ctx context.Context, groupID int64) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT m.name
		FROM models m
		JOIN channel_models cm ON cm.model_id = m.id
		JOIN channels c ON c.id = cm.channel_id
		JOIN channel_groups cg ON cg.channel_id = c.id AND cg.group_id IN `+routeGroupsSQL+`
		WHERE c.enabled
		ORDER BY m.name`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// RequestLog 对应 request_logs 一行。
type RequestLog struct {
	RequestID       string
	UserID          int64
	APIKeyID        int64
	KeyName         string // 名字快照：渠道或 key 被删除后，历史日志仍能看出是谁
	GroupID         int64
	GroupName       string
	ClientIP        string
	ReasoningEffort string
	ServiceTier     string // 档位结果：'' 标准、fast、fast_downgraded、unavailable（见迁移 0025）
	Protocol        string
	// UpstreamProtocol 是实际发给上游的协议，与入口协议分别记录
	UpstreamProtocol usage.Protocol
	// Operation 是这次请求的用途：generation 正常生成、compaction 上下文压缩
	Operation string
	// ConversionDiagnostics 记协议转接时丢字段、降级之类的诊断（数组，默认空）
	ConversionDiagnostics json.RawMessage
	Model                 string
	UpstreamModel         string
	ChannelID             int64 // 0 表示没选到渠道
	ChannelName           string
	ChannelKeyID          int64   // 最终用的渠道 key，0 表示没选到
	FailedKeyIDs          []int64 // 失败过的渠道 key（按尝试顺序），按 key 统计失败
	Stream                bool
	Status                string
	HTTPStatus            int
	Error                 string
	InputTokens           int64
	CacheReadTokens       int64
	CacheWriteTokens      int64
	OutputTokens          int64
	ReasoningTokens       int64
	UsageEstimated        bool
	Prices                billing.Prices  // 实际生效的单价快照（命中规则时是规则价）
	PriceRule             string          // 命中的价格规则名，空表示基础价
	GroupRatio            decimal.Decimal // 倍率快照
	ProjectID             int64           // 项目 Key 的项目，0 是个人 Key：结算从项目池扣
	Cost                  decimal.Decimal // 实际扣费，0 表示不扣
	Attempts              int
	FirstByteMS           int // 以下三个都从进入网关算起
	TotalMS               int
	UpstreamStartMS       int     // 发出最终那次上游请求的时刻，0 表示没发到上游
	UpstreamHeaderMS      int     // 最终那次上游响应头到达的时刻，0 表示没收到
	ReadMS                int     // 读请求体用的时间
	FailedChannelIDs      []int64 // 失败过的渠道（可换渠道重试的失败，按尝试顺序），渠道连接状态据此统计失败
	UpstreamResponseID    string
	Reserved              decimal.Decimal // 放行时冻结的额度，结算时解冻（不写进日志表）
}

func failedIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{} // 列不允许 NULL
	}
	return ids
}

// SettleRequest 在一个事务里写日志、扣用户余额、加 key 的已用额度，并更新 key 最后使用时间。
// 一个请求只调用一次。
func (s *Store) SettleRequest(ctx context.Context, l *RequestLog) error {
	var channelID, channelKeyID *int64
	if l.ChannelID != 0 {
		channelID = &l.ChannelID
	}
	if l.ChannelKeyID != 0 {
		channelKeyID = &l.ChannelKeyID
	}
	var projectID *int64
	if l.ProjectID != 0 {
		projectID = &l.ProjectID
	}
	ratio := l.GroupRatio
	if ratio.IsZero() && l.Cost.IsZero() {
		ratio = decimal.NewFromInt(1) // 没走到计费的请求（如被拒绝），倍率记 1
	}
	operation := l.Operation
	if operation == "" {
		operation = "generation"
	}
	upstreamProtocol := l.UpstreamProtocol
	if upstreamProtocol == "" {
		upstreamProtocol = usage.Protocol(l.Protocol) // 没记就当原生转发
	}
	diagnostics := l.ConversionDiagnostics
	if len(diagnostics) == 0 {
		diagnostics = json.RawMessage("[]") // 列不允许 NULL
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO request_logs (
				request_id, user_id, api_key_id, group_id, protocol, upstream_protocol, operation, conversion_diagnostics,
				model, upstream_model, channel_id,
				stream, status, http_status, error,
				input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens, usage_estimated,
				input_price, output_price, cache_read_price, cache_write_price, group_ratio, cost,
				attempts, first_byte_ms, total_ms, upstream_response_id, channel_name, key_name, price_rule, group_name, client_ip, reasoning_effort,
				service_tier, read_ms, upstream_start_ms, failed_channel_ids, channel_key_id, failed_key_ids, upstream_header_ms, project_id
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,
			          $22::numeric,$23::numeric,$24::numeric,$25::numeric,$26::numeric,$27::numeric,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45)`,
			l.RequestID, l.UserID, l.APIKeyID, l.GroupID, l.Protocol, string(upstreamProtocol), operation, diagnostics,
			l.Model, l.UpstreamModel, channelID,
			l.Stream, l.Status, l.HTTPStatus, l.Error,
			l.InputTokens, l.CacheReadTokens, l.CacheWriteTokens, l.OutputTokens, l.ReasoningTokens, l.UsageEstimated,
			l.Prices.Input.String(), l.Prices.Output.String(), l.Prices.CacheRead.String(), l.Prices.CacheWrite.String(),
			ratio.String(), l.Cost.String(),
			l.Attempts, l.FirstByteMS, l.TotalMS, l.UpstreamResponseID, l.ChannelName, l.KeyName, l.PriceRule, l.GroupName, l.ClientIP, l.ReasoningEffort,
			l.ServiceTier, l.ReadMS, l.UpstreamStartMS, failedIDs(l.FailedChannelIDs), channelKeyID, failedIDs(l.FailedKeyIDs), l.UpstreamHeaderMS, projectID,
		); err != nil {
			return fmt.Errorf("写请求日志: %w", err)
		}
		if l.Cost.IsPositive() || l.Reserved.IsPositive() {
			// 解冻放行时预扣的额度，按实际费用扣余额（项目 Key 扣项目池），多退少补
			table, owner := "users", l.UserID
			if l.ProjectID != 0 {
				table, owner = "projects", l.ProjectID
			}
			if _, err := tx.Exec(ctx, `UPDATE `+table+` SET balance = balance - $1::numeric, reserved = GREATEST(reserved - $3::numeric, 0) WHERE id = $2`,
				l.Cost.String(), owner, l.Reserved.String()); err != nil {
				return fmt.Errorf("扣余额: %w", err)
			}
			if l.ProjectID != 0 { // 成员行的冻结一起解开；人已被移出时没有这一行，不影响
				if _, err := tx.Exec(ctx, `UPDATE project_members SET reserved = GREATEST(reserved - $3::numeric, 0) WHERE project_id = $1 AND user_id = $2`,
					l.ProjectID, l.UserID, l.Reserved.String()); err != nil {
					return fmt.Errorf("解冻成员额度: %w", err)
				}
			}
		}
		_, err := tx.Exec(ctx, `UPDATE api_keys SET quota_used = quota_used + $1::numeric, last_used_at = now() WHERE id = $2`,
			l.Cost.String(), l.APIKeyID)
		return err
	})
}

// Reserve 冻结额度，返回实际冻结的金额（0 表示拒绝）和冻结前的可用余额（余额 − 已冻结）。
// 可用余额够就冻结 amount；不够但这个用户没有别的请求在进行、余额为正时，放行这一个请求并冻结剩下的全部可用余额
// （单个请求最多小额透支，和原来“透支一次”一样；要防的是并发请求一起把余额扣穿）。
// 判断和冻结在一条语句里完成，行锁保证并发请求不会同时用掉同一笔钱。
func (s *Store) Reserve(ctx context.Context, userID int64, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	var held, available string
	err := s.Pool.QueryRow(ctx, `
		WITH u AS (SELECT balance - reserved AS avail, reserved FROM users WHERE id = $1 FOR UPDATE)
		UPDATE users SET reserved = users.reserved + LEAST($2::numeric, u.avail)
		FROM u WHERE users.id = $1 AND (u.avail >= $2::numeric OR (u.reserved = 0 AND u.avail > 0))
		RETURNING LEAST($2::numeric, u.avail)::text, u.avail::text`, userID, amount.String()).Scan(&held, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.Pool.QueryRow(ctx, `SELECT (balance - reserved)::text FROM users WHERE id = $1`, userID).Scan(&available)
		return decimal.Zero, mustDecimal(available), err
	}
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	return mustDecimal(held), mustDecimal(available), nil
}

// RecentMaxCost 返回这个用户调用这个模型最近 20 次成功请求里最贵的一次花费，以及样本数。
// 预扣用它校正公式估算：公式按原始请求体估输入、按全价算缓存，长上下文 + 高缓存命中时会高估几十倍。
func (s *Store) RecentMaxCost(ctx context.Context, userID int64, model string) (decimal.Decimal, int, error) {
	var maxCost string
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(max(cost), 0)::text, count(*) FROM (
		SELECT cost FROM request_logs WHERE user_id = $1 AND model = $2 AND status = 'success'
		ORDER BY created_at DESC LIMIT 20) t`, userID, model).Scan(&maxCost, &n)
	return mustDecimal(maxCost), n, err
}

// ResetReservations 启动时清零所有冻结：单进程部署，上次进程里进行中的请求都已结束。
func (s *Store) ResetReservations(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET reserved = 0 WHERE reserved <> 0;
		UPDATE projects SET reserved = 0 WHERE reserved <> 0;
		UPDATE project_members SET reserved = 0 WHERE reserved <> 0`)
	return err
}

// CreateAPIKey 保存新 key 的哈希；quota 为 nil 表示不限额，expiresAt 为 nil 表示永不过期。
// CreateAPIKey 建 key；projectID 为 0 是个人 key，否则是项目 key（权限由接口层检查）。
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, name, keyHash, keyPrefix string, groupID int64, quota *decimal.Decimal, expiresAt *time.Time, projectID int64) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, keyEntity, &id, userID, func() error {
			return tx.QueryRow(ctx, `
				INSERT INTO api_keys (user_id, name, key_hash, key_prefix, group_id, quota_limit, expires_at, project_id)
				VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, NULLIF($8, 0)) RETURNING id`,
				userID, name, keyHash, keyPrefix, groupID, decimalArg(quota), expiresAt, projectID,
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
