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
	"strconv"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/billing"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/router"
	"github.com/aumi314/lingshu/internal/store"
	"github.com/shopspring/decimal"
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
	Health *router.Health    // 渠道整体的健康（5xx、超时）
	Keys   *router.KeyHealth // 渠道里单把 key 的冷却和失效（429、401 / 402 / 403）

	// 等上游响应头的时限，超时就换渠道。流式请求上游通常几秒内就会回响应头；
	// 非流式的推理模型要等全部生成完才回，所以给得长。
	StreamHeaderTimeout time.Duration
	BodyHeaderTimeout   time.Duration

	limits *limiter // 每个用户的并发数和每分钟请求数

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
		Keys:                router.NewKeyHealth(),
		StreamHeaderTimeout: 60 * time.Second,
		BodyHeaderTimeout:   5 * time.Minute,
		limits:              newLimiter(),
	}
}

// call 记录一次请求从进来到结束的全部信息。
type call struct {
	proto   usage.Protocol
	start   time.Time
	log     store.RequestLog
	tracker *usage.Tracker
	body    []byte // 客户端原始请求体，上游没给 usage 时用来估算输入
	retries int    // 渠道级失败后最多重试几次（服务分组设置）
	gotResp bool   // 是否收到了上游的响应头（收到才说明上游开始处理了）

	basePrices billing.Prices // 模型基础价，结算时按价格规则换成实际单价
	rules      []billing.PriceRule

	fastMultiplier decimal.Decimal // 原样转发了 GPT Fast 请求时的计费倍数，0 表示标准请求
	dropTier       bool            // 模型没开放客户端要的高速档位：转发前去掉 service_tier
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

	readStart := time.Now()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	c.log.ReadMS = int(time.Since(readStart).Milliseconds())
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
	// 并发与频率限制：超限返回 429 和 Retry-After，客户端会自动退避重试
	limits := h.limits.limits(r.Context(), h.Store)
	release, reason, retryAfter := h.limits.acquire(owner.UserID, limits, time.Now())
	if release == nil {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		c.reject(w, http.StatusTooManyRequests, reason)
		return
	}
	defer release()

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
	// 同一会话固定用同一把 key（按“灵枢 key + 会话编号”），坏 key 和冷却中的渠道排到后面
	sticky := strconv.FormatInt(owner.KeyID, 10) + "\x00" + sessionOf(r, req)
	var pinKey int64
	if c.proto == usage.Responses && req.previousResponseID != "" {
		// previous_response_id 引用的状态存在原上游账号里，只能发回原渠道、原来那把 key
		chID, keyID, err := h.Store.ChannelForResponse(r.Context(), owner.UserID, req.previousResponseID)
		if err != nil {
			slog.Error("查询原渠道失败", "err", err)
		}
		if chID != 0 {
			routes, pinKey = onlyChannel(routes, chID), keyID
		}
	}
	ordered := h.orderAttempts(routes, sticky, pinKey)
	if len(ordered) == 0 {
		c.reject(w, http.StatusServiceUnavailable, "这轮对话接着的上一次响应来自一个现在不可用的渠道或 key，没法继续，请开启新对话")
		return
	}
	c.log.GroupRatio, c.retries = owner.GroupRatio, owner.GroupRetries

	// GPT 的 Fast（docs/decisions.md「GPT Fast 计费」）：只看 OpenAI 协议的 service_tier。
	// 模型开放了 Fast 就原样转发、按倍率计费；没开放（或要的是 Ultrafast）就去掉档位，按标准处理，不让上游按高价收费而我们按标准价扣
	if req.premiumTier != "" && (c.proto == usage.Chat || c.proto == usage.Responses) {
		if m := ordered[0].FastMultiplier; req.premiumTier == "fast" && m != nil {
			c.fastMultiplier = *m
		} else {
			c.dropTier = true
			c.log.ServiceTier = "unavailable"
		}
	}

	// 预扣：按最大可能费用冻结额度，可用余额不够就拒绝；结算时解冻并按实际扣费（finish → SettleRequest）
	// 输出按“请求写的上限”和“预扣上限”取小：Claude Code 等常写 3–6 万，实际回复多为几千，
	// 全额冻结会让余额不多的用户几乎无法并发；超过预扣上限的部分在结算时按实际扣（有界的小额透支）。
	// 预扣上限设为 0 表示完全按请求写的上限冻结。
	maxOutput := req.maxOutput
	if cap := limits.ReserveOutput; cap > 0 && (maxOutput == 0 || maxOutput > cap) {
		maxOutput = cap
	}
	input := usage.EstimateRequest(body)
	reserve := billing.Reserve(input, maxOutput, ordered[0].Prices, ordered[0].PriceRules, owner.GroupRatio, c.start)
	// 有历史时按“最近 20 次里最贵的一次 × 2”封顶：公式按原始请求体估输入、缓存按全价，
	// 长上下文 + 高缓存命中时会高估几十倍（实测 $1.22 对实际 $0.02），把余额不多的正常用户拦住
	if recent, n, err := h.Store.RecentMaxCost(r.Context(), owner.UserID, req.model); err == nil && n > 0 {
		reserve = decimal.Min(reserve, recent.Mul(decimal.NewFromInt(2)))
	}
	if c.fastMultiplier.IsPositive() {
		reserve = reserve.Mul(c.fastMultiplier) // 历史多是标准请求，封顶后再乘，Fast 请求的冻结额同样够
	}
	if reserve.IsPositive() {
		held, available, err := h.Store.Reserve(r.Context(), owner.UserID, reserve)
		if err != nil {
			c.reject(w, http.StatusInternalServerError, "冻结额度失败，请稍后重试")
			slog.Error("预扣失败", "err", err)
			return
		}
		if !held.IsPositive() {
			c.reject(w, http.StatusPaymentRequired, fmt.Sprintf("可用余额不够这次请求的最大可能费用：预估最多 $%s（输入约 %d token、最多输出 %d token），当前可用 $%s（已扣除进行中请求冻结的额度）。可以调低 max_tokens，或充值后再试",
				reserve.StringFixed(4), input, maxOutput, available.StringFixed(4)))
			return
		}
		c.log.Reserved = held
	}
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

// 重试规则（docs/decisions.md「路由与重试」）
const (
	maxKeysPerChannel     = 10              // 后面还有别的渠道时，同一渠道一次请求最多连换几把 key
	sameChannelRetryDelay = 1 * time.Second // 没有别的渠道可换时，渠道级失败后等多久同渠道再试
)

// attemptAll 按顺序尝试“渠道 + key”，直到有一个给客户端写出了响应。只在还没给客户端写任何字节之前换；失败的尝试不收钱。
//   - key 自己的问题（429、401 / 402 / 403）：换下一把 key，不占重试次数。同一渠道最多连换 maxKeysPerChannel 把，
//     后面还有别的渠道时去下一个渠道；是最后一个渠道时一直换到可用的 key 用完（免费 key 聚合类渠道，限流是常态）。
//     冷却中、失效的 key 不碰。
//   - 渠道级失败（5xx、超时、断流）：占一次重试（服务分组设置，默认 2 次），换下一个渠道；没有别的渠道可换时等 1 秒同渠道再试。
//   - 整个请求最多 router.RetryBudget。
func (h *Handler) attemptAll(w http.ResponseWriter, r *http.Request, c *call, req *request, routes []store.Route) {
	fallback := false // true：没有可用的 key，只剩失效的，照样试（可能已经恢复，比如刚充值）
	var usable []store.Route
	for _, rt := range routes {
		if h.Keys.Usable(rt.KeyID) {
			usable = append(usable, rt)
		}
	}
	if len(usable) == 0 {
		ids := make([]int64, len(routes))
		for i, rt := range routes {
			ids[i] = rt.KeyID
		}
		if wait, ok := h.Keys.MinCooldown(ids); ok {
			// 所有 key 都在限流冷却：直接告诉客户端多久后再试（客户端按 Retry-After 退避），不去上游白挨 429
			secs := max(int((wait+time.Second-1)/time.Second), 1)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			c.reject(w, http.StatusTooManyRequests, fmt.Sprintf("这个模型可用渠道的 key 都在限流，约 %d 秒后恢复，请稍后重试", secs))
			return
		}
		usable, fallback = routes, true
	}
	alive := func(rt store.Route) bool { return fallback || h.Keys.Usable(rt.KeyID) }
	skip := map[int64]bool{} // 渠道级失败、不再试的渠道
	otherChannel := func(rest []store.Route, ch int64) bool {
		for _, rt := range rest {
			if rt.ChannelID != ch && !skip[rt.ChannelID] && alive(rt) {
				return true
			}
		}
		return false
	}

	var last *failure
	var reasons []string
	keysTried := map[int64]int{}
	chFails, n := 0, 0
	for i := 0; i < len(usable); i++ {
		route := usable[i]
		if skip[route.ChannelID] || !alive(route) {
			continue
		}
		if keysTried[route.ChannelID] >= maxKeysPerChannel && otherChannel(usable[i+1:], route.ChannelID) {
			continue
		}
		if n > 0 && time.Since(c.start) > router.RetryBudget {
			break
		}
		n++
		c.useRoute(route, n)
		f := h.attempt(w, r, c, route, req)
		if f == nil {
			return // 已经给客户端写了响应（成功、不该重试的错误），或者用户已经断开
		}
		last = f
		reasons = append(reasons, route.ChannelName+": "+f.reason)
		c.log.FailedChannelIDs = append(c.log.FailedChannelIDs, route.ChannelID)
		c.log.FailedKeyIDs = append(c.log.FailedKeyIDs, route.KeyID)
		if router.KeyFailure(f.status) {
			keysTried[route.ChannelID]++
			slog.Warn("key 失败，换下一把", "request_id", c.log.RequestID, "channel", route.ChannelName, "key_id", route.KeyID, "reason", f.reason)
			continue
		}
		chFails++
		if chFails > c.retries {
			break
		}
		if !otherChannel(usable[i+1:], route.ChannelID) {
			// 没有别的渠道可换：等一下同渠道（同一把 key）再试，偶发的 524、抖动常常过一下就好
			slog.Warn("渠道失败，稍后同渠道再试", "request_id", c.log.RequestID, "channel", route.ChannelName, "reason", f.reason)
			select {
			case <-r.Context().Done():
				c.log.Status = "client_cancelled"
				return
			case <-time.After(sameChannelRetryDelay):
			}
			i--
			continue
		}
		skip[route.ChannelID] = true
		slog.Warn("渠道失败，换下一个", "request_id", c.log.RequestID, "channel", route.ChannelName, "reason", f.reason)
	}
	if last == nil {
		c.reject(w, http.StatusServiceUnavailable, "没有可用的渠道，请稍后重试")
		return
	}
	c.log.Status, c.log.HTTPStatus = "upstream_error", last.status
	c.log.Error = truncate(strings.Join(reasons, "; "), 1000)
	last.write(w, c.proto)
}

// useRoute 切换到一个渠道，重置上一次尝试留下的状态。
func (c *call) useRoute(route store.Route, attempt int) {
	c.log.ChannelID, c.log.ChannelName, c.log.UpstreamModel, c.log.Prices = route.ChannelID, route.ChannelName, route.UpstreamModel, route.Prices
	c.log.ChannelKeyID = route.KeyID
	c.basePrices, c.rules = route.Prices, route.PriceRules
	c.log.Attempts = attempt
	c.tracker = usage.NewTracker(c.proto)
	c.gotResp = false
}

// attempt 向一个渠道发请求。返回 nil 表示响应已经交给客户端（或用户已断开），否则返回可重试的失败。
func (h *Handler) attempt(w http.ResponseWriter, r *http.Request, c *call, route store.Route, req *request) *failure {
	body, injected := req.bodyFor(c.proto, route.UpstreamModel, c.dropTier)
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
	var openCode *openCodeRequest
	if route.Template == openCodeTemplate && c.proto == usage.Chat && strings.HasSuffix(route.UpstreamModel, "-free") {
		// 会话归属用灵枢 key 的标识，随后恢复上游鉴权，不将用户 key 发给厂商。
		upReq.Header.Set("Authorization", fmt.Sprintf("lingshu-key:%d", c.log.APIKeyID))
		body, openCode, err = prepareOpenCode(upReq, body, route.KeyID) // 会话按 key 区分：不同账号的会话不混用
		setUpstreamAuth(upReq.Header, c.proto, route.APIKey)
		if err != nil {
			return &failure{status: http.StatusBadRequest, reason: "OpenCode 请求格式错误", msg: err.Error()}
		}
		upReq.Body = io.NopCloser(bytes.NewReader(body))
		upReq.ContentLength = int64(len(body))
		upReq.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}

	timeout := h.BodyHeaderTimeout
	if req.stream || openCode != nil {
		timeout = h.StreamHeaderTimeout
	}
	timer := time.AfterFunc(timeout, cancel)
	c.log.UpstreamStartMS = max(int(time.Since(c.start).Milliseconds()), 1) // 换渠道时以最后一次为准
	client := h.clientFor(route.ProxyURL)
	if route.Template == openCodeTemplate {
		client = h.openCodeClientFor(route.ProxyURL)
	}
	resp, err := client.Do(upReq)
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
		if router.KeyFailure(resp.StatusCode) {
			// 限流、失效、欠费只记在这把 key 上，不拖累整个渠道
			h.Keys.Fail(route.KeyID, resp.StatusCode, retryAfter(resp.Header), fmt.Sprintf("HTTP %d %s", resp.StatusCode, truncate(string(errBody), 120)))
		} else {
			h.Health.Record(route.ChannelID, outcome)
		}
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
		if openCode != nil {
			if !req.stream {
				if err := collectOpenCodeChat(resp, openCode); err != nil {
					if r.Context().Err() != nil {
						c.log.Status = "client_cancelled"
						return nil
					}
					h.Health.Record(route.ChannelID, router.Failure)
					return &failure{status: http.StatusBadGateway, reason: err.Error(), msg: err.Error()}
				}
				return h.relayBody(w, c, route, resp)
			}
			return h.relayOpenCodeStream(w, r, c, route, resp, injected, openCode)
		}
		return h.relayStream(w, r, c, route, resp, injected)
	}
	if openCode != nil {
		h.Health.Record(route.ChannelID, router.Failure)
		return &failure{status: http.StatusBadGateway, reason: "OpenCode 未返回流式响应", msg: "OpenCode 未返回预期的流式响应，请重试"}
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
	h.Keys.OK(route.KeyID)
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
	return h.relayOpenCodeStream(w, r, c, route, resp, injected, nil)
}

func (h *Handler) relayOpenCodeStream(w http.ResponseWriter, r *http.Request, c *call, route store.Route, resp *http.Response, injected bool, openCode *openCodeRequest) *failure {
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
	if openCode != nil {
		if err := openCode.sanitizeEvent(first.Data); err != nil {
			return &failure{status: http.StatusBadGateway, reason: err.Error(), msg: err.Error()}
		}
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
		if openCode != nil {
			if checkErr := openCode.sanitizeEvent(ev.Data); checkErr != nil {
				c.tracker.Failed = true
				// 已发出首字后不能重试；显式错误事件让客户端知道工具调用失败。
				b, _ := json.Marshal(map[string]any{"error": map[string]string{"type": "upstream_error", "message": checkErr.Error()}})
				fmt.Fprintf(w, "data: %s\n\n", b)
				rc.Flush()
				err = checkErr
				break
			}
		}
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
		h.Keys.OK(route.KeyID)
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
		// Fast 在规则选出的单价（含长上下文价）上再乘倍率。以响应里的实际档位为准：被上游降回标准（default）按标准价；
		// 响应没写档位（有的中转会去掉）按请求的档位算
		if c.fastMultiplier.IsPositive() {
			if t := c.tracker.ServiceTier; t == "" || premiumTier(t) == "fast" {
				c.log.Prices = c.log.Prices.Scale(c.fastMultiplier)
				c.log.ServiceTier = "fast"
			} else {
				c.log.ServiceTier = "fast_downgraded"
			}
		}
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

// truncate 按字节截到 n 以内。结果要写进数据库，必须是合法 UTF-8：切在多字节字符中间的残字节、
// 上游原文里本来就有的非法字节都去掉，否则写日志被 PostgreSQL 拒绝，整笔结算回滚。
func truncate(s string, n int) string {
	if len(s) <= n {
		return strings.ToValidUTF8(s, "")
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// ServeUsage 实现 GET /v1/usage：用 API key 查余额，给 Magpie 等工具的“余额查询”用（鉴权方式同对话请求）。
// available 是这个 key 实际还能花的钱：账户余额与 key 剩余限额取小。金额是美元，按 JSON 数字输出，不经过 float。
func (h *Handler) ServeUsage(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.authenticate(w, r, usage.Chat)
	if !ok {
		return
	}
	num := func(d decimal.Decimal) json.RawMessage { return json.RawMessage(d.String()) }
	available := owner.Balance.Sub(owner.Reserved) // 扣掉进行中请求冻结的额度
	key := map[string]any{"name": owner.KeyName, "group": owner.GroupName, "quota_used": num(owner.QuotaUsed), "quota_limit": nil, "quota_remaining": nil}
	if owner.QuotaLimit != nil {
		remaining := owner.QuotaLimit.Sub(owner.QuotaUsed)
		key["quota_limit"], key["quota_remaining"] = num(*owner.QuotaLimit), num(remaining)
		available = decimal.Min(available, remaining)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "usage", "currency": "USD", "balance": num(owner.Balance), "available": num(available), "key": key})
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
