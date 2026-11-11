package api

import (
	"strings"
	"testing"
)

// 公告：管理员在主设置里填，所有登录用户在概览页顶部看到；去掉首尾空白、最多 500 字、留空表示不显示。
func TestAnnouncement(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.login("admin"), e.login("bob")
	var ep struct {
		Announcement string `json:"announcement"`
	}
	if code, b := bob.do("GET", "/api/me/endpoints", nil, &ep); code != 200 || ep.Announcement != "" {
		t.Fatalf("默认没有公告: %d %s", code, b)
	}
	if code, b := admin.do("PUT", "/api/admin/settings", map[string]any{"announcement": "  客服邮箱：support@example.com \n"}, nil); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	bob.do("GET", "/api/me/endpoints", nil, &ep)
	if ep.Announcement != "客服邮箱：support@example.com" {
		t.Errorf("成员读到的公告 %q", ep.Announcement)
	}
	var s struct {
		Announcement string `json:"announcement"`
	}
	admin.do("GET", "/api/admin/settings", nil, &s)
	if s.Announcement != ep.Announcement {
		t.Errorf("管理端读回 %q", s.Announcement)
	}
	if code, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"announcement": strings.Repeat("长", 501)}, nil); code != 400 {
		t.Errorf("超过 500 字应拒绝: %d", code)
	}
	if code, _ := bob.do("PUT", "/api/admin/settings", map[string]any{"announcement": "x"}, nil); code != 403 {
		t.Errorf("成员不能改公告: %d", code)
	}
	admin.do("PUT", "/api/admin/settings", map[string]any{"announcement": "   "}, nil)
	bob.do("GET", "/api/me/endpoints", nil, &ep)
	if ep.Announcement != "" {
		t.Errorf("留空后不显示: %q", ep.Announcement)
	}
}
