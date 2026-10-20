-- 上游账户：在厂商那里的账号，用来估算上游余额。纯运维工具，和用户余额、扣费完全无关。
-- 金额统一 USD；人民币按系统汇率换算，原始输入记在流水里。
CREATE TABLE upstream_accounts (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    balance       NUMERIC(20,10) NOT NULL DEFAULT 0, -- 估算余额：每次请求按成本扣，校准时改成真实值
    cost_factor   NUMERIC(20,10) NOT NULL DEFAULT 1, -- 上游成本 = 模型站内基础价 × 系数
    alert_below   NUMERIC(20,10),                    -- 低于这个值提醒，空表示不提醒
    auto_sync     BOOLEAN NOT NULL DEFAULT FALSE,    -- 每小时用厂商的余额接口自动校准
    synced_at     TIMESTAMPTZ,
    sync_error    TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE channels ADD COLUMN upstream_account_id BIGINT REFERENCES upstream_accounts(id) ON DELETE SET NULL;

-- 流水：充值、手动校准、自动校准。消耗不逐条记（在 request_logs.upstream_cost 里）。
CREATE TABLE upstream_account_entries (
    id                BIGSERIAL PRIMARY KEY,
    account_id        BIGINT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL,                  -- recharge / calibrate / sync
    amount            NUMERIC(20,10) NOT NULL,        -- 余额变化（校准时 = 真实值 - 估算值，即估算偏差）
    balance_before    NUMERIC(20,10) NOT NULL,
    balance_after     NUMERIC(20,10) NOT NULL,
    original_amount   NUMERIC(20,10),                 -- 输入或接口返回的原始金额（充值额 / 真实余额）
    original_currency TEXT NOT NULL DEFAULT 'USD',
    operator_id       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    note              TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX upstream_account_entries_account ON upstream_account_entries (account_id, id DESC);

-- 每次请求的上游成本（按真实用量，和是否向用户收费无关）
ALTER TABLE request_logs ADD COLUMN upstream_cost NUMERIC(20,10) NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN upstream_account_id BIGINT;
CREATE INDEX request_logs_upstream_account ON request_logs (upstream_account_id, created_at) WHERE upstream_account_id IS NOT NULL;
