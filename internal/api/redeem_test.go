package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/store"
)

func TestRedeemCodes(t *testing.T) {
	e := newEnv(t)
	a, bob := e.login("admin"), e.login("bob")
	var codes []store.NewRegistrationCode
	// 人民币面额按系统汇率（默认 6.7）换成美元：33.5 元 = $5
	if code, b := a.do("POST", "/api/admin/redeem-codes", map[string]any{"count": 4, "amount": "33.5", "currency": "CNY", "note": "活动"}, &codes); code != 201 || len(codes) != 4 {
		t.Fatalf("%d %s", code, b)
	}
	var list []store.RedeemCode
	a.do("GET", "/api/admin/redeem-codes", nil, &list)
	if len(list) != 4 || list[0].Amount.String() != "5" || list[0].Note != "活动" || strings.Contains(listJSON(t, a), codes[0].Code) {
		t.Fatalf("列表应有面额 $5、不含明文: %+v", list)
	}
	type redeemResp struct {
		Amount  string `json:"amount"`
		Balance string `json:"balance"`
	}
	var r redeemResp
	// 输入不区分大小写、可带空格
	if code, b := bob.do("POST", "/api/me/redeem", map[string]string{"code": " " + strings.ToLower(codes[0].Code) + " "}, &r); code != 200 || r.Amount != "5" || r.Balance != "10" {
		t.Fatalf("兑换应到账 $5、余额 10: %d %s", code, b)
	}
	if code, _ := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[0].Code}, nil); code != 400 {
		t.Fatalf("同一个码不能用两次: %d", code)
	}
	// 并发兑换同一个码：只有一次成功
	var wg sync.WaitGroup
	results := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[1].Code}, nil)
			results <- code
		}()
	}
	wg.Wait()
	close(results)
	okCount := 0
	for c := range results {
		if c == 200 {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("并发兑换应只有一次成功，实际 %d", okCount)
	}
	// 已用的不能撤销；撤销后不能兑换；过期的不能兑换
	if code, _ := a.do("DELETE", "/api/admin/redeem-codes/"+itoa(codes[0].ID), nil, nil); code != 409 {
		t.Fatalf("已用的码撤销应 409: %d", code)
	}
	if code, b := a.do("DELETE", "/api/admin/redeem-codes/"+itoa(codes[2].ID), nil, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[2].Code}, nil); code != 400 {
		t.Fatalf("撤销的码不能兑换: %d", code)
	}
	e.store.Pool.Exec(context.Background(), `UPDATE redeem_codes SET expires_at = now() - interval '1 minute' WHERE id = $1`, codes[3].ID)
	if code, _ := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[3].Code}, nil); code != 400 {
		t.Fatalf("过期的码不能兑换: %d", code)
	}
	// 成员能在自己的操作日志里看到兑换，计入充值到账
	var ops struct {
		Logs    []store.OperationLog `json:"logs"`
		Summary store.LogSummary     `json:"summary"`
	}
	bob.do("GET", "/api/me/logs?type=operation&target=balance", nil, &ops)
	if len(ops.Logs) != 2 || ops.Logs[0].Action != "redeem.credit" || ops.Logs[0].ActorID == nil || *ops.Logs[0].ActorID != 2 || ops.Summary.TopupTotal.String() != "10" {
		t.Fatalf("兑换应记在成员自己的操作日志、计入充值到账: %+v %+v", ops.Logs, ops.Summary)
	}
	// 失败太多次：限制
	for i := 0; i < 10; i++ {
		bob.do("POST", "/api/me/redeem", map[string]string{"code": "WRONG-CODE-" + itoa(int64(i))}, nil)
	}
	if code, b := bob.do("POST", "/api/me/redeem", map[string]string{"code": "WRONG"}, nil); code != 429 || !strings.Contains(b, "分钟后再试") {
		t.Fatalf("失败太多次应限制: %d %s", code, b)
	}
	// 参数校验
	for _, in := range []map[string]any{{"count": 0, "amount": "1"}, {"count": 1, "amount": "0"}, {"count": 1, "amount": "1", "currency": "EUR"}, {"count": 1, "amount": "1", "expires_at": time.Now().Add(-time.Hour)}} {
		if code, _ := a.do("POST", "/api/admin/redeem-codes", in, nil); code != 400 {
			t.Fatalf("应拒绝 %v: %d", in, code)
		}
	}
}

func listJSON(t *testing.T, c *client) string {
	_, b := c.do("GET", "/api/admin/redeem-codes", nil, nil)
	return b
}

func TestRedeemEnabledSetting(t *testing.T) {
	e := newEnv(t)
	e.api.PublicURL = "https://gateway.example"
	a, bob := e.login("admin"), e.login("bob")
	checkFlags := func(redeem, payment bool) {
		t.Helper()
		var settings struct {
			Redeem  bool `json:"redeem_enabled"`
			Payment bool `json:"payment_enabled"`
		}
		if code, body := a.do("GET", "/api/admin/settings", nil, &settings); code != 200 || settings.Redeem != redeem || settings.Payment != payment {
			t.Fatalf("设置开关应为 redeem=%v payment=%v: %d %s", redeem, payment, code, body)
		}
		var methods struct {
			Redeem  bool `json:"redeem_enabled"`
			Payment bool `json:"enabled"`
		}
		if code, body := bob.do("GET", "/api/me/payment-methods", nil, &methods); code != 200 || methods.Redeem != redeem || methods.Payment != payment {
			t.Fatalf("钱包开关应为 redeem=%v payment=%v: %d %s", redeem, payment, code, body)
		}
	}
	save := func(in map[string]any) {
		t.Helper()
		if code, body := a.do("PUT", "/api/admin/settings", in, nil); code != 200 {
			t.Fatalf("保存设置: %d %s", code, body)
		}
	}
	// 没有保存设置的已有部署仍然默认开放兑换。
	checkFlags(true, false)
	if code, body := bob.do("PUT", "/api/admin/settings", map[string]bool{"redeem_enabled": false}, nil); code != 403 {
		t.Fatalf("成员不能关闭兑换: %d %s", code, body)
	}
	save(map[string]any{"redeem_enabled": false})
	checkFlags(false, false)
	if o := e.lastOp(); o.Action != "settings.update" || o.change("redeem_enabled") == nil {
		t.Fatalf("开关修改应有操作日志: %+v", o)
	}
	// 关闭时仍可生成、查看、撤销，已有兑换码不受影响。
	var codes []store.NewRegistrationCode
	if code, body := a.do("POST", "/api/admin/redeem-codes", map[string]any{"count": 2, "amount": "3"}, &codes); code != 201 || len(codes) != 2 {
		t.Fatalf("关闭时应可生成兑换码: %d %s", code, body)
	}
	before := e.opCount()
	for i := 0; i < redeemMaxFails+1; i++ {
		if code, body := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[0].Code}, nil); code != 403 || !strings.Contains(body, "兑换码充值已关闭") {
			t.Fatalf("关闭时应拒绝兑换: %d %s", code, body)
		}
	}
	if e.opCount() != before || len(e.api.redeemGuard.fails[2]) != 0 {
		t.Fatal("关闭时不应记入账或猜码失败")
	}
	var me struct {
		User struct {
			Balance string `json:"balance"`
		} `json:"user"`
	}
	if code, body := bob.do("GET", "/api/me", nil, &me); code != 200 || me.User.Balance != "5" {
		t.Fatalf("关闭时余额应保持 5: %d %s", code, body)
	}
	var list []store.RedeemCode
	if code, body := a.do("GET", "/api/admin/redeem-codes", nil, &list); code != 200 || len(list) != 2 || list[0].UsedAt != nil || list[1].UsedAt != nil {
		t.Fatalf("关闭时不应核销兑换码: %d %s", code, body)
	}
	if code, body := a.do("DELETE", "/api/admin/redeem-codes/"+itoa(codes[1].ID), nil, nil); code != 200 {
		t.Fatalf("关闭时应可撤销: %d %s", code, body)
	}
	// 局部保存与另一个开关互不覆盖。
	save(map[string]any{"cny_per_usd": "7"})
	checkFlags(false, false)
	save(map[string]any{"payment_enabled": true})
	checkFlags(false, true)
	save(map[string]any{"redeem_enabled": true})
	checkFlags(true, true)
	save(map[string]any{"payment_enabled": false})
	checkFlags(true, false)
	// 重新开启后，暂停期间未使用的码仍可兑换，且只能用一次。
	var redeemed struct {
		Balance string `json:"balance"`
	}
	if code, body := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[0].Code}, &redeemed); code != 200 || redeemed.Balance != "8" {
		t.Fatalf("重新开启应正常兑换并到账: %d %s", code, body)
	}
	if code, body := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[0].Code}, nil); code != 400 {
		t.Fatalf("同一码只能兑换一次: %d %s", code, body)
	}
}
