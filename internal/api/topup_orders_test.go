package api

import (
	"context"
	"strings"
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

// 充值并入操作日志（2026-11-06）：发起在线充值记一条“发起充值”，重复提交不重复记；订单状态实时显示；
// 成员能看到自己的，管理员能按“充值订单”筛选；全站订单列表接口已去掉。
func TestTopupOrdersInOperationLog(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.login("admin"), e.login("bob")
	configurePayment(t, e, admin, "epay")
	if _, err := e.store.Pool.Exec(context.Background(), `INSERT INTO settings(key,value) VALUES('payment_enabled','true') ON CONFLICT(key) DO UPDATE SET value='true'`); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"provider": "epay", "method": "alipay", "credit_usd": "2", "request_key": "fixture-order-0001"}
	var order struct {
		ID string `json:"id"`
	}
	if code, body := bob.do("POST", "/api/me/topups", in, &order); code != 200 {
		t.Fatal(code, body)
	}
	bob.do("POST", "/api/me/topups", in, nil) // 同一个 request_key 重复提交：返回原订单，不再记一条

	type resp struct {
		Logs  []store.OperationLog `json:"logs"`
		Total int64                `json:"total"`
	}
	var all resp
	if code, body := admin.do("GET", "/api/admin/logs?type=operation&target=topup", nil, &all); code != 200 || all.Total != 1 {
		t.Fatalf("管理员按充值订单筛选应只有 1 条: %d %s", code, body)
	}
	l := all.Logs[0]
	if l.Action != "topup.create" || l.Actor != "bob" || l.Order == nil || l.Order.ID != order.ID || l.Order.Status != "pending" || l.Order.Currency != "CNY" {
		t.Fatalf("发起充值记录不对: %+v %+v", l, l.Order)
	}
	changes := map[string]string{}
	for _, c := range l.Changes {
		changes[c.Field] = strings.Trim(string(c.To), `"`)
	}
	if changes["credit_usd"] != "2" || changes["pay_amount"] != "13.4 CNY" || changes["provider"] != "epay" || changes["method"] != "alipay" {
		t.Fatalf("订单摘要: %v", changes)
	}

	// 状态在查询时实时关联：订单失败后同一条记录显示失败
	e.store.Pool.Exec(context.Background(), `UPDATE topup_orders SET status = 'failed' WHERE id = $1`, order.ID)
	var mine resp
	if code, body := bob.do("GET", "/api/me/logs?type=operation&target=topup", nil, &mine); code != 200 || mine.Total != 1 || mine.Logs[0].Order.Status != "failed" {
		t.Fatalf("成员看自己的充值，状态实时: %d %s", code, body)
	}
	if code, _ := admin.do("GET", "/api/admin/topups", nil, nil); code != 404 && code != 405 {
		t.Fatalf("全站订单列表接口应已去掉: %d", code)
	}
	if code, _ := admin.do("GET", "/api/admin/logs?type=operation&target=redeem_code", nil, nil); code != 200 {
		t.Fatalf("按兑换码筛选: %d", code)
	}
}
