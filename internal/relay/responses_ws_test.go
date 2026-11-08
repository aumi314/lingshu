package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/store"
	"github.com/coder/websocket"
)

// fakeWSConn 记录写出去的帧。
type fakeWSConn struct {
	mu     sync.Mutex
	frames [][]byte
	fail   bool // 写帧一律失败，模拟连接断了
}

func (c *fakeWSConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	<-ctx.Done()
	return 0, nil, ctx.Err()
}

func (c *fakeWSConn) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("连接断了")
	}
	c.frames = append(c.frames, append([]byte(nil), p...))
	return nil
}

func (c *fakeWSConn) Close(code websocket.StatusCode, reason string) error { return nil }

func (c *fakeWSConn) SetReadLimit(n int64) {}

// events 解析已经写出去的帧。
func (c *fakeWSConn) events() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, 0, len(c.frames))
	for _, f := range c.frames {
		var m map[string]any
		if json.Unmarshal(f, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// waitEvents 等到帧数达到 want。
func (c *fakeWSConn) waitEvents(t *testing.T, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.events(); len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等了 3 秒还是没等到 %d 帧，实际 %d 帧", want, len(c.events()))
	return nil
}

func wsTestRequest() *http.Request {
	req := httptest.NewRequest("GET", "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Session-Id", "sess-1")
	return req
}

func newWSTestSession(conn wsConn) *wsSession {
	return newWSSession(conn, wsTestRequest(), newWSHistoryStore(wsHistoryMaxBytes))
}

func TestWSResponseCreatePassesThroughEvents(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	var body []byte
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
		w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\"}]}}\n\n"))
	}

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true}`))
	events := conn.waitEvents(t, 3)
	if events[0]["type"] != "response.created" || events[1]["type"] != "response.output_text.delta" || events[2]["type"] != "response.completed" {
		t.Fatalf("事件顺序不对: %v", events)
	}
	// 帧类型不是 Responses API 的字段，不能发给上游
	var sent map[string]any
	json.Unmarshal(body, &sent)
	if _, ok := sent["type"]; ok {
		t.Fatalf("发给上游的请求体里不该有 type 字段: %s", body)
	}
	if sent["model"] != "gpt-x" {
		t.Fatalf("model 没原样传给上游: %s", body)
	}
}

func TestWSWarmupSkipsModelAndBilling(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	called := false
	s.handle = func(w http.ResponseWriter, r *http.Request) { called = true }
	s.auth = func(w http.ResponseWriter, r *http.Request) (*store.KeyOwner, bool) {
		return &store.KeyOwner{GroupAllowed: true}, true
	}

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","generate":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"warm"}]}]}`))
	events := conn.waitEvents(t, 2)
	if called {
		t.Fatal("预热不该进模型路由")
	}
	if events[0]["type"] != "response.created" || events[1]["type"] != "response.completed" {
		t.Fatalf("预热应该回 response.created 和 response.completed: %v", events)
	}
	resp, _ := events[0]["response"].(map[string]any)
	id, _ := resp["id"].(string)
	if !strings.HasPrefix(id, "resp_lingshu_") {
		t.Fatalf("预热的响应 id 不对: %v", id)
	}
	// 预热的 id 要能被下一轮 previous_response_id 指回来
	if _, _, ok := s.history.get(s.userID, id); !ok {
		t.Fatal("预热的响应没进历史缓存")
	}
}

func TestWSWarmupRejectsLostGroup(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	s.handle = func(w http.ResponseWriter, r *http.Request) { t.Fatal("预热不该进模型路由") }
	s.auth = func(w http.ResponseWriter, r *http.Request) (*store.KeyOwner, bool) {
		return &store.KeyOwner{GroupAllowed: false}, true
	}

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","generate":false}`))
	events := conn.waitEvents(t, 1)
	if events[0]["type"] != "error" {
		t.Fatalf("没有分组权限应该回错误帧: %v", events)
	}
}

func TestWSPreviousResponseExpandsHistory(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	var mu sync.Mutex
	var bodies [][]byte
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\"}]}}\n\n"))
	}

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}]}`))
	conn.waitEvents(t, 2)

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","previous_response_id":"resp_1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]}]}`))
	conn.waitEvents(t, 4)

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("应该跑了两次生成，实际 %d 次", len(bodies))
	}
	var sent map[string]any
	json.Unmarshal(bodies[1], &sent)
	if _, ok := sent["previous_response_id"]; ok {
		t.Fatalf("展开之后不该再带 previous_response_id: %s", bodies[1])
	}
	input, _ := sent["input"].([]any)
	// 上次的输入、上次的输出、这次的输入，一共三条
	if len(input) != 3 {
		t.Fatalf("历史没展开对: %s", bodies[1])
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("第一条应该是上次的输入: %v", input[0])
	}
	second, _ := input[1].(map[string]any)
	if second["id"] != "msg_1" {
		t.Fatalf("第二条应该是上次的输出: %v", input[1])
	}
}

func TestWSPreviousResponseNotFound(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	s.handle = func(w http.ResponseWriter, r *http.Request) { t.Fatal("引用不存在的响应不该发上游") }

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","previous_response_id":"resp_missing","input":[]}`))
	events := conn.waitEvents(t, 1)
	if events[0]["type"] != "error" || events[0]["code"] != wsErrorCodePreviousResponseNotFound {
		t.Fatalf("应该回 previous_response_not_found: %v", events)
	}
}

func TestWSRejectsConcurrentGenerate(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	started := make(chan struct{})
	release := make(chan struct{})
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	<-started
	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	events := conn.waitEvents(t, 1)
	if events[0]["type"] != "error" || events[0]["code"] != "concurrent_generation_not_allowed" {
		t.Fatalf("同时两个生成应该回错误帧: %v", events)
	}
	close(release)
}

func TestWSCancelStopsGenerate(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	started := make(chan struct{})
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
	}

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	<-started
	s.onFrame(context.Background(), []byte(`{"type":"response.cancel"}`))
	events := conn.waitEvents(t, 2)
	last := events[len(events)-1]
	if last["type"] != "response.completed" {
		t.Fatalf("取消后应该给一个终态帧: %v", last)
	}
	resp, _ := last["response"].(map[string]any)
	if resp["status"] != "cancelled" {
		t.Fatalf("取消后的状态应该是 cancelled: %v", resp)
	}
}

func TestWSHistoryStoreEvictsAndPins(t *testing.T) {
	store := newWSHistoryStore(5000)
	for i := 1; i <= 3; i++ {
		// 每条记录的内容都不一样，才算得出真实的占用
		item := json.RawMessage(`{"type":"message","content":"` + strings.Repeat("x", 1000) + string(rune('0'+i)) + `"}`)
		store.put(1, "resp_"+string(rune('0'+i)), []json.RawMessage{item}, nil)
	}
	// 钉住最旧的那条
	store.pin([]string{"resp_1"})
	// 再塞一条就超了上限，没钉住的先被淘汰
	store.put(1, "resp_4", []json.RawMessage{json.RawMessage(`{"type":"message","content":"` + strings.Repeat("x", 1000) + `4"}`)}, nil)
	if _, _, ok := store.get(1, "resp_1"); !ok {
		t.Fatal("钉住的记录不该被淘汰")
	}
	if _, _, ok := store.get(1, "resp_2"); ok {
		t.Fatal("没钉住的记录应该被淘汰")
	}
	if _, _, ok := store.get(1, "resp_4"); !ok {
		t.Fatal("最新的记录不该被淘汰")
	}
}

func TestWSHistoryStoreScopesToUser(t *testing.T) {
	store := newWSHistoryStore(wsHistoryMaxBytes)
	store.put(1, "resp_1", []json.RawMessage{json.RawMessage(`{"role":"user"}`)}, nil)
	if _, _, ok := store.get(2, "resp_1"); ok {
		t.Fatal("别人的响应不该取得到")
	}
	if _, _, ok := store.get(1, "resp_1"); !ok {
		t.Fatal("自己的响应应该取得到")
	}
}

func TestWSHistoryStoreSharesNodes(t *testing.T) {
	store := newWSHistoryStore(wsHistoryMaxBytes)
	item := json.RawMessage(`{"role":"user","content":"同一段历史"}`)
	store.put(1, "resp_1", []json.RawMessage{item}, nil)
	store.put(1, "resp_2", []json.RawMessage{item, item}, nil)
	// 同一段内容只存一份，容量按一份算
	want := len(item) + wsHistoryBlobOverhead + 2*wsHistoryEntryOverhead
	if store.size != want {
		t.Fatalf("共享之后容量应该只算一份，实际 %d，期望 %d", store.size, want)
	}
}

// TestWSRapidConsecutiveCreates 模拟 Codex 的工具循环：拿到终态帧马上发下一轮。
// 上一轮的结算还没写完日志，这一轮也必须能开，并且带上前面的历史。
func TestWSRapidConsecutiveCreates(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	var mu sync.Mutex
	var bodies [][]byte
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\"}]}}\n\n"))
		// 模拟结算、写日志还没做完
		time.Sleep(300 * time.Millisecond)
	}

	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}]}`))
	conn.waitEvents(t, 2) // 收到终态帧
	// 客户端收到终态帧马上发下一轮
	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","previous_response_id":"resp_1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]}]}`))
	conn.waitEvents(t, 4)

	for _, ev := range conn.events() {
		if ev["type"] == "error" {
			t.Fatalf("不该有错误帧: %v", ev)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("应该跑了两次生成，实际 %d 次", len(bodies))
	}
	var sent map[string]any
	json.Unmarshal(bodies[1], &sent)
	input, _ := sent["input"].([]any)
	// 上次的输入、上次的输出、这次的输入
	if len(input) != 3 {
		t.Fatalf("第二轮的历史没展开: %s", bodies[1])
	}
}

// TestWSReconnectCannotUseOldResponse 连接断了以后，这条 id 就不能再被引用。
func TestWSReconnectCannotUseOldResponse(t *testing.T) {
	store := newWSHistoryStore(wsHistoryMaxBytes)
	firstConn := &fakeWSConn{}
	first := newWSSession(firstConn, wsTestRequest(), store)
	first.userID = 7
	first.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n"))
	}
	first.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	firstConn.waitEvents(t, 2)

	// 连接断了：历史清掉，容量还回来
	first.release()
	if _, _, ok := store.get(7, "resp_1"); ok {
		t.Fatal("连接断开后历史应该清掉")
	}

	// 重连之后（同一个用户）拿旧 id 来引用：拿不到
	secondConn := &fakeWSConn{}
	second := newWSSession(secondConn, wsTestRequest(), store)
	second.userID = 7
	second.handle = func(w http.ResponseWriter, r *http.Request) { t.Fatal("旧 id 不该发上游") }
	second.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","previous_response_id":"resp_1","input":[]}`))
	events := secondConn.waitEvents(t, 1)
	if events[0]["type"] != "error" || events[0]["code"] != wsErrorCodePreviousResponseNotFound {
		t.Fatalf("重连后引用旧 id 应该回错误帧: %v", events)
	}
}

// TestWSWriteFailureStopsGeneration 帧发不出去时，别让上游接着生成。
func TestWSWriteFailureStopsGeneration(t *testing.T) {
	conn := &fakeWSConn{fail: true}
	s := newWSTestSession(conn)
	var writeErr error
	stopped := make(chan struct{})
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, writeErr = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		select {
		case <-r.Context().Done():
			close(stopped)
		case <-time.After(2 * time.Second):
		}
	}
	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("写帧失败后上游请求没被取消")
	}
	if writeErr == nil {
		t.Fatal("写帧失败要告诉处理器，别让它以为发出去了")
	}
}

// TestWSTerminalFrameWaitsForSettlement 终态帧要等处理器把账结完再发：
// 客户端拿到终态帧就可能马上发下一轮，那时上一轮的日志必须已经写好，
// 否则下一轮按响应 id 查渠道会查不到。
func TestWSTerminalFrameWaitsForSettlement(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	finished := false
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n"))
		// 模拟结算、写日志
		time.Sleep(200 * time.Millisecond)
		finished = true
	}
	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	conn.waitEvents(t, 2)
	if !finished {
		t.Fatal("终态帧不能早于处理器结束")
	}
}

// TestWSErrorBodyKeepsCode 网关返回的结构化错误，转成错误帧时不能把 code 丢了。
func TestWSErrorBodyKeepsCode(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"message":"没有那次响应","type":"invalid_request_error","code":"previous_response_not_found"}}`))
	}
	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	events := conn.waitEvents(t, 1)
	if events[0]["type"] != "error" || events[0]["code"] != wsErrorCodePreviousResponseNotFound {
		t.Fatalf("错误帧要保留 code: %v", events)
	}
}

func TestWSRejectsForeignResponseID(t *testing.T) {
	conn := &fakeWSConn{}
	owner := &fakeWSConn{}
	first := newWSTestSession(owner)
	first.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n"))
	}
	first.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	owner.waitEvents(t, 2)

	// 另一条连接拿同一个 id 来引用，不该给
	second := newWSTestSession(conn)
	second.userID = 2
	second.handle = func(w http.ResponseWriter, r *http.Request) { t.Fatal("别人的响应不该发上游") }
	second.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","previous_response_id":"resp_1","input":[]}`))
	events := conn.waitEvents(t, 1)
	if events[0]["type"] != "error" || events[0]["code"] != wsErrorCodePreviousResponseNotFound {
		t.Fatalf("引用别的连接的响应应该回错误帧: %v", events)
	}
}

func TestWSUnknownFrameTypeGetsError(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	s.onFrame(context.Background(), []byte(`{"type":"response.bogus"}`))
	events := conn.waitEvents(t, 1)
	if events[0]["type"] != "error" || events[0]["code"] != "unsupported_type" {
		t.Fatalf("不认识的帧类型应该回错误帧: %v", events)
	}
}

func TestWSCancelChecksResponseID(t *testing.T) {
	conn := &fakeWSConn{}
	s := newWSTestSession(conn)
	started := make(chan struct{})
	s.handle = func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}
	s.onFrame(context.Background(), []byte(`{"type":"response.create","model":"gpt-x","input":[]}`))
	<-started
	s.onFrame(context.Background(), []byte(`{"type":"response.cancel","response_id":"resp_other"}`))
	events := conn.waitEvents(t, 1)
	if events[0]["type"] != "error" || events[0]["code"] != "response_not_found" {
		t.Fatalf("response_id 对不上应该回错误帧: %v", events)
	}
}

// TestWSEndToEnd 走完整的路：真的升级 WebSocket、真的鉴权、真的发上游、真的收到帧。
func TestWSEndToEnd(t *testing.T) {
	e := newEnv(t)
	var upstreamBody map[string]any
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("上游路径 = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&upstreamBody)
		sse(w, `{"type":"response.created","response":{"id":"resp_e2e"}}`,
			`{"type":"response.output_text.delta","delta":"hi"}`,
			`{"type":"response.completed","response":{"id":"resp_e2e","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":12,"output_tokens":4}}}`)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/responses", e.handler.ServeWebSocket())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+e.key)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/responses", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("连不上 WebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	frame := `{"type":"response.create","model":"std-chat","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	types := []string{}
	for len(types) < 3 {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("读帧失败: %v", err)
		}
		var ev struct {
			Type string `json:"type"`
		}
		json.Unmarshal(data, &ev)
		types = append(types, ev.Type)
	}
	if types[0] != "response.created" || types[2] != "response.completed" {
		t.Fatalf("帧顺序不对: %v", types)
	}

	// 发给上游的请求体：帧类型和预热开关不该出现，流式必须打开
	if upstreamBody["type"] != nil || upstreamBody["generate"] != nil {
		t.Errorf("发给上游的请求体里有不该有的字段: %v", upstreamBody)
	}
	if upstreamBody["stream"] != true {
		t.Errorf("发给上游的请求体没开流式: %v", upstreamBody)
	}

	// 这一轮要照常记日志、照常结算
	deadline := time.Now().Add(5 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		e.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM request_logs WHERE status = 'success'`).Scan(&count)
		if count > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if count == 0 {
		t.Fatal("WebSocket 这一轮没有记成功日志")
	}
}
