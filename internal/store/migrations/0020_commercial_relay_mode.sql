-- 2026-11-05 决策：现有功能（预付余额、在线充值、分组倍率计费）定位为“商业中转”模式；
-- 公司团队网关的专用功能另行开发。之前唯一可选的模式值改为商业中转。
INSERT INTO settings (key, value) VALUES ('deployment_mode', 'commercial_relay')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
