package api

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strconv"
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
	if mode == "" {
		mode = "manual"
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": mode == "code" || mode == "email", "mode": mode})
}

func (s *Server) registerWithCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		Code      string `json:"code"`
		Email     string `json:"email"`
		EmailCode string `json:"email_code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || len([]rune(in.Username)) > 64 {
		writeError(w, http.StatusBadRequest, "用户名不能为空且不能超过 64 个字符")
		return
	}
	mode, err := s.Store.Setting(r.Context(), "registration_mode")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	if mode == "email" && !s.emailRate(w, r, "register") {
		return
	}
	if mode != "email" && in.Code == "" {
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
	var registerErr error
	if mode == "email" {
		email, valid := validEmail(in.Email)
		if !valid || len(in.EmailCode) != 6 {
			writeError(w, 400, "请输入有效邮箱与 6 位验证码")
			return
		}
		registerErr = s.Store.RegisterUserWithEmail(r.Context(), in.Username, hash, email, in.EmailCode, auth.HashAPIKey(token), expires)
	} else {
		registerErr = s.Store.RegisterUserWithCode(r.Context(), in.Username, hash, in.Code, auth.HashAPIKey(token), expires)
	}
	err = registerErr
	switch {
	case err == nil:
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: token, Path: "/", Expires: expires,
			HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
	case errors.Is(err, store.ErrRegistrationClosed):
		writeError(w, http.StatusForbidden, "自助注册未开放，请联系管理员")
	case errors.Is(err, store.ErrEmailCode), errors.Is(err, store.ErrEmailTaken):
		writeError(w, http.StatusBadRequest, err.Error())
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

// emailRate 是单实例注册入口限流，旧窗口每次请求时清除。
func (s *Server) emailRate(w http.ResponseWriter, r *http.Request, purpose string) bool {
	s.emailLimitMu.Lock()
	defer s.emailLimitMu.Unlock()
	now := time.Now()
	if s.emailLimits == nil {
		s.emailLimits = map[string][]time.Time{}
	}
	for key, events := range s.emailLimits {
		valid := events[:0]
		for _, at := range events {
			if now.Sub(at) < time.Minute {
				valid = append(valid, at)
			}
		}
		if len(valid) == 0 {
			delete(s.emailLimits, key)
		} else {
			s.emailLimits[key] = valid
		}
	}
	key := purpose + ":" + auth.ClientIP(r)
	if len(s.emailLimits[key]) >= 5 {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, 429, map[string]any{"error": "请求太频繁，请一分钟后再试", "retry_after": 60})
		return false
	}
	s.emailLimits[key] = append(s.emailLimits[key], now)
	return true
}
func (s *Server) sendEmailCode(w http.ResponseWriter, r *http.Request) {
	mode, err := s.Store.Setting(r.Context(), "registration_mode")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	if mode != "email" {
		writeError(w, 403, "邮箱注册未开放")
		return
	}
	if !s.emailRate(w, r, "send") {
		return
	}
	var in struct {
		Email        string `json:"email"`
		CaptchaToken string `json:"captcha_token"`
		CaptchaNonce string `json:"captcha_nonce"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	email, valid := validEmail(in.Email)
	if !valid {
		writeError(w, 400, "邮箱格式不对")
		return
	}
	if !s.guard.checkPow(in.CaptchaToken, in.CaptchaNonce) {
		writeJSON(w, 428, map[string]any{"error": "请完成人机验证后重试", "captcha": true})
		return
	}
	code, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		writeError(w, 500, "生成验证码失败")
		return
	}
	digits := strconv.FormatInt(code.Int64()+1000000, 10)[1:]
	hash := store.EmailCodeHash(email, digits)
	if err := s.Store.ClaimEmailCode(r.Context(), email, hash); err != nil {
		switch {
		case errors.Is(err, store.ErrEmailCooldown):
			w.Header().Set("Retry-After", "60")
			writeJSON(w, 429, map[string]any{"error": err.Error(), "retry_after": 60})
		case errors.Is(err, store.ErrEmailTaken):
			writeError(w, 409, err.Error())
		case errors.Is(err, store.ErrRegistrationClosed):
			writeError(w, 403, err.Error())
		default:
			writeStoreError(w, err)
		}
		return
	}
	accounts, err := s.Store.MailAccounts(r.Context())
	if err == nil {
		err = s.mail.send(r.Context(), s.Store, accounts, email, "Lingshu 注册验证码", "你的 Lingshu 注册验证码是 "+digits+"，15 分钟内有效。如非本人操作，请忽略这封邮件。")
	}
	keep := err == nil
	status := "submitted"
	var me *mailError
	if err != nil {
		status = "failed"
		if errors.As(err, &me) && me.uncertain {
			keep = true
			status = "unknown"
		}
	}
	// HTTP 连接结束也要完成发送状态持久化。
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if finishErr := s.Store.FinishEmailCode(finishCtx, email, hash, status, keep); finishErr != nil {
		writeStoreError(w, finishErr)
		return
	}
	if err != nil {
		mailFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"retry_after": 60})
}
