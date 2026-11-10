package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/store"
)

func TestAPIKeyExpiration(t *testing.T) {
	e := newEnv(t)
	c := e.login("bob")
	ctx := context.Background()
	future := time.Now().Add(24 * time.Hour).Truncate(time.Millisecond)
	var created struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	if code, body := c.do("POST", "/api/me/keys", map[string]any{"name": "temporary", "group_id": 1, "expires_at": future.Format(time.RFC3339Nano)}, &created); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	path := fmt.Sprintf("/api/me/keys/%d", created.ID)
	read := func() store.APIKey {
		t.Helper()
		var keys []store.APIKey
		if code, body := c.do("GET", "/api/me/keys", nil, &keys); code != 200 {
			t.Fatalf("list: %d %s", code, body)
		}
		for _, k := range keys {
			if k.ID == created.ID {
				return k
			}
		}
		t.Fatal("key missing")
		return store.APIKey{}
	}
	assertExpiry := func(want *time.Time) {
		t.Helper()
		k := read()
		if want == nil {
			if k.ExpiresAt != nil {
				t.Fatalf("want never: %v", k.ExpiresAt)
			}
			return
		}
		if k.ExpiresAt == nil || !k.ExpiresAt.Equal(*want) {
			t.Fatalf("expiry = %v, want %v", k.ExpiresAt, want)
		}
	}
	assertExpiry(&future)
	if code, body := c.do("PATCH", path, map[string]any{"name": "renamed", "enabled": false}, nil); code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	assertExpiry(&future) // 省略字段保留原值
	later := future.Add(time.Hour)
	if code, body := c.do("PATCH", path, map[string]any{"expires_at": later.In(time.FixedZone("local", 8*3600)).Format(time.RFC3339Nano)}, nil); code != 200 {
		t.Fatalf("extend: %d %s", code, body)
	}
	assertExpiry(&later)
	if code, body := e.login("admin").do("PATCH", path, map[string]any{"expires_at": nil}, nil); code != 404 {
		t.Fatalf("other owner: %d %s", code, body)
	}
	assertExpiry(&later)
	for _, bad := range []any{"invalid", "2030-01-01T12:00:00", 42, "", time.Now().Add(-time.Hour)} {
		if code, body := c.do("PATCH", path, map[string]any{"name": "must-not-change", "expires_at": bad}, nil); code != 400 {
			t.Fatalf("invalid patch %v: %d %s", bad, code, body)
		}
		if read().Name != "renamed" {
			t.Fatal("failed patch changed name")
		}
		if code, body := c.do("POST", "/api/me/keys", map[string]any{"name": "invalid", "group_id": 1, "expires_at": bad}, nil); code != 400 {
			t.Fatalf("invalid create %v: %d %s", bad, code, body)
		}
	}
	assertExpiry(&later)
	past := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := e.store.Pool.Exec(ctx, `UPDATE api_keys SET expires_at=$1 WHERE id=$2`, past, created.ID); err != nil {
		t.Fatal(err)
	}
	// 浏览器往返会去掉微秒，保留原时间时仍能编辑其他字段。
	if code, body := c.do("PATCH", path, map[string]any{"name": "expired-renamed", "expires_at": past.Truncate(time.Millisecond)}, nil); code != 200 {
		t.Fatalf("unchanged expired: %d %s", code, body)
	}
	assertExpiry(&past)
	if code, body := c.do("PATCH", path, map[string]any{"expires_at": nil}, nil); code != 200 {
		t.Fatalf("clear: %d %s", code, body)
	}
	assertExpiry(nil)
	var changes []byte
	if err := e.store.Pool.QueryRow(ctx, `SELECT changes FROM operation_logs WHERE target_type='api_key' AND target_id=$1 ORDER BY id DESC LIMIT 1`, fmt.Sprint(created.ID)).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	var diff []store.Change
	if err := json.Unmarshal(changes, &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff) != 1 || diff[0].Field != "expires_at" || string(diff[0].To) != "null" {
		t.Fatalf("expiry audit: %s", changes)
	}
	for _, body := range []map[string]any{{"name": "omitted", "group_id": 1}, {"name": "null", "group_id": 1, "expires_at": nil}} {
		if code, raw := c.do("POST", "/api/me/keys", body, &created); code != 201 {
			t.Fatalf("permanent: %d %s", code, raw)
		}
		assertExpiry(nil)
	}
	_, raw := c.do("GET", "/api/me/keys", nil, nil)
	if strings.Contains(raw, created.Key) {
		t.Fatal("list exposed full key")
	}
}
