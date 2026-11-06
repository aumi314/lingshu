-- 渠道连接状态（2026-11-06）：一次请求在哪些渠道上失败过（按尝试顺序，含所有可换渠道重试的失败）。
-- 换渠道后成功的请求，日志的 channel_id 只记最后成功的渠道，前面失败的渠道靠这一列计入失败。旧日志为空数组。
ALTER TABLE request_logs ADD COLUMN failed_channel_ids BIGINT[] NOT NULL DEFAULT '{}';
