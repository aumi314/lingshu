package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aumi314/lingshu/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setupMail(t *testing.T, e *env) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			r.ParseForm()
			if r.Form.Get("client_id") == "invalid" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"access-secret","refresh_token":"rotated-secret","expires_in":3600,"scope":"Mail.Send"}`)
			return
		}
		calls.Add(1)
		w.WriteHeader(202)
	}))
	t.Cleanup(srv.Close)
	e.api.mail.tokenURL = srv.URL + "/token"
	e.api.mail.graphURL = srv.URL + "/send"
	return srv, calls
}
func addMail(t *testing.T, e *env, client string, order int) store.MailAccount {
	t.Helper()
	a := store.MailAccount{Email: fmt.Sprintf("sender%d@example.com", order), ClientID: client, RefreshToken: "refresh-secret", Enabled: true, SortOrder: order}
	id, err := e.store.SaveMailAccount(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = id
	return a
}
func TestMailTokenRefreshFailoverAndNoDuplicate(t *testing.T) {
	e := newEnv(t)
	_, calls := setupMail(t, e)
	bad := addMail(t, e, "invalid", 0)
	good := addMail(t, e, "valid", 1)
	ctx := context.Background()
	if err := e.api.mail.send(ctx, e.store, []store.MailAccount{bad, good}, "receiver@example.com", "test", "body"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("expected one send")
	}
	accounts, _ := e.store.MailAccounts(ctx)
	if accounts[1].RefreshToken != "rotated-secret" {
		t.Fatal("refresh rotation not persisted")
	}
	var tokenCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		fmt.Fprint(w, `{"access_token":"cached","expires_in":3600,"scope":"Mail.Send"}`)
	}))
	defer srv.Close()
	e.api.mail.tokenURL = srv.URL
	for i := 0; i < 2; i++ {
		if _, err := e.api.mail.token(ctx, e.store, good, false); err != nil {
			t.Fatal(err)
		}
	}
	if tokenCalls.Load() != 0 {
		t.Fatal("cache not used")
	}
	var graphStatus atomic.Int32
	graphStatus.Store(429)
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(int(graphStatus.Load()))
	}))
	defer graph.Close()
	e.api.mail.graphURL = graph.URL
	err := e.api.mail.send(ctx, e.store, []store.MailAccount{good, bad}, "receiver@example.com", "test", "body")
	var me *mailError
	if !errors.As(err, &me) || me.retry != 120 {
		t.Fatalf("429: %v", err)
	}
	before := calls.Load()
	_ = e.api.mail.send(ctx, e.store, []store.MailAccount{good, bad}, "receiver@example.com", "test", "body")
	if calls.Load() != before {
		t.Fatal("cooldown bypassed")
	}
	e.api.mail.cooldown = time.Time{}
	graphStatus.Store(503)
	err = e.api.mail.send(ctx, e.store, []store.MailAccount{good, bad}, "receiver@example.com", "test", "body")
	if !errors.As(err, &me) || !me.uncertain {
		t.Fatalf("uncertain: %v", err)
	}
	if calls.Load() != before+1 {
		t.Fatal("uncertain send retried")
	}
}
func TestMailAccountsPermissionsSecretsAndReadiness(t *testing.T) {
	e := newEnv(t)
	setupMail(t, e)
	admin := e.login("admin")
	if code, _ := admin.do("GET", "/api/admin/mail-accounts", nil, nil); code != 403 {
		t.Fatal(code)
	}
	e.store.Pool.Exec(context.Background(), `UPDATE users SET role='super_admin' WHERE id=1`)
	admin = e.login("admin")
	if code, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_mode": "email"}, nil); code != 400 {
		t.Fatal(code)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if code, body := admin.do("POST", "/api/admin/mail-accounts", map[string]any{"email": "sender@example.com", "client_id": "valid", "refresh_token": "refresh-secret", "enabled": true, "sort_order": 0}, &created); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := admin.do("POST", fmt.Sprintf("/api/admin/mail-accounts/%d/check", created.ID), nil, nil); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_mode": "email"}, nil); code != 200 {
		t.Fatal(code, body)
	}
	_, raw := admin.do("GET", "/api/admin/mail-accounts", nil, nil)
	if strings.Contains(raw, "refresh-secret") || strings.Contains(raw, "rotated-secret") || strings.Contains(raw, "access-secret") {
		t.Fatal("secret leaked")
	}
	var logs string
	e.store.Pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(changes::text,' '),'') FROM operation_logs`).Scan(&logs)
	if strings.Contains(logs, "secret") {
		t.Fatal("audit secret leaked")
	}
	if code, body := admin.do("PUT", fmt.Sprintf("/api/admin/mail-accounts/%d", created.ID), map[string]any{"email": "sender@example.com", "client_id": "changed", "refresh_token": "", "enabled": true}, nil); code != 200 {
		t.Fatal(code, body)
	}
	accounts, _ := e.store.MailAccounts(context.Background())
	if accounts[0].CheckedAt != nil || accounts[0].RefreshToken != "rotated-secret" {
		t.Fatal("credential update did not invalidate check or preserve secret")
	}
}
func TestEmailRegistrationAPI(t *testing.T) {
	e := newEnv(t)
	_, _ = setupMail(t, e)
	ctx := context.Background()
	a := addMail(t, e, "valid", 0)
	e.api.mail.token(ctx, e.store, a, true)
	e.store.MailResult(ctx, a.ID, "authorized", "", true)
	e.store.Pool.Exec(ctx, `UPDATE users SET role='super_admin' WHERE id=1`)
	admin := e.login("admin")
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_mode": "email"}, nil); code != 200 {
		t.Fatal(code, body)
	}
	anon := e.client()
	var status struct {
		Enabled bool
		Mode    string
	}
	anon.do("GET", "/api/auth/registration", nil, &status)
	if !status.Enabled || status.Mode != "email" {
		t.Fatal(status)
	}
	if code, _ := anon.do("POST", "/api/auth/email-code", map[string]any{"email": "person@example.com"}, nil); code != 428 {
		t.Fatal(code)
	}
	var captured string
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message struct{ Body struct{ Content string } }
		}
		json.NewDecoder(r.Body).Decode(&body)
		text := body.Message.Body.Content
		start := strings.Index(text, "是 ")
		if start >= 0 {
			captured = text[start+len("是 ") : start+len("是 ")+6]
		}
		w.WriteHeader(202)
	}))
	defer graph.Close()
	e.api.mail.graphURL = graph.URL
	challenge := e.api.guard.newChallenge()
	if code, body := anon.do("POST", "/api/auth/email-code", map[string]string{"email": "person@example.com", "captcha_token": challenge, "captcha_nonce": solve(challenge)}, nil); code != 200 {
		t.Fatal(code, body)
	}
	if len(captured) != 6 {
		t.Fatal("missing verification code")
	}
	email := "person@example.com"
	if code, body := anon.do("POST", "/api/auth/register", map[string]string{"username": "new-person", "email": email, "password": "strong-password-123", "email_code": captured}, nil); code != 201 {
		t.Fatal(code, body)
	}
	if code, _ := anon.do("GET", "/api/me", nil, nil); code != 200 {
		t.Fatal(code)
	}
	login := e.client()
	if code, body := login.do("POST", "/api/auth/login", map[string]string{"username": "new-person", "password": "strong-password-123"}, nil); code != 200 {
		t.Fatal(code, body)
	}
	if code, _ := e.client().do("POST", "/api/auth/login", map[string]string{"username": email, "password": "strong-password-123"}, nil); code != 401 {
		t.Fatal("email must not log in", code)
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), "refresh-secret") {
		t.Fatal("secret leaked")
	}
}

func TestMailConcurrentRefreshAndTimeout(t *testing.T) {
	e := newEnv(t)
	a := addMail(t, e, "valid", 0)
	var refreshes, sends atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			refreshes.Add(1)
			time.Sleep(10 * time.Millisecond)
			fmt.Fprint(w, `{"access_token":"access","refresh_token":"rotated","expires_in":3600,"scope":"Mail.Send"}`)
			return
		}
		sends.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(202)
	}))
	defer srv.Close()
	e.api.mail.tokenURL = srv.URL + "/token"
	e.api.mail.graphURL = srv.URL + "/send"
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.api.mail.token(context.Background(), e.store, a, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatal("concurrent refresh duplicated", refreshes.Load())
	}
	e.api.mail.client = &http.Client{Timeout: 30 * time.Millisecond}
	err := e.api.mail.send(context.Background(), e.store, []store.MailAccount{a, a}, "receiver@example.com", "test", "body")
	var me *mailError
	if !errors.As(err, &me) || !me.uncertain || sends.Load() != 1 {
		t.Fatal("timeout retried", err, sends.Load())
	}
}
func TestMailOAuthThrottleAndConfigRace(t *testing.T) {
	e := newEnv(t)
	a := addMail(t, e, "valid", 0)
	all, _ := e.store.MailAccounts(context.Background())
	a = all[0]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "77"); w.WriteHeader(429) }))
	defer srv.Close()
	e.api.mail.tokenURL = srv.URL
	_, err := e.api.mail.token(context.Background(), e.store, a, true)
	var me *mailError
	if !errors.As(err, &me) || me.retry != 77 {
		t.Fatal("empty OAuth throttle response", err)
	}
	changed := a
	changed.ClientID = "new-client"
	changed.RefreshToken = ""
	if _, err := e.store.SaveMailAccount(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MailResult(context.Background(), a.ID, "authorized", "", true, a.Version); err != nil {
		t.Fatal(err)
	}
	all, _ = e.store.MailAccounts(context.Background())
	if all[0].CheckedAt != nil {
		t.Fatal("old authorization accepted edited credentials")
	}
	if err := e.store.RotateMailToken(context.Background(), a.ID, a.ClientID, a.RefreshToken, "bad-rotation"); err == nil {
		t.Fatal("old client rotated changed config")
	}
	if _, err := e.api.mail.token(context.Background(), e.store, a, false); err == nil {
		t.Fatal("stale sender snapshot accepted")
	}
}
