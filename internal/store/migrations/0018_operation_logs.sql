-- 操作日志（2026-11-04 决策）：一次状态变更一行，与业务改动在同一事务写入。
-- actor_id 为空表示系统动作（如充值到账）；user_id 是受影响的用户，成员据此看到与自己有关的记录。
CREATE TABLE operation_logs (
    id          BIGSERIAL PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    actor_name  TEXT NOT NULL DEFAULT '',
    user_id     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   TEXT NOT NULL DEFAULT '',
    target_name TEXT NOT NULL DEFAULT '',
    changes     JSONB NOT NULL DEFAULT '[]',
    note        TEXT NOT NULL DEFAULT '',
    amount      NUMERIC(20,10),
    order_id    TEXT REFERENCES topup_orders(id)
);
CREATE INDEX operation_logs_created_idx ON operation_logs (created_at DESC, id DESC);
CREATE INDEX operation_logs_user_idx ON operation_logs (user_id, created_at DESC);
CREATE INDEX operation_logs_actor_idx ON operation_logs (actor_id, created_at DESC);

-- 历史余额流水回填：余额调整和充值到账是上线前唯一留有记录的操作
INSERT INTO operation_logs (created_at, actor_id, actor_name, user_id, action, target_type, target_id, target_name, changes, note, amount, order_id)
SELECT b.created_at, b.operator_id, COALESCE(o.username, ''), b.user_id,
       CASE WHEN b.topup_order_id IS NULL THEN 'balance.adjust' ELSE 'topup.credit' END,
       'balance', b.user_id::text, u.username,
       jsonb_build_array(jsonb_build_object('field', 'balance', 'from', trim_scale(b.balance_after - b.amount)::text, 'to', trim_scale(b.balance_after)::text)),
       b.note, b.amount, b.topup_order_id
FROM balance_adjustments b
JOIN users u ON u.id = b.user_id
LEFT JOIN users o ON o.id = b.operator_id
ORDER BY b.id;
