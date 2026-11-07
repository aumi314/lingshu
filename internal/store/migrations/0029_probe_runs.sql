-- 模型检测记录（2026-11-07，docs/decisions.md「模型检测」）：选渠道和模型发起一次检测，结果（逐项结论、请求质量、指标、每个请求的原文和回答）存在 report。
-- 渠道删除后保留记录和渠道名快照。检测请求不进调用日志、不计费。
CREATE TABLE probe_runs (
    id             BIGSERIAL PRIMARY KEY,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    created_by     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    channel_id     BIGINT REFERENCES channels(id) ON DELETE SET NULL,
    channel_name   TEXT NOT NULL,
    model          TEXT NOT NULL,          -- 站内模型名
    upstream_model TEXT NOT NULL,
    baseline       TEXT NOT NULL,          -- 对照的官方型号
    preset         TEXT NOT NULL,          -- quick / full
    status         TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed')),
    score          INT,
    verdict        TEXT NOT NULL DEFAULT '',
    cost           NUMERIC(20,10) NOT NULL DEFAULT 0, -- 按站内价估算的花费（检测不扣用户余额）
    report         JSONB
);
CREATE INDEX probe_runs_created_idx ON probe_runs (created_at DESC);
