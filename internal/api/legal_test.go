package api

import (
	"strings"
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

type legalDocs struct {
	UserAgreement string `json:"user_agreement"`
	PrivacyPolicy string `json:"privacy_policy"`
	Version       string `json:"version"`
}

func TestLegalPublicEndpointAndAdminSettings(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	bob := e.login("bob")

	var docs legalDocs
	if code, body := e.client().do("GET", "/api/auth/legal", nil, &docs); code != 200 {
		t.Fatal(code, body)
	}
	if docs.UserAgreement != "" || docs.PrivacyPolicy != "" {
		t.Fatalf("没配置时公开接口应返回空文本: %+v", docs)
	}
	if docs.Version == "" {
		t.Error("版本号应始终有值")
	}

	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"user_agreement": "用户协议正文", "privacy_policy": "隐私政策正文"}, nil); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := e.client().do("GET", "/api/auth/legal", nil, &docs); code != 200 {
		t.Fatal(code, body)
	}
	if docs.UserAgreement != "用户协议正文" || docs.PrivacyPolicy != "隐私政策正文" {
		t.Fatalf("公开接口应读到管理员保存的文本: %+v", docs)
	}
	if docs.Version == "" || docs.Version == strings.Repeat("0", 64) {
		t.Errorf("版本号应是内容哈希: %q", docs.Version)
	}

	var settings map[string]any
	if code, body := admin.do("GET", "/api/admin/settings", nil, &settings); code != 200 {
		t.Fatal(code, body)
	}
	if settings["user_agreement"] != "用户协议正文" || settings["privacy_policy"] != "隐私政策正文" {
		t.Errorf("管理端设置应返回协议文本: %+v", settings)
	}

	if code, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"user_agreement": strings.Repeat("字", 100001)}, nil); code != 400 {
		t.Errorf("用户协议超长应被拒绝: %d", code)
	}
	if code, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"privacy_policy": strings.Repeat("字", 100001)}, nil); code != 400 {
		t.Errorf("隐私政策超长应被拒绝: %d", code)
	}
	if code, _ := bob.do("PUT", "/api/admin/settings", map[string]any{"user_agreement": "x"}, nil); code != 403 {
		t.Errorf("普通用户不能改协议: %d", code)
	}

	// 两份 10 万字中文协议走真实请求体（全局 1MiB 上限）要能提交成功
	big := strings.Repeat("协议", 50000)
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"user_agreement": big, "privacy_policy": big}, nil); code != 200 {
		t.Fatalf("两份 10 万字协议应能提交: %d %s", code, body)
	}
	if code, body := e.client().do("GET", "/api/auth/legal", nil, &docs); code != 200 || len([]rune(docs.UserAgreement)) != 100000 {
		t.Fatalf("超长协议应能读回: %d %s", code, body)
	}
}

func TestLegalContentKeptVerbatim(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	raw := "  第一行协议\n第二行协议  \n"
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"user_agreement": raw}, nil); code != 200 {
		t.Fatal(code, body)
	}
	var docs legalDocs
	if code, body := e.client().do("GET", "/api/auth/legal", nil, &docs); code != 200 {
		t.Fatal(code, body)
	}
	if docs.UserAgreement != raw {
		t.Errorf("协议原文应原样保存: got %q want %q", docs.UserAgreement, raw)
	}
	if docs.Version != store.LegalVersion(raw, "") {
		t.Errorf("版本号应按原文算: got %q", docs.Version)
	}

	// 只填了空白协议时不算配置了协议，注册不用同意
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"user_agreement": "   ", "privacy_policy": ""}, nil); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_enabled": true}, nil); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := e.client().do("POST", "/api/auth/register", map[string]any{"username": "blank-legal-user", "password": "a-secure-password"}, nil); code != 201 {
		t.Fatalf("协议是空白时注册不该被拦: %d %s", code, body)
	}
}

func TestRegistrationRequiresLegalConsent(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin")
	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"registration_enabled": true}, nil); code != 200 {
		t.Fatal(code, body)
	}

	// 没配置协议时注册照旧，不需要同意参数
	if code, body := e.client().do("POST", "/api/auth/register", map[string]any{"username": "plain-user", "password": "a-secure-password"}, nil); code != 201 {
		t.Fatal(code, body)
	}

	if code, body := admin.do("PUT", "/api/admin/settings", map[string]any{"user_agreement": "用户协议正文", "privacy_policy": "隐私政策正文"}, nil); code != 200 {
		t.Fatal(code, body)
	}
	var docs legalDocs
	if code, body := e.client().do("GET", "/api/auth/legal", nil, &docs); code != 200 {
		t.Fatal(code, body)
	}

	if code, _ := e.client().do("POST", "/api/auth/register", map[string]any{"username": "no-consent", "password": "a-secure-password"}, nil); code != 400 {
		t.Errorf("没勾选同意应被拒绝: %d", code)
	}
	if code, _ := e.client().do("POST", "/api/auth/register", map[string]any{"username": "not-accepted", "password": "a-secure-password", "legal_accepted": false, "legal_version": docs.Version}, nil); code != 400 {
		t.Errorf("明确传 false 也应被拒绝: %d", code)
	}
	if code, _ := e.client().do("POST", "/api/auth/register", map[string]any{"username": "stale-version", "password": "a-secure-password", "legal_accepted": true, "legal_version": "0123456789abcdef"}, nil); code != 400 {
		t.Errorf("版本过期应被拒绝: %d", code)
	}
	if code, body := e.client().do("POST", "/api/auth/register", map[string]any{"username": "legal-user", "password": "a-secure-password", "legal_accepted": true, "legal_version": docs.Version}, nil); code != 201 {
		t.Fatalf("同意且版本正确应注册成功: %d %s", code, body)
	}

	var version string
	var accepted bool
	if err := e.store.Pool.QueryRow(e.t.Context(), `SELECT COALESCE(legal_version,''), legal_accepted_at IS NOT NULL FROM users WHERE username='legal-user'`).Scan(&version, &accepted); err != nil {
		t.Fatal(err)
	}
	if version != docs.Version || !accepted {
		t.Errorf("应记录同意的版本和时间: version=%q accepted=%v", version, accepted)
	}
}
