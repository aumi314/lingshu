-- 管理员给账号加内部备注（2026-11-01 决策）。
-- 只有管理端用户列表带出这个字段，不进 /api/me，成员看不到。
ALTER TABLE users ADD COLUMN note TEXT NOT NULL DEFAULT '';
