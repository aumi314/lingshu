package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// doBrowser 发一个模拟浏览器的请求：能带 Origin、Referer、Sec-Fetch-Site，
// 也能控制 Content-Type（传空串表示不发这个头）。
func (c *client) doBrowser(method, path string, in any, contentType string, headers map[string]string) (int, string) {
	multi := map[string][]string{}
	for k, v := range headers {
		multi[k] = []string{v}
	}
	return c.doBrowserRaw(method, path, in, contentType, multi)
}

// doBrowserRaw 和 doBrowser 一样，但一个头可以发多次。
func (c *client) doBrowserRaw(method, path string, in any, contentType string, headers map[string][]string) (int, string) {
	c.e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.e.srv.URL+path, body)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, values := range headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// 来源不可信时统一返回 403 和这句提示，测试用它区分「被来源校验拦下」和「接口自己的错误」。
func wantRejected(t *testing.T, code int, body string) {
	t.Helper()
	if code != http.StatusForbidden || !strings.Contains(body, "请求来源不可信") {
		t.Errorf("应被来源校验拒绝（403），实际 %d %s", code, body)
	}
}

func TestWriteGuardAuthOrigin(t *testing.T) {
	e := newEnv(t)
	site := originOf(e.srv.URL)
	e.api.PublicURL = site
	c := e.client()
	login := map[string]string{"username": "bob", "password": "password-1"}

	code, body := c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": "http://evil.example"})
	wantRejected(t, code, body)

	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": "null"})
	wantRejected(t, code, body)

	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": site, "Sec-Fetch-Site": "cross-site"})
	wantRejected(t, code, body)

	// 同源放行
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": site})
	if code != 200 {
		t.Errorf("同源登录应成功，实际 %d %s", code, body)
	}

	// 没有 Origin 也没有 Referer：CLI、脚本放行
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", nil)
	if code != 200 {
		t.Errorf("无来源头的登录应放行，实际 %d %s", code, body)
	}

	// 只有 Referer 时按 Referer 判
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Referer": site + "/console/login"})
	if code != 200 {
		t.Errorf("同源 Referer 应放行，实际 %d %s", code, body)
	}
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Referer": "http://evil.example/console/login"})
	wantRejected(t, code, body)
}

func TestWriteGuardLogoutOrigin(t *testing.T) {
	e := newEnv(t)
	site := originOf(e.srv.URL)
	e.api.PublicURL = site
	c := e.login("bob")

	code, body := c.doBrowser("POST", "/api/auth/logout", nil, "", map[string]string{"Origin": "http://evil.example"})
	wantRejected(t, code, body)
	if code, _ := c.do("GET", "/api/me", nil, nil); code != 200 {
		t.Errorf("被拒绝的登出不应影响会话，实际 %d", code)
	}

	code, body = c.doBrowser("POST", "/api/auth/logout", nil, "application/json", map[string]string{"Origin": site})
	if code != 200 {
		t.Errorf("同源登出应成功，实际 %d %s", code, body)
	}
	if code, _ := c.do("GET", "/api/me", nil, nil); code != 401 {
		t.Errorf("登出后应返回 401，实际 %d", code)
	}
}

func TestWriteGuardRegisterAndEmailCodeOrigin(t *testing.T) {
	e := newEnv(t)
	site := originOf(e.srv.URL)
	e.api.PublicURL = site
	c := e.client()

	code, body := c.doBrowser("POST", "/api/auth/register", map[string]any{"username": "newbie", "password": "password-123456"}, "application/json", map[string]string{"Origin": "http://evil.example"})
	wantRejected(t, code, body)

	code, body = c.doBrowser("POST", "/api/auth/email-code", map[string]any{"email": "someone@example.com"}, "application/json", map[string]string{"Origin": "http://evil.example"})
	wantRejected(t, code, body)
}

func TestWriteGuardUserWrites(t *testing.T) {
	e := newEnv(t)
	site := originOf(e.srv.URL)
	e.api.PublicURL = site
	c := e.login("bob")
	key := map[string]any{"name": "test-key", "group_id": 1}

	code, body := c.doBrowser("POST", "/api/me/keys", key, "application/json", map[string]string{"Origin": "http://evil.example"})
	wantRejected(t, code, body)

	// 同源的写操作正常生效
	code, body = c.doBrowser("POST", "/api/me/keys", key, "application/json", map[string]string{"Origin": site})
	if code != 201 {
		t.Fatalf("同源创建 key 应返回 201，实际 %d %s", code, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == 0 {
		t.Fatalf("创建 key 的响应不对: %s", body)
	}

	// 没有来源头：按 CLI 放行
	code, body = c.doBrowser("POST", "/api/me/keys", key, "application/json", nil)
	if code != 201 {
		t.Fatalf("无来源头的创建应放行，实际 %d %s", code, body)
	}

	// 读接口不受来源校验影响
	if code, _ := c.do("GET", "/api/me/keys", nil, nil); code != 200 {
		t.Errorf("读接口不应被拦，实际 %d", code)
	}
}

func TestWriteGuardJSONContentType(t *testing.T) {
	e := newEnv(t)
	site := originOf(e.srv.URL)
	e.api.PublicURL = site
	c := e.login("bob")

	// 带请求体的写操作必须是 JSON
	code, body := c.doBrowser("POST", "/api/me/keys", map[string]any{"name": "x", "group_id": 1}, "text/plain", map[string]string{"Origin": site})
	if code != http.StatusUnsupportedMediaType {
		t.Errorf("非 JSON 请求体应返回 415，实际 %d %s", code, body)
	}
	code, body = c.doBrowser("POST", "/api/me/keys", map[string]any{"name": "x", "group_id": 1}, "application/json; charset=utf-8", map[string]string{"Origin": site})
	if code != http.StatusCreated {
		t.Errorf("带 charset 的 JSON 应放行，实际 %d %s", code, body)
	}
	code, body = c.doBrowser("POST", "/api/me/keys", map[string]any{"name": "x", "group_id": 1}, "application/json; charset", map[string]string{"Origin": site})
	if code != http.StatusUnsupportedMediaType {
		t.Errorf("Content-Type 不合法应返回 415，实际 %d %s", code, body)
	}

	// 没有请求体的动作（比如 DELETE）不要求 Content-Type
	code, body = c.doBrowser("DELETE", "/api/me/keys/999", nil, "", map[string]string{"Origin": site})
	if code != 404 {
		t.Errorf("删除不存在的 key 应返回 404，实际 %d %s", code, body)
	}
}

func TestWriteGuardPublicURLEmpty(t *testing.T) {
	e := newEnv(t)
	e.api.PublicURL = "" // 本地开发没配 PublicURL
	site := originOf(e.srv.URL)
	c := e.client()
	login := map[string]string{"username": "bob", "password": "password-1"}

	// 同一个 Host 视为同源
	code, body := c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": site})
	if code != 200 {
		t.Errorf("同 Host 的登录应放行，实际 %d %s", code, body)
	}

	// 别的 Host 一律拒绝
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": "http://evil.example"})
	wantRejected(t, code, body)

	// 不采信 X-Forwarded-Host
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{
		"Origin":           "http://evil.example",
		"X-Forwarded-Host": "evil.example",
	})
	wantRejected(t, code, body)

	// 同一个 Host 但协议不同也不算同源（测试服务是 http）
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": "https" + strings.TrimPrefix(site, "http")})
	wantRejected(t, code, body)
}

func TestWriteGuardOriginHeaderStrict(t *testing.T) {
	e := newEnv(t)
	site := originOf(e.srv.URL)
	e.api.PublicURL = site
	c := e.client()
	login := map[string]string{"username": "bob", "password": "password-1"}

	// Origin 存在但是空的，不能当成 CLI 放行
	code, body := c.doBrowserRaw("POST", "/api/auth/login", login, "application/json", map[string][]string{"Origin": {""}})
	wantRejected(t, code, body)

	// 多个 Origin 一律拒绝
	code, body = c.doBrowserRaw("POST", "/api/auth/login", login, "application/json", map[string][]string{"Origin": {site, "http://evil.example"}})
	wantRejected(t, code, body)

	// Origin 带路径不合法
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": site + "/console"})
	wantRejected(t, code, body)

	// Referer 存在但是空的，同样不能当成 CLI 放行
	code, body = c.doBrowserRaw("POST", "/api/auth/login", login, "application/json", map[string][]string{"Referer": {""}})
	wantRejected(t, code, body)
}

func TestWriteGuardPublicURLPathAndDefaultPort(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	login := map[string]string{"username": "bob", "password": "password-1"}

	// PublicURL 带子路径时只取 origin
	e.api.PublicURL = "https://gateway.example/console"
	code, body := c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": "https://gateway.example"})
	if code != 200 {
		t.Errorf("PublicURL 带子路径时应按 origin 比对，实际 %d %s", code, body)
	}

	// https 的默认端口和省略端口等价
	e.api.PublicURL = "https://gateway.example"
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": "https://gateway.example:443"})
	if code != 200 {
		t.Errorf("默认端口应等价，实际 %d %s", code, body)
	}
	code, body = c.doBrowser("POST", "/api/auth/login", login, "application/json", map[string]string{"Origin": "https://gateway.example:8443"})
	wantRejected(t, code, body)
}

func TestWriteGuardPaymentNotifyExempt(t *testing.T) {
	e := newEnv(t)
	site := originOf(e.srv.URL)
	e.api.PublicURL = site
	c := e.client()

	// 支付回调来自支付平台，没有浏览器来源，不做来源校验
	code, _ := c.doBrowser("POST", "/api/payments/epay/1/live/notify", nil, "", map[string]string{"Origin": "http://evil.example"})
	if code == http.StatusForbidden {
		t.Errorf("支付回调不该被来源校验拦下，实际 %d", code)
	}
	code, _ = c.doBrowser("GET", "/api/payments/epay/1/live/notify", nil, "", map[string]string{"Origin": "http://evil.example"})
	if code == http.StatusForbidden {
		t.Errorf("支付回调不该被来源校验拦下，实际 %d", code)
	}
}
