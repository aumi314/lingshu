package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/crypto/bcrypt"

	"github.com/aumi314/lingshu/internal/relay"
	"github.com/aumi314/lingshu/internal/store"
)

type env struct {
	t     *testing.T
	store *store.Store
	srv   *httptest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("LINGSHU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("没有设置 LINGSHU_TEST_DATABASE_URL，跳过接口测试")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("password-1"), bcrypt.MinCost)
	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash, role, balance) VALUES (1, 'admin', '` + string(hash) + `', 'admin', 0)`,
		`INSERT INTO users (id, username, password_hash, role, balance) VALUES (2, 'bob', '` + string(hash) + `', 'user', 5)`,
		`SELECT setval('users_id_seq', 2)`,
		`INSERT INTO groups (id, name, ratio, is_public) VALUES (1, 'default', 1, true), (2, 'vip', 0.5, false)`,
		`SELECT setval('groups_id_seq', 2)`,
		`INSERT INTO user_groups (id, name) VALUES (2, 'vip-users')`,
		`SELECT setval('user_groups_id_seq', 2)`,
		`INSERT INTO group_user_groups (group_id, user_group_id) VALUES (2, 2)`,
	} {
		if _, err := s.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	h := relay.NewHandler(s)
	web := fstest.MapFS{
		"index.html":      {Data: []byte("<html>lingshu</html>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}
	(&Server{Store: s, Relay: h, Web: web}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{t: t, store: s, srv: srv}
}

// client 是一个带 cookie 的浏览器会话。
type client struct {
	e    *env
	http *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar}}
}

func (e *env) login(username string) *client {
	c := e.client()
	if code, body := c.do("POST", "/api/auth/login", map[string]string{"username": username, "password": "password-1"}, nil); code != 200 {
		e.t.Fatalf("%s 登录失败: %d %s", username, code, body)
	}
	return c
}

// do 发请求，out 非 nil 时把响应解析进去。返回状态码和响应原文。
func (c *client) do(method, path string, in any, out any) (int, string) {
	c.e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.e.t.Fatalf("解析 %s 响应失败: %v %s", path, err, raw)
		}
	}
	return resp.StatusCode, string(raw)
}

func TestLoginAndSession(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if code, _ := c.do("GET", "/api/me", nil, nil); code != 401 {
		t.Errorf("没登录应返回 401，实际 %d", code)
	}
	if code, body := c.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "wrong"}, nil); code != 401 || !strings.Contains(body, "用户名或密码错误") {
		t.Errorf("密码错误: %d %s", code, body)
	}
	if code, body := c.do("POST", "/api/auth/login", map[string]string{"username": "nobody", "password": "x"}, nil); code != 401 || !strings.Contains(body, "用户名或密码错误") {
		t.Errorf("用户不存在的提示应和密码错误一样，避免暴露用户名: %d %s", code, body)
	}

	c = e.login("bob")
	var me struct {
		User struct {
			Username string `json:"username"`
			Balance  string `json:"balance"`
		} `json:"user"`
		CNYPerUSD string `json:"cny_per_usd"`
	}
	c.do("GET", "/api/me", nil, &me)
	if me.User.Username != "bob" || me.User.Balance != "5" || me.CNYPerUSD != "6.7" {
		t.Errorf("/api/me = %+v", me)
	}
	c.do("POST", "/api/auth/logout", nil, nil)
	if code, _ := c.do("GET", "/api/me", nil, nil); code != 401 {
		t.Errorf("退出后应返回 401，实际 %d", code)
	}

	// 停用账号后已有会话失效
	c = e.login("bob")
	admin := e.login("admin")
	admin.do("PATCH", "/api/admin/users/2", map[string]any{"enabled": false}, nil)
	if code, _ := c.do("GET", "/api/me", nil, nil); code != 401 {
		t.Errorf("账号停用后会话应失效，实际 %d", code)
	}
}

func TestPreferences(t *testing.T) {
	e := newEnv(t)
	c := e.login("bob")
	if code, body := c.do("PUT", "/api/me/preferences", map[string]string{"language": "en", "currency": "CNY", "theme": "dark"}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := c.do("PUT", "/api/me/preferences", map[string]string{"theme": "purple"}, nil); code != 400 {
		t.Errorf("不支持的主题应返回 400，实际 %d", code)
	}
	var me struct {
		User struct {
			Preferences map[string]string `json:"preferences"`
		} `json:"user"`
	}
	c.do("GET", "/api/me", nil, &me)
	if p := me.User.Preferences; p["language"] != "en" || p["currency"] != "CNY" || p["theme"] != "dark" {
		t.Errorf("偏好没保存: %+v", p)
	}
}

func TestChangePasswordLogsOutOtherDevices(t *testing.T) {
	e := newEnv(t)
	phone := e.login("bob")
	laptop := e.login("bob")
	if code, _ := laptop.do("PUT", "/api/me/password", map[string]string{"old_password": "wrong", "new_password": "new-password"}, nil); code != 400 {
		t.Errorf("旧密码错误应返回 400，实际 %d", code)
	}
	if code, body := laptop.do("PUT", "/api/me/password", map[string]string{"old_password": "password-1", "new_password": "new-password"}, nil); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := phone.do("GET", "/api/me", nil, nil); code != 401 {
		t.Errorf("改密码后其他设备应下线，实际 %d", code)
	}
	c := e.client()
	if code, _ := c.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "new-password"}, nil); code != 200 {
		t.Errorf("新密码登录失败: %d", code)
	}
}

func TestAPIKeys(t *testing.T) {
	e := newEnv(t)
	c := e.login("bob")

	// vip 是特殊分组，bob 没被授权
	if code, body := c.do("POST", "/api/me/keys", map[string]any{"name": "k", "group_id": 2}, nil); code != 400 || !strings.Contains(body, "分组") {
		t.Errorf("没授权的分组不能建 key: %d %s", code, body)
	}
	var created struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	if code, body := c.do("POST", "/api/me/keys", map[string]any{"name": "laptop", "group_id": 1, "quota_limit": "2.5"}, &created); code != 201 {
		t.Fatalf("%d %s", code, body)
	}
	if !strings.HasPrefix(created.Key, "sk-ls-") {
		t.Fatalf("应返回完整 key，实际 %q", created.Key)
	}

	var keys []struct {
		ID         int64   `json:"id"`
		Prefix     string  `json:"prefix"`
		QuotaLimit *string `json:"quota_limit"`
		Enabled    bool    `json:"enabled"`
	}
	_, raw := c.do("GET", "/api/me/keys", nil, &keys)
	if strings.Contains(raw, created.Key) {
		t.Error("列表里不应再出现完整 key")
	}
	if len(keys) != 1 || keys[0].QuotaLimit == nil || *keys[0].QuotaLimit != "2.5" || !strings.HasPrefix(created.Key, keys[0].Prefix) {
		t.Errorf("key 列表 = %+v", keys)
	}

	// 改名、停用、清除限额
	c.do("PATCH", "/api/me/keys/1", map[string]any{"name": "laptop-2", "enabled": false, "quota_limit": nil}, nil)
	c.do("GET", "/api/me/keys", nil, &keys)
	if keys[0].Enabled || keys[0].QuotaLimit != nil {
		t.Errorf("修改没生效: %+v", keys[0])
	}

	// 授权 vip 后可以切过去
	e.login("admin").do("PUT", "/api/admin/users/2/user-groups", map[string]any{"user_group_ids": []int64{2}}, nil)
	if code, body := c.do("PATCH", "/api/me/keys/1", map[string]any{"group_id": 2}, nil); code != 200 {
		t.Errorf("授权后应能换到 vip: %d %s", code, body)
	}

	// 别人的 key 动不了
	other := e.login("admin")
	if code, _ := other.do("DELETE", "/api/me/keys/1", nil, nil); code != 404 {
		t.Errorf("删除别人的 key 应返回 404，实际 %d", code)
	}
	if code, _ := c.do("DELETE", "/api/me/keys/1", nil, nil); code != 200 {
		t.Errorf("删除自己的 key 失败: %d", code)
	}
}

func TestAdminOnly(t *testing.T) {
	e := newEnv(t)
	c := e.login("bob")
	for _, path := range []string{"/api/admin/users", "/api/admin/channels", "/api/admin/logs", "/api/admin/settings"} {
		if code, _ := c.do("GET", path, nil, nil); code != 403 {
			t.Errorf("普通用户访问 %s 应返回 403，实际 %d", path, code)
		}
	}
}

func TestAdminUsersAndBalance(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")

	if code, body := a.do("POST", "/api/admin/users", map[string]string{"username": "carol", "password": "short"}, nil); code != 400 || !strings.Contains(body, "12 位") {
		t.Errorf("短密码: %d %s", code, body)
	}
	if code, _ := a.do("POST", "/api/admin/users", map[string]string{"username": "bob", "password": "bob-password-1"}, nil); code != 409 {
		t.Errorf("重名应返回 409，实际 %d", code)
	}
	if code, body := a.do("POST", "/api/admin/users", map[string]string{"username": "carol", "password": "carol-password-1"}, nil); code != 201 {
		t.Fatalf("%d %s", code, body)
	}

	var res struct {
		Balance string `json:"balance"`
	}
	a.do("POST", "/api/admin/users/2/balance", map[string]any{"mode": "add", "amount": "10", "note": "月度额度"}, &res)
	if res.Balance != "15" {
		t.Errorf("5 + 10 = %s", res.Balance)
	}
	a.do("POST", "/api/admin/users/2/balance", map[string]any{"mode": "set", "amount": "3"}, &res)
	if res.Balance != "3" {
		t.Errorf("设为 3，实际 %s", res.Balance)
	}
	var history []struct {
		Amount       string `json:"amount"`
		BalanceAfter string `json:"balance_after"`
		Operator     string `json:"operator"`
		Note         string `json:"note"`
	}
	a.do("GET", "/api/admin/users/2/balance", nil, &history)
	if len(history) != 2 || history[0].Amount != "-12" || history[1].Note != "月度额度" || history[0].Operator != "admin" {
		t.Errorf("余额流水 = %+v", history)
	}

	if code, body := a.do("PATCH", "/api/admin/users/1", map[string]any{"enabled": false}, nil); code != 400 || !strings.Contains(body, "自己") {
		t.Errorf("不能停用自己: %d %s", code, body)
	}
}

func TestChannelKeyNeverLeaves(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	modelCode, modelBody := a.do("POST", "/api/admin/models", map[string]any{"name": "m1", "input_price": "0", "output_price": "0", "cache_read_price": "0", "cache_write_price": "0", "enabled": false}, nil)
	if modelCode != 200 || strings.Contains(modelBody, `"enabled"`) {
		t.Fatalf("零价模型应能创建，响应不再包含 enabled: %d %s", modelCode, modelBody)
	}
	secret := "sk-upstream-super-secret-123456"
	var saved struct {
		ID int64 `json:"id"`
	}
	code, body := a.do("POST", "/api/admin/channels", map[string]any{
		"name": "ds", "template": "deepseek", "api_key": secret, "priority": 0, "enabled": true,
		"base_url_chat": "https://api.deepseek.com/", "group_ids": []int64{1},
		"models": []map[string]any{{"model_id": 1, "model_name": "m1", "upstream_name": "deepseek-flash"}},
	}, &saved)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	_, raw := a.do("GET", "/api/admin/channels", nil, nil)
	if strings.Contains(raw, secret) {
		t.Fatal("渠道列表泄露了完整的上游 key")
	}
	if !strings.Contains(raw, `"sk-u…3456"`) || !strings.Contains(raw, `"base_url_chat":"https://api.deepseek.com"`) {
		t.Errorf("应显示遮掩后的 key、去掉地址末尾的 /: %s", raw)
	}

	// 编辑时不填 key 表示保留原 key
	a.do("PUT", "/api/admin/channels/1", map[string]any{"name": "ds-renamed", "base_url_chat": "https://api.deepseek.com", "group_ids": []int64{1}, "enabled": true}, nil)
	ch, _ := e.store.ChannelSecret(context.Background(), 1)
	if ch.APIKey != secret || ch.Name != "ds-renamed" {
		t.Errorf("编辑后 key 应保留: %+v", ch)
	}
}

func TestUserLogsHideChannel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.store.Pool.Exec(ctx, `INSERT INTO channels (id, name, api_key, base_url_chat) VALUES (1, 'secret-channel', 'k', 'https://x')`)
	e.store.Pool.Exec(ctx, `INSERT INTO request_logs (request_id, user_id, protocol, model, upstream_model, channel_id, status, cost)
		VALUES ('r1', 2, 'chat', 'm1', 'up-m1', 1, 'success', 0.5), ('r2', 1, 'chat', 'm2', '', 1, 'success', 1)`)
	_, raw := e.login("bob").do("GET", "/api/me/logs", nil, nil)
	if strings.Contains(raw, "secret-channel") || strings.Contains(raw, "up-m1") || strings.Contains(raw, "r2") {
		t.Errorf("用户日志不应包含渠道信息或别人的记录: %s", raw)
	}
	if !strings.Contains(raw, `"cost":"0.5"`) {
		t.Errorf("用户日志 = %s", raw)
	}
	_, raw = e.login("admin").do("GET", "/api/admin/logs", nil, nil)
	if !strings.Contains(raw, "secret-channel") || !strings.Contains(raw, `"requests":2`) {
		t.Errorf("管理端日志 = %s", raw)
	}
}

func TestStaticPages(t *testing.T) {
	e := newEnv(t)
	get := func(path string) (*http.Response, string) {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	for _, p := range []string{"/", "/keys", "/admin/channels"} {
		resp, body := get(p)
		if resp.StatusCode != 200 || body != "<html>lingshu</html>" || resp.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s 应返回 index.html 且不缓存: %d %q %q", p, resp.StatusCode, body, resp.Header.Get("Cache-Control"))
		}
	}
	resp, _ := get("/assets/app-1.js")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("带哈希的静态文件应长期缓存: %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if resp, _ := get("/assets/missing.js"); resp.StatusCode != 404 {
		t.Errorf("不存在的静态文件应 404，实际 %d", resp.StatusCode)
	}
	if resp, body := get("/api/nope"); resp.StatusCode != 404 || !strings.Contains(body, "接口不存在") {
		t.Errorf("未知接口应返回 JSON 404: %d %s", resp.StatusCode, body)
	}
}
