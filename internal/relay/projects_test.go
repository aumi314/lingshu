package relay

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/auth"
)

// withProject 建项目 1（创建人 alice，池子 balance，开放分组 1），成员 bob（个人余额 0）和他的项目 key，返回这把 key。
func (e *env) withProject(balance string) string {
	e.t.Helper()
	e.exec(`INSERT INTO settings (key, value) VALUES ('projects_enabled', 'true')`)
	e.handler.limits = newLimiter() // 设置缓存 10 秒，测试里直接换一个
	e.exec(`INSERT INTO users (id, username, password_hash, balance) VALUES (2, 'bob', 'x', 0)`)
	e.exec(`INSERT INTO projects (id, name, invite_code, owner_id, balance) VALUES (1, '研发组', 'CODE1234', 1, $1::numeric)`, balance)
	e.exec(`INSERT INTO project_groups VALUES (1, 1)`)
	e.exec(`INSERT INTO project_members (project_id, user_id, status) VALUES (1, 1, 'active'), (1, 2, 'active')`)
	key, hash, prefix := auth.NewAPIKey()
	e.exec(`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, group_id, project_id) VALUES (2, 'proj', $1, $2, 1, 1)`, hash, prefix)
	return key
}

func (e *env) postAs(key, body string) (int, string, string) {
	e.t.Helper()
	saved := e.key
	e.key = key
	defer func() { e.key = saved }()
	resp := e.post("/v1/chat/completions", body, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(b), resp.Header.Get("X-Lingshu-Request-Id")
}

// 项目 key 的钱从项目池扣，不碰成员和创建人的个人余额；日志记项目；/v1/usage 报项目池。
func TestProjectKeyChargesPool(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	key := e.withProject("1")
	code, body, id := e.postAs(key, `{"model":"std-chat"}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	cost, _ := e.cost(id)
	if pool := e.decimal(`SELECT balance::text FROM projects WHERE id = 1`); !pool.Equal(decimal.NewFromInt(1).Sub(cost)) || !cost.IsPositive() {
		t.Errorf("项目池 %s，应扣掉 %s", pool, cost)
	}
	if r := e.decimal(`SELECT reserved::text FROM projects WHERE id = 1`); !r.IsZero() {
		t.Errorf("结算后冻结应清零，实际 %s", r)
	}
	if b := e.decimal(`SELECT balance::text FROM users WHERE id = 2`); !b.IsZero() {
		t.Errorf("成员个人余额不应变化: %s", b)
	}
	if b := e.balance(); !b.Equal(decimal.NewFromInt(10)) {
		t.Errorf("创建人个人余额不应变化: %s", b)
	}
	if p := e.decimal(`SELECT project_id::text FROM request_logs WHERE request_id = $1`, id); !p.Equal(decimal.NewFromInt(1)) {
		t.Errorf("日志应记项目 1，实际 %s", p)
	}
	req, _ := http.NewRequest(http.MethodGet, e.gateway.URL+"/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"balance":`+decimal.NewFromInt(1).Sub(cost).String()) {
		t.Errorf("/v1/usage 应报项目池余额: %s", b)
	}
}

// 项目 key 什么时候不能用：池子空了、本周额度用完、被移出、项目冻结、解散、组件关闭、分组没开放，每种都说清原因。
func TestProjectKeyRejections(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	key := e.withProject("1")
	expect := func(name string, want int, words string) {
		t.Helper()
		e.handler.limits = newLimiter()
		code, body, _ := e.postAs(key, `{"model":"std-chat"}`)
		if code != want || !strings.Contains(body, words) {
			t.Errorf("%s：期望 %d 且提示含「%s」，实际 %d %s", name, want, words, code, body)
		}
	}
	expect("正常", 200, "")

	// 成员额度上限（创建人给每人单独设，按天 / 周 / 月）：上一次已经花了一点，上限设得比它还小，本周期剩余为负，直接拒，提示写明周期
	e.exec(`UPDATE project_members SET period_limit = 0.000001 WHERE project_id = 1 AND user_id = 2`)
	expect("本周额度用完", 402, "你在项目「研发组」本周")
	e.exec(`UPDATE project_members SET limit_period = 'month' WHERE project_id = 1 AND user_id = 2`)
	expect("本月额度用完", 402, "每月 1 号 0 点重置")
	e.exec(`UPDATE project_members SET limit_period = 'day' WHERE project_id = 1 AND user_id = 2`)
	expect("今天额度用完", 402, "今天")
	// 别人的上限不影响他：只给创建人设上限，bob 照常能用
	e.exec(`UPDATE project_members SET period_limit = NULL WHERE project_id = 1 AND user_id = 2`)
	e.exec(`UPDATE project_members SET period_limit = 0.000001 WHERE project_id = 1 AND user_id = 1`)
	expect("别人的上限不影响", 200, "")
	if r := e.decimal(`SELECT COALESCE(sum(reserved), 0)::text FROM project_members`); !r.IsZero() {
		t.Errorf("结算后成员冻结应清零，实际 %s", r)
	}

	e.exec(`UPDATE projects SET balance = 0 WHERE id = 1`)
	expect("池子空了", 402, "项目负责人 alice")
	e.exec(`UPDATE projects SET balance = 1 WHERE id = 1`)

	e.exec(`DELETE FROM project_groups WHERE project_id = 1`)
	expect("分组没开放", 403, "没有开放")
	e.exec(`INSERT INTO project_groups VALUES (1, 1)`)

	e.exec(`UPDATE projects SET frozen = true WHERE id = 1`)
	expect("冻结", 403, "冻结")
	e.exec(`UPDATE projects SET frozen = false WHERE id = 1`)

	e.exec(`UPDATE settings SET value = 'false' WHERE key = 'projects_enabled'`)
	expect("组件关闭", 403, "项目功能已关闭")
	e.exec(`UPDATE settings SET value = 'true' WHERE key = 'projects_enabled'`)

	e.exec(`DELETE FROM project_members WHERE user_id = 2`)
	expect("被移出", 403, "不是项目")
	e.exec(`INSERT INTO project_members (project_id, user_id, status) VALUES (1, 2, 'active')`)

	e.exec(`UPDATE projects SET closed_at = now() WHERE id = 1`)
	expect("解散", 403, "已解散")
}

// 并发请求冲不破项目池：池子只够一次预扣时，同时来的几个请求只有一个放行（和个人余额同样的行锁）。
func TestProjectPoolHoldsUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	key := e.withProject("1")
	if code, body, _ := e.postAs(key, `{"model":"std-chat"}`); code != 200 { // 先有一次历史，之后预扣按“最近最贵 × 2”算
		t.Fatalf("%d %s", code, body)
	}
	reserve := e.decimal(`SELECT (max(cost) * 2)::text FROM request_logs`)
	e.exec(`UPDATE projects SET balance = $1::numeric WHERE id = 1`, reserve.Mul(decimal.NewFromFloat(1.5)).String())
	release := make(chan struct{})
	var started atomic.Int32
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		started.Add(1)
		<-release
		okUpstream(w, r)
	}
	var wg sync.WaitGroup
	var ok, rejected atomic.Int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := e.postAs(key, `{"model":"std-chat"}`)
			switch code {
			case 200:
				ok.Add(1)
			case 402:
				rejected.Add(1)
			}
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for rejected.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	close(release)
	wg.Wait()
	if ok.Load() != 1 || rejected.Load() != 4 || started.Load() != 1 {
		t.Errorf("只应放行一个：成功 %d、拒绝 %d、到上游 %d", ok.Load(), rejected.Load(), started.Load())
	}
	if r := e.decimal(`SELECT reserved::text FROM projects WHERE id = 1`); !r.IsZero() {
		t.Errorf("全部结束后冻结应清零，实际 %s", r)
	}
}
