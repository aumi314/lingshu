package api

import (
	"strings"
	"sync"
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

func TestRegistrationCodeFlow(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	var status struct {
		Enabled bool `json:"enabled"`
	}
	if code, body := e.client().do("GET", "/api/auth/registration", nil, &status); code != 200 || status.Enabled {
		t.Fatalf("registration should default to closed: %d %s", code, body)
	}
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_mode": "email"}, nil); code != 400 {
		t.Fatalf("email registration must stay unavailable: %d %s", code, body)
	}

	var generated []store.NewRegistrationCode
	if code, body := admin.do("POST", "/api/admin/registration-codes", map[string]any{"count": 3}, &generated); code != 201 || len(generated) != 3 {
		t.Fatalf("generate multiple codes: %d %s", code, body)
	}
	if generated[0].Code == generated[1].Code || generated[1].Code == generated[2].Code {
		t.Fatal("generated codes must be unique")
	}
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_mode": "code"}, nil); code != 200 {
		t.Fatalf("enable code registration: %d %s", code, body)
	}
	if code, body := e.client().do("GET", "/api/auth/registration", nil, &status); code != 200 || !status.Enabled {
		t.Fatalf("registration should be open: %d %s", code, body)
	}

	public := e.client()
	newUser := map[string]string{"username": "invitee", "password": "a-secure-password", "code": generated[0].Code}
	if code, body := public.do("POST", "/api/auth/register", newUser, nil); code != 201 {
		t.Fatalf("register with a valid code: %d %s", code, body)
	}
	var me struct {
		User struct {
			ID           int64   `json:"id"`
			Username     string  `json:"username"`
			Role         string  `json:"role"`
			UserGroupIDs []int64 `json:"user_group_ids"`
		} `json:"user"`
	}
	if code, body := public.do("GET", "/api/me", nil, &me); code != 200 || me.User.Username != "invitee" || me.User.Role != "user" {
		t.Fatalf("registration should create and sign in a member: %d %s %+v", code, body, me.User)
	}
	if len(me.User.UserGroupIDs) != 1 || me.User.UserGroupIDs[0] != 1 {
		t.Fatalf("new user should join default: %+v", me.User.UserGroupIDs)
	}

	if code, body := e.client().do("POST", "/api/auth/register", newUser, nil); code != 400 || !strings.Contains(body, "注册码") {
		t.Fatalf("used code must be rejected: %d %s", code, body)
	}
	if code, body := admin.do("POST", "/api/admin/registration-codes", map[string]any{"count": 0}, nil); code != 400 {
		t.Fatalf("invalid batch size should be rejected: %d %s", code, body)
	}

	var listed []store.RegistrationCode
	if code, body := admin.do("GET", "/api/admin/registration-codes", nil, &listed); code != 200 || len(listed) != 3 {
		t.Fatalf("list registration codes: %d %s", code, body)
	}
	if listed[0].UsedAt == nil && listed[1].UsedAt == nil && listed[2].UsedAt == nil {
		t.Fatalf("redeemed code should be marked used: %+v", listed)
	}
	listedRaw := mustRaw(t, admin, "/api/admin/registration-codes")
	for _, c := range generated {
		if strings.Contains(listedRaw, c.Code) {
			t.Fatal("code listing must not reveal full codes")
		}
	}

	if code, body := admin.do("DELETE", "/api/admin/registration-codes/"+itoa(generated[1].ID), nil, nil); code != 200 {
		t.Fatalf("revoke unused code: %d %s", code, body)
	}
	if code, body := e.client().do("POST", "/api/auth/register", map[string]string{"username": "revoked", "password": "a-secure-password", "code": generated[1].Code}, nil); code != 400 {
		t.Fatalf("revoked code must be rejected: %d %s", code, body)
	}

	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_mode": "manual"}, nil); code != 200 {
		t.Fatalf("close code registration: %d %s", code, body)
	}
	if code, body := e.client().do("POST", "/api/auth/register", map[string]string{"username": "closed", "password": "a-secure-password", "code": generated[2].Code}, nil); code != 403 {
		t.Fatalf("closed registration must reject valid codes: %d %s", code, body)
	}
}

func TestRegistrationCodeRedemptionIsAtomic(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	var generated []store.NewRegistrationCode
	if code, body := admin.do("POST", "/api/admin/registration-codes", map[string]any{"count": 2}, &generated); code != 201 {
		t.Fatalf("generate codes: %d %s", code, body)
	}
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_mode": "code"}, nil); code != 200 {
		t.Fatalf("enable code registration: %d %s", code, body)
	}

	duplicate := map[string]string{"username": "bob", "password": "a-secure-password", "code": generated[0].Code}
	if code, body := e.client().do("POST", "/api/auth/register", duplicate, nil); code != 409 {
		t.Fatalf("duplicate username should fail: %d %s", code, body)
	}
	valid := map[string]string{"username": "after-duplicate", "password": "a-secure-password", "code": generated[0].Code}
	if code, body := e.client().do("POST", "/api/auth/register", valid, nil); code != 201 {
		t.Fatalf("failed registration must not consume code: %d %s", code, body)
	}

	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, username := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(username string) {
			defer wg.Done()
			<-start
			input := map[string]string{"username": username, "password": "a-secure-password", "code": generated[1].Code}
			status, _ := e.client().do("POST", "/api/auth/register", input, nil)
			results <- status
		}(username)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for status := range results {
		if status == 201 {
			successes++
		} else if status != 400 {
			t.Errorf("concurrent code redemption status = %d, want 201 or 400", status)
		}
	}
	if successes != 1 {
		t.Fatalf("one code must create exactly one account; succeeded %d times", successes)
	}
}

func mustRaw(t *testing.T, c *client, path string) string {
	t.Helper()
	code, raw := c.do("GET", path, nil, nil)
	if code != 200 {
		t.Fatalf("GET %s: %d %s", path, code, raw)
	}
	return raw
}
