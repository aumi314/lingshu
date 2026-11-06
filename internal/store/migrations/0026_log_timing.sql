-- 调用耗时拆分（之前的日志没有，保持 0）：
-- read_ms：读完客户端请求体用的时间（“读体”）。长对话每次都要上传整段上下文，用户网络上行慢时这段会变长。
-- upstream_start_ms：从进入网关到发出最终那次上游请求的时间（有换渠道时是最后一次尝试）。
-- 页面据此算：首字 = first_byte_ms − upstream_start_ms（从发给上游算起）；转发 = total_ms − upstream_start_ms；
-- 网关处理 = upstream_start_ms − read_ms（鉴权、选渠道、预扣、失败渠道的重试）。
ALTER TABLE request_logs ADD COLUMN read_ms INT NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN upstream_start_ms INT NOT NULL DEFAULT 0;
