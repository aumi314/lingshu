package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ProbeRun 是一次模型检测（docs/decisions.md「模型检测」）。Report 是 probe.Report 的 JSON，列表里不带。
type ProbeRun struct {
	ID            int64           `json:"id"`
	CreatedAt     time.Time       `json:"created_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	CreatedBy     string          `json:"created_by"`
	ChannelID     *int64          `json:"channel_id"`
	ChannelName   string          `json:"channel_name"`
	Model         string          `json:"model"`
	UpstreamModel string          `json:"upstream_model"`
	Baseline      string          `json:"baseline"`
	Preset        string          `json:"preset"`
	Status        string          `json:"status"`
	Score         *int            `json:"score"`
	Verdict       string          `json:"verdict"`
	Cost          decimal.Decimal `json:"cost"`
	Report        json.RawMessage `json:"report,omitempty"`
}

func (s *Store) CreateProbeRun(ctx context.Context, r *ProbeRun) error {
	return s.Pool.QueryRow(ctx, `
		INSERT INTO probe_runs (created_by, channel_id, channel_name, model, upstream_model, baseline, preset, status)
		VALUES (NULLIF($1, 0), $2, $3, $4, $5, $6, $7, 'running') RETURNING id, created_at`,
		actorFrom(ctx), r.ChannelID, r.ChannelName, r.Model, r.UpstreamModel, r.Baseline, r.Preset).Scan(&r.ID, &r.CreatedAt)
}

// FinishProbeRun 写入检测结果；report 为 nil 表示失败，verdict 是失败原因。
func (s *Store) FinishProbeRun(ctx context.Context, id int64, status string, score *int, verdict string, cost decimal.Decimal, report []byte) error {
	return execOne(ctx, s.Pool, `UPDATE probe_runs SET finished_at = now(), status = $2, score = $3, verdict = $4, cost = $5::numeric, report = $6 WHERE id = $1`,
		id, status, score, verdict, cost.String(), report)
}

// FailStaleProbeRuns 把上次进程里没跑完的检测标成失败（检测在内存里跑，重启后不会继续）。
func (s *Store) FailStaleProbeRuns(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE probe_runs SET status = 'failed', finished_at = now(), verdict = '服务重启，检测中断' WHERE status = 'running'`)
	return err
}

const probeColumns = `r.id, r.created_at, r.finished_at, COALESCE(u.username, ''), r.channel_id, r.channel_name, r.model, r.upstream_model,
	r.baseline, r.preset, r.status, r.score, r.verdict, r.cost::text`

func scanProbe(row pgx.Row, withReport bool) (ProbeRun, error) {
	var r ProbeRun
	var cost string
	dest := []any{&r.ID, &r.CreatedAt, &r.FinishedAt, &r.CreatedBy, &r.ChannelID, &r.ChannelName, &r.Model, &r.UpstreamModel,
		&r.Baseline, &r.Preset, &r.Status, &r.Score, &r.Verdict, &cost}
	var report []byte
	if withReport {
		dest = append(dest, &report)
	}
	if err := row.Scan(dest...); err != nil {
		return r, translate(err)
	}
	r.Cost = mustDecimal(cost)
	if len(report) > 0 {
		r.Report = report
	}
	return r, nil
}

// ListProbeRuns 返回最近的检测记录（不带完整结果），新的在前。
func (s *Store) ListProbeRuns(ctx context.Context, limit int) ([]ProbeRun, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+probeColumns+` FROM probe_runs r LEFT JOIN users u ON u.id = r.created_by ORDER BY r.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ProbeRun, error) { return scanProbe(row, false) })
}

func (s *Store) GetProbeRun(ctx context.Context, id int64) (ProbeRun, error) {
	return scanProbe(s.Pool.QueryRow(ctx, `SELECT `+probeColumns+`, r.report FROM probe_runs r LEFT JOIN users u ON u.id = r.created_by WHERE r.id = $1`, id), true)
}

// CountRunningProbes 是进行中的检测数，用来限制同时检测的数量。
func (s *Store) CountRunningProbes(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM probe_runs WHERE status = 'running'`).Scan(&n)
	return n, err
}
