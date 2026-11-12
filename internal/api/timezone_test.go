package api

import (
	"testing"

	"github.com/aumi314/lingshu/internal/billing"
)

// 主设置的时区：默认北京时间，保存后立即生效并在 /api/me 里给出显示名，非法时区拒绝。
func TestTimezoneSetting(t *testing.T) {
	t.Cleanup(func() { billing.SetZone(billing.DefaultZone) })
	e := newEnv(t)
	a := e.login("admin")
	var s struct {
		Timezone string `json:"timezone"`
	}
	a.do("GET", "/api/admin/settings", nil, &s)
	if s.Timezone != "Asia/Shanghai" {
		t.Fatalf("默认时区应是 Asia/Shanghai，实际 %q", s.Timezone)
	}
	if code, _ := a.do("PUT", "/api/admin/settings", map[string]string{"timezone": "Mars/Olympus"}, nil); code != 400 {
		t.Errorf("非法时区应拒绝: %d", code)
	}
	if code, body := a.do("PUT", "/api/admin/settings", map[string]string{"timezone": "Europe/Berlin"}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var me struct {
		Timezone string `json:"timezone"`
		Label    string `json:"timezone_label"`
	}
	a.do("GET", "/api/me", nil, &me)
	if me.Timezone != "Europe/Berlin" || me.Label != "Europe/Berlin" || billing.ZoneName() != "Europe/Berlin" {
		t.Errorf("保存后应立即生效: %+v，当前 %s", me, billing.ZoneName())
	}
}
