-- 余额估算并入渠道页：一个渠道一把 key，对应唯一一个上游账户（2026-10-20 决定）。
-- 以后一个渠道支持多把 key 时，每把 key 各对应一个账户。
CREATE UNIQUE INDEX channels_upstream_account_unique ON channels (upstream_account_id) WHERE upstream_account_id IS NOT NULL;
-- 账户名不再单独展示（显示渠道名），不再要求唯一
ALTER TABLE upstream_accounts DROP CONSTRAINT upstream_accounts_name_key;
