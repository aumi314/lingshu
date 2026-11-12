-- 项目额度上限的周期由创建人选：按天、按周、按月（2026-11-12 用户要求，原来固定按周）。
-- 用新列 period_limit 代替 weekly_limit（旧列留着不用：发布失败自动换回旧版本时旧程序还能读），已有项目保持按周
ALTER TABLE projects ADD COLUMN period_limit NUMERIC(20,10) CHECK (period_limit IS NULL OR period_limit > 0);
ALTER TABLE projects ADD COLUMN limit_period TEXT NOT NULL DEFAULT 'week' CHECK (limit_period IN ('day', 'week', 'month'));
UPDATE projects SET period_limit = weekly_limit;
