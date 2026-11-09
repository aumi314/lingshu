package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/payments"
)

// ---- 操作日志 ----
//
// 每次状态变更在同一个事务里写一行：改前、改后各取一份快照，比对出变更列表。
// 这样各接口不用手写“改了哪些字段”，也不会出现改成功了却没留记录。

type actorKey struct{}

// WithActor 把当前操作人放进 context，存储层写操作日志时取用；没有操作人记为系统。
func WithActor(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, actorKey{}, userID)
}

func actorFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(actorKey{}).(int64)
	return id
}

// Change 是一个字段的改前 / 改后；密钥类字段只标记 Secret，不带值。
type Change struct {
	Field  string          `json:"field"`
	From   json.RawMessage `json:"from,omitempty"`
	To     json.RawMessage `json:"to,omitempty"`
	Secret bool            `json:"secret,omitempty"`
}

type snapshot map[string]json.RawMessage

// entity 描述一类对象怎么取快照。快照 SQL 以 $1 为对象 id，返回一个 jsonb；
// 关联数据用名称而不是 id，数值转成文本避免精度丢失。secrets 里的字段快照存指纹，日志只记“已更换”。
type entity struct {
	typ       string
	nameField string
	sql       string
	secrets   []string
}

var (
	userEntity = entity{"user", "username", `
		SELECT jsonb_build_object('username', u.username, 'role', u.role, 'enabled', u.enabled, 'note', u.note,
		       'password', md5(u.password_hash),
		       'user_groups', COALESCE((SELECT jsonb_agg(g.name ORDER BY g.name) FROM user_group_members m
		                                JOIN user_groups g ON g.id = m.user_group_id WHERE m.user_id = u.id), '[]'))
		FROM users u WHERE u.id = $1`, []string{"password"}}
	keyEntity = entity{"api_key", "name", `
		SELECT jsonb_build_object('name', k.name, 'prefix', k.key_prefix, 'enabled', k.enabled, 'group', g.name,
		       'quota_limit', k.quota_limit::text)
		FROM api_keys k JOIN groups g ON g.id = k.group_id WHERE k.id = $1`, nil}
	groupEntity = entity{"group", "name", `
		SELECT jsonb_build_object('name', g.name, 'ratio', g.ratio::text, 'enabled', g.enabled, 'is_public', g.is_public, 'retry_count', g.retry_count,
		       'description', g.description, 'is_collection', g.is_collection,
		       'members', COALESCE((SELECT jsonb_agg(m.name ORDER BY m.name) FROM collection_groups x
		                            JOIN groups m ON m.id = x.group_id WHERE x.collection_id = g.id), '[]'),
		       'user_groups', COALESCE((SELECT jsonb_agg(ug.name ORDER BY ug.name) FROM group_user_groups x
		                                JOIN user_groups ug ON ug.id = x.user_group_id WHERE x.group_id = g.id), '[]'))
		FROM groups g WHERE g.id = $1`, nil}
	userGroupEntity = entity{"user_group", "name", `SELECT jsonb_build_object('name', name) FROM user_groups WHERE id = $1`, nil}
	modelEntity     = entity{"model", "name", `
		SELECT jsonb_build_object('name', name, 'input_price', input_price::text, 'output_price', output_price::text,
		       'cache_read_price', cache_read_price::text, 'cache_write_price', cache_write_price::text, 'price_rules', price_rules,
		       'fast_multiplier', fast_multiplier::text)
		FROM models WHERE id = $1`, nil}
	// 代理地址里可能带账号密码，快照前遮掉
	channelEntity = entity{"channel", "name", `
		SELECT jsonb_build_object('name', c.name, 'template', c.template, 'note', c.note, 'priority', c.priority,
		       'api_key', (SELECT md5(string_agg(k.api_key, ',' ORDER BY k.id)) FROM channel_keys k WHERE k.channel_id = c.id),
		       'keys', COALESCE((SELECT jsonb_agg(CASE WHEN length(k.api_key) > 10 THEN left(k.api_key, 4) || '…' || right(k.api_key, 4) ELSE repeat('*', length(k.api_key)) END
		                                          || CASE WHEN k.enabled THEN '' ELSE '（停用）' END || CASE WHEN k.note <> '' THEN '：' || k.note ELSE '' END
		                                          ORDER BY k.sort_order, k.id) FROM channel_keys k WHERE k.channel_id = c.id), '[]'),
		       'enabled', c.enabled, 'base_url_chat', c.base_url_chat, 'base_url_messages', c.base_url_messages,
		       'base_url_responses', c.base_url_responses, 'base_url_systemone', c.base_url_systemone,
		       'proxy_url', regexp_replace(c.proxy_url, '//[^@/]*@', '//***@'),
		       'groups', COALESCE((SELECT jsonb_agg(g.name ORDER BY g.name) FROM channel_groups cg
		                           JOIN groups g ON g.id = cg.group_id WHERE cg.channel_id = c.id), '[]'),
		       'models', COALESCE((SELECT jsonb_agg(m.name || CASE WHEN cm.upstream_name <> '' THEN ' → ' || cm.upstream_name ELSE '' END ORDER BY m.name)
		                           FROM channel_models cm JOIN models m ON m.id = cm.model_id WHERE cm.channel_id = c.id), '[]'))
		FROM channels c WHERE c.id = $1`, []string{"api_key"}}
	codeEntity = entity{"registration_code", "prefix", `
		SELECT jsonb_build_object('prefix', code_prefix,
		       'status', CASE WHEN used_at IS NOT NULL THEN 'used' WHEN revoked_at IS NOT NULL THEN 'revoked' ELSE 'active' END)
		FROM registration_codes WHERE id = $1`, nil}
	// fx_status 是实时汇率的拉取状态（时间、错误），每小时都会变，不进操作日志
	settingsEntity = entity{"settings", "", `SELECT COALESCE(jsonb_object_agg(key, value), '{}') FROM settings WHERE $1::bigint = 0 AND key <> 'fx_status'`, nil}
)

func takeSnapshot(ctx context.Context, tx pgx.Tx, e entity, id int64) (snapshot, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, e.sql, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var s snapshot
	return s, json.Unmarshal(raw, &s)
}

// diff 按字段名排序比对两份快照；任一边为 nil 表示新建或删除。
func diff(before, after snapshot, secrets []string) []Change {
	fields := map[string]bool{}
	for k := range before {
		fields[k] = true
	}
	for k := range after {
		fields[k] = true
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	changes := []Change{}
	for _, k := range names {
		from, to := before[k], after[k]
		if bytes.Equal(from, to) {
			continue
		}
		c := Change{Field: k, From: from, To: to}
		for _, s := range secrets {
			if s == k {
				c = Change{Field: k, Secret: true}
			}
		}
		changes = append(changes, c)
	}
	return changes
}

type operation struct {
	actor      *int64 // nil 取 context 里的操作人
	userID     int64  // 受影响用户，0 表示与具体用户无关
	action     string
	targetType string
	targetID   string
	targetName string
	changes    []Change
	note       string
	amount     *decimal.Decimal
	orderID    string
}

func record(ctx context.Context, tx pgx.Tx, o operation) error {
	actor := actorFrom(ctx)
	if o.actor != nil {
		actor = *o.actor
	}
	if o.changes == nil {
		o.changes = []Change{}
	}
	changes, err := json.Marshal(o.changes)
	if err != nil {
		return err
	}
	var amount *string
	if o.amount != nil {
		v := o.amount.String()
		amount = &v
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO operation_logs (actor_id, actor_name, user_id, action, target_type, target_id, target_name, changes, note, amount, order_id)
		VALUES (NULLIF($1, 0), COALESCE((SELECT username FROM users WHERE id = $1), ''), NULLIF($2, 0), $3, $4, $5, $6, $7, $8, $9::numeric, NULLIF($10, ''))`,
		actor, o.userID, o.action, o.targetType, o.targetID, o.targetName, changes, o.note, amount, o.orderID)
	return err
}

// recordSnapshots 按两份快照写一条操作日志；没有变化的保存不记。
func recordSnapshots(ctx context.Context, tx pgx.Tx, e entity, id, userID int64, before, after snapshot) error {
	action := e.typ + ".update"
	named := after
	switch {
	case before == nil && after == nil:
		return nil
	case before == nil:
		action = e.typ + ".create"
	case after == nil:
		action, named = e.typ+".delete", before
	}
	changes := diff(before, after, e.secrets)
	if len(changes) == 0 {
		return nil
	}
	var name string
	if e.nameField != "" {
		json.Unmarshal(named[e.nameField], &name)
	}
	targetID := ""
	if id != 0 {
		targetID = strconv.FormatInt(id, 10)
	}
	return record(ctx, tx, operation{userID: userID, action: action, targetType: e.typ, targetID: targetID, targetName: name, changes: changes})
}

// track 在 fn 前后各取一次快照并记录差异。新建时 *id 为 0，由 fn 填入。
// userID 为受影响用户；对用户本身的操作传 -1，表示就是 *id。
func track(ctx context.Context, tx pgx.Tx, e entity, id *int64, userID int64, fn func() error) error {
	var before snapshot
	if *id != 0 || e.typ == settingsEntity.typ { // 设置没有 id，总是先取改前
		var err error
		if before, err = takeSnapshot(ctx, tx, e, *id); err != nil {
			return err
		}
	}
	if err := fn(); err != nil {
		return err
	}
	after, err := takeSnapshot(ctx, tx, e, *id)
	if err != nil {
		return err
	}
	if userID == -1 {
		userID = *id
	}
	return recordSnapshots(ctx, tx, e, *id, userID, before, after)
}

// recordBalance 记录一次余额变动，与 balance_adjustments 一一对应。action 为 balance.adjust（人工调整）、
// topup.credit（在线充值到账）或 redeem.credit（兑换码充值）。
func recordBalance(ctx context.Context, tx pgx.Tx, actor *int64, userID int64, amount decimal.Decimal, after, note, orderID, action string) error {
	afterValue := mustDecimal(after)
	from, _ := json.Marshal(afterValue.Sub(amount).String())
	to, _ := json.Marshal(afterValue.String())
	var name string
	if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		return err
	}
	return record(ctx, tx, operation{
		actor: actor, userID: userID, action: action, targetType: "balance", targetID: strconv.FormatInt(userID, 10), targetName: name,
		changes: []Change{{Field: "balance", From: from, To: to}}, note: note, amount: &amount, orderID: orderID,
	})
}

// recordTopupCreate 记录用户发起一笔在线充值（充值并入操作日志，2026-11-06）。操作人取 context 里的下单用户；
// 订单之后的状态不再写新记录，查询时按 order_id 实时关联。
func recordTopupCreate(ctx context.Context, tx pgx.Tx, o payments.Order) error {
	str := func(field, v string) Change {
		b, _ := json.Marshal(v)
		return Change{Field: field, To: b}
	}
	return record(ctx, tx, operation{
		userID: o.UserID, action: "topup.create", targetType: "topup", targetID: o.ID, targetName: o.ID, orderID: o.ID,
		changes: []Change{
			str("credit_usd", o.CreditUSD.String()),
			str("pay_amount", o.Amount.String()+" "+o.Currency),
			str("provider", o.Provider),
			str("method", o.Method),
		},
	})
}

// ---- 查询 ----

type OperationLog struct {
	ID         int64            `json:"id"`
	CreatedAt  time.Time        `json:"created_at"`
	ActorID    *int64           `json:"actor_id"` // nil 表示系统
	Actor      string           `json:"actor"`
	UserID     *int64           `json:"user_id"`
	Action     string           `json:"action"`
	TargetType string           `json:"target_type"`
	TargetID   string           `json:"target_id"`
	TargetName string           `json:"target_name"`
	Changes    []Change         `json:"changes"`
	Note       string           `json:"note"`
	Amount     *decimal.Decimal `json:"amount,omitempty"`
	Order      *LogOrderSummary `json:"order,omitempty"`
}

// ListOperations 按时间倒序分页。member 为 true 时 f.UserID 是成员本人：只看影响自己的记录，
// 用户备注属于管理员内部信息，从变更里剔除，只改了备注的记录整条不返回。
// 管理员按用户筛选时，既看该用户被做了什么，也看他做了什么。
func (s *Store) ListOperations(ctx context.Context, f LogFilter, member bool) ([]OperationLog, LogSummary, int64, error) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.UserID != 0 {
		if member {
			add("o.user_id = $%d", f.UserID)
			conds = append(conds, `NOT (o.target_type = 'user' AND NOT EXISTS (
				SELECT 1 FROM jsonb_array_elements(o.changes) c WHERE c->>'field' <> 'note'))`)
		} else {
			add("(o.user_id = $%[1]d OR o.actor_id = $%[1]d)", f.UserID)
		}
	}
	if f.Target != "" {
		add("o.target_type = $%d", f.Target)
	}
	if f.From != nil {
		add("o.created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("o.created_at < $%d", *f.To)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	var summary LogSummary
	var total int64
	var topup, adjustment string
	if err := s.Pool.QueryRow(ctx, `SELECT count(*),
		COALESCE(sum(o.amount) FILTER (WHERE o.action IN ('topup.credit', 'redeem.credit')), 0)::text,
		COALESCE(sum(o.amount) FILTER (WHERE o.action = 'balance.adjust'), 0)::text
		FROM operation_logs o `+where, args...).Scan(&total, &topup, &adjustment); err != nil {
		return nil, summary, 0, err
	}
	summary.TopupTotal, summary.AdjustmentNet = mustDecimal(topup), mustDecimal(adjustment)
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit, f.Offset)
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
		SELECT o.id, o.created_at, o.actor_id, COALESCE(a.username, NULLIF(o.actor_name, ''), ''), o.user_id, o.action, o.target_type,
		       o.target_id, o.target_name, o.changes, o.note, o.amount::text,
		       t.id, COALESCE(t.provider, ''), COALESCE(t.method, ''), COALESCE(t.currency, ''),
		       COALESCE(t.amount, 0)::text, COALESCE(t.tax, 0)::text, COALESCE(t.total, 0)::text, t.completed_at, COALESCE(t.status, '')
		FROM operation_logs o
		LEFT JOIN users a ON a.id = o.actor_id
		LEFT JOIN topup_orders t ON t.id = o.order_id
		%s ORDER BY o.created_at DESC, o.id DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, summary, 0, err
	}
	logs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (OperationLog, error) {
		var o OperationLog
		var changes []byte
		var amount, orderID *string
		var order LogOrderSummary
		var orderAmount, tax, paid string
		err := r.Scan(&o.ID, &o.CreatedAt, &o.ActorID, &o.Actor, &o.UserID, &o.Action, &o.TargetType, &o.TargetID, &o.TargetName,
			&changes, &o.Note, &amount, &orderID, &order.Provider, &order.Method, &order.Currency, &orderAmount, &tax, &paid, &order.CompletedAt, &order.Status)
		if err != nil {
			return o, err
		}
		if err := json.Unmarshal(changes, &o.Changes); err != nil {
			return o, err
		}
		if member && o.TargetType == "user" {
			kept := o.Changes[:0]
			for _, c := range o.Changes {
				if c.Field != "note" {
					kept = append(kept, c)
				}
			}
			o.Changes = kept
		}
		o.Amount = optDecimal(amount)
		if orderID != nil {
			order.ID = *orderID
			order.Amount, order.Tax, order.Total = mustDecimal(orderAmount), mustDecimal(tax), mustDecimal(paid)
			o.Order = &order
		}
		return o, nil
	})
	return logs, summary, total, err
}
