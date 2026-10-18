package store

import (
	"context"
	"os"
	"testing"
)

// 需要真实数据库：设置 LINGSHU_TEST_DATABASE_URL（make test 会自动设置）。
// 测试会清空并重建 public schema，不要指向正式库。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("LINGSHU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("没有设置 LINGSHU_TEST_DATABASE_URL，跳过数据库测试")
	}
	ctx := context.Background()
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrateCreatesAllTablesAndIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("第 %d 次迁移失败: %v", i+1, err)
		}
	}

	want := []string{"users", "groups", "user_groups", "models", "channels", "channel_groups",
		"channel_models", "api_keys", "request_logs", "balance_adjustments", "settings"}
	for _, table := range want {
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("缺少表 %s", table)
		}
	}

	var rate string
	if err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'cny_per_usd'`).Scan(&rate); err != nil || rate != "6.7" {
		t.Errorf("默认汇率不对: %q %v", rate, err)
	}
}

func TestCreateUserRejectsDuplicateUsername(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "alice", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "alice", "hash", "user"); err != ErrUsernameTaken {
		t.Fatalf("重复用户名应返回 ErrUsernameTaken，实际: %v", err)
	}
}
