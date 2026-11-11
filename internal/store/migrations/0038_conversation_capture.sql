-- 对话留存（docs/decisions.md「对话留存」）：请求里的消息、system、tools 按内容去重存成块（gzip），
-- 每次调用只存骨架（其余字段 + 块的哈希顺序）和客户端收到的回复。开关在 settings.capture_conversations，默认关闭
CREATE TABLE conversation_blobs (
    hash       BYTEA PRIMARY KEY,        -- 原文的 SHA-256
    data       BYTEA NOT NULL,           -- gzip 压缩后的原文
    size       INT NOT NULL,             -- 原文字节数
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE request_captures (
    request_id         TEXT PRIMARY KEY REFERENCES request_logs(request_id) ON DELETE CASCADE,
    session            TEXT NOT NULL DEFAULT '',   -- 客户端的会话编号，没有为空
    skeleton           BYTEA NOT NULL,             -- gzip 的请求骨架
    request_bytes      INT NOT NULL,               -- 原请求体字节数
    response           BYTEA NOT NULL,             -- gzip 的回复原文
    response_bytes     INT NOT NULL,               -- 回复原文字节数（截断后）
    response_truncated BOOLEAN NOT NULL DEFAULT false,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX request_captures_session ON request_captures (session, created_at) WHERE session <> '';
