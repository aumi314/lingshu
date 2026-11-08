package api

import (
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// 浏览器写请求的来源校验，防跨站请求伪造（CSRF）。
//
// 规则：
//   - 只管写方法（POST/PUT/PATCH/DELETE），读接口不受影响；
//   - Origin 必须和本站 origin 完全一致；没有 Origin 时退回看 Referer；
//   - Origin 为 null、为空、有多个、来源跨站（Sec-Fetch-Site: cross-site）一律拒绝；
//   - Origin 和 Referer 都没有时按非浏览器调用（CLI、脚本）放行；
//   - 带请求体的写操作必须是 application/json，挡住跨站表单提交。
//
// /v1 网关接口和支付回调不经过这里，它们不带浏览器会话。

// 记日志时截断来源，避免恶意长串撑爆日志。
const maxLoggedOrigin = 64

// isWriteMethod 报告这个方法是不是会改数据的写请求。
func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// guardWrite 给需要校验来源的写接口套一层。
func (s *Server) guardWrite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowWrite(w, r) {
			return
		}
		next(w, r)
	}
}

// allowWrite 校验来源和请求格式，不通过时写错误响应并返回 false。
func (s *Server) allowWrite(w http.ResponseWriter, r *http.Request) bool {
	if !s.sameOrigin(r) {
		slog.Warn("拒绝来源不可信的请求", "method", r.Method, "path", r.URL.Path, "origin", shortOrigin(r.Header.Get("Origin")))
		writeError(w, http.StatusForbidden, "请求来源不可信，请刷新页面后重试")
		return false
	}
	if !jsonBody(r) {
		writeError(w, http.StatusUnsupportedMediaType, "请用 application/json 提交")
		return false
	}
	return true
}

// sameOrigin 判断请求是不是来自本站。
func (s *Server) sameOrigin(r *http.Request) bool {
	// 浏览器自带的跨站标记，最直接的拒绝依据。
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origins := r.Header.Values("Origin"); len(origins) > 0 {
		if len(origins) > 1 {
			return false
		}
		origin := strings.TrimSpace(origins[0])
		if origin == "" || origin == "null" {
			return false
		}
		candidate := originOf(origin)
		return candidate != "" && s.matchOrigin(r, candidate)
	}
	if refers := r.Header.Values("Referer"); len(refers) > 0 {
		if len(refers) > 1 {
			return false
		}
		referer := strings.TrimSpace(refers[0])
		if referer == "" || referer == "null" {
			return false
		}
		candidate := refererOrigin(referer)
		return candidate != "" && s.matchOrigin(r, candidate)
	}
	return true // 没有浏览器痕迹，按 CLI、脚本放行
}

// matchOrigin 拿请求里的来源和本站比对。
func (s *Server) matchOrigin(r *http.Request, candidate string) bool {
	want := s.siteOrigin(r)
	return want != "" && strings.EqualFold(candidate, want)
}

// siteOrigin 本站的 origin。
// 配了 PublicURL 就以它为准（PublicURL 可以带子路径，只取 origin）；
// 没配（本地开发）就用请求自身的 TLS 和 Host，绝不信 X-Forwarded-Host。
func (s *Server) siteOrigin(r *http.Request) string {
	if s.PublicURL != "" {
		scheme, host := parseSite(s.PublicURL)
		if scheme == "" || host == "" {
			return ""
		}
		return scheme + "://" + normalizeHost(scheme, host)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := normalizeHost(scheme, r.Host)
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// jsonBody 带请求体的写操作必须是 application/json。
// 没有请求体的动作（比如 DELETE）不要求 Content-Type。
func jsonBody(r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return ct == "application/json"
}

// originOf 解析浏览器发的 Origin：只能是 "scheme://host[:port]"，带路径就算非法。
func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	if u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if u.Path != "" && u.Path != "/" {
		return ""
	}
	return scheme + "://" + normalizeHost(scheme, u.Host)
}

// refererOrigin 从 Referer 里取出 origin，Referer 是完整地址。
func refererOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + normalizeHost(scheme, u.Host)
}

// parseSite 取地址的 scheme 和 host，忽略路径（PublicURL 可以带子路径）。
func parseSite(raw string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", ""
	}
	if u.User != nil {
		return "", ""
	}
	return scheme, u.Host
}

// normalizeHost 按 origin 语义去掉默认端口：https 的 443、http 的 80。
func normalizeHost(scheme, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch scheme {
	case "https":
		host = strings.TrimSuffix(host, ":443")
	case "http":
		host = strings.TrimSuffix(host, ":80")
	}
	return host
}

// shortOrigin 截断记日志用的来源。
func shortOrigin(origin string) string {
	r := []rune(origin)
	if len(r) > maxLoggedOrigin {
		return string(r[:maxLoggedOrigin]) + "…"
	}
	return origin
}
