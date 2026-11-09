-- 集合：一种不挂渠道的服务分组，包含若干普通分组。请求某个模型时按分组排序取第一个提供它的启用成员，
-- 走它的渠道、按它的倍率计费；启用、适用范围、失败重试、绑定 key 和普通分组一样。类型创建后不改。
ALTER TABLE groups ADD COLUMN is_collection BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE collection_groups (
    collection_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    group_id      BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (collection_id, group_id),
    CHECK (collection_id <> group_id)
);
CREATE INDEX collection_groups_group_id ON collection_groups (group_id);
