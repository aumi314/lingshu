package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/store"
	"github.com/shopspring/decimal"
)

func TestUnifiedUserUpdate(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	bob := e.login("bob")
	change := map[string]any{
		"username": "renamed", "password": "new-password-123", "enabled": false,
		"user_group_ids":     []int64{2},
		"balance_adjustment": map[string]string{"mode": "add", "amount": "6.7", "currency": "CNY", "note": " unified "},
	}
	if code, body := a.do("PATCH", "/api/admin/users/2", change, nil); code != 200 {
		t.Fatalf("统一提交失败: %d %s", code, body)
	}
	u, err := e.store.UserByID(context.Background(), 2)
	if err != nil || u.Username != "renamed" || u.Enabled || u.Balance.String() != "6" || len(u.UserGroupIDs) != 1 || u.UserGroupIDs[0] != 2 {
		t.Fatalf("用户未完整更新: %+v %v", u, err)
	}
	history, err := e.store.BalanceAdjustments(context.Background(), 2)
	if err != nil || len(history) != 1 || history[0].Amount.String() != "1" || history[0].Operator != "admin" || history[0].Note != "unified" {
		t.Fatalf("流水错误: %+v %v", history, err)
	}
	if code, _ := bob.do("GET", "/api/me", nil, nil); code != 401 {
		t.Fatalf("旧会话未失效: %d", code)
	}
	// 余额留空对应不发送 balance_adjustment；空密码不重置密码。
	if code, body := a.do("PATCH", "/api/admin/users/2", map[string]any{"username": "renamed-again", "password": "", "enabled": true}, nil); code != 200 {
		t.Fatalf("仅资料更新失败: %d %s", code, body)
	}
	if code, body := e.client().do("POST", "/api/auth/login", map[string]string{"username": "renamed-again", "password": "new-password-123"}, nil); code != 200 {
		t.Fatalf("新密码登录失败: %d %s", code, body)
	}
	// 设为余额按提交时的实际余额计算差额；零差额不新增流水。
	for _, amount := range []string{"0", "0"} {
		if code, body := a.do("PATCH", "/api/admin/users/2", map[string]any{"balance_adjustment": map[string]string{"mode": "set", "amount": amount, "currency": "USD"}}, nil); code != 200 {
			t.Fatalf("设为余额失败: %d %s", code, body)
		}
	}
	history, _ = e.store.BalanceAdjustments(context.Background(), 2)
	if len(history) != 2 || history[0].Amount.String() != "-6" || !history[0].BalanceAfter.IsZero() {
		t.Fatalf("设为余额或零差额流水错误: %+v", history)
	}
}

// TestUserNote 覆盖管理员内部备注：随 PATCH 写入、管理端列表读回、
// 空串表示清空，以及最重要的——不出现在成员自己的 /api/me 里。
func TestUserNote(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	bob := e.login("bob")

	if code, body := a.do("PATCH", "/api/admin/users/2", map[string]any{"note": "  外部合作方，额度按月给  "}, nil); code != 200 {
		t.Fatalf("写备注失败: %d %s", code, body)
	}
	users, err := e.store.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.ID == 2 && u.Note != "外部合作方，额度按月给" {
			t.Fatalf("备注未去除首尾空白或未写入: %q", u.Note)
		}
	}

	var meBody struct {
		User store.User `json:"user"`
	}
	if code, _ := bob.do("GET", "/api/me", nil, &meBody); code != 200 {
		t.Fatalf("成员读取自身资料失败: %d", code)
	}
	if meBody.User.Note != "" {
		t.Fatalf("内部备注泄漏给了成员: %q", meBody.User.Note)
	}

	if code, body := a.do("PATCH", "/api/admin/users/2", map[string]any{"note": "   "}, nil); code != 200 {
		t.Fatalf("清空备注失败: %d %s", code, body)
	}
	if users, err = e.store.ListUsers(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		for _, u := range users {
			if u.ID == 2 && u.Note != "" {
				t.Fatalf("备注没有被清空: %q", u.Note)
			}
		}
	}

	tooLong := strings.Repeat("字", userNoteLimit+1)
	if code, _ := a.do("PATCH", "/api/admin/users/2", map[string]any{"note": tooLong}, nil); code != 400 {
		t.Fatalf("超长备注应被拒绝，实际 %d", code)
	}
}

func TestUnifiedBalanceConcurrentDebit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	errors := make(chan error, 2)
	go func() {
		errors <- e.store.UpdateUser(ctx, 2, store.UserPatch{BalanceChange: &store.UserBalanceChange{Mode: "add", Amount: decimal.NewFromInt(3), OperatorID: 1}})
	}()
	go func() {
		_, err := e.store.AdjustBalance(ctx, 2, decimal.NewFromInt(-1), 1, "concurrent debit")
		errors <- err
	}()
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	u, _ := e.store.UserByID(ctx, 2)
	history, _ := e.store.BalanceAdjustments(ctx, 2)
	if u.Balance.String() != "7" || len(history) != 2 {
		t.Fatalf("并发调整丢失余额或流水: user=%+v history=%+v", u, history)
	}
}

func TestUnifiedUserUpdateRollback(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	bob := e.login("bob")
	ctx := context.Background()
	if _, err := e.store.Pool.Exec(ctx, `CREATE FUNCTION reject_user_edit_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test ledger failure'; END $$; CREATE TRIGGER reject_user_edit_ledger BEFORE INSERT ON balance_adjustments FOR EACH ROW EXECUTE FUNCTION reject_user_edit_ledger()`); err != nil {
		t.Fatal(err)
	}
	if code, _ := a.do("PATCH", "/api/admin/users/2", map[string]any{
		"username": "must-rollback", "password": "new-password-123", "enabled": false, "user_group_ids": []int64{2},
		"balance_adjustment": map[string]string{"mode": "add", "amount": "3"},
	}, nil); code < 400 {
		t.Fatal("流水失败应导致整个提交失败")
	}
	u, _ := e.store.UserByID(ctx, 2)
	if u.Username != "bob" || !u.Enabled || u.Balance.String() != "5" || len(u.UserGroupIDs) != 0 {
		t.Fatalf("用户资料或余额未回滚: %+v", u)
	}
	history, _ := e.store.BalanceAdjustments(ctx, 2)
	if len(history) != 0 {
		t.Fatalf("失败后仍产生流水: %+v", history)
	}
	if code, _ := bob.do("GET", "/api/me", nil, nil); code != 200 {
		t.Fatalf("失败提交不应清除会话: %d", code)
	}
	e.login("bob")
}

func TestUnifiedUserUpdateValidation(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	for _, adjustment := range []map[string]string{
		{"mode": "unknown", "amount": "1"}, {"mode": "add"}, {"mode": "add", "amount": "1", "currency": "EUR"},
	} {
		if code, _ := a.do("PATCH", "/api/admin/users/2", map[string]any{"username": "must-not-save", "balance_adjustment": adjustment}, nil); code != 400 {
			t.Errorf("无效额度调整应返回 400: %d", code)
		}
	}
	if code, _ := a.do("PATCH", "/api/admin/users/2", map[string]any{"username": "admin", "balance_adjustment": map[string]string{"mode": "add", "amount": "3"}}, nil); code != 409 {
		t.Errorf("重名应返回 409: %d", code)
	}
	var users []store.User
	a.do("GET", "/api/admin/users", nil, &users)
	for _, u := range users {
		if u.ID == 2 && (u.Username != "bob" || u.Balance.String() != "5") {
			t.Fatalf("无效请求修改了用户: %+v", u)
		}
	}
	if code, body := a.do("PATCH", "/api/admin/users/1", map[string]any{"enabled": false, "balance_adjustment": map[string]string{"mode": "add", "amount": "3"}}, nil); code != 400 || !strings.Contains(body, "自己") {
		t.Fatalf("不得停用自己: %d %s", code, body)
	}
}

// 管理端用户列表带出最近调用时间（取该用户所有 key 的最近一次），成员自己的 /api/me 不带。
func TestAdminUserListLastUsed(t *testing.T) {
	e := newEnv(t)
	a, bob := e.login("admin"), e.login("bob")
	bob.do("POST", "/api/me/keys", map[string]any{"name": "old", "group_id": 1}, nil)
	bob.do("POST", "/api/me/keys", map[string]any{"name": "new", "group_id": 1}, nil)
	if _, err := e.store.Pool.Exec(context.Background(), `UPDATE api_keys SET last_used_at = CASE name WHEN 'old' THEN '2026-11-01T00:00:00Z'::timestamptz ELSE '2026-11-03T00:00:00Z'::timestamptz END`); err != nil {
		t.Fatal(err)
	}
	var users []store.User
	if code, body := a.do("GET", "/api/admin/users", nil, &users); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, u := range users {
		switch u.Username {
		case "bob":
			if u.LastUsedAt == nil || !u.LastUsedAt.Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("bob 最近调用应取最新的 key: %v", u.LastUsedAt)
			}
		case "admin":
			if u.LastUsedAt != nil {
				t.Fatalf("没调用过的用户不应有最近调用: %v", u.LastUsedAt)
			}
		}
	}
	if _, body := bob.do("GET", "/api/me", nil, nil); strings.Contains(body, "last_used_at") {
		t.Fatalf("/api/me 不应带最近调用: %s", body)
	}
}
