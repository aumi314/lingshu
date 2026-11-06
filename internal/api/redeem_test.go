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
		go func() { defer wg.Done(); code, _ := bob.do("POST", "/api/me/redeem", map[string]string{"code": codes[1].Code}, nil); results <- code }()
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
