-- 渠道多 key 与渠道备注（2026-11-07，docs/decisions.md「渠道多 key 与渠道备注」）。
-- 一个渠道可挂多把 key：同一会话固定用一把，429 / 失效只影响那一把。现有 key 搬成每个渠道的第一把。
-- channels.api_key 保留不删（删列算破坏性迁移，要暂停发布等确认），代码改读 channel_keys。
CREATE TABLE channel_keys (
    id          BIGSERIAL PRIMARY KEY,
    channel_id  BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    api_key     TEXT NOT NULL CHECK (api_key <> ''),
    enabled     BOOLEAN NOT NULL DEFAULT true,
    note        TEXT NOT NULL DEFAULT '',
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX channel_keys_channel ON channel_keys (channel_id, sort_order, id);
INSERT INTO channel_keys (channel_id, api_key) SELECT id, api_key FROM channels WHERE api_key <> '';

ALTER TABLE channels ADD COLUMN note TEXT NOT NULL DEFAULT '';

-- 最终用的 key、失败过的 key（按 key 统计成功率；换 key 后成功的请求也记下前面失败的 key）
ALTER TABLE request_logs ADD COLUMN channel_key_id BIGINT;
ALTER TABLE request_logs ADD COLUMN failed_key_ids BIGINT[] NOT NULL DEFAULT '{}';
