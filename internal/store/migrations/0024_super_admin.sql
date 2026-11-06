-- 超级管理员（2026-11-06 决策）：比管理员只多一项“任免管理员”；管理员不能修改超级管理员的账号。
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('super_admin', 'admin', 'user'));
-- 已有部署：最早创建的启用中的管理员成为超级管理员（没有启用中的就取最早的管理员）
UPDATE users SET role = 'super_admin' WHERE id = (
    SELECT id FROM users WHERE role = 'admin' ORDER BY enabled DESC, id LIMIT 1
);
