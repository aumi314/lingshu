-- 上游响应头到达的时刻（从请求进来算，毫秒；0 表示没收到）。用来定“等响应头多久算卡死”：
-- 正常上游回响应头很快、之后才慢慢推理，卡死的上游连响应头都不回（2026-11-11）
ALTER TABLE request_logs ADD COLUMN upstream_header_ms INT NOT NULL DEFAULT 0;
