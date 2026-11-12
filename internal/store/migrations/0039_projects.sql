-- 项目额度（docs/decisions.md「项目额度」）：创建人从自己余额转入项目池，成员用项目 Key 调用时从池里扣。
-- 组件开关在 settings.projects_enabled，默认关闭
ALTER TABLE users ADD COLUMN can_create_projects BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE projects (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    invite_code  TEXT NOT NULL UNIQUE,              -- 随机邀请码，可重置
    owner_id     BIGINT NOT NULL REFERENCES users(id),
    balance      NUMERIC(20,10) NOT NULL DEFAULT 0, -- 池子余额
    reserved     NUMERIC(20,10) NOT NULL DEFAULT 0, -- 进行中请求冻结的额度
    weekly_limit NUMERIC(20,10),                    -- 整个项目每周最多花多少，NULL 表示不限（北京时间周一 0 点重置）
    frozen       BOOLEAN NOT NULL DEFAULT false,    -- 管理员冻结
    closed_at    TIMESTAMPTZ,                       -- 解散时间，解散后不再能用
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (weekly_limit IS NULL OR weekly_limit > 0)
);
CREATE INDEX projects_owner ON projects (owner_id);

-- 项目开放的服务分组（只能是创建人自己有权用的）
CREATE TABLE project_groups (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, group_id)
);

-- 成员：申请后 pending，创建人通过后 active；创建人建项目时自动是成员
CREATE TABLE project_members (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status     TEXT NOT NULL CHECK (status IN ('pending', 'active')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user ON project_members (user_id);

-- 项目 Key：调用从项目池扣；NULL 是个人 Key
ALTER TABLE api_keys ADD COLUMN project_id BIGINT REFERENCES projects(id);
-- 调用日志的项目快照，用来按项目、按成员统计每周用量
ALTER TABLE request_logs ADD COLUMN project_id BIGINT;
CREATE INDEX request_logs_project_created ON request_logs (project_id, created_at) WHERE project_id IS NOT NULL;
-- 转入、取回、解散退回在创建人的余额流水里记一笔，标明是哪个项目
ALTER TABLE balance_adjustments ADD COLUMN project_id BIGINT REFERENCES projects(id);
