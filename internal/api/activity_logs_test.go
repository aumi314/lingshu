package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

func TestActivityLogs(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	queries := []string{
		`INSERT INTO request_logs(id,request_id,user_id,protocol,model,upstream_model,status,cost,input_tokens,cache_read_tokens,output_tokens,created_at) VALUES(1,'req-bob',2,'chat','test-model','private-model','success',0.25,100,300,20,'2026-10-25T12:00:00Z'),(2,'req-admin',1,'chat','admin-model','','rejected',0,0,0,0,'2026-10-25T12:00:00Z')`,
		`INSERT INTO payment_config_versions(id,provider,config,secrets) VALUES(1,'epay','{}','{"merchant_key":"secret-test-value"}')`,
		`INSERT INTO topup_orders(id,user_id,provider,version_id,environment,credit_usd,amount,currency,unit_price,status,request_key,checkout_data,method,tax,total,completed_at) VALUES('paid-order',2,'epay',1,'prod',1,6.7,'CNY',6.7,'success','paid','{"token":"private-checkout"}','alipay',0,6.7,'2026-10-25T12:00:00Z'),('pending-order',2,'epay',1,'prod',1,6.7,'CNY',6.7,'pending','pending',NULL,'alipay',0,0,NULL),('sandbox-order',2,'epay',1,'test',1,6.7,'CNY',6.7,'success','sandbox',NULL,'alipay',0,6.7,'2026-10-25T12:00:00Z'),('failed-order',2,'epay',1,'prod',1,6.7,'CNY',6.7,'failed','failed',NULL,'alipay',0,0,NULL)`,
		`INSERT INTO balance_adjustments(id,user_id,amount,balance_after,operator_id,note,topup_order_id,created_at) VALUES(1,2,1,6,NULL,'online','paid-order','2026-10-25T12:00:00Z'),(2,2,2,8,1,'manual add',NULL,'2026-10-25T12:00:00Z'),(3,2,-0.5,7.5,1,'manual subtract',NULL,'2026-10-25T12:00:00Z'),(4,1,3,3,1,'admin only',NULL,'2026-10-25T12:00:00Z'),(5,2,0,7.5,NULL,'legacy zero',NULL,'2026-10-25T12:00:00Z'),(6,2,0.0000000001,7.5000000001,1,'tiny amount',NULL,'2026-10-25T12:00:00Z')`,
	}
	for _, q := range queries {
		if _, err := e.store.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	a := e.login("admin")
	bob := e.login("bob")
	type response struct {
		Logs    []store.ActivityLog `json:"logs"`
		Total   int64               `json:"total"`
		Summary store.LogSummary    `json:"summary"`
	}
	var result response
	code, body := a.do("GET", "/api/admin/logs?type=all&page_size=2", nil, &result)
	if code != 200 || result.Total != 8 || result.Summary.Requests != 2 || result.Summary.Cost.String() != "0.25" || result.Summary.TopupTotal.String() != "1" || result.Summary.AdjustmentNet.String() != "4.5000000001" || len(result.Logs) != 2 {
		t.Fatalf("合并统计或分页错误: %d %s", code, body)
	}
	keys := map[string]bool{}
	for page := 1; page <= 4; page++ {
		path := "/api/admin/logs?type=all&page_size=2&page=" + strconv.Itoa(page)
		if code, body := a.do("GET", path, nil, &result); code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		for _, entry := range result.Logs {
			if keys[entry.EventKey] {
				t.Fatalf("重复记录 %s", entry.EventKey)
			}
			keys[entry.EventKey] = true
		}
	}
	if len(keys) != 8 || !keys["request:1"] || !keys["balance:1"] {
		t.Fatalf("跨来源或跨页遗漏: %+v", keys)
	}
	code, body = bob.do("GET", "/api/me/logs?type=all&user_id=1&channel_id=99", nil, &result)
	if code != 200 || result.Total != 6 || result.Summary.Requests != 1 || result.Summary.InputTokens != 400 || result.Summary.CacheRead != 300 || result.Summary.OutputTokens != 20 || result.Summary.AdjustmentNet.String() != "1.5000000001" {
		t.Fatalf("用户隔离错误: %d %s", code, body)
	}
	if strings.Contains(body, "private-model") || strings.Contains(body, "secret-test-value") || strings.Contains(body, "private-checkout") || strings.Contains(body, "admin only") {
		t.Fatalf("泄露非用户数据: %s", body)
	}
	for _, entry := range result.Logs {
		if entry.UserID != 2 {
			t.Fatalf("读取到其他用户: %+v", entry)
		}
		if entry.Type == "topup" && (entry.Order == nil || entry.Order.ID != "paid-order" || entry.Order.Total.String() != "6.7" || entry.Operator != "") {
			t.Fatalf("充值摘要错误: %+v", entry)
		}
		if entry.Type == "request" && (entry.LogEntry == nil || entry.BalanceAfter != nil || entry.BalanceDelta.String() != "-0.25") {
			t.Fatalf("请求字段错误: %+v", entry)
		}
	}
	for _, test := range []struct {
		query           string
		total, requests int64
	}{
		{"type=topup", 1, 0}, {"type=adjustment", 4, 0}, {"type=request&model=test-model&status=success", 1, 1},
		{"type=request&model=missing", 0, 0}, {"type=all&from=2026-10-26T00:00:00Z", 0, 0},
		{"type=all&to=2026-10-25T12:00:00Z", 0, 0}, {"type=all&from=2026-10-25T12:00:00Z", 6, 1},
	} {
		if code, body := bob.do("GET", "/api/me/logs?"+test.query, nil, &result); code != 200 || result.Total != test.total || result.Summary.Requests != test.requests {
			t.Fatalf("筛选 %s: %d %s", test.query, code, body)
		}
	}
	if code, _ := a.do("GET", "/api/admin/logs?type=unknown", nil, nil); code != 400 {
		t.Fatalf("非法类型: %d", code)
	}
	if code, _ := bob.do("GET", "/api/admin/logs?type=all", nil, nil); code != 403 {
		t.Fatalf("管理员权限: %d", code)
	}
	var legacy struct {
		Logs    []json.RawMessage `json:"logs"`
		Summary store.LogSummary  `json:"summary"`
	}
	if code, body := bob.do("GET", "/api/me/logs", nil, &legacy); code != 200 || len(legacy.Logs) != 1 || legacy.Summary.Requests != 1 {
		t.Fatalf("旧接口兼容: %d %s", code, body)
	}
}
