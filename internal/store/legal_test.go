package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func enableOpenRegistration(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.Pool.Exec(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('registration_enabled','true') ON CONFLICT (key) DO UPDATE SET value='true'`); err != nil {
		t.Fatal(err)
	}
}

func TestLegalVersionIsDeterministicAndBoundToContent(t *testing.T) {
	v1 := LegalVersion("用户协议", "隐私政策")
	if v1 != LegalVersion("用户协议", "隐私政策") {
		t.Error("同样的文本应得到同样的版本号")
	}
	if len(v1) != 64 {
		t.Errorf("版本号应是 sha256 十六进制: %q", v1)
	}
	if v1 == LegalVersion("用户协议改了", "隐私政策") {
		t.Error("改了用户协议，版本号应变化")
	}
	if v1 == LegalVersion("用户协议", "隐私政策改了") {
		t.Error("改了隐私政策，版本号应变化")
	}
	if LegalVersion("a", "b") == LegalVersion("ab", "") {
		t.Error("不同文本不应得到相同版本号")
	}
}

func TestRegisterUserWithConsentRequiresAgreementAndCurrentVersion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	enableOpenRegistration(t, s)
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ('user_agreement','用户协议正文'),('privacy_policy','隐私政策正文')`); err != nil {
		t.Fatal(err)
	}
	docs, err := s.LegalDocuments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !docs.Required() {
		t.Fatal("配置了协议后应要求同意")
	}
	if docs.Version != LegalVersion("用户协议正文", "隐私政策正文") {
		t.Fatal("版本号应由两份文本算出")
	}

	if err := s.RegisterUserWithConsent(ctx, "no-consent", "hash", "", "", "", "s1", false, docs.Version, time.Now().Add(time.Hour)); !errors.Is(err, ErrLegalNotAccepted) {
		t.Fatalf("没勾选同意应被拒绝: %v", err)
	}
	if err := s.RegisterUserWithConsent(ctx, "stale", "hash", "", "", "", "s2", true, "0123456789abcdef", time.Now().Add(time.Hour)); !errors.Is(err, ErrLegalVersionMismatch) {
		t.Fatalf("版本对不上应被拒绝: %v", err)
	}
	if err := s.RegisterUser(ctx, "legacy", "hash", "", "", "", "s3", time.Now().Add(time.Hour)); !errors.Is(err, ErrLegalNotAccepted) {
		t.Fatalf("旧入口在配置协议后应被拒绝: %v", err)
	}
	if err := s.RegisterUserWithConsent(ctx, "ok-user", "hash", "", "", "", "s4", true, docs.Version, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("同意且版本正确应注册成功: %v", err)
	}

	var version string
	var accepted bool
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(legal_version,''), legal_accepted_at IS NOT NULL FROM users WHERE username='ok-user'`).Scan(&version, &accepted); err != nil {
		t.Fatal(err)
	}
	if version != docs.Version {
		t.Errorf("应记录同意的版本号: got %q want %q", version, docs.Version)
	}
	if !accepted {
		t.Error("应记录同意时间")
	}

	if _, err := s.CreateUser(ctx, "admin-made", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(legal_version,''), legal_accepted_at IS NOT NULL FROM users WHERE username='admin-made'`).Scan(&version, &accepted); err != nil {
		t.Fatal(err)
	}
	if version != "" || accepted {
		t.Error("管理员手动创建的用户不应补录同意记录")
	}
}

func TestRegisterUserWithoutLegalConfiguredStaysUnchanged(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	enableOpenRegistration(t, s)
	docs, err := s.LegalDocuments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if docs.Required() {
		t.Fatal("没配置协议时不应要求同意")
	}
	if err := s.RegisterUser(ctx, "plain-user", "hash", "", "", "", "s1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("没配置协议时旧注册入口应照旧可用: %v", err)
	}
	if err := s.RegisterUserWithConsent(ctx, "consent-user", "hash", "", "", "", "s2", false, "", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("没配置协议时带同意参数也应可用: %v", err)
	}
	var version string
	var accepted bool
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(legal_version,''), legal_accepted_at IS NOT NULL FROM users WHERE username='plain-user'`).Scan(&version, &accepted); err != nil {
		t.Fatal(err)
	}
	if version != "" || accepted {
		t.Error("没配置协议时不应记录同意")
	}
}

func TestLegalWhitespaceOnlyCountsAsNotConfigured(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	enableOpenRegistration(t, s)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('user_agreement','   ')`); err != nil {
		t.Fatal(err)
	}
	docs, err := s.LegalDocuments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if docs.Required() {
		t.Fatal("全是空白的协议按没配置算")
	}
	if err := s.RegisterUser(ctx, "blank-user", "hash", "", "", "", "s1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("协议是空白时注册不应要求同意: %v", err)
	}
}
