package relay

// Responses 协议的 WebSocket 入口：GET /v1/responses 升级成 WebSocket，
// 客户端每一帧是一个 response.create 请求，网关把它按普通 HTTP 请求走一遍现有处理器
//（鉴权、权限、限流、预扣、结算都在里面），只是把回来的 SSE 事件拆成一帧一个 JSON 发回去。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
	"github.com/coder/websocket"
)

const (
	// wsMaxFrameSize 单帧上限，和 HTTP 请求体上限一致（32MB）。
	wsMaxFrameSize = 32 << 20
	// wsFrameWriteTimeout 发一帧的上限。写帧要能撑过"生成被取消"这种情况，
	// 所以它不用生成的上下文，自己带超时。
	wsFrameWriteTimeout = 30 * time.Second
	// wsErrorCodePreviousResponseNotFound previous_response_id 指向的响应不在缓存里。
	wsErrorCodePreviousResponseNotFound = "previous_response_not_found"
)

// wsHistory 所有连接共享的历史缓存（进程内，不落库）。
var wsHistory = newWSHistoryStore(wsHistoryMaxBytes)

// wsConn 是连接需要用到的部分，测试可以换成假的。
type wsConn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	Close(code websocket.StatusCode, reason string) error
	SetReadLimit(n int64)
}

// ServeWebSocket 处理 GET /v1/responses 的 WebSocket 升级。
func (h *Handler) ServeWebSocket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.serveWebSocket(w, r)
	}
}

func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	// 升级前先认一次 key：key 无效就直接按普通 HTTP 错误返回，别让客户端白等一个连接
	probe := &wsProbeWriter{header: http.Header{}}
	owner, ok := h.authenticate(probe, r, usage.Responses)
	if !ok {
		for k, vs := range probe.header {
			w.Header()[k] = vs
		}
		w.WriteHeader(probe.status)
		w.Write(probe.body.Bytes())
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.Warn("WebSocket 升级失败", "err", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(wsMaxFrameSize)

	s := newWSSession(conn, r, wsHistory)
	s.userID = owner.UserID
	s.handle = h.Serve(usage.Responses)
	s.auth = func(w http.ResponseWriter, r *http.Request) (*store.KeyOwner, bool) {
		return h.authenticate(w, r, usage.Responses)
	}
	s.run(r.Context())
}

// wsSession 一条 WebSocket 连接。
type wsSession struct {
	conn    wsConn
	upgrade *http.Request   // 升级请求：每帧克隆请求时用它的 header、路径和 query
	history *wsHistoryStore // 共享的历史缓存
	handle  func(http.ResponseWriter, *http.Request)
	auth    func(http.ResponseWriter, *http.Request) (*store.KeyOwner, bool)
	userID  int64 // 这个连接属于哪个用户，历史只在自己名下取

	writeMu sync.Mutex

	mu     sync.Mutex
	active *wsGen   // 当前在跑的那次生成，空闲时是 nil
	keep   []string // 最近几个响应 id，钉在缓存里不被淘汰
	respID string   // 当前这次生成的响应 id，用来核对 response.cancel
}

// wsGen 是一次生成的句柄。客户端拿到终态帧马上就会发下一轮，
// 所以要用它分清先后两次生成，别让上一次的收尾把下一次的状态清掉。
type wsGen struct {
	cancel context.CancelFunc
	input  []json.RawMessage // 这次实际发出去的输入，终态时进历史
	stored bool              // 历史是不是已经记过了
}

func newWSSession(conn wsConn, upgrade *http.Request, history *wsHistoryStore) *wsSession {
	return &wsSession{conn: conn, upgrade: upgrade, history: history}
}

// run 读帧循环。连接断了就把正在跑的生成停掉。
func (s *wsSession) run(ctx context.Context) {
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			s.abort()
			s.release()
			return
		}
		if typ != websocket.MessageText {
			continue // 二进制帧不是协议约定的，忽略
		}
		s.onFrame(ctx, data)
	}
}

// abort 连接断开时中止正在跑的那次生成。
func (s *wsSession) abort() {
	s.mu.Lock()
	var cancel context.CancelFunc
	if s.active != nil {
		cancel = s.active.cancel
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// release 连接断了：把钉住的响应 id 放开，让缓存按容量回收。
func (s *wsSession) release() {
	s.mu.Lock()
	keep := s.keep
	s.keep = nil
	s.mu.Unlock()
	// 这条连接的历史别人也用不上，直接删掉，容量马上还给缓存
	s.history.remove(keep)
}

// onFrame 处理一帧客户端消息。
func (s *wsSession) onFrame(ctx context.Context, data []byte) {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(data, &frame); err != nil {
		s.sendError(ctx, "invalid_request_error", "这一帧不是合法的 JSON 对象", "invalid_frame")
		return
	}
	var kind string
	json.Unmarshal(frame["type"], &kind)
	switch kind {
	case "response.create":
		s.startCreate(ctx, frame)
	case "response.cancel":
		s.cancelActive(ctx, frame)
	default:
		// 不认识的帧类型明确回错，但不打断连接
		s.sendError(ctx, "invalid_request_error", "不支持的帧类型: "+kind, "unsupported_type")
	}
}

// startCreate 起一次生成。同一连接同时只跑一个，第二个直接回错误帧。
func (s *wsSession) startCreate(ctx context.Context, frame map[string]json.RawMessage) {
	s.mu.Lock()
	if s.active != nil {
		s.mu.Unlock()
		s.sendError(ctx, "invalid_request_error",
			"这个连接上还有一个生成任务在进行，请等它结束，或者先发 response.cancel 取消", "concurrent_generation_not_allowed")
		return
	}
	frameCtx, cancel := context.WithCancel(ctx)
	gen := &wsGen{cancel: cancel}
	s.active = gen
	s.respID = ""
	s.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			if s.active == gen {
				s.active = nil
			}
			s.mu.Unlock()
		}()
		s.runCreate(frameCtx, frame, gen)
	}()
}

// cancelActive 取消正在跑的那次生成（response.cancel）。
// 帧里带了 response_id 就核对一下，对不上明确回错。
func (s *wsSession) cancelActive(ctx context.Context, frame map[string]json.RawMessage) {
	var want string
	if raw, ok := frame["response_id"]; ok {
		if err := json.Unmarshal(raw, &want); err != nil {
			s.sendError(ctx, "invalid_request_error", "response_id 必须是字符串", "invalid_frame")
			return
		}
	}
	s.mu.Lock()
	var cancel context.CancelFunc
	if s.active != nil {
		cancel = s.active.cancel
	}
	active := s.respID
	s.mu.Unlock()
	if want != "" && want != active {
		s.sendError(ctx, "invalid_request_error", "response_id 和正在进行的生成对不上，没法取消", "response_not_found")
		return
	}
	if cancel != nil {
		cancel()
	}
}

// runCreate 跑一次生成：组请求体、交给现有 HTTP 处理器、把回来的事件转成帧。
func (s *wsSession) runCreate(ctx context.Context, frame map[string]json.RawMessage, gen *wsGen) {
	// 预热（generate:false）：只认 key 和分组权限，不路由模型、不计费
	if wsIsFalse(frame["generate"]) {
		s.runWarmup(ctx, frame, gen)
		return
	}

	body, input, prev, err := s.buildRequestBody(frame)
	if err != nil {
		var reqErr *wsRequestError
		if errors.As(err, &reqErr) {
			s.sendError(ctx, reqErr.errType, reqErr.message, reqErr.code)
			return
		}
		s.sendError(ctx, "invalid_request_error", err.Error(), "invalid_request")
		return
	}

	if prev != "" {
		// 上一轮的响应 id 放进 context：桥接层可以拿它把这次请求固定回原来的渠道
		ctx = context.WithValue(ctx, wsPreviousResponseKey{}, prev)
	}
	gen.input = input // 终态时要把这次的输入记进历史
	w := newWSResponseWriter(s, ctx, gen)
	s.handle(w, s.newRequest(ctx, body))
	w.finish(ctx.Err() != nil)
}

// runWarmup 处理预热请求：不碰模型和账单，只把连接热起来。
func (s *wsSession) runWarmup(ctx context.Context, frame map[string]json.RawMessage, gen *wsGen) {
	probe := &wsProbeWriter{header: http.Header{}}
	owner, ok := s.auth(probe, s.upgrade.Clone(ctx))
	if !ok {
		s.sendError(ctx, probe.errType(), probe.message(), "")
		return
	}
	if !owner.GroupAllowed {
		s.sendError(ctx, "permission_error", "你已经没有这个 key 所在分组的使用权限，请换一个 key 或联系管理员", "group_forbidden")
		return
	}

	id := newWSResponseID()
	model := ""
	if raw, ok := frame["model"]; ok {
		json.Unmarshal(raw, &model)
	}
	created := map[string]any{
		"id": id, "object": "response", "created_at": time.Now().Unix(),
		"model": model, "status": "in_progress", "output": []any{},
	}
	s.sendFrame(ctx, map[string]any{"type": "response.created", "response": created, "sequence_number": 0})

	// 预热也要能被下一轮的 previous_response_id 指回来：记下这次发出去的输入
	input, _ := wsDecodeInput(frame["input"])
	gen.input = input
	s.completeGeneration(gen, id, nil)
	s.releaseGeneration(gen)

	completed := map[string]any{
		"id": id, "object": "response", "created_at": time.Now().Unix(),
		"model": model, "status": "completed", "output": []any{},
	}
	s.sendFrame(ctx, map[string]any{"type": "response.completed", "response": completed, "sequence_number": 1})
}

// buildRequestBody 把一帧变成发给上游的请求体。
// previous_response_id 在这里展开：网关把上次的输入和输出接到这次输入前面，
// 再把 previous_response_id 去掉，免得上游把同一段历史算两遍。
func (s *wsSession) buildRequestBody(frame map[string]json.RawMessage) ([]byte, []json.RawMessage, string, error) {
	fields := make(map[string]json.RawMessage, len(frame))
	for k, v := range frame {
		fields[k] = v
	}
	delete(fields, "type")     // 帧类型不是 Responses API 的字段，别发给上游
	delete(fields, "generate") // 预热开关也不是 API 字段

	// WebSocket 这边本来就是流式的，Codex 的帧里常常不带 stream，上游必须给事件流
	if !strings.HasSuffix(s.upgrade.URL.Path, "/compact") {
		fields["stream"] = json.RawMessage("true")
	}

	var prev string
	if raw, ok := fields["previous_response_id"]; ok {
		json.Unmarshal(raw, &prev)
		delete(fields, "previous_response_id")
	}

	input, hasInput := wsDecodeInput(fields["input"])
	if prev != "" {
		// 只能引用这条连接自己那几次响应，不能拿别的连接的 id 翻别人的历史
		if !s.keeps(prev) {
			return nil, nil, "", &wsRequestError{
				errType: "invalid_request_error",
				code:    wsErrorCodePreviousResponseNotFound,
				message: "找不到 previous_response_id 指向的那次响应，它不在这条连接的历史里，请开始新的一轮对话",
			}
		}
		prevInput, prevOutput, ok := s.history.get(s.userID, prev)
		if !ok {
			return nil, nil, "", &wsRequestError{
				errType: "invalid_request_error",
				code:    wsErrorCodePreviousResponseNotFound,
				message: "找不到 previous_response_id 指向的那次响应，它可能已经过期或被清理了，请开始新的一轮对话",
			}
		}
		full := make([]json.RawMessage, 0, len(prevInput)+len(prevOutput)+len(input))
		full = append(full, prevInput...)
		full = append(full, prevOutput...)
		full = append(full, input...)
		input, hasInput = full, true
	}
	if hasInput {
		fields["input"], _ = json.Marshal(input)
	}

	body, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, "", err
	}
	return body, input, prev, nil
}

// wsPreviousResponseKey 是把上一轮响应 id 放进请求 context 的键，桥接层可以取。
type wsPreviousResponseKey struct{}

// newRequest 克隆升级请求当作这次生成的 HTTP 请求：认证头、路径、query 都沿用。
func (s *wsSession) newRequest(ctx context.Context, body []byte) *http.Request {
	req := s.upgrade.Clone(ctx)
	req.Method = http.MethodPost
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	// 握手用的头别发给上游
	req.Header.Del("Connection")
	req.Header.Del("Upgrade")
	req.Header.Del("Origin")
	for k := range req.Header {
		if strings.HasPrefix(strings.ToLower(k), "sec-websocket-") {
			req.Header.Del(k)
		}
	}
	return req
}

// completeGeneration 把这次响应的 id 和输出记进历史缓存，客户端下一轮可以指回来。
// 终态帧一发出去客户端马上就会发下一轮，所以要赶在写帧之前做完。
func (s *wsSession) completeGeneration(gen *wsGen, id string, output []json.RawMessage) {
	if id == "" || gen == nil {
		return
	}
	s.mu.Lock()
	if gen.stored {
		s.mu.Unlock()
		return
	}
	gen.stored = true
	s.mu.Unlock()
	s.history.put(s.userID, id, gen.input, output)
	s.keepHistory(id)
}

// releaseGeneration 把连接让给下一次生成。
func (s *wsSession) releaseGeneration(gen *wsGen) {
	s.mu.Lock()
	if s.active == gen {
		s.active = nil
	}
	s.mu.Unlock()
}

// failGeneration 写帧失败时中止这次生成：上游不用再继续生成下去。
func (s *wsSession) failGeneration(gen *wsGen) {
	if gen == nil {
		return
	}
	gen.cancel()
}

// keeps 判断这个响应 id 是不是这条连接自己的。
func (s *wsSession) keeps(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.keep {
		if k == id {
			return true
		}
	}
	return false
}

// keepHistory 只保留最近几个响应 id：钉在缓存里不被淘汰，别的交给 LRU。
func (s *wsSession) keepHistory(id string) {
	s.mu.Lock()
	old := s.keep
	keep := make([]string, 0, wsHistoryKeepPerConn)
	for _, k := range s.keep {
		if k != id {
			keep = append(keep, k)
		}
	}
	keep = append(keep, id)
	if len(keep) > wsHistoryKeepPerConn {
		keep = keep[len(keep)-wsHistoryKeepPerConn:]
	}
	s.keep = keep
	s.mu.Unlock()

	added := make([]string, 0, len(keep))
	for _, k := range keep {
		if !containsString(old, k) {
			added = append(added, k)
		}
	}
	s.history.pin(added)
	// 掉出最近列表的 id 已经不能被引用了，直接清掉，别让没人用的历史占着容量
	removed := make([]string, 0, len(old))
	for _, k := range old {
		if !containsString(keep, k) {
			removed = append(removed, k)
		}
	}
	s.history.remove(removed)
}

// sendFrame 发一帧 JSON。
func (s *wsSession) sendFrame(ctx context.Context, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("WebSocket 帧序列化失败", "err", err)
		return err
	}
	return s.writeFrame(ctx, data)
}

// writeFrame 发一帧。写失败要告诉调用方：上游还在生成，得让它停。
func (s *wsSession) writeFrame(ctx context.Context, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
		slog.Debug("WebSocket 写帧失败", "err", err)
		return err
	}
	return nil
}

// setActiveResponse 记下当前这次生成的响应 id，response.cancel 拿来核对。
func (s *wsSession) setActiveResponse(id string) {
	s.mu.Lock()
	s.respID = id
	s.mu.Unlock()
}

// sendError 发一个错误帧。Responses 协议的错误事件是扁平的：type/code/message/param。
func (s *wsSession) sendError(ctx context.Context, errType, message, code string) {
	s.sendFrame(ctx, map[string]any{
		"type": "error", "code": code, "message": message, "param": nil,
		"error": map[string]any{"type": errType, "message": message, "code": code},
	})
}

// wsRequestError 是客户端请求本身的问题，用错误帧回给客户端。
type wsRequestError struct {
	errType string
	code    string
	message string
}

func (e *wsRequestError) Error() string { return e.message }

// wsProbeWriter 收下鉴权失败时写的错误，用来转成 WebSocket 的错误帧。
type wsProbeWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *wsProbeWriter) Header() http.Header { return w.header }

func (w *wsProbeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *wsProbeWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

// message 取出错误信息。
func (w *wsProbeWriter) message() string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(w.body.Bytes(), &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return strings.TrimSpace(w.body.String())
}

// errType 取出错误类型。
func (w *wsProbeWriter) errType() string {
	var e struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(w.body.Bytes(), &e) == nil && e.Error.Type != "" {
		return e.Error.Type
	}
	return "authentication_error"
}

// wsIsFalse 判断字段是不是显式的 false。
func wsIsFalse(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return !b
	}
	return false
}

// wsDecodeInput 把 input 字段转成消息列表。input 可以是数组，也可以是一段纯文本。
func wsDecodeInput(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		return items, true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		item, _ := json.Marshal(map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		})
		return []json.RawMessage{item}, true
	}
	return []json.RawMessage{raw}, true
}

// newWSResponseID 生成网关自己造的响应 id。
func newWSResponseID() string {
	return "resp_lingshu_" + strings.ToLower(rand.Text())
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
