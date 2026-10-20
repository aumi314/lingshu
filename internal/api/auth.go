package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/store"
)

const (
	sessionCookie = "lingshu_session"
	sessionTTL    = 30 * 24 * time.Hour
)

func (s *Server) sessionUser(r *http.Request) (*store.User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errors.New("no session")
	}
	return s.Store.SessionUser(r.Context(), auth.HashAPIKey(c.Value))
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// 用户不存在时也比对一次密码，让响应时间和密码错误时一样，猜不出哪些用户名存在。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("lingshu-dummy-password"), bcrypt.DefaultCost)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		CaptchaToken string `json:"captcha_token"`
		CaptchaNonce string `json:"captcha_nonce"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	username, ip := strings.TrimSpace(in.Username), clientIP(r)
	if wait := s.guard.lockedFor(username, ip); wait > 0 {
		minutes := int(wait.Minutes()) + 1
		slog.Warn("登录被锁定", "username", logName(username), "ip", ip)
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("密码错误次数太多，请 %d 分钟后再试", minutes))
		return
	}
	if s.guard.needCaptcha(username, ip) && !s.guard.checkPow(in.CaptchaToken, in.CaptchaNonce) {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "需要人机验证，请重试", "captcha": true})
		return
	}
	id, hash, enabled, err := s.Store.UserCredentials(r.Context(), username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	if err != nil {
		hash = string(dummyHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || err != nil {
		captcha := s.guard.fail(username, ip)
		slog.Warn("登录失败", "username", logName(username), "ip", ip)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "用户名或密码错误", "captcha": captcha})
		return
	}
	if !enabled {
		writeError(w, http.StatusForbidden, "账号已被停用，请联系管理员")
		return
	}
	s.guard.succeed(username)
	token := rand.Text() + rand.Text()
	expires := time.Now().Add(sessionTTL)
	if err := s.Store.CreateSession(r.Context(), auth.HashAPIKey(token), id, expires); err != nil {
		writeStoreError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode,
	})
	ok(w)
}

// logName 截断用户名再写日志，防止有人用超长用户名刷日志。
func logName(s string) string {
	if r := []rune(s); len(r) > 64 {
		return string(r[:64]) + "…"
	}
	return s
}

// challenge 发一道人机验证题，浏览器算出答案后随登录请求一起提交。
func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"token": s.guard.newChallenge(), "difficulty": powDifficulty})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.Store.DeleteSession(r.Context(), auth.HashAPIKey(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	ok(w)
}

func (s *Server) cnyPerUSD(r *http.Request) decimal.Decimal {
	v, err := s.Store.Setting(r.Context(), "cny_per_usd")
	if err != nil {
		return decimal.RequireFromString("6.7")
	}
	d, err := decimal.NewFromString(v)
	if err != nil {
		return decimal.RequireFromString("6.7")
	}
	return d
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r), "cny_per_usd": s.cnyPerUSD(r)})
}

var allowedPrefs = map[string][]string{
	"language": {"zh", "en"},
	"currency": {"USD", "CNY"},
	"theme":    {"system", "light", "dark"},
}

func (s *Server) setPreferences(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if !readJSON(w, r, &in) {
		return
	}
	prefs := map[string]string{}
	json.Unmarshal(currentUser(r).Preferences, &prefs)
	for k, v := range in {
		allowed, known := allowedPrefs[k]
		if !known || !contains(allowed, v) {
			writeError(w, http.StatusBadRequest, "不支持的设置: "+k+"="+v)
			return
		}
		prefs[k] = v
	}
	raw, _ := json.Marshal(prefs)
	if err := s.Store.SetPreferences(r.Context(), currentUser(r).ID, raw); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	u := currentUser(r)
	hash, err := s.Store.PasswordHash(r.Context(), u.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Old)) != nil {
		writeError(w, http.StatusBadRequest, "当前密码不对")
		return
	}
	newHash, msg := hashPassword(in.New)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.Store.UpdateUser(r.Context(), u.ID, store.UserPatch{PasswordHash: &newHash}); err != nil {
		writeStoreError(w, err)
		return
	}
	// 其他设备上的登录全部失效，当前设备重新登录
	s.Store.DeleteUserSessions(r.Context(), u.ID)
	ok(w)
}

// MinPasswordLen 只约束新设置的密码，已有密码不受影响。
const MinPasswordLen = 12

func hashPassword(p string) (hash string, problem string) {
	if len(p) < MinPasswordLen {
		return "", fmt.Sprintf("密码至少 %d 位", MinPasswordLen)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return "", "密码处理失败: " + err.Error()
	}
	return string(h), ""
}
