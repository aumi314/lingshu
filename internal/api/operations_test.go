package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

// 不写操作日志的写路由，每条都要写明原因。新增写路由时，要么在 TestOperationLogCoverage 里登记并验证，要么加到这里。
var operationExempt = map[string]string{
	"POST /api/auth/login":                                         "登录不记，失败已写服务日志",
	"POST /api/auth/challenge":                                     "人机验证",
	"POST /api/auth/logout":                                        "登出",
	"PUT /api/me/preferences":                                      "个人偏好",
	"POST /api/me/topups":                                          "未付款订单",
	"POST /api/me/topups/{order}/checkout":                         "未付款订单",
	"POST /api/admin/payments/{provider}/test-order":               "未付款订单",
	"POST /api/me/topups/{order}/refresh":                          "入账在 ApplyTopupEvent 里记，见 store.TestTopupCreditOperation",
	"POST /api/payments/{provider}/{version}/{environment}/notify": "入账在 ApplyTopupEvent 里记，见 store.TestTopupCreditOperation",
	"PUT /api/admin/groups/order":                                  "拖拽排序只影响显示顺序",
	"PUT /api/admin/channels/order":                                "拖拽排序只影响列表显示",
	"POST /api/admin/channels/fetch-models":                        "只读",
	"POST /api/admin/channels/probe":                               "冒烟测试",
	"POST /api/admin/channels/{id}/balance":                        "余额查询",
	"POST /api/admin/payments/{provider}/probe":                    "支付探测",
	"POST /api/admin/payments/waffo_pancake/catalog":               "只读外部目录",
	"POST /api/admin/payments/waffo_pancake/create-product":        "只在外部建商品，本地不存",
}

type opRow struct {
	Action     string
	TargetType string
	TargetName string
	ActorID    *int64
	UserID     *int64
	Changes    []store.Change
}

func (e *env) opCount() int {
	e.t.Helper()
	var n int
	if err := e.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) lastOp() opRow {
	e.t.Helper()
	var o opRow
	var changes []byte
	if err := e.store.Pool.QueryRow(context.Background(), `SELECT action, target_type, target_name, actor_id, user_id, changes
		FROM operation_logs ORDER BY id DESC LIMIT 1`).Scan(&o.Action, &o.TargetType, &o.TargetName, &o.ActorID, &o.UserID, &changes); err != nil {
		e.t.Fatal(err)
	}
	if err := json.Unmarshal(changes, &o.Changes); err != nil {
		e.t.Fatal(err)
	}
	return o
}

func (o opRow) change(field string) *store.Change {
	for i := range o.Changes {
		if o.Changes[i].Field == field {
			return &o.Changes[i]
		}
	}
	return nil
}

// writeRoutes 从源码里扫出所有注册的写路由。
func writeRoutes(t *testing.T) map[string]bool {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`HandleFunc\("((?:POST|PUT|PATCH|DELETE) [^"]+)"`)
	routes := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			routes[m[1]] = true
		}
	}
	if len(routes) < 30 {
		t.Fatalf("只扫到 %d 条写路由，扫描方式可能失效", len(routes))
	}
	return routes
}

func TestOperationLogCoverage(t *testing.T) {
	e := newEnv(t)
	e.api.PublicURL = "https://gateway.example"
	a, bob := e.login("admin"), e.login("bob")
	covered := map[string]bool{}
	step := func(route string, c *client, path string, in any, out any, want string) opRow {
		t.Helper()
		before := e.opCount()
		method := strings.SplitN(route, " ", 2)[0]
		if code, body := c.do(method, path, in, out); code >= 300 {
			t.Fatalf("%s: %d %s", route, code, body)
		}
		if got := e.opCount(); got != before+1 {
			t.Fatalf("%s 应写 1 条操作日志，实际 %d 条", route, got-before)
		}
		o := e.lastOp()
		if o.Action != want {
			t.Fatalf("%s 动作应为 %s，实际 %s", route, want, o.Action)
		}
		covered[route] = true
		return o
	}
	model := map[string]any{"name": "m1", "input_price": "0.3", "output_price": "1.2", "cache_read_price": "0", "cache_write_price": "0"}
	channel := map[string]any{"name": "c1", "template": "custom", "api_key": "sk-secret-1", "enabled": true,
		"base_url_chat": "https://up.example/v1", "group_ids": []int64{1}, "models": []map[string]any{{"model_id": 1, "model_name": "m1"}}}

	o := step("POST /api/admin/users", a, "/api/admin/users", map[string]string{"username": "carol", "password": "carol-password-1", "role": "user"}, nil, "user.create")
	if o.TargetName != "carol" || o.change("password") == nil || !o.change("password").Secret || o.change("password").To != nil {
		t.Fatalf("建用户记录不对: %+v", o)
	}
	step("PATCH /api/admin/users/{id}", a, "/api/admin/users/2", map[string]any{"note": "internal note"}, nil, "user.update")
	o = step("POST /api/admin/users/{id}/balance", a, "/api/admin/users/2/balance", map[string]any{"mode": "add", "amount": "10", "note": "月度额度"}, nil, "balance.adjust")
	if *o.ActorID != 1 || *o.UserID != 2 {
		t.Fatalf("余额调整的操作人和受影响用户不对: %+v", o)
	}
	step("PUT /api/admin/users/{id}/user-groups", a, "/api/admin/users/2/user-groups", map[string]any{"user_group_ids": []int64{2}}, nil, "user.update")
	step("POST /api/admin/user-groups", a, "/api/admin/user-groups", map[string]string{"name": "team-a"}, nil, "user_group.create")
	step("PUT /api/admin/user-groups/{id}", a, "/api/admin/user-groups/3", map[string]string{"name": "team-b"}, nil, "user_group.update")
	step("DELETE /api/admin/user-groups/{id}", a, "/api/admin/user-groups/3", nil, nil, "user_group.delete")
	step("POST /api/admin/groups", a, "/api/admin/groups", map[string]any{"name": "g3", "ratio": "2", "enabled": true, "is_public": true}, nil, "group.create")
	o = step("PUT /api/admin/groups/{id}", a, "/api/admin/groups/3", map[string]any{"name": "g3", "ratio": "1.5", "enabled": true, "is_public": true}, nil, "group.update")
	if len(o.Changes) != 1 || o.Changes[0].Field != "ratio" {
		t.Fatalf("只改倍率应只记倍率: %+v", o.Changes)
	}
	step("POST /api/admin/models", a, "/api/admin/models", model, nil, "model.create")
	model["input_price"] = "0.15"
	o = step("PUT /api/admin/models/{id}", a, "/api/admin/models/1", model, nil, "model.update")
	if c := o.change("input_price"); len(o.Changes) != 1 || c == nil || !strings.Contains(string(c.From), "0.3") || !strings.Contains(string(c.To), "0.15") {
		t.Fatalf("改价记录不对: %+v", o.Changes)
	}
	step("POST /api/admin/channels", a, "/api/admin/channels", channel, nil, "channel.create")
	channel["api_key"], channel["enabled"] = "sk-secret-2", false
	o = step("PUT /api/admin/channels/{id}", a, "/api/admin/channels/1", channel, nil, "channel.update")
	if c := o.change("api_key"); c == nil || !c.Secret || c.From != nil || c.To != nil || o.change("enabled") == nil || len(o.Changes) != 2 {
		t.Fatalf("渠道改 key 应只标记已更换: %+v", o.Changes)
	}
	step("DELETE /api/admin/channels/{id}", a, "/api/admin/channels/1", nil, nil, "channel.delete")
	o = step("DELETE /api/admin/models/{id}", a, "/api/admin/models/1", nil, nil, "model.delete")
	if o.TargetName != "m1" || o.change("name") == nil || o.change("name").To != nil {
		t.Fatalf("删除应记下删掉的是什么: %+v", o)
	}
	step("DELETE /api/admin/groups/{id}", a, "/api/admin/groups/3", nil, nil, "group.delete")
	o = step("PUT /api/admin/settings", a, "/api/admin/settings", map[string]any{"cny_per_usd": "7.1", "registration_mode": "code"}, nil, "settings.update")
	if len(o.Changes) != 2 {
		t.Fatalf("一次保存多项设置应记一条、两项变更: %+v", o.Changes)
	}
	o = step("PUT /api/admin/payments/{provider}", a, "/api/admin/payments/epay", map[string]any{"enabled": true,
		"values": map[string]string{"merchant_id": "100", "gateway": "https://pay.example"}, "secrets": map[string]string{"key": "payment-fixture-secret"}}, nil, "payment.create")
	if c := o.change("secrets.key"); c == nil || !c.Secret {
		t.Fatalf("支付密钥应只标记: %+v", o.Changes)
	}
	var codes []store.NewRegistrationCode
	step("POST /api/admin/registration-codes", a, "/api/admin/registration-codes", map[string]any{"count": 2}, &codes, "registration_code.create")
	step("DELETE /api/admin/registration-codes/{id}", a, "/api/admin/registration-codes/"+itoa(codes[0].ID), nil, nil, "registration_code.update")
	o = step("POST /api/auth/register", e.client(), "/api/auth/register", map[string]string{"username": "invitee", "password": "a-secure-password", "code": codes[1].Code}, nil, "user.create")
	if o.ActorID == nil || o.UserID == nil || *o.ActorID != *o.UserID {
		t.Fatalf("自助注册的操作人应是新用户自己: %+v", o)
	}
	var key struct {
		ID int64 `json:"id"`
	}
	step("POST /api/me/keys", bob, "/api/me/keys", map[string]any{"name": "laptop", "group_id": 1, "quota_limit": "5"}, &key, "api_key.create")
	o = step("PATCH /api/me/keys/{id}", bob, "/api/me/keys/"+itoa(key.ID), map[string]any{"name": "laptop-2", "quota_limit": nil}, nil, "api_key.update")
	if len(o.Changes) != 2 || o.change("name") == nil || o.change("quota_limit") == nil || *o.UserID != 2 {
		t.Fatalf("改名和限额应在同一条记录里: %+v", o)
	}
	step("DELETE /api/me/keys/{id}", bob, "/api/me/keys/"+itoa(key.ID), nil, nil, "api_key.delete")
	step("PUT /api/me/password", bob, "/api/me/password", map[string]string{"old_password": "password-1", "new_password": "new-password-123"}, nil, "user.update")

	for route := range writeRoutes(t) {
		_, exempt := operationExempt[route]
		if covered[route] && exempt {
			t.Errorf("%s 同时登记为记录和豁免", route)
		}
		if !covered[route] && !exempt {
			t.Errorf("%s 没有登记：要么写操作日志并加到本测试，要么在 operationExempt 写明原因", route)
		}
	}
	routes := writeRoutes(t)
	for route := range operationExempt {
		if !routes[route] {
			t.Errorf("豁免清单里的 %s 已不存在，删掉这条", route)
		}
	}
}

func TestOperationLogRulesAndVisibility(t *testing.T) {
	e := newEnv(t)
	a, bob := e.login("admin"), e.login("bob")
	model := map[string]any{"name": "m1", "input_price": "0.3", "output_price": "1.2", "cache_read_price": "0", "cache_write_price": "0"}
	a.do("POST", "/api/admin/models", model, nil)
	n := e.opCount()
	// 没有变化的保存不记
	if code, body := a.do("PUT", "/api/admin/models/1", model, nil); code != 200 || e.opCount() != n {
		t.Fatalf("没有变化的保存不应记录: %d %s", code, body)
	}
	// 失败的操作随事务回滚，不留记录
	bob.do("POST", "/api/me/keys", map[string]any{"name": "k", "group_id": 1}, nil)
	n = e.opCount()
	if code, _ := a.do("DELETE", "/api/admin/groups/1", nil, nil); code < 400 || e.opCount() != n {
		t.Fatalf("删除失败不应留记录: %d", code)
	}
	a.do("PATCH", "/api/admin/users/2", map[string]any{"note": "internal note"}, nil)
	a.do("PATCH", "/api/admin/users/2", map[string]any{"note": "another note", "enabled": false}, nil)
	a.do("POST", "/api/admin/users/2/balance", map[string]any{"mode": "add", "amount": "1", "note": "补偿"}, nil)

	type resp struct {
		Logs    []store.OperationLog `json:"logs"`
		Total   int64                `json:"total"`
		Summary store.LogSummary     `json:"summary"`
	}
	var mine resp
	// 停用会踢掉会话，重新启用后再登录
	a.do("PATCH", "/api/admin/users/2", map[string]any{"enabled": true}, nil)
	bob = e.login("bob")
	code, body := bob.do("GET", "/api/me/logs?type=operation", nil, &mine)
	if code != 200 || strings.Contains(body, "internal note") || strings.Contains(body, "another note") || strings.Contains(body, `"field":"note"`) || strings.Contains(body, "m1") {
		t.Fatalf("成员只看自己的记录且看不到备注: %d %s", code, body)
	}
	actions := map[string]int{}
	for _, l := range mine.Logs {
		actions[l.Action]++
		if l.UserID == nil || *l.UserID != 2 {
			t.Fatalf("成员看到了别人的记录: %+v", l)
		}
	}
	// 只改备注的那条整条隐藏；备注加停用的那条保留停用；启用一条；Key 创建一条；余额调整一条
	if actions["user.update"] != 2 || actions["api_key.create"] != 1 || actions["balance.adjust"] != 1 || mine.Total != 4 || mine.Summary.AdjustmentNet.String() != "1" {
		t.Fatalf("成员视图不对: %+v total=%d summary=%+v", actions, mine.Total, mine.Summary)
	}
	var all resp
	if code, body := a.do("GET", "/api/admin/logs?type=operation&target=model", nil, &all); code != 200 || all.Total != 1 || all.Logs[0].Action != "model.create" {
		t.Fatalf("按对象类型筛选: %d %s", code, body)
	}
	if code, body := a.do("GET", "/api/admin/logs?type=operation&user_id=2", nil, &all); code != 200 || !strings.Contains(body, "internal note") {
		t.Fatalf("管理员能看到备注变更: %d %s", code, body)
	}
	if code, _ := a.do("GET", "/api/admin/logs?type=operation&target=unknown", nil, nil); code != 400 {
		t.Fatalf("非法对象类型: %d", code)
	}
}
