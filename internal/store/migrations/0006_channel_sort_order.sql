-- 渠道列表的手动排序，只影响管理页显示，和路由优先级（priority）无关。
ALTER TABLE channels ADD COLUMN sort_order INT NOT NULL DEFAULT 0;
-- 老渠道按原来的显示顺序（优先级高的在前，同优先级按 id）
UPDATE channels c SET sort_order = o.n FROM (
    SELECT id, row_number() OVER (ORDER BY priority DESC, id) AS n FROM channels
) o WHERE o.id = c.id;
