-- System One 使用独立地址；空值保持已有渠道的协议能力不变。
ALTER TABLE channels ADD COLUMN base_url_systemone TEXT NOT NULL DEFAULT '';
