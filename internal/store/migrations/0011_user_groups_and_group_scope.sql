ALTER TABLE groups ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE user_groups RENAME TO legacy_user_group_grants;
ALTER INDEX user_groups_pkey RENAME TO legacy_user_group_grants_pkey;

CREATE TABLE user_groups (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX user_groups_single_default_idx ON user_groups (is_default) WHERE is_default;

CREATE TABLE user_group_members (
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_group_id BIGINT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, user_group_id)
);

CREATE TABLE group_user_groups (
    group_id      BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_group_id BIGINT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_group_id)
);

INSERT INTO user_groups (name, is_default) VALUES ('default', TRUE);

INSERT INTO user_group_members (user_id, user_group_id)
SELECT u.id, ug.id
FROM users u CROSS JOIN user_groups ug
WHERE ug.is_default;

-- Preserve existing per-user special-group access by grouping users with
-- identical grants into a generated user group.
WITH access_sets AS (
    SELECT user_id, array_agg(group_id ORDER BY group_id) AS group_ids
    FROM legacy_user_group_grants
    GROUP BY user_id
)
INSERT INTO user_groups (name)
SELECT DISTINCT 'migrated-access-' || md5(group_ids::text)
FROM access_sets;

WITH access_sets AS (
    SELECT user_id, array_agg(group_id ORDER BY group_id) AS group_ids
    FROM legacy_user_group_grants
    GROUP BY user_id
)
INSERT INTO user_group_members (user_id, user_group_id)
SELECT a.user_id, ug.id
FROM access_sets a
JOIN user_groups ug ON ug.name = 'migrated-access-' || md5(a.group_ids::text);

WITH access_sets AS (
    SELECT user_id, array_agg(group_id ORDER BY group_id) AS group_ids
    FROM legacy_user_group_grants
    GROUP BY user_id
)
INSERT INTO group_user_groups (group_id, user_group_id)
SELECT DISTINCT old.group_id, ug.id
FROM legacy_user_group_grants old
JOIN access_sets a ON a.user_id = old.user_id
JOIN user_groups ug ON ug.name = 'migrated-access-' || md5(a.group_ids::text);

DROP TABLE legacy_user_group_grants;
