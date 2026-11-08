-- 服务分组的失败重试次数（2026-11-07，docs/decisions.md「路由与重试」）：只计渠道级失败（5xx、超时、断流），
-- 换 key 不占次数。默认 2，等于原来固定的“最多试 3 次”，上线后行为不变。
ALTER TABLE groups ADD COLUMN retry_count INT NOT NULL DEFAULT 2 CHECK (retry_count BETWEEN 0 AND 4);
