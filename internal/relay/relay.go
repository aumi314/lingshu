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
	"net/url"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/billing"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/router"
	"github.com/aumi314/lingshu/internal/store"
)

const maxRequestBody = 32 << 20 // 32MB，够放带图片的长上下文

// 各协议在渠道地址后面拼的路径。
var upstreamPaths = map[usage.Protocol]string{
	usage.Chat:      "/chat/completions",
	usage.Messages:  "/v1/messages",
	usage.Responses: "/responses",
	usage.SystemOne: "/systemone",
}

// 各协议对应的灵枢入口，用于提示用户换入口。
var entryPaths = map[string]string{
	"chat":      "/v1/chat/completions",
	"messages":  "/v1/messages",
	"responses": "/v1/responses",
	"systemone": "/v1/systemone",
}

type Handler struct {
	Store  *store.Store
	Client *http.Client
	Health *router.Health

	// 等上游响应头的时限，超时就换渠道。流式请求上游通常几秒内就会回响应头；
	// 非流式的推理模型要等全部生成完才回，所以给得长。
	StreamHeaderTimeout time.Duration
	BodyHeaderTimeout   time.Duration

	proxies proxyClients
}

// newTransport 是上游请求用的连接设置。每个代理各建一个全新的 Transport，
// 不要 Clone 用过的 Transport：克隆会带上已经协商好的 HTTP/2 设置，经代理时会把 HTTP/2 响应当 HTTP/1.1 读。
func newTransport(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:               proxy,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 32,
	}
}

func NewHandler(s *store.Store) *Handler {
	return &Handler{
		Store:               s,
		Client:              &http.Client{Transport: newTransport(http.ProxyFromEnvironment)},
		Health:              router.NewHealth(),
		StreamHeaderTimeout: 60 * time.Second,
		BodyHeaderTimeout:   5 * time.Minute,
	}
}

// call 记录一次请求从进来到结束的全部信息。
type call struct {
	proto   usage.Protocol
	start   time.Time
	log     store.RequestLog
	tracker *usage.Tracker
	body    []byte // 客户端原始请求体，上游没给 usage 时用来估算输入
	gotResp bool   // 是否收到了上游的响应头（收到才说明上游开始处理了）

	basePrices billing.Prices // 模型基础价，结算时按价格规则换成实际单价
	rules      []billing.PriceRule
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
	c.log.UserID, c.log.APIKeyID, c.log.GroupID, c.log.KeyName, c.log.GroupName = owner.UserID, owner.KeyID, owner.GroupID, owner.KeyName, owner.GroupName
	c.log.ClientIP = auth.ClientIP(r)
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
	c.log.Model, c.log.Stream, c.log.ReasoningEffort = req.model, req.stream, req.effort
	c.body = body
	if c.proto == usage.SystemOne && req.stream {
		c.reject(w, http.StatusBadRequest, "System One 不支持流式调用，请移除 stream 或设为 false")
		return
	}

	if !owner.GroupAllowed {
		c.reject(w, http.StatusForbidden, "你已经没有这个 key 所在分组的使用权限，请换一个 key 或联系管理员")
		return
	}
	if rej := billing.Admit(owner.Balance, owner.QuotaLimit, owner.QuotaUsed); rej != nil {
		c.reject(w, rej.Status, rej.Message)
		return
	}

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
	ordered := router.Order(h.Health, routes, func(rt store.Route) int64 { return rt.ChannelID })
	if c.proto == usage.Responses && req.previousResponseID != "" {
		// previous_response_id 引用的状态存在原上游账号里，只能发回原渠道
		chID, err := h.Store.ChannelForResponse(r.Context(), owner.UserID, req.previousResponseID)
		if err != nil {
			slog.Error("查询原渠道失败", "err", err)
		}
		if chID != 0 {
			ordered = onlyChannel(ordered, chID)
			if len(ordered) == 0 {
				c.reject(w, http.StatusServiceUnavailable, "这轮对话接着的上一次响应来自一个现在不可用的渠道，没法继续，请开启新对话")
				return
			}
		}
	}
	c.log.GroupRatio = owner.GroupRatio
	h.attemptAll(w, r, c, req, ordered)
}

func onlyChannel(routes []store.Route, channelID int64) []store.Route {
	for _, rt := range routes {
		if rt.ChannelID == channelID {
			return []store.Route{rt}
		}
	}
	return nil
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

// failure 是一次可以换渠道重试的失败。所有渠道都失败时，把最后一次的失败返回给客户端。
type failure struct {
	status int
	header http.Header // 上游的原始响应（有的话原样返回）
	body   []byte
	reason string // 写进日志的简短原因
	msg    string // 没有上游原始响应时，返回给客户端的说明
}

func (f *failure) write(w http.ResponseWriter, proto usage.Protocol) {
	if f.body != nil {
		copyResponseHeaders(w.Header(), f.header)
		w.WriteHeader(f.status)
		w.Write(f.body)
		return
	}
	writeError(w, proto, f.status, f.msg)
}

// attemptAll 按顺序尝试渠道，直到有一个给客户端写出了响应。
// 只在还没给客户端写任何字节之前换渠道；失败的尝试不收钱。
func (h *Handler) attemptAll(w http.ResponseWriter, r *http.Request, c *call, req *request, routes []store.Route) {
	var last *failure
	var reasons []string
	for i, route := range routes {
		if i >= router.MaxAttempts || (i > 0 && time.Since(c.start) > router.RetryBudget) {
			break
		}
		c.useRoute(route, i+1)
		f := h.attempt(w, r, c, route, req)
		if f == nil {
			return // 已经给客户端写了响应（成功、不该重试的错误），或者用户已经断开
		}
		last = f
		reasons = append(reasons, route.ChannelName+": "+f.reason)
		slog.Warn("渠道失败，换下一个", "request_id", c.log.RequestID, "channel", route.ChannelName, "reason", f.reason)
	}
	c.log.Status, c.log.HTTPStatus = "upstream_error", last.status
	c.log.Error = truncate(strings.Join(reasons, "; "), 1000)
	last.write(w, c.proto)
}

// useRoute 切换到一个渠道，重置上一次尝试留下的状态。
func (c *call) useRoute(route store.Route, attempt int) {
	c.log.ChannelID, c.log.ChannelName, c.log.UpstreamModel, c.log.Prices = route.ChannelID, route.ChannelName, route.UpstreamModel, route.Prices
	c.basePrices, c.rules = route.Prices, route.PriceRules
	c.log.Attempts = attempt
	c.tracker = usage.NewTracker(c.proto)
	c.gotResp = false
}

// attempt 向一个渠道发请求。返回 nil 表示响应已经交给客户端（或用户已断开），否则返回可重试的失败。
func (h *Handler) attempt(w http.ResponseWriter, r *http.Request, c *call, route store.Route, req *request) *failure {
	body, injected := req.bodyFor(c.proto, route.UpstreamModel)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel() // 返回时（包括客户端断开）立刻取消上游请求

	url := strings.TrimRight(route.BaseURL, "/") + upstreamPaths[c.proto]
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery // Claude Code 会带 ?beta=true
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return &failure{status: http.StatusInternalServerError, reason: "构造请求失败", msg: "构造上游请求失败: " + err.Error()}
	}
	copyRequestHeaders(upReq.Header, r.Header)
	setUpstreamAuth(upReq.Header, c.proto, route.APIKey)

	timeout := h.BodyHeaderTimeout
	if req.stream {
		timeout = h.StreamHeaderTimeout
	}
	timer := time.AfterFunc(timeout, cancel)
	resp, err := h.clientFor(route.ProxyURL).Do(upReq)
	timedOut := !timer.Stop()
	if err == nil && timedOut {
		resp.Body.Close()
		err = context.DeadlineExceeded
	}
	if err != nil {
		if r.Context().Err() != nil {
			c.log.Status = "client_cancelled"
			return nil
		}
		h.Health.Record(route.ChannelID, router.Failure)
		reason := "连接失败: " + err.Error()
		if timedOut {
			reason = fmt.Sprintf("%v 内没有响应", timeout)
		}
		return &failure{status: http.StatusBadGateway, reason: reason, msg: "上游暂时连不上，已经试过所有可用渠道，请稍后重试"}
	}
	defer resp.Body.Close()
	c.gotResp = true

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		errBody = redactKey(errBody, route.APIKey) // 有的上游会在报错里回显 key，不能传给用户或写进日志
		retry, outcome := router.Classify(resp.StatusCode)
		h.Health.Record(route.ChannelID, outcome)
		if retry {
			return &failure{status: resp.StatusCode, header: resp.Header.Clone(), body: errBody,
				reason: fmt.Sprintf("HTTP %d %s", resp.StatusCode, truncate(string(errBody), 200))}
		}
		// 请求本身的问题，换渠道也一样，原样返回
		copyResponseHeaders(w.Header(), resp.Header)
		c.log.Status, c.log.HTTPStatus, c.log.Error = "upstream_error", resp.StatusCode, truncate(string(errBody), 1000)
		w.WriteHeader(resp.StatusCode)
		w.Write(errBody)
		return nil
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return h.relayStream(w, r, c, route, resp, injected)
	}
	return h.relayBody(w, c, route, resp)
}

func (h *Handler) relayBody(w http.ResponseWriter, c *call, route store.Route, resp *http.Response) *failure {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.Health.Record(route.ChannelID, router.Failure)
		return &failure{status: http.StatusBadGateway, reason: "读取响应失败: " + err.Error(), msg: "读取上游响应失败，请稍后重试"}
	}
	c.tracker.ObserveBody(body)
	if c.proto == usage.SystemOne && !c.tracker.Terminal {
		h.Health.Record(route.ChannelID, router.Failure)
		return &failure{status: http.StatusBadGateway, reason: "无效的 System One 响应", msg: "上游没有返回有效的决策结果，请稍后重试"}
	}
	h.Health.Record(route.ChannelID, router.Success)
	copyResponseHeaders(w.Header(), resp.Header)
	c.log.HTTPStatus = resp.StatusCode
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(body); err != nil {
		c.log.Status = "client_cancelled"
		return nil
	}
	c.markFirstByte()
	c.log.Status = "success"
	return nil
}

func (h *Handler) relayStream(w http.ResponseWriter, r *http.Request, c *call, route store.Route, resp *http.Response, injected bool) *failure {
	reader := usage.NewSSEReader(resp.Body)

	// 先读第一个事件再给客户端回响应头：上游一开口就报错或断开时，还能换渠道
	first, err := reader.Next()
	if err != nil {
		if r.Context().Err() != nil {
			c.log.Status = "client_cancelled"
			return nil
		}
		h.Health.Record(route.ChannelID, router.Failure)
		return &failure{status: http.StatusBadGateway, reason: "返回内容前断开", msg: "上游在返回内容前断开了连接，请稍后重试"}
	}
	c.tracker.ObserveEvent(first.Data)
	if c.tracker.Failed {
		h.Health.Record(route.ChannelID, router.Failure)
		// 所有渠道都这样时，把这个协议原生的错误事件原样交给客户端
		return &failure{status: resp.StatusCode, header: resp.Header.Clone(), body: first.Raw,
			reason: "流的第一个事件就是错误: " + truncate(string(first.Data), 200)}
	}

	copyResponseHeaders(w.Header(), resp.Header)
	c.log.HTTPStatus = resp.StatusCode
	rc := http.NewResponseController(w)
	w.WriteHeader(resp.StatusCode)

	ev := first
	for {
		if !(injected && usage.IsUsageOnlyChatChunk(ev.Data)) { // 客户端没要 usage、是网关替它要的，不转发
			if _, err := w.Write(ev.Raw); err != nil {
				c.log.Status = "client_cancelled"
				return nil
			}
			if err := rc.Flush(); err != nil {
				c.log.Status = "client_cancelled"
				return nil
			}
			c.markFirstByte()
		}

		ev, err = reader.Next()
		if err != nil {
			break
		}
		c.tracker.ObserveEvent(ev.Data)
	}

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
	switch c.log.Status {
	case "success":
		h.Health.Record(route.ChannelID, router.Success)
	case "upstream_error":
		h.Health.Record(route.ChannelID, router.Failure)
	}
	return nil
}

func (c *call) markFirstByte() {
	if c.log.FirstByteMS == 0 {
		c.log.FirstByteMS = max(int(time.Since(c.start).Milliseconds()), 1)
	}
}

// finish 按计费规则算出用量和扣费，结算并写日志。用独立的 context，客户端断开后也要写进去。
func (h *Handler) finish(reqCtx context.Context, c *call) {
	if c.log.Status == "" {
		c.log.Status = "upstream_error"
	}
	u, estimated := c.settledUsage()
	c.log.InputTokens, c.log.CacheReadTokens, c.log.CacheWriteTokens = u.Input, u.CacheRead, u.CacheWrite
	c.log.OutputTokens, c.log.ReasoningTokens, c.log.UsageEstimated = u.Output, u.Reasoning, estimated
	c.log.UpstreamResponseID = c.tracker.ResponseID
	c.log.TotalMS = int(time.Since(c.start).Milliseconds())

	// 计费规则（docs/decisions.md）：成功和用户断开要扣；上游出错、被拒绝不扣，用量照记
	// 价格规则（阶梯、分时段）：按实际用量和请求开始时间选出单价，日志记的是生效的单价和规则名
	if c.log.ChannelID != 0 {
		c.log.Prices, c.log.PriceRule = billing.Select(c.basePrices, c.rules, u, c.start)
	}
	if c.log.Status == "success" || c.log.Status == "client_cancelled" {
		c.log.Cost = billing.Cost(u, c.log.Prices, c.log.GroupRatio)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), 10*time.Second)
	defer cancel()
	if err := h.Store.SettleRequest(ctx, &c.log); err != nil {
		slog.Error("结算失败", "request_id", c.log.RequestID, "cost", c.log.Cost, "err", err)
	}
}

// settledUsage 决定用哪份用量计费，返回是否用了估算值。
func (c *call) settledUsage() (usage.Usage, bool) {
	u := c.tracker.Result()
	switch c.log.Status {
	case "success":
		if c.tracker.Seen() {
			return u, false
		}
		// 上游正常结束却没给 usage：全部估算
		return usage.Usage{Input: usage.EstimateRequest(c.body), Output: c.tracker.EstimatedOutput()}, true
	case "client_cancelled":
		if !c.gotResp {
			return usage.Usage{}, false // 上游还没开始处理用户就走了，不扣
		}
		// 用户中途断开：上游给的用量可能不全（阿里 message_start 的输入数就不准），取上游值和估算值里大的
		estimated := false
		if est := usage.EstimateRequest(c.body); est > u.Input+u.CacheRead+u.CacheWrite {
			u.Input, estimated = est-u.CacheRead-u.CacheWrite, true
		}
		if est := c.tracker.EstimatedOutput(); est > u.Output {
			u.Output, estimated = est, true
		}
		return u, estimated
	}
	return u, false
}

func redactKey(b []byte, key string) []byte {
	if len(key) < 8 {
		return b
	}
	return bytes.ReplaceAll(b, []byte(key), []byte("***"))
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
