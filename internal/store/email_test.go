package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func emailStore(t *testing.T) *Store {
	s := openTestStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := s.Pool.Exec(context.Background(), `INSERT INTO settings(key,value) VALUES('registration_mode','email') ON CONFLICT(key) DO UPDATE SET value='email'`)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestEmailCodeRegistrationTransactions(t *testing.T) {
	s := emailStore(t)
	ctx := context.Background()
	email := "person@example.com"
	hash := EmailCodeHash(email, "123456")
	if err := s.ClaimEmailCode(ctx, email, hash); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimEmailCode(ctx, email, hash); !errors.Is(err, ErrEmailCooldown) {
		t.Fatalf("cooldown: %v", err)
	}
	if err := s.FinishEmailCode(ctx, email, hash, "submitted", true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.RegisterUserWithEmail(ctx, "person", "hash", email, "000000", "session", time.Now().Add(time.Hour)); !errors.Is(err, ErrEmailCode) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := s.RegisterUserWithEmail(ctx, "person", "hash", email, "123456", "session", time.Now().Add(time.Hour)); !errors.Is(err, ErrEmailCode) {
		t.Fatalf("attempt cap: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE registration_emails SET attempts=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO users(username,password_hash,role) VALUES('person','hash','user')`); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterUserWithEmail(ctx, "person", "hash", email, "123456", "session", time.Now().Add(time.Hour)); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("username conflict %v", err)
	}
	if err := s.RegisterUserWithEmail(ctx, "new-person", "hash", email, "123456", "session", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users u JOIN user_group_members m ON m.user_id=u.id JOIN sessions s ON s.user_id=u.id WHERE email=$1`, email).Scan(&count); err != nil || count != 1 {
		t.Fatalf("created default member session %d %v", count, err)
	}
	if err := s.ClaimEmailCode(ctx, email, hash); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("email unique: %v", err)
	}
}
func TestEmailConcurrentClaimAndRedeem(t *testing.T) {
	s := emailStore(t)
	ctx := context.Background()
	email := "parallel@example.com"
	hash := EmailCodeHash(email, "123456")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.ClaimEmailCode(ctx, email, hash) }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("claims %d", success)
	}
	if err := s.RegisterUserWithEmail(ctx, "pending", "hash", email, "123456", "pending", time.Now().Add(time.Hour)); !errors.Is(err, ErrEmailCode) {
		t.Fatalf("pending: %v", err)
	}
	if err := s.FinishEmailCode(ctx, email, hash, "unknown", true); err != nil {
		t.Fatal(err)
	}
	results = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.RegisterUserWithEmail(ctx, "parallel", "hash", email, "123456", "session", time.Now().Add(time.Hour))
		}()
	}
	wg.Wait()
	close(results)
	success = 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("redeems %d", success)
	}
}
func TestEmailExpiredClosedAndFailedResend(t *testing.T) {
	s := emailStore(t)
	ctx := context.Background()
	email := "expiry@example.com"
	hash := EmailCodeHash(email, "123456")
	if err := s.ClaimEmailCode(ctx, email, hash); err != nil {
		t.Fatal(err)
	}
	s.FinishEmailCode(ctx, email, hash, "submitted", true)
	s.Pool.Exec(ctx, `UPDATE registration_emails SET last_sent_at=now()-interval '61 seconds'`)
	second := EmailCodeHash(email, "654321")
	if err := s.ClaimEmailCode(ctx, email, second); err != nil {
		t.Fatal(err)
	}
	s.FinishEmailCode(ctx, email, second, "failed", false)
	var got string
	s.Pool.QueryRow(ctx, `SELECT code_hash FROM registration_emails`).Scan(&got)
	if got != hash {
		t.Fatal("failed resend lost original code")
	}
	s.Pool.Exec(ctx, `UPDATE registration_emails SET expires_at=now()-interval '1 second'`)
	if err := s.RegisterUserWithEmail(ctx, "expired", "hash", email, "123456", "session", time.Now()); !errors.Is(err, ErrEmailCode) {
		t.Fatalf("expiry %v", err)
	}
	s.Pool.Exec(ctx, `UPDATE settings SET value='manual' WHERE key='registration_mode'`)
	if err := s.ClaimEmailCode(ctx, "closed@example.com", hash); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatal(err)
	}
	if err := s.RegisterUserWithEmail(ctx, "closed", "hash", email, "123456", "session", time.Now()); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatal(err)
	}
}
