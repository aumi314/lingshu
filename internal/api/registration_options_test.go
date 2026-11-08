package api

import (
	"context"
	"github.com/aumi314/lingshu/internal/store"
	"testing"
)

func TestRegistrationSettingsRestrictions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.store.Pool.Exec(ctx, `UPDATE users SET role='super_admin' WHERE id=1`)
	admin := e.login("admin")
	var status store.RegistrationFlags
	check := func(want store.RegistrationFlags) {
		t.Helper()
		if code, body := e.client().do("GET", "/api/auth/registration", nil, &status); code != 200 || status != want {
			t.Fatalf("flags %d %s %+v", code, body, status)
		}
	}
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_enabled": true, "registration_code_required": false, "registration_email_required": false}, nil); code != 200 {
		t.Fatal(code, body)
	}
	check(store.RegistrationFlags{Enabled: true})
	if code, body := e.client().do("POST", "/api/auth/register", map[string]string{"username": "open-signup", "password": "a-secure-password"}, nil); code != 201 {
		t.Fatal(code, body)
	}
	if code, _ := e.client().do("POST", "/api/auth/email-code", map[string]string{"email": "open@example.com"}, nil); code != 403 {
		t.Fatal(code)
	}
	admin.do("PUT", "/api/admin/settings", map[string]any{"registration_code_required": true}, nil)
	check(store.RegistrationFlags{Enabled: true, CodeRequired: true})
	if code, _ := e.client().do("POST", "/api/auth/register", map[string]string{"username": "missing-code", "password": "a-secure-password"}, nil); code != 400 {
		t.Fatal(code)
	}
	if code, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_email_required": true}, nil); code != 400 {
		t.Fatal(code)
	}
	a, err := e.store.SaveMailAccount(ctx, store.MailAccount{Email: "sender@example.com", ClientID: "client", RefreshToken: "private-test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e.store.MailResult(ctx, a, "authorized", "", true)
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_email_required": true}, nil); code != 200 {
		t.Fatal(code, body)
	}
	check(store.RegistrationFlags{Enabled: true, CodeRequired: true, EmailRequired: true})
	e.store.MailResult(ctx, a, "authorization_error", "expired", false)
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_enabled": false}, nil); code != 200 {
		t.Fatal(code, body)
	}
	check(store.RegistrationFlags{CodeRequired: true, EmailRequired: true})
	if code, _ := e.client().do("POST", "/api/auth/register", map[string]string{"username": "closed", "password": "a-secure-password"}, nil); code != 403 {
		t.Fatal(code)
	}
	if code, _ := e.client().do("POST", "/api/auth/email-code", nil, nil); code != 403 {
		t.Fatal(code)
	}
}
