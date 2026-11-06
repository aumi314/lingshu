-- 兑换码充值（2026-11-06 决策）：管理员生成带面额的一次性兑换码，用户兑换后余额到账。
-- 明文只在生成时返回，库里只存哈希；面额按美元存。
CREATE TABLE redeem_codes (
    id           BIGSERIAL PRIMARY KEY,
    code_hash    TEXT NOT NULL UNIQUE,
    code_prefix  TEXT NOT NULL,
    amount       NUMERIC(20,10) NOT NULL CHECK (amount > 0),
    note         TEXT NOT NULL DEFAULT '',
    expires_at   TIMESTAMPTZ,
    created_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_by      BIGINT REFERENCES users(id) ON DELETE SET NULL,
    used_at      TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX redeem_codes_created_at_idx ON redeem_codes (created_at DESC);
