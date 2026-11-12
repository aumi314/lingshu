package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func (e *env) captureCount() (captures, blobs int) {
	e.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM request_captures), (SELECT count(*) FROM conversation_blobs)`).Scan(&captures, &blobs)
	return
}

func (e *env) logID(requestID string) int64 {
	e.waitLog(requestID)
	var id int64
	e.store.Pool.QueryRow(context.Background(), `SELECT id FROM request_logs WHERE request_id = $1`, requestID).Scan(&id)
	return id
}

func sameJSON(t *testing.T, a, b []byte) {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("还原的请求不是 JSON: %s", a)
	}
	json.Unmarshal(b, &y)
	if !reflect.DeepEqual(x, y) {
		t.Errorf("还原的请求和原请求不一致:\n还原 %s\n原文 %s", a, b)
	}
}

// 对话留存：开关关着不存；开着时普通和流式回复都存下，请求能原样还原，两轮共享的历史只存一次。
func TestConversationCapture(t *testing.T) {
	e := newEnv(t)
	e.upstream = okUpstream
	first := `{"model":"std-chat","messages":[{"role":"system","content":"你是助手"},{"role":"user","content":"第一问"}]}`
	resp := e.post("/v1/chat/completions", first, nil)
	resp.Body.Close()
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if c, _ := e.captureCount(); c != 0 {
		t.Fatalf("开关关着不应留存，实际 %d 条", c)
	}

	e.exec(`INSERT INTO settings (key, value) VALUES ('capture_conversations', 'true')`)
	e.handler.limits = newLimiter() // 设置缓存 10 秒，测试里直接换一个
	resp = e.post("/v1/chat/completions", first, map[string]string{"Session-Id": "s-1"})
	reply, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	id1 := e.logID(resp.Header.Get("X-Lingshu-Request-Id"))
	_, blobs1 := e.captureCount()
	v, err := e.store.LoadCapture(context.Background(), id1)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, v.Request, []byte(first))
	if string(v.Response) != string(reply) || v.Session != "s-1" || v.ResponseTruncated {
		t.Errorf("回复或会话不对: %q %q %v", v.Response, v.Session, v.ResponseTruncated)
	}

	// 第二轮带上第一轮的历史，只多出新的助手回复和用户提问两块
	second := `{"model":"std-chat","stream":true,"messages":[{"role":"system","content":"你是助手"},{"role":"user","content":"第一问"},{"role":"assistant","content":"hi"},{"role":"user","content":"第二问"}]}`
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"第二答"}}]}`, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`, `[DONE]`)
	}
	resp = e.post("/v1/chat/completions", second, map[string]string{"Session-Id": "s-1"})
	reply, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	id2 := e.logID(resp.Header.Get("X-Lingshu-Request-Id"))
	if c, blobs2 := e.captureCount(); c != 2 || blobs2 != blobs1+2 {
		t.Errorf("第二轮应只新增 2 个内容块：留存 %d 条，内容块 %d → %d", c, blobs1, blobs2)
	}
	v, err = e.store.LoadCapture(context.Background(), id2)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, v.Request, []byte(second))
	if string(v.Response) != string(reply) {
		t.Errorf("流式回复应原样记下客户端收到的内容:\n%q\n%q", v.Response, reply)
	}
}

// 底层短写或断开时，只留存成功写入的前缀，并原样返回写入结果。
type partialCaptureWriter struct {
	http.ResponseWriter
	n   int
	err error
}

func (w *partialCaptureWriter) Write([]byte) (int, error) { return w.n, w.err }

func TestCaptureWriterWriteResult(t *testing.T) {
	failure := errors.New("connection closed")
	for _, tc := range []struct {
		name string
		n    int
		err  error
	}{
		{"full", 6, nil}, {"partial", 2, failure}, {"failed", 0, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &captureWriter{ResponseWriter: &partialCaptureWriter{n: tc.n, err: tc.err}}
			n, err := w.Write([]byte("abcdef"))
			if n != tc.n || err != tc.err || string(w.buf) != "abcdef"[:tc.n] || w.truncated {
				t.Fatalf("n=%d err=%v captured=%q truncated=%v", n, err, w.buf, w.truncated)
			}
		})
	}
}

func TestCaptureWriterLimitAndFlush(t *testing.T) {
	target := httptest.NewRecorder()
	w := &captureWriter{ResponseWriter: target}
	p := bytes.Repeat([]byte("x"), maxCapturedResponse+1)
	n, err := w.Write(p)
	if err != nil || n != len(p) || target.Body.Len() != len(p) || len(w.buf) != maxCapturedResponse || !w.truncated {
		t.Fatalf("n=%d err=%v sent=%d captured=%d truncated=%v", n, err, target.Body.Len(), len(w.buf), w.truncated)
	}
	if err := http.NewResponseController(w).Flush(); err != nil || !target.Flushed {
		t.Fatalf("flush: %v", err)
	}
}
