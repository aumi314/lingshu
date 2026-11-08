package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/aumi314/lingshu/internal/store"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// mailGateway 对每个账号串行刷新令牌；发信结果不明确时停止，避免重复投递。
type mailGateway struct {
	mu                 sync.Mutex
	accounts           map[int64]*mailToken
	client             *http.Client
	tokenURL, graphURL string
	cooldown           time.Time
}
type mailToken struct {
	mu              sync.Mutex
	access, refresh string
	clientID        string
	expires         time.Time
}
type mailError struct {
	message             string
	retry               int
	failover, uncertain bool
}

func (e *mailError) Error() string { return e.message }
func newMailGateway() *mailGateway {
	return &mailGateway{accounts: map[int64]*mailToken{}, client: &http.Client{Timeout: 20 * time.Second}, tokenURL: "https://login.microsoftonline.com/consumers/oauth2/v2.0/token", graphURL: "https://graph.microsoft.com/v1.0/me/sendMail"}
}
func (g *mailGateway) token(ctx context.Context, s *store.Store, a store.MailAccount, force bool) (string, error) {
	g.mu.Lock()
	t := g.accounts[a.ID]
	if t == nil {
		t = &mailToken{}
		g.accounts[a.ID] = t
	}
	g.mu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	// DB 可能持有上一次刷新的新令牌；强制检查时重新读取账号授权。
	current, err := s.MailAccounts(ctx)
	if err != nil {
		return "", err
	}
	found := false
	for _, v := range current {
		if v.ID == a.ID {
			if a.Version != 0 && v.Version != a.Version {
				return "", errors.New("发信账号配置已更新，请重试")
			}
			a = v
			found = true
			break
		}
	}
	if !found {
		return "", store.ErrNotFound
	}
	if !force && t.clientID == a.ClientID && t.refresh == a.RefreshToken && time.Now().Add(time.Minute).Before(t.expires) {
		return t.access, nil
	}
	form := url.Values{"client_id": {a.ClientID}, "refresh_token": {a.RefreshToken}, "grant_type": {"refresh_token"}, "scope": {"https://graph.microsoft.com/Mail.Send offline_access"}}
	req, _ := http.NewRequestWithContext(ctx, "POST", g.tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", &mailError{message: "授权服务连接失败，请稍后重试"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return "", &mailError{message: "邮件服务限流，请稍后再试", retry: retrySeconds(resp.Header)}
	}
	var body struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
		Scope   string `json:"scope"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", &mailError{message: "授权服务响应异常"}
	}
	if resp.StatusCode != 200 {
		return "", &mailError{message: "发信账号授权失效，请更新授权", failover: body.Error == "invalid_grant" || body.Error == "invalid_client" || body.Error == "unauthorized_client"}
	}
	if body.Access == "" || !strings.Contains(strings.ToLower(body.Scope), "mail.send") {
		return "", &mailError{message: "发信账号缺少 Mail.Send 权限", failover: true}
	}
	if body.Refresh != "" && body.Refresh != a.RefreshToken {
		if err := s.RotateMailToken(ctx, a.ID, a.ClientID, a.RefreshToken, body.Refresh); err != nil {
			return "", err
		}
		a.RefreshToken = body.Refresh
	}
	t.clientID = a.ClientID
	t.access, t.refresh, t.expires = body.Access, a.RefreshToken, time.Now().Add(time.Duration(body.Expires)*time.Second)
	return t.access, nil
}
func retrySeconds(h http.Header) int {
	if n, e := strconv.Atoi(h.Get("Retry-After")); e == nil && n > 0 {
		return n
	}
	if at, e := http.ParseTime(h.Get("Retry-After")); e == nil && time.Until(at) > 0 {
		return int(time.Until(at).Seconds()) + 1
	}
	return 60
}
func (g *mailGateway) send(ctx context.Context, s *store.Store, accounts []store.MailAccount, to, subject, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	g.mu.Lock()
	wait := time.Until(g.cooldown)
	g.mu.Unlock()
	if wait > 0 {
		return &mailError{message: "邮件服务限流，请稍后再试", retry: int(wait.Seconds()) + 1}
	}
	var last error = &mailError{message: "没有可用的发信账号"}
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		token, err := g.token(ctx, s, a, false)
		if err == nil {
			payload := map[string]any{"message": map[string]any{"subject": subject, "body": map[string]string{"contentType": "Text", "content": text}, "from": map[string]any{"emailAddress": map[string]string{"address": a.Email, "name": "Lingshu"}}, "toRecipients": []any{map[string]any{"emailAddress": map[string]string{"address": to}}}}, "saveToSentItems": true}
			raw, _ := json.Marshal(payload)
			req, _ := http.NewRequestWithContext(ctx, "POST", g.graphURL, bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			resp, e := g.client.Do(req)
			if e != nil {
				err = &mailError{message: "发送结果待核对，请检查收件箱后再重试", uncertain: true}
			} else {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
				resp.Body.Close()
				switch {
				case resp.StatusCode == 202:
					_ = s.MailResult(ctx, a.ID, "submitted", "", true, a.Version)
					return nil
				case resp.StatusCode == 429:
					err = &mailError{message: "邮件服务限流，请稍后再试", retry: retrySeconds(resp.Header)}
				case resp.StatusCode == 401 || resp.StatusCode == 403:
					err = &mailError{message: "发信账号授权或发信权限异常", failover: true}
				case resp.StatusCode >= 500:
					err = &mailError{message: "发送结果待核对，请检查收件箱后再重试", uncertain: true}
				default:
					err = &mailError{message: "邮件请求被拒绝，请核对收件地址"}
				}
			}
		}
		var me *mailError
		if errors.As(err, &me) {
			status := "error"
			if me.failover {
				status = "authorization_error"
			}
			_ = s.MailResult(ctx, a.ID, status, me.message, false, a.Version)
			if me.retry > 0 {
				g.mu.Lock()
				g.cooldown = time.Now().Add(time.Duration(me.retry) * time.Second)
				g.mu.Unlock()
			}
			if me.failover {
				g.mu.Lock()
				t := g.accounts[a.ID]
				g.mu.Unlock()
				if t != nil {
					t.mu.Lock()
					t.access = ""
					t.expires = time.Time{}
					t.mu.Unlock()
				}
				last = err
				continue
			}
			return err
		}
		return err
	}
	return last
}
func validEmail(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	a, err := mail.ParseAddress(value)
	return value, err == nil && a.Address == value && len(value) <= 254 && !strings.ContainsAny(value, "\r\n")
}
func (s *Server) superAdmin(next handler) http.HandlerFunc {
	return s.user(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).Role != "super_admin" {
			writeError(w, 403, "只有超级管理员能管理发信账号")
			return
		}
		next(w, r)
	})
}
func (s *Server) registerMail(mux *http.ServeMux) {
	s.mail = newMailGateway()
	mux.HandleFunc("GET /api/admin/mail-accounts", s.superAdmin(s.listMailAccounts))
	mux.HandleFunc("POST /api/admin/mail-accounts", s.superAdmin(s.saveMailAccount))
	mux.HandleFunc("PUT /api/admin/mail-accounts/{id}", s.superAdmin(s.saveMailAccount))
	mux.HandleFunc("DELETE /api/admin/mail-accounts/{id}", s.superAdmin(s.deleteMailAccount))
	mux.HandleFunc("PUT /api/admin/mail-accounts/order", s.superAdmin(s.orderMailAccounts))
	mux.HandleFunc("POST /api/admin/mail-accounts/{id}/check", s.superAdmin(s.checkMailAccount))
	mux.HandleFunc("POST /api/admin/mail-accounts/{id}/test", s.superAdmin(s.testMailAccount))
	mux.HandleFunc("POST /api/auth/email-code", s.sendEmailCode)
}
func (s *Server) listMailAccounts(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.MailAccounts(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, a)
}
func (s *Server) saveMailAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email        string `json:"email"`
		ClientID     string `json:"client_id"`
		RefreshToken string `json:"refresh_token"`
		Enabled      bool   `json:"enabled"`
		SortOrder    int    `json:"sort_order"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	email, valid := validEmail(in.Email)
	if !valid || strings.TrimSpace(in.ClientID) == "" {
		writeError(w, 400, "请填写有效邮箱与 Client ID")
		return
	}
	var id int64
	if r.PathValue("id") != "" {
		var ok bool
		id, ok = pathID(w, r)
		if !ok {
			return
		}
	}
	if id == 0 && strings.TrimSpace(in.RefreshToken) == "" {
		writeError(w, 400, "请填写 Refresh Token")
		return
	}
	id, err := s.Store.SaveMailAccount(r.Context(), store.MailAccount{ID: id, Email: email, ClientID: strings.TrimSpace(in.ClientID), RefreshToken: strings.TrimSpace(in.RefreshToken), Enabled: in.Enabled, SortOrder: in.SortOrder})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]int64{"id": id})
}
func (s *Server) deleteMailAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteMailAccount(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	okResponse(w)
}
func okResponse(w http.ResponseWriter) { writeJSON(w, 200, map[string]string{"status": "ok"}) }
func (s *Server) orderMailAccounts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.Store.OrderMailAccounts(r.Context(), in.IDs); err != nil {
		writeError(w, 400, "账号顺序无效，请刷新重试")
		return
	}
	okResponse(w)
}
func (s *Server) mailAccount(w http.ResponseWriter, r *http.Request) (store.MailAccount, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.MailAccount{}, false
	}
	accounts, err := s.Store.MailAccounts(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return store.MailAccount{}, false
	}
	for _, a := range accounts {
		if a.ID == id {
			return a, true
		}
	}
	writeError(w, 404, "发信账号不存在")
	return store.MailAccount{}, false
}
func (s *Server) checkMailAccount(w http.ResponseWriter, r *http.Request) {
	a, ok := s.mailAccount(w, r)
	if !ok {
		return
	}
	_, err := s.mail.token(r.Context(), s.Store, a, true)
	if err != nil {
		_ = s.Store.MailResult(r.Context(), a.ID, "authorization_error", "授权检查失败，请更新授权后重试", false, a.Version)
		mailFailure(w, err)
		return
	}
	if err := s.Store.MailResult(r.Context(), a.ID, "authorized", "", true, a.Version); err != nil {
		writeStoreError(w, err)
		return
	}
	okResponse(w)
}
func (s *Server) testMailAccount(w http.ResponseWriter, r *http.Request) {
	a, ok := s.mailAccount(w, r)
	if !ok {
		return
	}
	var in struct {
		Recipient string `json:"recipient"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	to, valid := validEmail(in.Recipient)
	if !valid {
		writeError(w, 400, "收件邮箱格式不对")
		return
	}
	a.Enabled = true
	if err := s.mail.send(r.Context(), s.Store, []store.MailAccount{a}, to, "Lingshu Outlook delivery test", "This is an authorized Lingshu email delivery test. No action is required."); err != nil {
		mailFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "submitted"})
}
func mailFailure(w http.ResponseWriter, err error) {
	var me *mailError
	if errors.As(err, &me) {
		if me.retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(me.retry))
			writeJSON(w, 429, map[string]any{"error": me.message, "retry_after": me.retry})
			return
		}
		writeError(w, 502, me.message)
		return
	}
	writeError(w, 503, "邮件服务暂时不可用，请稍后重试")
}
