package store

import (
	"context"
	"time"
)

// ChannelStat 是一个渠道在统计窗口内的连接状态（渠道页「连接状态」列，2026-11-06）。
//
// 口径：
//   - 成功：最终由这个渠道完成、状态为 success 的请求。用户中途断开的不计入成功也不计入失败。
//   - 失败：这个渠道上可换渠道重试的失败（5xx、429、鉴权、超时、断开等，记在 failed_channel_ids，含换渠道后成功的请求），
//     加上已开始返回后才断掉的流（状态 upstream_error、HTTP 状态 < 400，且没记进 failed_channel_ids）。
//     参数错、上下文超长这类请求本身的问题（原样返回的 4xx）不算渠道失败。
//   - 首字、速度取成功流式请求的中位数，口径与调用日志一致：首字从发给上游算起（旧日志没有发出时刻，不计入），
//     速度 = 可见输出 ÷ 首字之后的传输时间，传输不足 1 秒或可见输出不足 50 的不计入。
//   - 缓存命中率 = 缓存读 ÷ 全部输入（含缓存读写），只算成功请求。
type ChannelStat struct {
	Success       int64      `json:"success"`
	Failed        int64      `json:"failed"`
	FirstTokenMS  *int64     `json:"first_token_ms"` // 中位数；没有可用样本时为 null
	TokensPerSec  *int64     `json:"tokens_per_sec"`
	CacheRead     int64      `json:"cache_read_tokens"`
	InputTokens   int64      `json:"input_tokens"` // 含缓存读写
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastFailureAt *time.Time `json:"last_failure_at"`
}

// ChannelStats 统计 since 之后各渠道的连接状态；没有任何尝试的渠道不在结果里。
func (s *Store) ChannelStats(ctx context.Context, since time.Time) (map[int64]ChannelStat, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH w AS (SELECT * FROM request_logs WHERE created_at >= $1),
		fails AS (
			SELECT f AS ch, count(*) AS n, max(w.created_at) AS last_at FROM w, unnest(w.failed_channel_ids) AS f GROUP BY f
			UNION ALL
			SELECT channel_id, count(*), max(created_at) FROM w
			WHERE status = 'upstream_error' AND channel_id IS NOT NULL AND http_status < 400 AND NOT (channel_id = ANY(failed_channel_ids))
			GROUP BY channel_id
		),
		f AS (SELECT ch, sum(n)::bigint AS n, max(last_at) AS last_at FROM fails GROUP BY ch),
		ok AS (
			SELECT channel_id AS ch, count(*) AS n,
			       percentile_cont(0.5) WITHIN GROUP (ORDER BY first_byte_ms - upstream_start_ms)
			           FILTER (WHERE stream AND upstream_start_ms > 0 AND first_byte_ms > upstream_start_ms) AS ttft,
			       percentile_cont(0.5) WITHIN GROUP (ORDER BY (output_tokens - reasoning_tokens) * 1000.0 / (total_ms - first_byte_ms))
			           FILTER (WHERE stream AND first_byte_ms > 0 AND total_ms - first_byte_ms >= 1000 AND output_tokens - reasoning_tokens >= 50) AS tps,
			       sum(cache_read_tokens)::bigint AS cache_read, sum(input_tokens + cache_read_tokens + cache_write_tokens)::bigint AS input,
			       max(created_at) AS last_ok
			FROM w WHERE status = 'success' AND channel_id IS NOT NULL GROUP BY channel_id
		)
		SELECT COALESCE(ok.ch, f.ch), COALESCE(ok.n, 0), COALESCE(f.n, 0), round(ok.ttft)::bigint, round(ok.tps)::bigint,
		       COALESCE(ok.cache_read, 0), COALESCE(ok.input, 0), ok.last_ok, f.last_at
		FROM ok FULL JOIN f ON f.ch = ok.ch`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ChannelStat{}
	for rows.Next() {
		var id int64
		var st ChannelStat
		if err := rows.Scan(&id, &st.Success, &st.Failed, &st.FirstTokenMS, &st.TokensPerSec, &st.CacheRead, &st.InputTokens, &st.LastSuccessAt, &st.LastFailureAt); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}
