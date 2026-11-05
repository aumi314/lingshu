package api

import "testing"

// 现有功能定位为商业中转（2026-11-05）：迁移后默认就是商业中转，公司团队网关还在开发，接口拒绝。
func TestDeploymentModeIsCommercialRelay(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	var s struct {
		DeploymentMode string `json:"deployment_mode"`
	}
	if code, body := a.do("GET", "/api/admin/settings", nil, &s); code != 200 || s.DeploymentMode != "commercial_relay" {
		t.Fatalf("默认模式应为商业中转: %d %s", code, body)
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]string{"deployment_mode": "company_team_gateway"}, nil); code != 400 {
		t.Fatalf("公司团队网关尚未开放，应拒绝: %d %s", code, body)
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]string{"deployment_mode": "commercial_relay"}, nil); code != 200 {
		t.Fatalf("保存商业中转: %d %s", code, body)
	}
}
