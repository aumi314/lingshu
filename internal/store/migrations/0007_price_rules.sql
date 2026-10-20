-- 价格规则（阶梯计价、分时段计价）：按顺序匹配，第一条满足的生效，都不满足用基础价。
-- 结构见 internal/billing/rules.go 的 PriceRule。
ALTER TABLE models ADD COLUMN price_rules JSONB NOT NULL DEFAULT '[]';
-- 请求命中的规则名快照，空表示基础价；input_price 等列记的是实际生效的单价
ALTER TABLE request_logs ADD COLUMN price_rule TEXT NOT NULL DEFAULT '';
