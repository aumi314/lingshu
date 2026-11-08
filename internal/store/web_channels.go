package store

import (
	"context"
	"encoding/json"
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

// ChannelKey 是渠道里的一把 key。管理端只看掩码；保存时 ID 为 0 表示新加（APIKey 必填），
// 带 ID 的 APIKey 留空表示不改；保存时不在列表里的 key 会被删除。Status、Stats 由接口层补上。
type ChannelKey struct {
	ID      int64  `json:"id,omitempty"`
	APIKey  string `json:"api_key,omitempty"` // 只用于保存，读取时为空
	Masked  string `json:"masked"`
	Enabled bool   `json:"enabled"`
	Note    string `json:"note"`
	Status  any    `json:"status,omitempty"`
	Stats   any    `json:"stats,omitempty"`
}

// MaxChannelKeys 是一个渠道最多挂几把 key。
const MaxChannelKeys = 50

// Channel 返回给管理端时 key 只显示首尾，完整 key 不出服务器。
type Channel struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	Template         string         `json:"template"`
	Note             string         `json:"note"`
	APIKey           string         `json:"api_key,omitempty"` // 只用于拉取模型、冒烟测试和旧客户端保存（等于只有一把 key），读取时为空
	APIKeyMasked     string         `json:"api_key_masked"`    // 第一把启用的 key 的掩码
	Keys             []ChannelKey   `json:"keys"`
	TestKeyID        int64          `json:"test_key_id,omitempty"`  // 冒烟测试指定用哪把 key，0 表示第一把启用的
	KeysDeduped      int            `json:"keys_deduped,omitempty"` // 保存时自动去掉的重复 key 数（只出现在保存的响应里）
	Priority         int            `json:"priority"`
	Enabled          bool           `json:"enabled"`
	BaseURLChat      string         `json:"base_url_chat"`
	BaseURLMessages  string         `json:"base_url_messages"`
	BaseURLResponses string         `json:"base_url_responses"`
	BaseURLSystemOne string         `json:"base_url_systemone"`
	ProxyURL         string         `json:"proxy_url"` // 空表示直连
	GroupIDs         []int64        `json:"group_ids"`
	Models           []ChannelModel `json:"models"`
	// 上次查询到的厂商余额，原样保存；只有 DeepSeek、Kimi、OpenRouter 能查。
	Balance          []BalanceItem `json:"balance"`
	BalanceCheckedAt *time.Time    `json:"balance_checked_at"`
	BalanceError     string        `json:"balance_error"`
}

func maskKey(k string) string {
	if len(k) <= 10 {
		return strings.Repeat("*", len(k))
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func (s *Store) ListChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, c.name, c.template, c.note, COALESCE((SELECT k.api_key FROM channel_keys k WHERE k.channel_id = c.id AND k.enabled ORDER BY k.sort_order, k.id LIMIT 1), ''), c.priority, c.enabled,
		       c.base_url_chat, c.base_url_messages, c.base_url_responses, c.base_url_systemone, c.proxy_url,
		       COALESCE((SELECT array_agg(group_id ORDER BY group_id) FROM channel_groups WHERE channel_id = c.id), '{}'),
		       c.balance_detail, c.balance_checked_at, c.balance_error
		FROM channels c ORDER BY c.sort_order, c.id`)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Channel{}
	var out []*Channel
	for rows.Next() {
		c := &Channel{Models: []ChannelModel{}, Keys: []ChannelKey{}}
		var key string
		var detail []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Template, &c.Note, &key, &c.Priority, &c.Enabled,
			&c.BaseURLChat, &c.BaseURLMessages, &c.BaseURLResponses, &c.BaseURLSystemOne, &c.ProxyURL, &c.GroupIDs,
			&detail, &c.BalanceCheckedAt, &c.BalanceError); err != nil {
			rows.Close()
			return nil, err
		}
		c.APIKeyMasked = maskKey(key)
		c.Balance = []BalanceItem{}
		if err := json.Unmarshal(detail, &c.Balance); err != nil {
			rows.Close()
			return nil, fmt.Errorf("解析渠道 %d 的余额快照失败: %w", c.ID, err)
		}
		byID[c.ID] = c
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	krows, err := s.Pool.Query(ctx, `SELECT channel_id, id, api_key, enabled, note FROM channel_keys ORDER BY channel_id, sort_order, id`)
	if err != nil {
		return nil, err
	}
	for krows.Next() {
		var chID int64
		var k ChannelKey
		var key string
		if err := krows.Scan(&chID, &k.ID, &key, &k.Enabled, &k.Note); err != nil {
			krows.Close()
			return nil, err
		}
		k.Masked = maskKey(key)
		if c := byID[chID]; c != nil {
			c.Keys = append(c.Keys, k)
		}
	}
	krows.Close()
	if err := krows.Err(); err != nil {
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

// ChannelSecret 返回渠道的完整配置，APIKey 是第一把启用的 key；只给服务器内部用（拉取模型、冒烟测试、模型检测）。
func (s *Store) ChannelSecret(ctx context.Context, id int64) (*Channel, error) {
	var c Channel
	err := s.Pool.QueryRow(ctx, `SELECT c.id, c.name, c.template, COALESCE((SELECT k.api_key FROM channel_keys k WHERE k.channel_id = c.id AND k.enabled ORDER BY k.sort_order, k.id LIMIT 1), ''), c.base_url_chat, c.base_url_messages, c.base_url_responses, c.base_url_systemone, c.proxy_url
		FROM channels c WHERE c.id = $1`, id).Scan(&c.ID, &c.Name, &c.Template, &c.APIKey, &c.BaseURLChat, &c.BaseURLMessages, &c.BaseURLResponses, &c.BaseURLSystemOne, &c.ProxyURL)
	if err != nil {
		return nil, translate(err)
	}
	return &c, nil
}

// ChannelKeySecret 返回渠道里指定的一把 key（冒烟测试选 key 用）；不属于这个渠道时返回 ErrNotFound。
func (s *Store) ChannelKeySecret(ctx context.Context, channelID, keyID int64) (string, error) {
	var key string
	err := s.Pool.QueryRow(ctx, `SELECT api_key FROM channel_keys WHERE id = $1 AND channel_id = $2`, keyID, channelID).Scan(&key)
	return key, translate(err)
}

// ChannelKeyStat 是一把 key 在统计窗口内的成功和失败次数（口径同渠道连接状态，按 key 记）。
type ChannelKeyStat struct {
	Success int64 `json:"success"`
	Failed  int64 `json:"failed"`
}

// ChannelKeyStats 统计 since 之后每把 key 的成功（最终由它完成的 success 请求）和失败（failed_key_ids）次数。
func (s *Store) ChannelKeyStats(ctx context.Context, since time.Time) (map[int64]ChannelKeyStat, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH w AS (SELECT channel_key_id, status, failed_key_ids FROM request_logs WHERE created_at >= $1),
		ok AS (SELECT channel_key_id AS k, count(*) AS n FROM w WHERE status = 'success' AND channel_key_id IS NOT NULL GROUP BY 1),
		f AS (SELECT k, count(*) AS n FROM w, unnest(w.failed_key_ids) AS k GROUP BY 1)
		SELECT COALESCE(ok.k, f.k), COALESCE(ok.n, 0), COALESCE(f.n, 0) FROM ok FULL JOIN f ON f.k = ok.k`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ChannelKeyStat{}
	for rows.Next() {
		var id int64
		var st ChannelKeyStat
		if err := rows.Scan(&id, &st.Success, &st.Failed); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// SaveChannel 新建或更新渠道，连同所属分组、模型映射和 key 一起整体保存。
// Keys 为 nil 时不动 key（旧客户端只传 APIKey 时，接口层会把它转成一把 key）。
func (s *Store) SaveChannel(ctx context.Context, c *Channel) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, channelEntity, &c.ID, 0, func() error { return saveChannel(ctx, tx, c) })
	})
}

func saveChannel(ctx context.Context, tx pgx.Tx, c *Channel) error {
	if c.ID == 0 {
		if len(c.Keys) == 0 {
			return badInput("新建渠道至少要有一把 key")
		}
		// channels.api_key 已不再使用（迁移 0030 起 key 都在 channel_keys），新渠道写空串
		if err := tx.QueryRow(ctx, `
			INSERT INTO channels (name, template, note, api_key, priority, enabled, base_url_chat, base_url_messages, base_url_responses, base_url_systemone, proxy_url, sort_order)
			VALUES ($1, $2, $3, '', $4, $5, $6, $7, $8, $9, $10, (SELECT COALESCE(max(sort_order), 0) + 1 FROM channels)) RETURNING id`,
			c.Name, c.Template, c.Note, c.Priority, c.Enabled, c.BaseURLChat, c.BaseURLMessages, c.BaseURLResponses, c.BaseURLSystemOne, c.ProxyURL,
		).Scan(&c.ID); err != nil {
			return translate(err)
		}
	} else {
		if err := execOne(ctx, tx, `
			UPDATE channels SET name = $2, template = $3, note = $4, priority = $5,
			       enabled = $6, base_url_chat = $7, base_url_messages = $8, base_url_responses = $9, proxy_url = $10, base_url_systemone = $11
			WHERE id = $1`,
			c.ID, c.Name, c.Template, c.Note, c.Priority, c.Enabled, c.BaseURLChat, c.BaseURLMessages, c.BaseURLResponses, c.ProxyURL, c.BaseURLSystemOne,
		); err != nil {
			return err
		}
	}
	if c.Keys != nil {
		n, err := saveChannelKeys(ctx, tx, c.ID, c.Keys)
		if err != nil {
			return err
		}
		c.KeysDeduped = n
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
}

// saveChannelKeys 按列表顺序整体保存渠道的 key，返回自动去掉的重复 key 数：
// 带 ID 的更新（APIKey 留空不改），不带 ID 的新加，不在列表里的删除。
// 自动去重：新加的 key 和本次已处理的 key 重复就跳过；和渠道里已有的某把相同（覆盖模式下常见）就沿用那一条，
// 不删了重建——保留它的统计和会话归属。
func saveChannelKeys(ctx context.Context, tx pgx.Tx, channelID int64, keys []ChannelKey) (int, error) {
	existing := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT id, api_key FROM channel_keys WHERE channel_id = $1`, channelID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return 0, err
		}
		existing[key] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	keyOf := map[int64]string{}
	for k, id := range existing {
		keyOf[id] = k
	}

	type row struct {
		id      int64 // 0 表示新加
		key     string
		enabled bool
		note    string
	}
	var plan []row
	seen := map[string]bool{}
	kept := map[int64]bool{}
	dups := 0
	for _, k := range keys {
		key := strings.TrimSpace(k.APIKey)
		id := k.ID
		if id != 0 {
			if _, ok := keyOf[id]; !ok {
				return 0, badInput("key %d 不属于这个渠道", id)
			}
			if key == "" {
				key = keyOf[id]
			}
		} else if key == "" {
			continue // 粘贴里的空行
		} else if old, ok := existing[key]; ok {
			id = old // 和已有的某把相同：沿用那一条
		}
		if seen[key] || (id != 0 && kept[id]) {
			dups++
			continue
		}
		seen[key] = true
		if id != 0 {
			kept[id] = true
		}
		plan = append(plan, row{id: id, key: key, enabled: k.Enabled, note: strings.TrimSpace(k.Note)})
	}
	if len(plan) == 0 {
		return 0, badInput("渠道至少要有一把 key")
	}
	if len(plan) > MaxChannelKeys {
		return 0, badInput("一个渠道最多 %d 把 key（去重后有 %d 把）", MaxChannelKeys, len(plan))
	}
	var keep []int64
	for i, r := range plan {
		if r.id != 0 {
			if _, err := tx.Exec(ctx, `UPDATE channel_keys SET api_key = $3, enabled = $4, note = $5, sort_order = $6 WHERE id = $1 AND channel_id = $2`,
				r.id, channelID, r.key, r.enabled, r.note, i); err != nil {
				return 0, translate(err)
			}
			keep = append(keep, r.id)
			continue
		}
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO channel_keys (channel_id, api_key, enabled, note, sort_order) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			channelID, r.key, r.enabled, r.note, i).Scan(&id); err != nil {
			return 0, translate(err)
		}
		keep = append(keep, id)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM channel_keys WHERE channel_id = $1 AND NOT (id = ANY($2::bigint[]))`, channelID, keep); err != nil {
		return 0, err
	}
	return dups, nil
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

// DeleteChannel 删除渠道；余额快照随渠道一起删，请求日志不受影响。
func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, channelEntity, &id, 0, func() error {
			return execOne(ctx, tx, `DELETE FROM channels WHERE id = $1`, id)
		})
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

type APIKeyPatch struct {
	Name     *string
	Enabled  *bool
	GroupID  *int64
	SetQuota bool // 为 true 时把限额改成 Quota（nil 表示不限）
	Quota    *decimal.Decimal
}

// UpdateAPIKey 在一个事务里改名称、启停、分组和限额，要么全改要么全不改。
func (s *Store) UpdateAPIKey(ctx context.Context, userID, keyID int64, p APIKeyPatch) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, keyEntity, &keyID, userID, func() error {
			return execOne(ctx, tx, `
				UPDATE api_keys SET name = COALESCE($3, name), enabled = COALESCE($4, enabled), group_id = COALESCE($5, group_id),
				       quota_limit = CASE WHEN $6 THEN $7::numeric ELSE quota_limit END
				WHERE id = $2 AND user_id = $1`, userID, keyID, p.Name, p.Enabled, p.GroupID, p.SetQuota, decimalArg(p.Quota))
		})
	})
}

func (s *Store) DeleteAPIKey(ctx context.Context, userID, keyID int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, keyEntity, &keyID, userID, func() error {
			return execOne(ctx, tx, `DELETE FROM api_keys WHERE id = $2 AND user_id = $1`, userID, keyID)
		})
	})
}

// ---- 请求日志 ----

type LogFilter struct {
	Type      string
	IDs       []int64
	UserID    int64 // 0 表示全部用户（管理端）
	Model     string
	ChannelID int64
	Status    string
	Target    string // 操作日志的对象类型
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
	ClientIP         string          `json:"client_ip"`
	ReasoningEffort  string          `json:"reasoning_effort"`
	ServiceTier      string          `json:"service_tier"`      // '' 标准、fast、fast_downgraded、unavailable
	ReadMS           int             `json:"read_ms"`           // 读请求体用时，旧日志为 0
	UpstreamStartMS  int             `json:"upstream_start_ms"` // 发出最终上游请求的时刻（从进入网关算），旧日志为 0
}

type LogSummary struct {
	TopupTotal    decimal.Decimal `json:"topup_total"`
	AdjustmentNet decimal.Decimal `json:"adjustment_net"`
	Requests      int64           `json:"requests"`
	Failed        int64           `json:"failed"`
	InputTokens   int64           `json:"input_tokens"` // 含缓存读写
	CacheRead     int64           `json:"cache_read_tokens"`
	OutputTokens  int64           `json:"output_tokens"`
	Cost          decimal.Decimal `json:"cost"`
	// 负载与响应（调用日志，2026-11-07）：区间平均的 RPM 没有意义（7 天 3000 次平均每分钟 0.3 次），
	// 取最忙那一分钟——它决定会不会撞上游限流。TPM 按输入（含缓存）+ 输出算，和厂商限流口径一致。
	// 响应时间取中位数：推理请求偶尔几分钟，平均值会被少数长请求拉高。只算成功的请求。
	PeakRPM            int64      `json:"peak_rpm"`
	PeakRPMAt          *time.Time `json:"peak_rpm_at"`
	PeakTPM            int64      `json:"peak_tpm"`
	PeakTPMAt          *time.Time `json:"peak_tpm_at"`
	MedianFirstTokenMS *int64     `json:"median_first_token_ms"` // 成功流式请求，从发给上游算起（同调用日志的首字）
	MedianTotalMS      *int64     `json:"median_total_ms"`
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
		       COALESCE(sum(l.input_tokens + l.cache_read_tokens + l.cache_write_tokens), 0), COALESCE(sum(l.cache_read_tokens), 0),
		       COALESCE(sum(l.output_tokens), 0), COALESCE(sum(l.cost), 0)::text
		FROM request_logs l `+where, args...).Scan(&sum.Requests, &sum.Failed, &sum.InputTokens, &sum.CacheRead, &sum.OutputTokens, &cost); err != nil {
		return nil, sum, err
	}
	sum.Cost = mustDecimal(cost)

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit, f.Offset)
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
		SELECT l.id, l.request_id, u.username, COALESCE(NULLIF(l.key_name, ''), k.name, ''), COALESCE(NULLIF(l.group_name, ''), g.name, ''), l.protocol, l.model,
		       l.upstream_model, COALESCE(NULLIF(l.channel_name, ''), c.name, ''), l.stream, l.status, l.http_status, l.error,
		       l.input_tokens, l.cache_read_tokens, l.cache_write_tokens, l.output_tokens, l.reasoning_tokens,
		       l.usage_estimated, l.group_ratio::text, l.cost::text, l.attempts, l.first_byte_ms, l.total_ms, l.created_at,
		       l.price_rule, l.input_price::text, l.output_price::text, l.client_ip, l.reasoning_effort, l.service_tier,
		       l.read_ms, l.upstream_start_ms
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
			&e.PriceRule, &inPrice, &outPrice, &e.ClientIP, &e.ReasoningEffort, &e.ServiceTier,
			&e.ReadMS, &e.UpstreamStartMS)
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
