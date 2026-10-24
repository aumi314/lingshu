-- 支付配置版本不可变，旧订单始终使用下单时的凭据和报价。
CREATE TABLE payment_config_versions (
    id BIGSERIAL PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('epay','stripe','creem','waffo','waffo_pancake')),
    config JSONB NOT NULL,
    secrets JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE payment_services (
    provider TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    version_id BIGINT NOT NULL REFERENCES payment_config_versions(id)
);
CREATE TABLE topup_orders (
    id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    provider TEXT NOT NULL,
    version_id BIGINT NOT NULL REFERENCES payment_config_versions(id),
    environment TEXT NOT NULL CHECK (environment IN ('test','prod')),
    method TEXT NOT NULL DEFAULT '',
    credit_usd NUMERIC(20,10) NOT NULL CHECK (credit_usd > 0),
    amount NUMERIC(20,2) NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL CHECK (currency IN ('USD','CNY')),
    unit_price NUMERIC(20,10) NOT NULL CHECK (unit_price > 0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','success','failed','expired')),
    request_key TEXT NOT NULL,
    checkout_started BOOLEAN NOT NULL DEFAULT FALSE,
    checkout_data JSONB,
    checkout_id TEXT NOT NULL DEFAULT '',
    transaction_id TEXT NOT NULL DEFAULT '',
    tax NUMERIC(20,2) NOT NULL DEFAULT 0,
    total NUMERIC(20,2) NOT NULL DEFAULT 0,
    admin_test BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE(user_id,request_key)
);
CREATE INDEX topup_orders_user_created_idx ON topup_orders(user_id,created_at DESC);
CREATE UNIQUE INDEX topup_orders_transaction_idx ON topup_orders(provider,environment,transaction_id) WHERE transaction_id <> '';
ALTER TABLE balance_adjustments ADD COLUMN topup_order_id TEXT REFERENCES topup_orders(id);
CREATE UNIQUE INDEX balance_adjustments_topup_idx ON balance_adjustments(topup_order_id) WHERE topup_order_id IS NOT NULL;
