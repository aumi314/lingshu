package api

import (
	"context"
	"testing"
)

// 超级管理员（2026-11-06 决策）：比管理员只多任免管理员；管理员不能改超级管理员的账号；至少保留一个启用中的超级管理员。
func TestSuperAdminPermissions(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.Pool.Exec(context.Background(), `UPDATE users SET role = 'super_admin' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	super := e.login("admin")
	var created struct {
		ID int64 `json:"id"`
	}
	if code, b := super.do("POST", "/api/admin/users", map[string]string{"username": "ops", "password": "password-1-ops", "role": "admin"}, &created); code != 201 {
		t.Fatalf("超级管理员任命管理员: %d %s", code, b)
	}
	ops := e.client()
	if code, b := ops.do("POST", "/api/auth/login", map[string]string{"username": "ops", "password": "password-1-ops"}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	opsID := itoa(created.ID)

	// 管理员：不能任免，其他照常
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"POST", "/api/admin/users", map[string]string{"username": "x1", "password": "password-1-x1", "role": "admin"}, 403},
		{"POST", "/api/admin/users", map[string]string{"username": "x2", "password": "password-1-x2", "role": "super_admin"}, 403},
		{"POST", "/api/admin/users", map[string]string{"username": "x3", "password": "password-1-x3"}, 201},
		{"PATCH", "/api/admin/users/2", map[string]any{"role": "admin"}, 403},
		{"PATCH", "/api/admin/users/2", map[string]any{"note": "ok"}, 200},
		{"PUT", "/api/admin/settings", map[string]any{"cny_per_usd": "7"}, 200},
		// 不能动超级管理员的账号
		{"PATCH", "/api/admin/users/1", map[string]any{"password": "hijacked-password-1"}, 403},
		{"PATCH", "/api/admin/users/1", map[string]any{"enabled": false}, 403},
		{"POST", "/api/admin/users/1/balance", map[string]any{"mode": "add", "amount": "1"}, 403},
		{"PUT", "/api/admin/users/1/user-groups", map[string]any{"user_group_ids": []int64{1}}, 403},
	} {
		if code, b := ops.do(c.method, c.path, c.body, nil); code != c.want {
			t.Fatalf("管理员 %s %s 应 %d，实际 %d %s", c.method, c.path, c.want, code, b)
		}
	}
	// 超级管理员：可任免；最后一个超级管理员不能被降级或停用
	if code, b := super.do("PATCH", "/api/admin/users/2", map[string]any{"role": "admin"}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if code, b := super.do("PATCH", "/api/admin/users/1", map[string]any{"role": "admin"}, nil); code != 400 {
		t.Fatalf("唯一的超级管理员不能降级: %d %s", code, b)
	}
	if code, b := super.do("PATCH", "/api/admin/users/"+opsID, map[string]any{"role": "super_admin"}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if code, b := super.do("PATCH", "/api/admin/users/1", map[string]any{"role": "admin"}, nil); code != 200 {
		t.Fatalf("有另一个超级管理员后可以降级自己: %d %s", code, b)
	}
	if code, b := ops.do("PATCH", "/api/admin/users/2", map[string]any{"role": "user"}, nil); code != 200 {
		t.Fatalf("新的超级管理员可以任免: %d %s", code, b)
	}
}
