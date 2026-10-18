// Package relay 是网关的透传核心：鉴权、选渠道、改写请求、转发、提取 usage、写日志。
package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

const maxRequestBody = 32 << 20 // 32MB，够放带图片的长上下文

// 各协议在渠道地址后面拼的路径。
var upstreamPaths = map[usage.Protocol]string{
	usage.Chat:      "/chat/completions",
	usage.Messages:  "/v1/messages",
	usage.Responses: "/responses",
}

// 各协议对应的灵枢入口，用于提示用户换入口。
var entryPaths = map[string]string{
	"chat":      "/v1/chat/completions",
	"messages":  "/v1/messages",
	"responses": "/v1/responses",
}

type Handler struct {
	Store  *store.Store
	Client *http.Client
}

func NewHandler(s *store.Store) *Handler {
	return &Handler{
		Store: s,
		Client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 5 * time.Minute, // 非流式的推理模型可能很久才返回
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   32,
		}},
	}
}

// call 记录一次请求从进来到结束的全部信息。
type call struct {
	proto   usage.Protocol
	start   time.Time
	log     store.RequestLog
	tracker *usage.Tracker
}

func (h *Handler) Serve(proto usage.Protocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := &call{proto: proto, start: time.Now(), tracker: usage.NewTracker(proto)}
		c.log.RequestID = newRequestID()
		c.log.Protocol = string(proto)
		w.Header().Set("X-Lingshu-Request-Id", c.log.RequestID)
		h.serve(w, r, c)
	}
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, c *call) {
	owner, ok := h.authenticate(w, r, c.proto)
	if !ok {
		return // 没认出用户，不写日志
	}
	c.log.UserID, c.log.APIKeyID, c.log.GroupID = owner.UserID, owner.KeyID, owner.GroupID
	defer h.finish(r.Context(), c)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		c.reject(w, http.StatusRequestEntityTooLarge, "请求体太大或读取失败: "+err.Error())
		return
	}
	req, err := parseRequest(body)
	if err != nil {
		c.reject(w, http.StatusBadRequest, err.Error())
		return
	}
	c.log.Model, c.log.Stream = req.model, req.stream

	routes, err := h.Store.FindRoutes(r.Context(), owner.GroupID, string(c.proto), req.model)
	if err != nil {
		c.reject(w, http.StatusInternalServerError, "查询渠道失败，请稍后重试")
		slog.Error("查询渠道失败", "err", err)
		return
	}
	if len(routes) == 0 {
		h.rejectNoRoute(w, r.Context(), c, owner.GroupID, req.model)
		return
	}
	route := routes[0] // 阶段 1：单渠道，不重试
	c.log.ChannelID, c.log.UpstreamModel, c.log.Attempts = route.ChannelID, route.UpstreamModel, 1

	injected := req.rewrite(c.proto, route.UpstreamModel)
	h.forward(w, r, c, route, req, injected)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request, proto usage.Protocol) (*store.KeyOwner, bool) {
	keys := auth.KeysFromRequest(r)
	if len(keys) == 0 {
		writeError(w, proto, http.StatusUnauthorized, "缺少 API key，请在 Authorization: Bearer 或 x-api-key 请求头里带上灵枢的 key")
		return nil, false
	}
	var owner *store.KeyOwner
	for _, key := range keys {
		o, err := h.Store.LookupKey(r.Context(), auth.HashAPIKey(key))
		if err != nil {
			writeError(w, proto, http.StatusInternalServerError, "鉴权失败，请稍后重试")
			slog.Error("查询 key 失败", "err", err)
			return nil, false
		}
		if o != nil {
			owner = o
			break
		}
	}
	if owner == nil {
		writeError(w, proto, http.StatusUnauthorized, "API key 无效，请检查是否复制完整")
		return nil, false
	}
	if !owner.KeyEnabled || !owner.UserEnabled {
		writeError(w, proto, http.StatusForbidden, "这个 API key 或所属账号已被停用，请联系管理员")
		return nil, false
	}
	return owner, true
}

func (h *Handler) rejectNoRoute(w http.ResponseWriter, ctx context.Context, c *call, groupID int64, model string) {
	protocols, err := h.Store.ModelProtocols(ctx, groupID, model)
	if err == nil && len(protocols) > 0 {
		entries := make([]string, len(protocols))
		for i, p := range protocols {
			entries[i] = entryPaths[p]
		}
		c.reject(w, http.StatusBadRequest, fmt.Sprintf("模型 %s 不支持 %s，请改用 %s 调用", model, entryPaths[string(c.proto)], strings.Join(entries, " 或 ")))
		return
	}
	c.reject(w, http.StatusNotFound, fmt.Sprintf("模型 %s 不存在，或你的 key 所在分组不能使用它", model))
}

func (c *call) reject(w http.ResponseWriter, status int, msg string) {
	c.log.Status, c.log.HTTPStatus, c.log.Error = "rejected", status, msg
	writeError(w, c.proto, status, msg)
}

// forward 把改写后的请求发给上游，再把响应原样转回客户端。
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, c *call, route store.Route, req *request, injected bool) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel() // 函数返回时（包括客户端断开）立刻取消上游请求

	url := strings.TrimRight(route.BaseURL, "/") + upstreamPaths[c.proto]
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery // Claude Code 会带 ?beta=true
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(req.body))
	if err != nil {
		c.fail(w, http.StatusInternalServerError, "构造上游请求失败: "+err.Error())
		return
	}
	copyRequestHeaders(upReq.Header, r.Header)
	setUpstreamAuth(upReq.Header, c.proto, route.APIKey)

	resp, err := h.Client.Do(upReq)
	if err != nil {
		if r.Context().Err() != nil {
			c.log.Status = "client_cancelled"
			return
		}
		c.fail(w, http.StatusBadGateway, "上游连接失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	c.log.HTTPStatus = resp.StatusCode

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		c.log.Status, c.log.Error = "upstream_error", truncate(string(errBody), 1000)
		w.WriteHeader(resp.StatusCode)
		w.Write(errBody)
		return
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		h.relayStream(w, r, c, resp, injected)
	} else {
		h.relayBody(w, c, resp)
	}
}

func (h *Handler) relayBody(w http.ResponseWriter, c *call, resp *http.Response) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.fail(w, http.StatusBadGateway, "读取上游响应失败: "+err.Error())
		return
	}
	c.tracker.ObserveBody(body)
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(body); err != nil {
		c.log.Status = "client_cancelled"
		return
	}
	c.markFirstByte()
	c.log.Status = "success"
}

func (h *Handler) relayStream(w http.ResponseWriter, r *http.Request, c *call, resp *http.Response, injected bool) {
	rc := http.NewResponseController(w)
	w.WriteHeader(resp.StatusCode)
	reader := usage.NewSSEReader(resp.Body)
	for {
		ev, err := reader.Next()
		if err != nil {
			switch {
			case r.Context().Err() != nil:
				c.log.Status = "client_cancelled"
			case errors.Is(err, io.EOF) && c.tracker.Terminal && !c.tracker.Failed:
				c.log.Status = "success"
			case errors.Is(err, io.EOF) && c.tracker.Failed:
				c.log.Status, c.log.Error = "upstream_error", "上游在流中报告错误"
			case errors.Is(err, io.EOF):
				c.log.Status, c.log.Error = "upstream_error", "上游流在正常结束前断开"
			default:
				c.log.Status, c.log.Error = "upstream_error", "读取上游流失败: "+err.Error()
			}
			return
		}
		c.tracker.ObserveEvent(ev.Data)
		if injected && usage.IsUsageOnlyChatChunk(ev.Data) {
			continue // 客户端没要 usage，是网关替它要的，不转发
		}
		if _, err := w.Write(ev.Raw); err != nil {
			c.log.Status = "client_cancelled"
			return
		}
		if err := rc.Flush(); err != nil {
			c.log.Status = "client_cancelled"
			return
		}
		c.markFirstByte()
	}
}

func (c *call) markFirstByte() {
	if c.log.FirstByteMS == 0 {
		c.log.FirstByteMS = max(int(time.Since(c.start).Milliseconds()), 1)
	}
}

// fail 用于网关自己产生的上游错误（连接失败等）。
func (c *call) fail(w http.ResponseWriter, status int, msg string) {
	c.log.Status, c.log.HTTPStatus, c.log.Error = "upstream_error", status, msg
	writeError(w, c.proto, status, msg)
}

// finish 写日志。用独立的 context，客户端断开后也要写进去。
func (h *Handler) finish(reqCtx context.Context, c *call) {
	u := c.tracker.Result()
	c.log.InputTokens, c.log.CacheReadTokens, c.log.CacheWriteTokens = u.Input, u.CacheRead, u.CacheWrite
	c.log.OutputTokens, c.log.ReasoningTokens = u.Output, u.Reasoning
	c.log.UpstreamResponseID = c.tracker.ResponseID
	c.log.TotalMS = int(time.Since(c.start).Milliseconds())
	if c.log.Status == "" {
		c.log.Status = "upstream_error"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), 10*time.Second)
	defer cancel()
	if err := h.Store.InsertRequestLog(ctx, &c.log); err != nil {
		slog.Error("写请求日志失败", "request_id", c.log.RequestID, "err", err)
	}
	if err := h.Store.TouchKey(ctx, c.log.APIKeyID); err != nil {
		slog.Error("更新 key 使用时间失败", "err", err)
	}
}

func newRequestID() string {
	return "req_" + strings.ToLower(rand.Text())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ServeModels 实现 GET /v1/models，列出 key 所在分组能用的模型（OpenAI 格式）。
func (h *Handler) ServeModels(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.authenticate(w, r, usage.Chat)
	if !ok {
		return
	}
	names, err := h.Store.GroupModels(r.Context(), owner.GroupID)
	if err != nil {
		writeError(w, usage.Chat, http.StatusInternalServerError, "查询模型失败，请稍后重试")
		return
	}
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	data := make([]model, len(names))
	for i, n := range names {
		data[i] = model{ID: n, Object: "model", OwnedBy: "lingshu"}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}
