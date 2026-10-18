package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
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

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id, hash, enabled, err := s.Store.UserCredentials(r.Context(), strings.TrimSpace(in.Username))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if !enabled {
		writeError(w, http.StatusForbidden, "账号已被停用，请联系管理员")
		return
	}
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

func hashPassword(p string) (hash string, problem string) {
	if len(p) < 8 {
		return "", "密码至少 8 位"
	}
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return "", "密码处理失败: " + err.Error()
	}
	return string(h), ""
}
