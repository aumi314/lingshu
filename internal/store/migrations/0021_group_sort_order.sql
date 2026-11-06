-- 服务分组的手动排序（2026-11-05），管理页和成员的「模型与价格」、建 key 时都按这个顺序显示，和倍率、路由无关。
ALTER TABLE groups ADD COLUMN sort_order INT NOT NULL DEFAULT 0;
-- 老分组按原来管理页的显示顺序（id）
UPDATE groups g SET sort_order = o.n FROM (
    SELECT id, row_number() OVER (ORDER BY id) AS n FROM groups
) o WHERE o.id = g.id;
