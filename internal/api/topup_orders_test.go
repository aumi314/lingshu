package api

import (
	"context"
	"testing"
)

// 充值订单列表（全站日志的「充值订单」标签）：管理员可按用户、状态、时间筛选；成员只能看自己的。
func TestTopupOrderFilters(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{
		`INSERT INTO payment_config_versions(id,provider,config,secrets) VALUES(1,'epay','{}','{}')`,
		`INSERT INTO topup_orders(id,user_id,provider,version_id,environment,credit_usd,amount,currency,unit_price,status,request_key,method,tax,total,created_at) VALUES
			('bob-paid',2,'epay',1,'prod',1,6.7,'CNY',6.7,'success','k1','alipay',0,6.7,'2026-11-01T00:00:00Z'),
			('bob-pending',2,'epay',1,'prod',2,13.4,'CNY',6.7,'pending','k2','alipay',0,0,'2026-11-03T00:00:00Z'),
			('admin-paid',1,'epay',1,'prod',1,6.7,'CNY',6.7,'success','k3','alipay',0,6.7,'2026-11-02T00:00:00Z')`,
	} {
		if _, err := e.store.Pool.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	a, bob := e.login("admin"), e.login("bob")
	type list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	ids := func(c *client, path string) (int, []string) {
		var l list
		if code, b := c.do("GET", path, nil, &l); code != 200 {
			t.Fatalf("%s: %d %s", path, code, b)
		}
		out := []string{}
		for _, o := range l.Items {
			out = append(out, o.ID)
		}
		return l.Total, out
	}
	if total, got := ids(a, "/api/admin/topups"); total != 3 || got[0] != "bob-pending" {
		t.Fatalf("管理员看全部、按时间倒序: %d %v", total, got)
	}
	if total, got := ids(a, "/api/admin/topups?user_id=2&status=success"); total != 1 || got[0] != "bob-paid" {
		t.Fatalf("按用户和状态筛: %d %v", total, got)
	}
	if total, _ := ids(a, "/api/admin/topups?from=2026-11-02T00:00:00Z&to=2026-11-03T00:00:00Z"); total != 1 {
		t.Fatalf("按时间筛: %d", total)
	}
	if total, got := ids(bob, "/api/me/topups?user_id=1"); total != 2 || got[0] != "bob-pending" {
		t.Fatalf("成员只能看自己的: %d %v", total, got)
	}
	if code, _ := a.do("GET", "/api/admin/topups?status=bogus", nil, nil); code != 400 {
		t.Fatalf("非法状态: %d", code)
	}
}
