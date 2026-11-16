package api

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/store"
)

func TestProjectOwnerTransfer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.store.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users (id, username, password_hash, role) SELECT 3, 'carol', password_hash, 'user' FROM users WHERE id = 2`)
	a, bob, carol := e.login("admin"), e.login("bob"), e.login("carol")
	a.do("PUT", "/api/admin/settings", map[string]any{"projects_enabled": true}, nil)
	a.do("PATCH", "/api/admin/users/2", map[string]any{"can_create_projects": true}, nil)
	var p projectView
	if code, body := bob.do("POST", "/api/me/projects", map[string]string{"name": "handoff"}, &p); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	path := "/api/me/projects/" + itoa(p.ID)
	ownerPath := path + "/owner"
	check := func(c *client, want int, id int64) {
		t.Helper()
		if code, body := c.do("POST", ownerPath, map[string]int64{"user_id": id}, nil); code != want {
			t.Fatalf("transfer to %d: %d %s, want %d", id, code, body, want)
		}
	}
	bob.do("PATCH", path, map[string]any{"group_ids": []int64{1}}, nil)
	bob.do("POST", path+"/transfer", map[string]string{"direction": "in", "amount": "3.1234567891"}, nil)
	check(bob, 400, 0)
	check(bob, 400, 2)
	check(bob, 400, 999)
	check(bob, 400, 3) // 接收人须为正式成员。
	carol.do("POST", "/api/me/projects/join", map[string]string{"code": p.InviteCode}, nil)
	check(bob, 400, 3)
	bob.do("POST", path+"/members/3/approve", nil, nil)
	check(carol, 404, 2)
	check(a, 404, 3) // 管理员也不能代转让。
	exec(`UPDATE users SET enabled = false WHERE id = 3`)
	check(bob, 400, 3)
	exec(`UPDATE users SET enabled = true WHERE id = 3`)
	bob.do("PUT", path+"/members/3/limit", map[string]any{"period_limit": "2", "limit_period": "month"}, nil)
	var key struct {
		ID int64 `json:"id"`
	}
	if code, body := carol.do("POST", "/api/me/keys", map[string]any{"name": "kept", "group_id": 1, "project_id": p.ID}, &key); code != 201 {
		t.Fatalf("project key: %d %s", code, body)
	}

	// 真实预扣期间拒绝转让；失败不能退款、改身份或产生操作日志。
	hold, err := e.store.ReserveProject(ctx, p.ID, 3, decimal.RequireFromString("0.1"))
	if err != nil || !hold.Held.IsPositive() {
		t.Fatalf("reserve: %+v %v", hold, err)
	}
	beforeOps := e.opCount()
	check(bob, 409, 3)
	if e.opCount() != beforeOps {
		t.Fatal("failed transfer wrote operations")
	}
	assertBalance := func(userID int64, want string) {
		t.Helper()
		var got string
		if err := e.store.Pool.QueryRow(ctx, `SELECT balance::text FROM users WHERE id = $1`, userID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !decimal.RequireFromString(got).Equal(decimal.RequireFromString(want)) {
			t.Fatalf("user %d balance %s, want %s", userID, got, want)
		}
	}
	assertBalance(2, "1.8765432109")
	current, err := e.store.ProjectByID(ctx, p.ID)
	if err != nil || current.OwnerID != 2 || !current.Balance.Equal(decimal.RequireFromString("3.1234567891")) {
		t.Fatalf("failed transfer changed project: %+v %v", current, err)
	}
	exec(`UPDATE projects SET reserved = 0 WHERE id = $1`, p.ID)
	exec(`UPDATE project_members SET reserved = 0 WHERE project_id = $1`, p.ID)

	// 退款流水失败时，负责人和池子也必须回滚。
	exec(`CREATE FUNCTION reject_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test refund failure'; END $$;
		CREATE TRIGGER reject_handoff BEFORE INSERT ON balance_adjustments FOR EACH ROW EXECUTE FUNCTION reject_handoff()`)
	check(bob, 500, 3)
	assertBalance(2, "1.8765432109")
	current, err = e.store.ProjectByID(ctx, p.ID)
	if err != nil || current.OwnerID != 2 || !current.Balance.Equal(decimal.RequireFromString("3.1234567891")) || e.opCount() != beforeOps {
		t.Fatalf("failed refund did not roll back: %+v %v", current, err)
	}
	exec(`DROP TRIGGER reject_handoff ON balance_adjustments; DROP FUNCTION reject_handoff()`)
	check(bob, 200, 3) // carol 没有创建项目权限，也能接收已有项目。
	assertBalance(2, "5")
	assertBalance(3, "0")
	current, err = e.store.ProjectByID(ctx, p.ID)
	if err != nil || current.OwnerID != 3 || !current.Balance.IsZero() || !current.Reserved.IsZero() || len(current.GroupIDs) != 1 || current.GroupIDs[0] != 1 {
		t.Fatalf("transferred project: %+v %v", current, err)
	}
	if got := e.opCount() - beforeOps; got != 2 {
		t.Fatalf("refund and ownership should each be logged, got %d", got)
	}
	op := e.lastOp()
	if op.Action != "project.update" || op.ActorID == nil || *op.ActorID != 2 || op.change("owner") == nil {
		t.Fatalf("ownership audit missing: %+v", op)
	}
	var refunded string
	if err := e.store.Pool.QueryRow(ctx, `SELECT amount::text FROM balance_adjustments WHERE project_id = $1 AND user_id = 2 AND amount > 0`, p.ID).Scan(&refunded); err != nil || !decimal.RequireFromString(refunded).Equal(decimal.RequireFromString("3.1234567891")) {
		t.Fatalf("refund record: %s %v", refunded, err)
	}
	var details struct {
		Members []struct {
			UserID int64  `json:"user_id"`
			Status string `json:"status"`
			Limit  string `json:"period_limit"`
			Period string `json:"limit_period"`
		} `json:"members"`
	}
	if code, body := carol.do("GET", path, nil, &details); code != 200 || len(details.Members) != 2 || details.Members[0].UserID != 3 || details.Members[0].Status != "owner" || details.Members[0].Limit != "2" || details.Members[0].Period != "month" {
		t.Fatalf("new owner detail/limits: %d %s", code, body)
	}
	var oldDetail struct {
		Members []any `json:"members"`
	}
	if code, body := bob.do("GET", path, nil, &oldDetail); code != 200 || len(oldDetail.Members) != 0 {
		t.Fatalf("previous owner should stay a regular member: %d %s", code, body)
	}
	var count int
	if err := e.store.Pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE id = $1 AND project_id = $2 AND user_id = 3 AND enabled`, key.ID, p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("project key changed: %d %v", count, err)
	}
	check(bob, 404, 3) // 重试不得重复退款。
	if err := e.store.TransferProjectOwner(ctx, 2, p.ID, 3); err == nil {
		t.Fatal("stale owner bypassed transactional ownership check")
	}
	// 已过接口鉴权的旧请求也须在事务里复核负责人。
	changedName := "stale owner"
	if err := e.store.UpdateProject(ctx, 2, p.ID, store.ProjectPatch{Name: &changedName}); err == nil {
		t.Fatal("stale owner updated project")
	}
	if _, err := e.store.ResetInviteCode(ctx, 2, p.ID); err == nil {
		t.Fatal("stale owner reset invite")
	}
	if err := e.store.SetMemberLimit(ctx, 2, p.ID, 3, nil, "week"); err == nil {
		t.Fatal("stale owner changed member limit")
	}
	if err := e.store.RemoveMember(ctx, 2, p.ID, 3); err == nil {
		t.Fatal("stale owner removed new owner")
	}
	assertBalance(2, "5")
	if code, _ := bob.do("PATCH", path, map[string]string{"name": "forbidden"}, nil); code != 404 {
		t.Fatalf("old owner can still edit: %d", code)
	}
	for _, request := range []struct {
		method string
		path   string
		body   any
	}{
		{"POST", path + "/invite-code", nil},
		{"PUT", path + "/members/2/limit", map[string]any{"period_limit": "1", "limit_period": "week"}},
		{"POST", path + "/members/2/approve", nil},
		{"DELETE", path + "/members/3", nil},
		{"POST", path + "/transfer", map[string]string{"direction": "out", "amount": "1"}},
		{"DELETE", path, nil},
	} {
		if code, body := bob.do(request.method, request.path, request.body, nil); code != 404 {
			t.Fatalf("old owner %s %s: %d %s", request.method, request.path, code, body)
		}
	}
	if code, body := carol.do("POST", path+"/transfer", map[string]string{"direction": "in", "amount": "1"}, nil); code != 400 {
		t.Fatalf("new owner should fund from own account: %d %s", code, body)
	}
	exec(`UPDATE users SET balance = 2 WHERE id = 3`)
	if code, body := carol.do("POST", path+"/transfer", map[string]string{"direction": "in", "amount": "1"}, nil); code != 200 {
		t.Fatalf("new owner funding: %d %s", code, body)
	}
	if code, body := carol.do("DELETE", path, nil, nil); code != 200 {
		t.Fatalf("new owner closing: %d %s", code, body)
	}
	assertBalance(2, "5")
	assertBalance(3, "2")
	check(carol, 400, 2)
}

// 并发重复确认只允许一次交接和一次退款，空池也能正常交接。
func TestProjectOwnerTransferConcurrent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.store.Pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) SELECT 3, 'carol', password_hash, 'user' FROM users WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	for _, amount := range []string{"0", "3.1234567891"} {
		t.Run(amount, func(t *testing.T) {
			id, err := e.store.CreateProject(ctx, 2, "concurrent")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.Pool.Exec(ctx, `INSERT INTO project_members (project_id, user_id, status) VALUES ($1, 3, 'active')`, id); err != nil {
				t.Fatal(err)
			}
			if amount != "0" {
				if err := e.store.TransferProject(ctx, 2, id, decimal.RequireFromString(amount)); err != nil {
					t.Fatal(err)
				}
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			for range 2 {
				go func() {
					<-start
					results <- e.store.TransferProjectOwner(ctx, 2, id, 3)
				}()
			}
			close(start)
			var successes int
			for range 2 {
				if err := <-results; err == nil {
					successes++
				}
			}
			if successes != 1 {
				t.Fatalf("successful handoffs = %d", successes)
			}
			var balance string
			if err := e.store.Pool.QueryRow(ctx, `SELECT balance::text FROM users WHERE id = 2`).Scan(&balance); err != nil || !decimal.RequireFromString(balance).Equal(decimal.NewFromInt(5)) {
				t.Fatalf("refund duplicated: %s %v", balance, err)
			}
			p, err := e.store.ProjectByID(ctx, id)
			if err != nil || p.OwnerID != 3 || !p.Balance.IsZero() {
				t.Fatalf("handoff: %+v %v", p, err)
			}
			var refunds int
			if err := e.store.Pool.QueryRow(ctx, `SELECT count(*) FROM balance_adjustments WHERE project_id = $1 AND amount > 0`, id).Scan(&refunds); err != nil {
				t.Fatal(err)
			}
			want := 1
			if amount == "0" {
				want = 0
			}
			if refunds != want {
				t.Fatalf("refund entries = %d, want %d", refunds, want)
			}
		})
	}
}
