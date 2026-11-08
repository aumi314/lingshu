-- Responses 入口转接到 Messages 上游后，日志要能看出实际走的协议和这次请求的用途。
-- upstream_protocol：实际发给上游的协议，老日志按当时的入口协议回填（那时没有转接）；
-- operation：generation 正常生成、compaction 上下文压缩；
-- conversion_diagnostics：协议转接的诊断（数组），不记请求体和密钥。
ALTER TABLE request_logs ADD COLUMN upstream_protocol TEXT;
UPDATE request_logs SET upstream_protocol = protocol WHERE upstream_protocol IS NULL;
ALTER TABLE request_logs ALTER COLUMN upstream_protocol SET DEFAULT 'chat';
ALTER TABLE request_logs ALTER COLUMN upstream_protocol SET NOT NULL;

ALTER TABLE request_logs ADD COLUMN operation TEXT NOT NULL DEFAULT 'generation';

ALTER TABLE request_logs ADD COLUMN conversion_diagnostics JSONB NOT NULL DEFAULT '[]'::jsonb;
