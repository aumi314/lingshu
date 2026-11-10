-- 留空表示永不过期，已有 key 继续永不过期。
ALTER TABLE api_keys ADD COLUMN expires_at TIMESTAMPTZ;
