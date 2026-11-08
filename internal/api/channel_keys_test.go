package api

import (
	"strconv"
	"strings"
	"testing"
)

// 渠道多 key 的保存：自动去重、追加、覆盖（相同的 key 沿用原记录）、上限 50 把、渠道备注。
func TestChannelKeysSave(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	type key struct {
		ID      int64  `json:"id,omitempty"`
		APIKey  string `json:"api_key,omitempty"`
		Masked  string `json:"masked,omitempty"`
		Enabled bool   `json:"enabled"`
		Note    string `json:"note"`
		Status  *struct {
			State string `json:"state"`
		} `json:"status,omitempty"`
	}
	base := map[string]any{"name": "multi", "template": "custom", "base_url_chat": "https://up.example/v1", "enabled": true, "priority": 0, "group_ids": []int64{}, "models": []any{}}
	save := func(id int64, keys []key, note string) (int, map[string]int64, string) {
		body := map[string]any{"keys": keys, "note": note}
		for k, v := range base {
			body[k] = v
		}
		var out map[string]int64
		path, method := "/api/admin/channels", "POST"
		if id != 0 {
			path, method = "/api/admin/channels/"+strconv.FormatInt(id, 10), "PUT"
		}
		code, raw := a.do(method, path, body, &out)
		return code, out, raw
	}
	list := func(id int64) ([]key, string) {
		var chs []struct {
			ID   int64  `json:"id"`
			Note string `json:"note"`
			Keys []key  `json:"keys"`
		}
		a.do("GET", "/api/admin/channels", nil, &chs)
		for _, c := range chs {
			if c.ID == id {
				return c.Keys, c.Note
			}
		}
		t.Fatalf("没有渠道 %d", id)
		return nil, ""
	}

	// 新建：粘贴里有重复，自动去掉
	code, out, raw := save(0, []key{{APIKey: "sk-aaaa-1111-aaaa", Enabled: true}, {APIKey: " sk-aaaa-1111-aaaa ", Enabled: true}, {APIKey: "sk-bbbb-2222-bbbb", Enabled: true}}, " 质量好，0.15 倍 ")
	if code != 200 || out["keys_deduped"] != 1 {
		t.Fatalf("新建应去掉一把重复: %d %s", code, raw)
	}
	id := out["id"]
	keys, note := list(id)
	if len(keys) != 2 || note != "质量好，0.15 倍" || keys[0].Masked != "sk-a…aaaa" || keys[0].Status == nil || keys[0].Status.State != "ok" {
		t.Fatalf("列表: %+v 备注 %q", keys, note)
	}
	for _, k := range keys {
		if k.APIKey != "" {
			t.Fatal("列表不能返回完整 key")
		}
	}
	idA, idB := keys[0].ID, keys[1].ID

	// 追加：保留现有两把（只带 ID），再加 b（重复）和 c
	_, out, _ = save(id, []key{{ID: idA, Enabled: true}, {ID: idB, Enabled: false, Note: "备用"}, {APIKey: "sk-bbbb-2222-bbbb", Enabled: true}, {APIKey: "sk-cccc-3333-cccc", Enabled: true}}, "")
	keys, _ = list(id)
	if out["keys_deduped"] != 1 || len(keys) != 3 || keys[1].Enabled || keys[1].Note != "备用" {
		t.Fatalf("追加: 去重 %d %+v", out["keys_deduped"], keys)
	}
	idC := keys[2].ID

	// 覆盖：不带 ID，只给 c 和 d；c 和已有的相同，沿用原记录（ID 不变），a、b 被删
	_, out, _ = save(id, []key{{APIKey: "sk-cccc-3333-cccc", Enabled: true}, {APIKey: "sk-dddd-4444-dddd", Enabled: true}}, "")
	keys, _ = list(id)
	if len(keys) != 2 || keys[0].ID != idC || keys[1].Masked != "sk-d…dddd" || out["keys_deduped"] != 0 {
		t.Fatalf("覆盖: %+v", keys)
	}

	// 上限 50 把（按去重后算）、备注上限、至少一把
	var many []key
	for i := range 51 {
		many = append(many, key{APIKey: "sk-many-" + strconv.Itoa(i) + "-xxxxxx", Enabled: true})
	}
	if code, _, raw := save(id, many, ""); code != 400 || !strings.Contains(raw, "最多 50 把") {
		t.Errorf("超过 50 把应拒绝: %d %s", code, raw)
	}
	if code, _, _ := save(id, []key{{APIKey: "sk-x", Enabled: true}}, strings.Repeat("备", 501)); code != 400 {
		t.Errorf("备注超过 500 字应拒绝: %d", code)
	}
	if code, _, raw := save(id, []key{}, ""); code != 400 || !strings.Contains(raw, "至少要有一把") {
		t.Errorf("不能删光 key: %d %s", code, raw)
	}
}
