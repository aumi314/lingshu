package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"sort"
	"strings"
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

	want := []string{"users", "groups", "user_groups", "user_group_members", "group_user_groups", "models", "channels", "channel_groups",
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

func TestRemoveModelEnabledPreservesModelsAndMappings(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasSuffix(name, "0012_remove_model_enabled.sql") {
			break
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		if _, err := s.Pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO models (id, name, input_price, output_price, cache_read_price, cache_write_price, enabled, price_rules) VALUES (7, 'free-model', 0, 0, 0, 0, false, '[]'::jsonb)`,
		`INSERT INTO channels (id, name, api_key) VALUES (4, 'supplier', 'secret')`,
		`INSERT INTO channel_models (channel_id, model_id, upstream_name) VALUES (4, 7, 'upstream-free')`,
	} {
		if _, err := s.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var hasEnabled bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'models' AND column_name = 'enabled')`).Scan(&hasEnabled); err != nil {
		t.Fatal(err)
	}
	if hasEnabled {
		t.Fatal("models.enabled 列仍存在")
	}
	var id int64
	var name, input, output, cacheRead, cacheWrite, rules, upstream string
	if err := s.Pool.QueryRow(ctx, `SELECT m.id, m.name, m.input_price::text, m.output_price::text, m.cache_read_price::text, m.cache_write_price::text, m.price_rules::text, cm.upstream_name FROM models m JOIN channel_models cm ON cm.model_id = m.id WHERE m.id = 7`).Scan(&id, &name, &input, &output, &cacheRead, &cacheWrite, &rules, &upstream); err != nil {
		t.Fatal(err)
	}
	if id != 7 || name != "free-model" || input != "0.0000000000" || output != "0.0000000000" || cacheRead != "0.0000000000" || cacheWrite != "0.0000000000" || rules != "[]" || upstream != "upstream-free" {
		t.Fatalf("迁移后模型或映射发生变化: id=%d name=%q prices=%s/%s/%s/%s rules=%q upstream=%q", id, name, input, output, cacheRead, cacheWrite, rules, upstream)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
}

func TestUserGroupMigrationPreservesSpecialGroupAccess(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.Contains(name, "0011_user_groups_and_group_scope.sql") {
			break
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		if _, err := s.Pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'alice', 'x'), (2, 'bob', 'x'), (3, 'carol', 'x')`,
		`SELECT setval('users_id_seq', 3)`,
		`INSERT INTO groups (id, name, is_public) VALUES (1, 'public', true), (2, 'private-a', false), (3, 'private-b', false)`,
		`INSERT INTO user_groups (user_id, group_id) VALUES (1, 2), (2, 3)`,
	} {
		if _, err := s.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		userID int64
		want   []string
	}{
		{1, []string{"private-a", "public"}},
		{2, []string{"private-b", "public"}},
		{3, []string{"public"}},
	} {
		groups, err := s.AccessibleGroups(ctx, tc.userID)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(groups))
		for i, g := range groups {
			got[i] = g.Name
		}
		sort.Strings(got)
		if len(got) != len(tc.want) {
			t.Errorf("user %d groups = %v, want %v", tc.userID, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("user %d groups = %v, want %v", tc.userID, got, tc.want)
				break
			}
		}
	}

	newID, err := s.CreateUser(ctx, "dave", "x", "user")
	if err != nil {
		t.Fatal(err)
	}
	var inDefault bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM user_group_members ugm JOIN user_groups ug ON ug.id = ugm.user_group_id
		WHERE ugm.user_id = $1 AND ug.is_default)`, newID).Scan(&inDefault); err != nil {
		t.Fatal(err)
	}
	if !inDefault {
		t.Error("new user was not added to default user group")
	}
}

func TestMigration0010KeepsWeekendRulesAndDeletesHolidaySetting(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO models (name, price_rules) VALUES ($1, $2::jsonb)`,
		"deepseek-v4.1-flash", `[{"name":"低谷（周末和节假日）","days":"non_workday","input_price":"0.15","output_price":"0.6","cache_read_price":"0.003","cache_write_price":"0"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('cn_holidays', '["2027-01-01"]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = '0010_weekend_only'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.Pool.QueryRow(ctx, `SELECT price_rules::text FROM models WHERE name = $1`, "deepseek-v4.1-flash").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var rules []struct {
		Name string `json:"name"`
		Days string `json:"days"`
	}
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Days != "weekend" || rules[0].Name != "低谷（周末）" {
		t.Errorf("周末规则应转换为新条件并更新名称: %+v", rules)
	}
	var holidays int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM settings WHERE key = 'cn_holidays'`).Scan(&holidays); err != nil || holidays != 0 {
		t.Errorf("节假日设置应删除: count=%d err=%v", holidays, err)
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
