-- 请求日志补两项请求信息，之前的日志没有，保持空串：
-- client_ip：访问者 IP（Caddy 经 X-Real-IP 传入真实地址），便于排查异常调用、确认 key 是否外泄。
-- reasoning_effort：客户端要求的推理强度，原样记录（chat 的 reasoning_effort、responses 的 reasoning.effort、
-- messages 的 output_config.effort；只传 thinking 时记 thinking 类型或预算）。
ALTER TABLE request_logs ADD COLUMN client_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';
