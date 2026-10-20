-- 日志里只存 id 的话，渠道或 key 被删除后历史记录就看不出是谁；和价格一样，写入时存一份名字快照。
ALTER TABLE request_logs ADD COLUMN channel_name TEXT NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN key_name TEXT NOT NULL DEFAULT '';

-- 老日志：渠道和 key 还在的，补上当前的名字
UPDATE request_logs l SET channel_name = c.name FROM channels c WHERE c.id = l.channel_id AND l.channel_name = '';
UPDATE request_logs l SET key_name = k.name FROM api_keys k WHERE k.id = l.api_key_id AND l.key_name = '';
