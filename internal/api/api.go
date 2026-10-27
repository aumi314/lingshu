// Package api 是管理端和用户端页面用的 JSON 接口，挂在 /api 下，用登录会话鉴权。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/aumi314/lingshu/internal/payments"
	"github.com/aumi314/lingshu/internal/relay"
	"github.com/aumi314/lingshu/internal/store"
)

type Server struct {
	Store     *store.Store
	Relay     *relay.Handler // 复用网关的上游客户端和渠道健康状态
	Web       fs.FS          // 前端构建产物，nil 时不提供页面
	PublicURL string
	Payments  *payments.Gateway

	guard *loginGuard
}

func (s *Server) Register(mux *http.ServeMux) {
	// 登录
	s.guard = newLoginGuard()
	if s.Payments == nil {
		s.Payments = payments.NewGateway()
	}
	s.registerPayments(mux)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("GET /api/auth/registration", s.registrationStatus)
	mux.HandleFunc("POST /api/auth/register", s.registerWithCode)
	mux.HandleFunc("POST /api/auth/challenge", s.challenge)
	mux.HandleFunc("POST /api/auth/logout", s.logout)

	// 当前用户
	mux.HandleFunc("GET /api/me", s.user(s.me))
	mux.HandleFunc("GET /api/me/endpoints", s.user(s.getAPIEndpoints))
	mux.HandleFunc("PUT /api/me/preferences", s.user(s.setPreferences))
	mux.HandleFunc("PUT /api/me/password", s.user(s.changePassword))
	mux.HandleFunc("GET /api/me/models", s.user(s.myModels))
	mux.HandleFunc("GET /api/me/keys", s.user(s.listKeys))
	mux.HandleFunc("POST /api/me/keys", s.user(s.createKey))
	mux.HandleFunc("PATCH /api/me/keys/{id}", s.user(s.updateKey))
	mux.HandleFunc("DELETE /api/me/keys/{id}", s.user(s.deleteKey))
	mux.HandleFunc("GET /api/me/logs", s.user(s.myLogs))

	// 管理端
	mux.HandleFunc("GET /api/admin/users", s.admin(s.listUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.createUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.admin(s.updateUser))
	mux.HandleFunc("POST /api/admin/users/{id}/balance", s.admin(s.adjustBalance))
	mux.HandleFunc("GET /api/admin/users/{id}/balance", s.admin(s.balanceHistory))
	mux.HandleFunc("PUT /api/admin/users/{id}/user-groups", s.admin(s.setUserGroups))
	mux.HandleFunc("GET /api/admin/registration-codes", s.admin(s.listRegistrationCodes))
	mux.HandleFunc("POST /api/admin/registration-codes", s.admin(s.createRegistrationCodes))
	mux.HandleFunc("DELETE /api/admin/registration-codes/{id}", s.admin(s.revokeRegistrationCode))

	mux.HandleFunc("GET /api/admin/user-groups", s.admin(s.listUserGroups))
	mux.HandleFunc("POST /api/admin/user-groups", s.admin(s.saveUserGroup))
	mux.HandleFunc("PUT /api/admin/user-groups/{id}", s.admin(s.saveUserGroup))
	mux.HandleFunc("DELETE /api/admin/user-groups/{id}", s.admin(s.deleteUserGroup))

	mux.HandleFunc("GET /api/admin/groups", s.admin(s.listGroups))
	mux.HandleFunc("POST /api/admin/groups", s.admin(s.saveGroup))
	mux.HandleFunc("PUT /api/admin/groups/{id}", s.admin(s.saveGroup))
	mux.HandleFunc("DELETE /api/admin/groups/{id}", s.admin(s.deleteGroup))

	mux.HandleFunc("GET /api/admin/models", s.admin(s.listModels))
	mux.HandleFunc("POST /api/admin/models", s.admin(s.saveModel))
	mux.HandleFunc("PUT /api/admin/models/{id}", s.admin(s.saveModel))
	mux.HandleFunc("DELETE /api/admin/models/{id}", s.admin(s.deleteModel))

	mux.HandleFunc("GET /api/admin/channel-templates", s.admin(s.channelTemplates))
	mux.HandleFunc("GET /api/admin/channels", s.admin(s.listChannels))
	mux.HandleFunc("POST /api/admin/channels", s.admin(s.saveChannel))
	mux.HandleFunc("PUT /api/admin/channels/order", s.admin(s.reorderChannels))
	mux.HandleFunc("PUT /api/admin/channels/{id}", s.admin(s.saveChannel))
	mux.HandleFunc("DELETE /api/admin/channels/{id}", s.admin(s.deleteChannel))
	mux.HandleFunc("POST /api/admin/channels/fetch-models", s.admin(s.fetchModels))
	mux.HandleFunc("POST /api/admin/channels/probe", s.admin(s.probeChannel))
	mux.HandleFunc("POST /api/admin/channels/{id}/balance", s.admin(s.queryChannelBalance))

	mux.HandleFunc("GET /api/admin/logs", s.admin(s.adminLogs))
	mux.HandleFunc("GET /api/admin/settings", s.admin(s.getSettings))
	mux.HandleFunc("PUT /api/admin/settings", s.admin(s.putSettings))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "接口不存在")
	})

	// 其余路径交给前端页面
	if s.Web != nil {
		mux.Handle("/", Static(s.Web))
	}
}

// ---- 通用 ----

type ctxKey struct{}

func currentUser(r *http.Request) *store.User {
	return r.Context().Value(ctxKey{}).(*store.User)
}

type handler func(w http.ResponseWriter, r *http.Request)

// user 要求已登录。
func (s *Server) user(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := s.sessionUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

// admin 要求已登录且是管理员。
func (s *Server) admin(next handler) http.HandlerFunc {
	return s.user(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).Role != "admin" {
			writeError(w, http.StatusForbidden, "只有管理员能做这个操作")
			return
		}
		next(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeStoreError 把数据层错误换成合适的状态码和提示，未知错误只记日志不外露。
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "要操作的记录不存在，可能已被删除，请刷新页面")
	case errors.Is(err, store.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "用户名已存在，请换一个")
	case errors.Is(err, store.ErrNameTaken):
		writeError(w, http.StatusConflict, "名称已存在，换一个名称")
	case errors.Is(err, store.ErrInUse):
		writeError(w, http.StatusConflict, "还有其他数据在使用它（比如 key 或渠道），先解除关联再删除")
	default:
		slog.Error("接口出错", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器出错了，请稍后重试")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求内容格式不对: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id 不对")
		return 0, false
	}
	return id, true
}

func ok(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
