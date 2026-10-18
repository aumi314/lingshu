-- 网页登录会话：只存 token 的哈希
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

-- 用户偏好：language（zh/en）、currency（USD/CNY）、theme（system/light/dark）
ALTER TABLE users ADD COLUMN preferences JSONB NOT NULL DEFAULT '{}';
