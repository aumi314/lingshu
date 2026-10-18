-- 金额单位 USD，NUMERIC(20,10)；价格单位：每百万 token 的 USD。

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    balance       NUMERIC(20,10) NOT NULL DEFAULT 0,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE groups (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    ratio       NUMERIC(20,10) NOT NULL DEFAULT 1 CHECK (ratio >= 0),
    is_public   BOOLEAN NOT NULL DEFAULT TRUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 特殊分组的授权
CREATE TABLE user_groups (
    user_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

-- 站内标准模型，价格只挂在这里
CREATE TABLE models (
    id                BIGSERIAL PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    input_price       NUMERIC(20,10) NOT NULL DEFAULT 0,
    output_price      NUMERIC(20,10) NOT NULL DEFAULT 0,
    cache_read_price  NUMERIC(20,10) NOT NULL DEFAULT 0,
    cache_write_price NUMERIC(20,10) NOT NULL DEFAULT 0,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 填了哪个协议的地址，就算支持哪个协议
CREATE TABLE channels (
    id                 BIGSERIAL PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,
    template           TEXT NOT NULL DEFAULT '',
    api_key            TEXT NOT NULL,
    priority           INT NOT NULL DEFAULT 0,
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    base_url_chat      TEXT NOT NULL DEFAULT '',
    base_url_messages  TEXT NOT NULL DEFAULT '',
    base_url_responses TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE channel_groups (
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (channel_id, group_id)
);

-- upstream_name 为空表示和标准名相同
CREATE TABLE channel_models (
    channel_id    BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    model_id      BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    upstream_name TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (channel_id, model_id)
);

-- 只存哈希，完整 key 创建时只显示一次
CREATE TABLE api_keys (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    key_prefix   TEXT NOT NULL,
    group_id     BIGINT NOT NULL REFERENCES groups(id),
    quota_limit  NUMERIC(20,10),
    quota_used   NUMERIC(20,10) NOT NULL DEFAULT 0,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);

-- 一个客户端请求一行
CREATE TABLE request_logs (
    id                   BIGSERIAL PRIMARY KEY,
    request_id           TEXT NOT NULL UNIQUE,
    user_id              BIGINT NOT NULL REFERENCES users(id),
    api_key_id           BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    group_id             BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    protocol             TEXT NOT NULL,
    model                TEXT NOT NULL,
    upstream_model       TEXT NOT NULL DEFAULT '',
    channel_id           BIGINT REFERENCES channels(id) ON DELETE SET NULL,
    stream               BOOLEAN NOT NULL DEFAULT FALSE,
    status               TEXT NOT NULL CHECK (status IN ('success', 'client_cancelled', 'upstream_error', 'rejected')),
    http_status          INT NOT NULL DEFAULT 0,
    error                TEXT NOT NULL DEFAULT '',
    input_tokens         BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens    BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens   BIGINT NOT NULL DEFAULT 0,
    output_tokens        BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens     BIGINT NOT NULL DEFAULT 0,
    usage_estimated      BOOLEAN NOT NULL DEFAULT FALSE,
    input_price          NUMERIC(20,10) NOT NULL DEFAULT 0,
    output_price         NUMERIC(20,10) NOT NULL DEFAULT 0,
    cache_read_price     NUMERIC(20,10) NOT NULL DEFAULT 0,
    cache_write_price    NUMERIC(20,10) NOT NULL DEFAULT 0,
    group_ratio          NUMERIC(20,10) NOT NULL DEFAULT 1,
    cost                 NUMERIC(20,10) NOT NULL DEFAULT 0,
    attempts             INT NOT NULL DEFAULT 0,
    first_byte_ms        INT NOT NULL DEFAULT 0,
    total_ms             INT NOT NULL DEFAULT 0,
    upstream_response_id TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX request_logs_user_created_idx ON request_logs (user_id, created_at DESC);
CREATE INDEX request_logs_created_idx ON request_logs (created_at DESC);
CREATE INDEX request_logs_upstream_response_id_idx ON request_logs (upstream_response_id) WHERE upstream_response_id <> '';

-- 管理员改余额的流水
CREATE TABLE balance_adjustments (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount        NUMERIC(20,10) NOT NULL,
    balance_after NUMERIC(20,10) NOT NULL,
    operator_id   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    note          TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO settings (key, value) VALUES ('cny_per_usd', '6.7');
