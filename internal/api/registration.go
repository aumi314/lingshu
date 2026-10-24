package api

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/store"
)

func (s *Server) registrationStatus(w http.ResponseWriter, r *http.Request) {
	mode, err := s.Store.Setting(r.Context(), "registration_mode")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": mode == "code"})
}

func (s *Server) registerWithCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || len([]rune(in.Username)) > 64 {
		writeError(w, http.StatusBadRequest, "用户名不能为空且不能超过 64 个字符")
		return
	}
	if in.Code == "" {
		writeError(w, http.StatusBadRequest, "请输入注册码")
		return
	}
	hash, msg := hashPassword(in.Password)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	token := rand.Text() + rand.Text()
	expires := time.Now().Add(sessionTTL)
	err := s.Store.RegisterUserWithCode(r.Context(), in.Username, hash, in.Code, auth.HashAPIKey(token), expires)
	switch {
	case err == nil:
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: token, Path: "/", Expires: expires,
			HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
	case errors.Is(err, store.ErrRegistrationClosed):
		writeError(w, http.StatusForbidden, "自助注册未开放，请联系管理员")
	case errors.Is(err, store.ErrInvalidRegistrationCode):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "用户名已存在，请换一个")
	default:
		writeStoreError(w, err)
	}
}

func (s *Server) listRegistrationCodes(w http.ResponseWriter, r *http.Request) {
	codes, err := s.Store.ListRegistrationCodes(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if codes == nil {
		codes = []store.RegistrationCode{}
	}
	writeJSON(w, http.StatusOK, codes)
}

func (s *Server) createRegistrationCodes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Count int `json:"count"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Count < 1 || in.Count > 100 {
		writeError(w, http.StatusBadRequest, "每次生成数量为 1–100 个")
		return
	}
	codes, err := s.Store.CreateRegistrationCodes(r.Context(), currentUser(r).ID, in.Count)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, codes)
}

func (s *Server) revokeRegistrationCode(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	if err := s.Store.RevokeRegistrationCode(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrRegistrationCodeUsed) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeStoreError(w, err)
		return
	}
	ok(w)
}
