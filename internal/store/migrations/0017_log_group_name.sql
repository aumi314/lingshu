-- 服务分组被删除后 group_id 会置空，历史日志就看不出当时用的哪个分组；和渠道、key 一样存名字快照。
ALTER TABLE request_logs ADD COLUMN group_name TEXT NOT NULL DEFAULT '';

-- 老日志：分组还在的，补上当前的名字
UPDATE request_logs l SET group_name = g.name FROM groups g WHERE g.id = l.group_id AND l.group_name = '';
