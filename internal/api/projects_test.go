package api

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

type projectView struct {
	ID         int64   `json:"id"`
	InviteCode string  `json:"invite_code"`
	Balance    string  `json:"balance"`
	GroupIDs   []int64 `json:"group_ids"`
	MyStatus   string  `json:"my_status"`
	Members    int     `json:"members"`
	Closed     bool    `json:"closed"`
}

// 项目额度的完整流程：开关和建项目权限、只能开放自己能用的分组、邀请码申请和审批、转入取回不凭空产生钱、
// 项目 key 只能用开放的分组、移出成员、解散退回余额。
func TestProjectFlow(t *testing.T) {
	e := newEnv(t)
	a, bob := e.login("admin"), e.login("bob")
	ctx := context.Background()
	if _, err := e.store.Pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) SELECT 3, 'carol', password_hash, 'user' FROM users WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	carol := e.login("carol")
	decimalOf := func(sql string, args ...any) decimal.Decimal {
		var s string
		if err := e.store.Pool.QueryRow(ctx, sql, args...).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return decimal.RequireFromString(s)
	}

	if code, _ := bob.do("POST", "/api/me/projects", map[string]string{"name": "p"}, nil); code != 403 {
		t.Fatalf("组件没开时不能建项目: %d", code)
	}
	a.do("PUT", "/api/admin/settings", map[string]any{"projects_enabled": true}, nil)
	if code, _ := bob.do("POST", "/api/me/projects", map[string]string{"name": "p"}, nil); code != 403 {
		t.Fatalf("没有建项目权限时应拒绝: %d", code)
	}
	if code, body := a.do("PATCH", "/api/admin/users/2", map[string]any{"can_create_projects": true}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var p projectView
	if code, body := bob.do("POST", "/api/me/projects", map[string]string{"name": "研发组"}, &p); code != 201 || p.InviteCode == "" || p.MyStatus != "owner" {
		t.Fatalf("建项目: %d %s", code, body)
	}
	path := "/api/me/projects/" + itoa(p.ID)

	// 只能开放自己能用的分组：bob 不在 vip 用户分组里
	if code, _ := bob.do("PATCH", path, map[string]any{"group_ids": []int64{2}}, nil); code != 400 {
		t.Errorf("开放自己没权用的分组应拒绝: %d", code)
	}
	if code, body := bob.do("PATCH", path, map[string]any{"group_ids": []int64{1}, "period_limit": "3", "limit_period": "month"}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := bob.do("PATCH", path, map[string]any{"limit_period": "year"}, nil); code != 400 {
		t.Errorf("周期只能是天、周、月: %d", code)
	}
	var got struct {
		Project struct {
			PeriodLimit string `json:"period_limit"`
			LimitPeriod string `json:"limit_period"`
		} `json:"project"`
	}
	bob.do("GET", path, nil, &got)
	if got.Project.LimitPeriod != "month" || got.Project.PeriodLimit != "3" {
		t.Errorf("周期和上限应读回: %+v", got.Project)
	}

	// 转入：不能超过可用余额；转入后两边对账
	if code, _ := bob.do("POST", path+"/transfer", map[string]any{"direction": "in", "amount": "6"}, nil); code != 400 {
		t.Errorf("超过余额的转入应拒绝: %d", code)
	}
	if code, body := bob.do("POST", path+"/transfer", map[string]any{"direction": "in", "amount": "4"}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if b, pb := decimalOf(`SELECT balance::text FROM users WHERE id = 2`), decimalOf(`SELECT balance::text FROM projects WHERE id = $1`, p.ID); !b.Equal(decimal.NewFromInt(1)) || !pb.Equal(decimal.NewFromInt(4)) {
		t.Errorf("转入后 bob %s、项目 %s，期望 1 / 4", b, pb)
	}
	if code, _ := bob.do("POST", path+"/transfer", map[string]any{"direction": "out", "amount": "5"}, nil); code != 400 {
		t.Errorf("取回超过池子可用的应拒绝: %d", code)
	}
	bob.do("POST", path+"/transfer", map[string]any{"direction": "out", "amount": "1"}, nil)
	if n := decimalOf(`SELECT count(*)::text FROM balance_adjustments WHERE user_id = 2 AND project_id = $1`, p.ID); !n.Equal(decimal.NewFromInt(2)) {
		t.Errorf("转入和取回各记一笔余额流水，实际 %s 笔", n)
	}

	// 申请加入：错的码拒绝；申请后是 pending，不能建项目 key；创建人审批后可以
	if code, _ := carol.do("POST", "/api/me/projects/join", map[string]string{"code": "WRONG123"}, nil); code != 400 {
		t.Errorf("错误邀请码应拒绝: %d", code)
	}
	if code, body := carol.do("POST", "/api/me/projects/join", map[string]string{"code": p.InviteCode}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := carol.do("POST", "/api/me/projects/join", map[string]string{"code": p.InviteCode}, nil); code != 400 {
		t.Errorf("重复申请应拒绝: %d", code)
	}
	keyBody := map[string]any{"name": "proj", "group_id": 1, "project_id": p.ID}
	if code, _ := carol.do("POST", "/api/me/keys", keyBody, nil); code != 400 {
		t.Errorf("审批前不能建项目 key: %d", code)
	}
	if code, _ := carol.do("POST", path+"/members/3/approve", nil, nil); code != 404 {
		t.Errorf("成员不能审批: %d", code)
	}
	if code, body := bob.do("POST", path+"/members/3/approve", nil, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := carol.do("POST", "/api/me/keys", map[string]any{"name": "proj", "group_id": 2, "project_id": p.ID}, nil); code != 400 {
		t.Errorf("项目没开放的分组不能建 key: %d", code)
	}
	if code, body := carol.do("POST", "/api/me/keys", keyBody, nil); code != 201 {
		t.Fatalf("审批后建项目 key: %d %s", code, body)
	}
	var mine struct {
		Projects []projectView `json:"projects"`
	}
	carol.do("GET", "/api/me/projects", nil, &mine)
	if len(mine.Projects) != 1 || mine.Projects[0].MyStatus != "active" || mine.Projects[0].InviteCode != "" {
		t.Errorf("成员看到的项目不对（不应看到邀请码）: %+v", mine.Projects)
	}
	var detail struct {
		Members []struct {
			Username string `json:"username"`
			Status   string `json:"status"`
		} `json:"members"`
	}
	bob.do("GET", path, nil, &detail)
	if len(detail.Members) != 2 || detail.Members[0].Status != "owner" {
		t.Errorf("创建人看到的成员列表不对: %+v", detail.Members)
	}

	// 移出成员；创建人不能被移出
	if code, _ := bob.do("DELETE", path+"/members/2", nil, nil); code != 400 {
		t.Errorf("创建人不能移出自己: %d", code)
	}
	if code, body := bob.do("DELETE", path+"/members/3", nil, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}

	// 重置邀请码后旧码失效
	var reset struct {
		InviteCode string `json:"invite_code"`
	}
	bob.do("POST", path+"/invite-code", nil, &reset)
	if reset.InviteCode == "" || reset.InviteCode == p.InviteCode {
		t.Fatalf("重置邀请码: %+v", reset)
	}
	if code, _ := carol.do("POST", "/api/me/projects/join", map[string]string{"code": p.InviteCode}, nil); code != 400 {
		t.Errorf("旧邀请码应失效: %d", code)
	}

	// 解散：池里剩的 3 退回 bob（1 + 1 + 3 = 5，和最初一样），之后不能再操作
	if code, body := bob.do("DELETE", path, nil, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if b := decimalOf(`SELECT balance::text FROM users WHERE id = 2`); !b.Equal(decimal.NewFromInt(5)) {
		t.Errorf("解散后 bob 余额 %s，期望退回到 5", b)
	}
	if code, _ := bob.do("POST", path+"/transfer", map[string]any{"direction": "in", "amount": "1"}, nil); code != 400 {
		t.Errorf("解散后不能再转入: %d", code)
	}
	var all []projectView
	a.do("GET", "/api/admin/projects", nil, &all)
	if len(all) != 1 || !all[0].Closed {
		t.Errorf("管理员应看到已解散的项目: %+v", all)
	}
}
