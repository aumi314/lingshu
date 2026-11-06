-- 充值并入操作日志（2026-11-06 决策）：用户发起在线充值记一条 topup.create（操作人是下单用户，对象是订单），
-- 订单当前状态（待付款 / 已到账 / 失败 / 已过期）在查询时按 order_id 实时关联；付款成功另有 topup.credit。
-- 回填已有订单。
INSERT INTO operation_logs (created_at, actor_id, actor_name, user_id, action, target_type, target_id, target_name, changes, order_id)
SELECT t.created_at, t.user_id, u.username, t.user_id, 'topup.create', 'topup', t.id, t.id,
       jsonb_build_array(
           jsonb_build_object('field', 'credit_usd', 'to', trim_scale(t.credit_usd)::text),
           jsonb_build_object('field', 'pay_amount', 'to', trim_scale(t.amount)::text || ' ' || t.currency),
           jsonb_build_object('field', 'provider', 'to', t.provider),
           jsonb_build_object('field', 'method', 'to', t.method)),
       t.id
FROM topup_orders t
JOIN users u ON u.id = t.user_id
ORDER BY t.created_at;
