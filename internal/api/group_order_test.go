package api

import (
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

// 服务分组可拖拽排序：管理端列表和成员看到的分组都按手动顺序，新建的排最后。
func TestGroupOrder(t *testing.T) {
	e := newEnv(t)
	a, bob := e.login("admin"), e.login("bob")
	a.do("POST", "/api/admin/groups", map[string]any{"name": "g3", "ratio": "2", "enabled": true, "is_public": true}, nil)
	names := func(c *client, path string) []string {
		var groups []store.Group
		if path == "/api/me/models" {
			var r struct {
				Groups []store.Group `json:"groups"`
			}
			if code, body := c.do("GET", path, nil, &r); code != 200 {
				t.Fatalf("%d %s", code, body)
			}
			groups = r.Groups
		} else if code, body := c.do("GET", path, nil, &groups); code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		out := []string{}
		for _, g := range groups {
			out = append(out, g.Name)
		}
		return out
	}
	if got := names(a, "/api/admin/groups"); len(got) != 3 || got[2] != "g3" {
		t.Fatalf("新建分组应排在最后: %v", got)
	}
	if code, body := a.do("PUT", "/api/admin/groups/order", map[string]any{"ids": []int64{3, 1}}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if got := names(a, "/api/admin/groups"); len(got) != 3 || got[0] != "g3" || got[1] != "default" || got[2] != "vip" {
		t.Fatalf("管理端应按手动顺序，未列出的排后面: %v", got)
	}
	if got := names(bob, "/api/me/models"); len(got) != 2 || got[0] != "g3" || got[1] != "default" {
		t.Fatalf("成员看到的可用分组也应按手动顺序: %v", got)
	}
}
