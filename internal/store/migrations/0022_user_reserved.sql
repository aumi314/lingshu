-- 预扣费（2026-11-06 决策）：放行请求前按最大可能费用冻结额度，结算时解冻并按实际扣费。
-- 可用余额 = balance - reserved。进程启动时清零（单进程部署，进行中的请求已随进程结束）。
ALTER TABLE users ADD COLUMN reserved NUMERIC(20,10) NOT NULL DEFAULT 0;
