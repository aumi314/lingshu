package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegistrationRestrictions(t *testing.T) {
	s := emailStore(t)
	ctx := context.Background()
	set := func(mode string) {
		t.Helper()
		_, e := s.Pool.Exec(ctx, `UPDATE settings SET value=$1 WHERE key='registration_mode'`, mode)
		if e != nil {
			t.Fatal(e)
		}
	}
	set("open")
	if e := s.RegisterUser(ctx, "open-user", "hash", "", "", "", "open-session", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	set("both")
	admin, e := s.CreateUser(ctx, "admin", "hash", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	codes, e := s.CreateRegistrationCodes(ctx, admin, 1)
	if e != nil {
		t.Fatal(e)
	}
	email := "both@example.com"
	hash := EmailCodeHash(email, "123456")
	if e = s.ClaimEmailCode(ctx, email, hash); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishEmailCode(ctx, email, hash, "submitted", true); e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterUser(ctx, "both-user", "hash", codes[0].Code, email, "000000", "both-session", time.Now().Add(time.Hour)); !errors.Is(e, ErrEmailCode) {
		t.Fatal(e)
	}
	var used bool
	if e = s.Pool.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM registration_codes WHERE id=$1`, codes[0].ID).Scan(&used); e != nil || used {
		t.Fatalf("code consumed on email failure: %v", e)
	}
	if e = s.RegisterUser(ctx, "open-user", "hash", codes[0].Code, email, "123456", "both-session", time.Now().Add(time.Hour)); !errors.Is(e, ErrUsernameTaken) {
		t.Fatal(e)
	}
	if e = s.RegisterUser(ctx, "both-user", "hash", codes[0].Code, email, "123456", "both-session", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	set("manual")
	if e = s.RegisterUser(ctx, "closed", "hash", "", "", "", "closed", time.Now()); !errors.Is(e, ErrRegistrationClosed) {
		t.Fatal(e)
	}
}
func TestRegistrationFlagFallbackAndOverrides(t *testing.T) {
	s := emailStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		mode                 string
		enabled, code, email bool
	}{{"manual", false, false, false}, {"open", true, false, false}, {"code", true, true, false}, {"email", true, false, true}, {"both", true, true, true}} {
		s.Pool.Exec(ctx, `UPDATE settings SET value=$1 WHERE key='registration_mode'`, tc.mode)
		f, e := s.Registration(ctx)
		if e != nil || f.Enabled != tc.enabled || f.CodeRequired != tc.code || f.EmailRequired != tc.email {
			t.Fatalf("%s: %+v %v", tc.mode, f, e)
		}
	}
	s.Pool.Exec(ctx, `INSERT INTO settings(key,value) VALUES('registration_enabled','false'),('registration_code_required','true'),('registration_email_required','true')`)
	f, e := s.Registration(ctx)
	if e != nil || f.Enabled || !f.CodeRequired || !f.EmailRequired {
		t.Fatalf("disabled requirements: %+v %v", f, e)
	}
}
