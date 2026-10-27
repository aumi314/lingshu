-- 取消上游余额估算，改为按需查询（2026-10-27 决策）。
-- 渠道上只保留一份"上次查询结果"，不再有账户、流水和逐请求成本。

ALTER TABLE channels
    ADD COLUMN balance_detail JSONB NOT NULL DEFAULT '[]'::jsonb, -- [{"currency":"CNY","amount":"9.00"}]
    ADD COLUMN balance_checked_at TIMESTAMPTZ,
    ADD COLUMN balance_error TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS request_logs_upstream_account;
ALTER TABLE request_logs DROP COLUMN upstream_account_id;
ALTER TABLE request_logs DROP COLUMN upstream_cost;

DROP INDEX IF EXISTS channels_upstream_account_unique;
ALTER TABLE channels DROP COLUMN upstream_account_id;

DROP TABLE upstream_account_entries;
DROP TABLE upstream_accounts;
