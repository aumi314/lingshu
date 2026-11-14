-- 项目额度上限从“整个项目合计”改成创建人给每个成员单独设（2026-11-12 用户要求）：每人自己的周期和金额，NULL 不限。
-- 成员行也记进行中请求冻结的额度，预扣时和项目池一起锁住，并发请求冲不破个人上限。
-- projects.period_limit / limit_period 留着不用（发布失败自动换回旧版本时旧程序还能读）
ALTER TABLE project_members ADD COLUMN period_limit NUMERIC(20,10) CHECK (period_limit IS NULL OR period_limit > 0);
ALTER TABLE project_members ADD COLUMN limit_period TEXT NOT NULL DEFAULT 'week' CHECK (limit_period IN ('day', 'week', 'month'));
ALTER TABLE project_members ADD COLUMN reserved NUMERIC(20,10) NOT NULL DEFAULT 0;
