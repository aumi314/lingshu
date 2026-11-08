package store

import (
	"context"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

type LogOrderSummary struct {
	ID          string          `json:"id"`
	Provider    string          `json:"provider"`
	Method      string          `json:"method"`
	Currency    string          `json:"currency"`
	Amount      decimal.Decimal `json:"amount"`
	Tax         decimal.Decimal `json:"tax"`
	Total       decimal.Decimal `json:"total"`
	CompletedAt *time.Time      `json:"completed_at"`
	Status      string          `json:"status"` // 订单当前状态：pending、success、failed、expired
}

type ActivityLog struct {
	*LogEntry
	ID           int64            `json:"id"`
	EventKey     string           `json:"event_key"`
	Type         string           `json:"type"`
	UserID       int64            `json:"user_id"`
	Username     string           `json:"username"`
	CreatedAt    time.Time        `json:"created_at"`
	Status       string           `json:"status"`
	BalanceDelta decimal.Decimal  `json:"balance_delta"`
	BalanceAfter *decimal.Decimal `json:"balance_after,omitempty"`
	Operator     string           `json:"operator"`
	Note         string           `json:"note"`
	Order        *LogOrderSummary `json:"order,omitempty"`
}

// 以余额流水而非订单状态判断入账，历史记录自然纳入且不重复记账。
const activityCTE = `WITH events AS (
 SELECT l.id,0 AS source,'request'::text AS type,l.user_id,l.created_at,l.status,
        -l.cost AS delta,NULL::numeric AS balance_after,NULL::bigint AS operator_id,
        ''::text AS note,NULL::text AS order_id,l.cost,
        l.input_tokens+l.cache_read_tokens+l.cache_write_tokens AS input_tokens,l.cache_read_tokens,l.output_tokens
 FROM request_logs l
 WHERE ($5::text='' OR l.model=$5) AND ($6::text='' OR l.status=$6) AND ($7::bigint=0 OR l.channel_id=$7)
 UNION ALL
 SELECT b.id,1,CASE WHEN b.topup_order_id IS NULL THEN 'adjustment' ELSE 'topup' END,
        b.user_id,b.created_at,'success',b.amount,b.balance_after,b.operator_id,b.note,b.topup_order_id,
        0::numeric,0::bigint,0::bigint,0::bigint
 FROM balance_adjustments b
 WHERE $5::text='' AND $6::text='' AND $7::bigint=0
), filtered AS (
 SELECT * FROM events WHERE ($1::text='all' OR type=$1) AND ($2::bigint=0 OR user_id=$2)
 AND ($3::timestamptz IS NULL OR created_at >= $3) AND ($4::timestamptz IS NULL OR created_at < $4)
)
`

// requestLoad 按和调用日志相同的筛选（用户、模型、状态、渠道、时间）算峰值 RPM / TPM 和响应时间中位数。
func (s *Store) requestLoad(ctx context.Context, f LogFilter, sum *LogSummary) error {
	return s.Pool.QueryRow(ctx, `
		WITH r AS (
			SELECT created_at, status, stream, first_byte_ms, upstream_start_ms, total_ms,
			       input_tokens + cache_read_tokens + cache_write_tokens + output_tokens AS tok
			FROM request_logs
			WHERE ($1::bigint = 0 OR user_id = $1) AND ($2::timestamptz IS NULL OR created_at >= $2) AND ($3::timestamptz IS NULL OR created_at < $3)
			  AND ($4::text = '' OR model = $4) AND ($5::text = '' OR status = $5) AND ($6::bigint = 0 OR channel_id = $6)
		), m AS (SELECT date_trunc('minute', created_at) AS at, count(*) AS n, sum(tok) AS tok FROM r GROUP BY 1)
		SELECT COALESCE((SELECT max(n) FROM m), 0), (SELECT at FROM m ORDER BY n DESC, at DESC LIMIT 1),
		       COALESCE((SELECT max(tok) FROM m), 0)::bigint, (SELECT at FROM m ORDER BY tok DESC, at DESC LIMIT 1),
		       (SELECT round(percentile_cont(0.5) WITHIN GROUP (ORDER BY first_byte_ms - upstream_start_ms))::bigint FROM r
		        WHERE status = 'success' AND stream AND upstream_start_ms > 0 AND first_byte_ms > upstream_start_ms),
		       (SELECT round(percentile_cont(0.5) WITHIN GROUP (ORDER BY total_ms))::bigint FROM r WHERE status = 'success')`,
		f.UserID, f.From, f.To, f.Model, f.Status, f.ChannelID,
	).Scan(&sum.PeakRPM, &sum.PeakRPMAt, &sum.PeakTPM, &sum.PeakTPMAt, &sum.MedianFirstTokenMS, &sum.MedianTotalMS)
}

func (s *Store) ListActivityLogs(ctx context.Context, f LogFilter) ([]ActivityLog, LogSummary, int64, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{f.Type, f.UserID, f.From, f.To, f.Model, f.Status, f.ChannelID}
	var summary LogSummary
	var total int64
	var cost, topup, adjustment string
	err := s.Pool.QueryRow(ctx, activityCTE+`SELECT count(*),count(*) FILTER(WHERE type='request'),
 count(*) FILTER(WHERE type='request' AND status NOT IN ('success','client_cancelled')),
 COALESCE(sum(input_tokens),0),COALESCE(sum(cache_read_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(cost),0)::text,
 COALESCE(sum(delta) FILTER(WHERE type='topup'),0)::text,COALESCE(sum(delta) FILTER(WHERE type='adjustment'),0)::text
 FROM filtered`, args...).Scan(&total, &summary.Requests, &summary.Failed, &summary.InputTokens, &summary.CacheRead, &summary.OutputTokens, &cost, &topup, &adjustment)
	if err != nil {
		return nil, summary, 0, err
	}
	summary.Cost, summary.TopupTotal, summary.AdjustmentNet = mustDecimal(cost), mustDecimal(topup), mustDecimal(adjustment)
	if f.Type == "request" || f.Type == "all" {
		if err := s.requestLoad(ctx, f, &summary); err != nil {
			return nil, summary, 0, err
		}
	}
	args = append(args, limit, f.Offset)
	rows, err := s.Pool.Query(ctx, activityCTE+`SELECT f.id,f.type,f.user_id,u.username,f.created_at,f.status,
 f.delta::text,f.balance_after::text,COALESCE(actor.username,''),f.note,
 o.id,COALESCE(o.provider,''),COALESCE(o.method,''),COALESCE(o.currency,''),
 COALESCE(o.amount,0)::text,COALESCE(o.tax,0)::text,COALESCE(o.total,0)::text,o.completed_at
 FROM filtered f JOIN users u ON u.id=f.user_id LEFT JOIN users actor ON actor.id=f.operator_id
 LEFT JOIN topup_orders o ON o.id=f.order_id
 ORDER BY f.created_at DESC,f.source DESC,f.id DESC LIMIT $8 OFFSET $9`, args...)
	if err != nil {
		return nil, summary, 0, err
	}
	logs := make([]ActivityLog, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		var e ActivityLog
		var delta string
		var after, orderID *string
		var order LogOrderSummary
		var amount, tax, paid string
		err = rows.Scan(&e.ID, &e.Type, &e.UserID, &e.Username, &e.CreatedAt, &e.Status, &delta, &after, &e.Operator, &e.Note,
			&orderID, &order.Provider, &order.Method, &order.Currency, &amount, &tax, &paid, &order.CompletedAt)
		if err != nil {
			rows.Close()
			return nil, summary, 0, err
		}
		e.BalanceDelta = mustDecimal(delta)
		if after != nil {
			v := mustDecimal(*after)
			e.BalanceAfter = &v
		}
		if orderID != nil {
			order.ID = *orderID
			order.Amount = mustDecimal(amount)
			order.Tax = mustDecimal(tax)
			order.Total = mustDecimal(paid)
			e.Order = &order
		}
		e.EventKey = "balance:" + strconv.FormatInt(e.ID, 10)
		if e.Type == "request" {
			e.EventKey = "request:" + strconv.FormatInt(e.ID, 10)
			ids = append(ids, e.ID)
		}
		logs = append(logs, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, summary, 0, err
	}
	// 只补齐当前页的调用详情，保留原有字段和渠道名称快照规则。
	if len(ids) > 0 {
		requests, _, err := s.ListLogs(ctx, LogFilter{IDs: ids, Limit: 200})
		if err != nil {
			return nil, summary, 0, err
		}
		byID := make(map[int64]LogEntry, len(requests))
		for _, request := range requests {
			byID[request.ID] = request
		}
		for i := range logs {
			if logs[i].Type == "request" {
				request := byID[logs[i].ID]
				logs[i].LogEntry = &request
			}
		}
	}
	return logs, summary, total, nil
}
