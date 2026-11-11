package relay

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/store"
)

// withBackup 给 std-chat 加一个低优先级的兜底渠道 c-backup（id=3）。
// 模拟上游按路径区分：/v1/... 是主渠道 c-chat，/backup/v1/... 是兜底渠道。
func (e *env) withBackup(primary, backup http.HandlerFunc) (primaryHits, backupHits *atomic.Int32) {
	e.t.Helper()
	upstreamURL := strings.TrimSuffix(e.channelBase(1), "/v1")
	e.exec(`INSERT INTO channels (id, name, api_key, priority, base_url_chat, base_url_responses) VALUES (3, 'c-backup', 'k', -1, $1, $1)`, upstreamURL+"/backup/v1")
	e.exec(`INSERT INTO channel_groups VALUES (3, 1)`)
	e.exec(`INSERT INTO channel_models VALUES (3, 1, 'up-backup')`)
	primaryHits, backupHits = &atomic.Int32{}, &atomic.Int32{}
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/backup/") {
			backupHits.Add(1)
			backup(w, r)
		} else {
			primaryHits.Add(1)
			primary(w, r)
		}
	}
	return primaryHits, backupHits
}

func (e *env) channelBase(id int) string {
	var url string
	e.store.Pool.QueryRow(context.Background(), `SELECT base_url_chat FROM channels WHERE id = $1`, id).Scan(&url)
	return url
}

func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

type logRow struct {
	status    string
	channel   int64
	attempts  int
	httpCode  int
	errorText string
	cost      decimal.Decimal
}

func (e *env) logRow(requestID string) logRow {
	e.t.Helper()
	e.waitLog(requestID)
	var l logRow
	var cost string
	e.store.Pool.QueryRow(context.Background(), `
		SELECT status, COALESCE(channel_id, 0), attempts, http_status, error, cost::text
		FROM request_logs WHERE request_id = $1`, requestID,
	).Scan(&l.status, &l.channel, &l.attempts, &l.httpCode, &l.errorText, &cost)
	l.cost = decimal.RequireFromString(cost)
	return l
}

func TestFailoverToBackupOnServerError(t *testing.T) {
	e := newEnv(t)
	var backupModel string
	e.withBackup(status(500, `{"error":{"message":"boom"}}`), func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		backupModel = string(body)
		okUpstream(w, r)
	})
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(got), `"content":"hi"`) {
		t.Fatalf("应由兜底渠道返回成功，实际 %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(backupModel, `"up-backup"`) {
		t.Errorf("兜底渠道应收到它自己的上游模型名，实际请求体 %s", backupModel)
	}
	l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.status != "success" || l.channel != 3 || l.attempts != 2 {
		t.Errorf("日志 = %+v", l)
	}
	if !l.cost.Equal(decimal.RequireFromString("0.00182")) {
		t.Errorf("只应按成功那次扣一次费，实际 %s", l.cost)
	}
}

func TestNoRetryOnBadRequest(t *testing.T) {
	e := newEnv(t)
	_, backupHits := e.withBackup(status(400, `{"error":{"message":"context too long"}}`), okUpstream)
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || !strings.Contains(string(got), "context too long") {
		t.Errorf("400 应原样返回，实际 %d %s", resp.StatusCode, got)
	}
	if backupHits.Load() != 0 {
		t.Error("400 不应该换渠道重试")
	}
}

func TestUnauthorizedChannelCoolsDownImmediately(t *testing.T) {
	e := newEnv(t)
	primaryHits, _ := e.withBackup(status(401, `{"error":{"message":"invalid key"}}`), okUpstream)
	for i := 0; i < 3; i++ {
		resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("第 %d 次应由兜底渠道成功，实际 %d", i+1, resp.StatusCode)
		}
	}
	if n := primaryHits.Load(); n != 1 {
		t.Errorf("key 失效的主渠道应只被请求 1 次就冷却，实际被请求 %d 次", n)
	}
}

func TestRepeatedFailuresCoolDown(t *testing.T) {
	e := newEnv(t)
	primaryHits, _ := e.withBackup(status(503, `{"error":{"message":"overloaded"}}`), okUpstream)
	for i := 0; i < 5; i++ {
		resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
		resp.Body.Close()
	}
	if n := primaryHits.Load(); n != 3 {
		t.Errorf("连续失败 3 次后应冷却，主渠道实际被请求 %d 次", n)
	}
}

func TestAllChannelsFailReturnsLastErrorAndNoCharge(t *testing.T) {
	e := newEnv(t)
	e.withBackup(status(500, `{"error":{"message":"primary down"}}`), status(429, `{"error":{"message":"backup busy"}}`))
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 429 || !strings.Contains(string(got), "backup busy") {
		t.Errorf("应返回最后一个渠道的错误，实际 %d %s", resp.StatusCode, got)
	}
	l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.status != "upstream_error" || l.attempts != 2 || !l.cost.IsZero() {
		t.Errorf("日志 = %+v", l)
	}
	if !strings.Contains(l.errorText, "c-chat") || !strings.Contains(l.errorText, "c-backup") {
		t.Errorf("日志里应记下每个渠道的失败原因，实际 %q", l.errorText)
	}
}

// 上游报错是中文长文本时，截断不能切在汉字中间；上游原文里的非法字节也要清掉。
// 否则写请求日志被 PostgreSQL 拒绝（invalid byte sequence for encoding "UTF8"），日志丢失、预扣冻结不释放（2026-11-07 线上发生）。
func TestLongChineseUpstreamErrorIsLogged(t *testing.T) {
	e := newEnv(t)
	msg := strings.Repeat("当前模型需要可用的账号", 40)
	e.withBackup(status(429, `{"error":{"message":"`+msg+`"}}`), status(429, "{\"error\":{\"message\":\"bad \xff\xfe byte "+msg+"\"}}"))
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.status != "upstream_error" || !utf8.ValidString(l.errorText) || !strings.Contains(l.errorText, "当前模型") {
		t.Errorf("日志 = %+v", l)
	}
	if got := e.decimal(`SELECT reserved::text FROM users WHERE id = 1`); !got.IsZero() {
		t.Errorf("预扣冻结应已释放，实际 %s", got)
	}
}

func TestStreamErrorAsFirstEventFailsOver(t *testing.T) {
	e := newEnv(t)
	e.withBackup(func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"response.failed","response":{"id":"bad"}}`)
	}, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"response.created","response":{"id":"good"}}`,
			`{"type":"response.completed","response":{"id":"good","usage":{"input_tokens":5,"output_tokens":5}}}`)
	})
	resp := e.post("/v1/responses", `{"model":"std-chat","stream":true}`, nil)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(got), "response.failed") || !strings.Contains(string(got), "response.completed") {
		t.Errorf("一开口就报错的流应换渠道，客户端收到：\n%s", got)
	}
	if l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id")); l.status != "success" || l.channel != 3 {
		t.Errorf("日志 = %+v", l)
	}
}

func TestNoRetryAfterFirstByte(t *testing.T) {
	e := newEnv(t)
	_, backupHits := e.withBackup(func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"half"}}]}`) // 发了内容后断开
	}, okUpstream)
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","stream":true}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if backupHits.Load() != 0 {
		t.Error("已经给客户端发了内容，不能再换渠道（会重复输出）")
	}
	if l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id")); l.status != "upstream_error" || !l.cost.IsZero() {
		t.Errorf("日志 = %+v", l)
	}
}

func TestHeaderTimeoutFailsOver(t *testing.T) {
	e := newEnv(t)
	e.withBackup(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body) // Go 的服务端读完请求体后才会感知连接断开
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}, okUpstream)
	e.handler.BodyHeaderTimeout = 200 * time.Millisecond
	start := time.Now()
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 || time.Since(start) > 3*time.Second {
		t.Errorf("主渠道没响应应很快换到兜底渠道，实际 %d，耗时 %v", resp.StatusCode, time.Since(start))
	}
	if l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id")); l.channel != 3 {
		t.Errorf("日志 = %+v", l)
	}
}

// 同优先级的渠道按会话分流（渠道亲和）：不同会话分散到各渠道，同一个 key 不带会话编号时固定一个渠道。
func TestSamePriorityChannelsShareTraffic(t *testing.T) {
	e := newEnv(t)
	primaryHits, backupHits := e.withBackup(okUpstream, okUpstream)
	e.exec(`UPDATE channels SET priority = 0 WHERE id = 3`)
	for i := 0; i < 40; i++ {
		resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, map[string]string{"Session-Id": "session-" + strconv.Itoa(i)})
		resp.Body.Close()
	}
	if primaryHits.Load() < 5 || backupHits.Load() < 5 {
		t.Errorf("不同会话应分散到同优先级的渠道，实际 主 %d / 备 %d", primaryHits.Load(), backupHits.Load())
	}
	primary, backup := primaryHits.Load(), backupHits.Load()
	for i := 0; i < 10; i++ {
		resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
		resp.Body.Close()
	}
	if dp, db := primaryHits.Load()-primary, backupHits.Load()-backup; dp != 0 && db != 0 {
		t.Errorf("同一个 key 不带会话编号时应固定一个渠道，实际 主 %d / 备 %d", dp, db)
	}
}

func TestPreviousResponseIDSticksToOriginalChannel(t *testing.T) {
	e := newEnv(t)
	primaryHits, backupHits := e.withBackup(func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"response.completed","response":{"id":"resp_A","usage":{"input_tokens":1,"output_tokens":1}}}`)
	}, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"response.completed","response":{"id":"resp_B","usage":{"input_tokens":1,"output_tokens":1}}}`)
	})
	// 第一轮走主渠道，记下 resp_A
	resp := e.post("/v1/responses", `{"model":"std-chat","stream":true}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))

	// 把兜底渠道提到最高优先级，正常情况下请求会去兜底渠道
	e.exec(`UPDATE channels SET priority = 10 WHERE id = 3`)
	resp = e.post("/v1/responses", `{"model":"std-chat","stream":true,"previous_response_id":"resp_A"}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if primaryHits.Load() != 2 || backupHits.Load() != 0 {
		t.Errorf("接着 resp_A 的请求应发回主渠道，实际 主 %d / 备 %d", primaryHits.Load(), backupHits.Load())
	}

	// 原渠道停用后，给出明确提示而不是发到别的渠道
	e.exec(`UPDATE channels SET enabled = false WHERE id = 1`)
	resp = e.post("/v1/responses", `{"model":"std-chat","stream":true,"previous_response_id":"resp_A"}`, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 || !strings.Contains(string(body), "新对话") || backupHits.Load() != 0 {
		t.Errorf("原渠道不可用时应提示开新对话，实际 %d %s", resp.StatusCode, body)
	}
}

func TestUpstreamKeyEchoIsRedacted(t *testing.T) {
	e := newEnv(t)
	e.upstream = status(401, `{"error":{"message":"invalid key `+upstreamKey+`"}}`)
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(got), upstreamKey) {
		t.Errorf("上游 key 泄露给了用户: %s", got)
	}
	if l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id")); strings.Contains(l.errorText, upstreamKey) {
		t.Errorf("上游 key 写进了日志: %s", l.errorText)
	}
}

func TestLogKeepsNamesAfterChannelKeyAndGroupDeleted(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","reasoning_effort":"high"}`, map[string]string{"X-Real-IP": "203.0.113.9"}) // 测试服务器经本机回环转发
	resp.Body.Close()
	rid := resp.Header.Get("X-Lingshu-Request-Id")
	e.logRow(rid)
	e.exec(`DELETE FROM channels WHERE id = 1`)
	e.exec(`DELETE FROM api_keys`)
	e.exec(`DELETE FROM groups WHERE id = 1`)
	logs, _, err := e.store.ListLogs(context.Background(), store.LogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].ChannelName != "c-chat" || logs[0].KeyName != "k" || logs[0].GroupName != "default" || logs[0].ClientIP != "203.0.113.9" || logs[0].ReasoningEffort != "high" {
		t.Errorf("渠道、key 和分组删除后，日志应保留名字快照和访问者 IP；，日志应保留名字快照，实际 %+v", logs)
	}
}

func TestReferencedContentTimeoutFailsOverAndCoolsDown(t *testing.T) {
	e := newEnv(t)
	primary, backup := e.withBackup(status(400, `{"error":{"type":"invalid_request_error","message":"Upstream could not download the referenced content before its timeout. Check the URL's reachability and lifetime."}}`), okUpstream)
	for i := 0; i < 4; i++ {
		resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))
		want := 2
		if i == 3 {
			want = 1
		}
		if l.status != "success" || l.channel != 3 || l.attempts != want {
			t.Fatalf("log = %+v", l)
		}
	}
	if primary.Load() != 3 || backup.Load() != 4 {
		t.Fatalf("hits = %d/%d", primary.Load(), backup.Load())
	}
	var count int
	e.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM request_logs`).Scan(&count)
	if count != 4 {
		t.Fatalf("expected one log per request, got %d", count)
	}
}
func TestReferencedContentTimeoutClassification(t *testing.T) {
	const msg = "Upstream could not download the referenced content before its timeout."
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"known", 400, `{"error":{"type":"invalid_request_error","message":"` + msg + `"}}`, true},
		{"parameter", 400, `{"error":{"type":"invalid_request_error","message":"Invalid timeout parameter"}}`, false},
		{"wrong type", 400, `{"error":{"type":"authentication_error","message":"` + msg + `"}}`, false},
		{"plain text", 400, msg, false},
		{"wrong status", 422, `{"error":{"type":"invalid_request_error","message":"` + msg + `"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := referencedContentTimeout(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// 上游卡死（等不到响应头）时，超时后要换到下一个渠道，不能因为重试总时限和等待时限一样长就直接失败
// （2026-11-11 超超 gpt 连续 5 次 60 秒超时，后面三个渠道一个都没试）。
func TestStreamHeaderTimeoutStillFailsOver(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	defer close(release)
	hang := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	_, backupHits := e.withBackup(hang, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"hi"}}]}`, `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, `[DONE]`)
	})
	e.handler.StreamHeaderTimeout = 300 * time.Millisecond
	e.handler.RetryBudget = 3 * e.handler.StreamHeaderTimeout
	resp := e.post("/v1/chat/completions", `{"model":"std-chat","stream":true}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || backupHits.Load() != 1 {
		t.Fatalf("主渠道超时后应换到兜底渠道: %d，兜底 %d 次", resp.StatusCode, backupHits.Load())
	}
	if l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id")); l.channel != 3 || l.attempts != 2 {
		t.Errorf("日志 = %+v", l)
	}
}

// 日志记下上游响应头到达的时刻（用来定等响应头的时限）。
func TestLogsUpstreamHeaderTime(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	id := resp.Header.Get("X-Lingshu-Request-Id")
	e.waitLog(id)
	var start, header, total int
	e.store.Pool.QueryRow(context.Background(), `SELECT upstream_start_ms, upstream_header_ms, total_ms FROM request_logs WHERE request_id = $1`, id).Scan(&start, &header, &total)
	if header < start || header == 0 || header > total {
		t.Errorf("响应头时刻应在发出请求之后、结束之前: start %d header %d total %d", start, header, total)
	}
}
