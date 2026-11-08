package relay

// 把 HTTP 处理器的输出转成 WebSocket 帧。
// 上游是 SSE 时，一个 SSE 事件一帧，data 原样发出去（字段顺序、sequence_number 都不动）；
// 上游不是 SSE 时（普通 JSON 响应或错误），等处理完再合成一帧。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// wsResponseWriter 是给现有 HTTP 处理器用的 ResponseWriter：把写进来的内容变成 WebSocket 帧。
type wsResponseWriter struct {
	sess *wsSession
	ctx  context.Context
	gen  *wsGen // 这次生成，用来记历史和中止上游

	header        http.Header
	status        int
	headerWritten bool
	sse           bool // 上游给的是事件流
	buf           []byte
	writeErr      error

	responseID   string            // 上游这次响应的 id
	lastResp     json.RawMessage   // 最近一次事件里的 response 对象
	output       []json.RawMessage // response.completed 里的 output
	terminal     bool              // 上游有没有给终态事件
	terminalData []byte            // 终态事件先攒着，等账结完再发
}

func newWSResponseWriter(sess *wsSession, ctx context.Context, gen *wsGen) *wsResponseWriter {
	return &wsResponseWriter{sess: sess, ctx: ctx, gen: gen, header: http.Header{}}
}

func (w *wsResponseWriter) Header() http.Header { return w.header }

func (w *wsResponseWriter) WriteHeader(status int) {
	if w.headerWritten {
		return
	}
	w.headerWritten = true
	w.status = status
	w.sse = strings.HasPrefix(w.header.Get("Content-Type"), "text/event-stream")
}

func (w *wsResponseWriter) Write(p []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if !w.sse {
		// 不是事件流：整个响应体攒着，最后合成一帧
		w.buf = append(w.buf, p...)
		if len(w.buf) > wsMaxFrameSize {
			w.writeErr = errors.New("响应体太大")
			return 0, w.writeErr
		}
		return len(p), nil
	}

	w.buf = append(w.buf, p...)
	for {
		at, sep := sseEventEnd(w.buf)
		if at < 0 {
			break
		}
		raw := w.buf[:at]
		w.buf = w.buf[at+sep:]
		w.emit(raw)
	}
	if w.writeErr != nil {
		// 帧发不出去就别再收上游的内容了
		return 0, w.writeErr
	}
	if len(w.buf) > wsMaxFrameSize {
		w.writeErr = errors.New("事件太大")
		return 0, w.writeErr
	}
	return len(p), nil
}

// Flush 让现有处理器的 http.NewResponseController(w).Flush() 有地方可调。
func (w *wsResponseWriter) Flush() {}

// emit 把一个 SSE 事件变成一帧。
func (w *wsResponseWriter) emit(raw []byte) {
	data := sseData(raw)
	if len(data) == 0 {
		return
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return
	}
	w.observe(data)
	if w.terminal {
		// 终态帧先攒着：等处理器把账结完、日志写进去再发。
		// 客户端拿到终态帧就可能马上发下一轮，那时要能查到上一轮的历史和渠道。
		w.terminalData = data
		return
	}
	// 上游的 data 原样发出去，客户端拿到的字段和顺序跟直连上游一样
	if err := w.writeFrame(data); err != nil {
		w.writeErr = err
		// 连接已经写不动了，让这次生成停下来，别让上游白生成
		w.sess.failGeneration(w.gen)
	}
}

// writeFrame 发一帧。写帧不能跟着生成的取消走：取消之后 ctx 已经作废，
// 用它发帧会立刻失败，客户端就收不到终态帧了。
func (w *wsResponseWriter) writeFrame(data []byte) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(w.ctx), wsFrameWriteTimeout)
	defer cancel()
	return w.sess.writeFrame(ctx, data)
}

// observe 从事件里记下这次响应的 id、输出和终态。
func (w *wsResponseWriter) observe(data []byte) {
	var ev struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Type {
	case "response.completed", "response.failed", "response.incomplete", "error":
		w.terminal = true
	}
	if len(ev.Response) == 0 {
		return
	}
	w.lastResp = ev.Response
	var obj struct {
		ID     string            `json:"id"`
		Output []json.RawMessage `json:"output"`
	}
	if json.Unmarshal(ev.Response, &obj) != nil {
		return
	}
	if obj.ID != "" {
		w.responseID = obj.ID
		w.sess.setActiveResponse(obj.ID)
	}
	if len(obj.Output) > 0 {
		w.output = obj.Output
	}
}

// finish 处理器跑完之后的收尾：把剩下的内容发出去，必要时补一个终态帧。
// 这时候账已经结完、日志也写好了。
func (w *wsResponseWriter) finish(cancelled bool) {
	if w.sse {
		// 上游可能没留最后的空行，剩下一段也当事件发出去
		if len(bytes.TrimSpace(w.buf)) > 0 {
			w.emit(w.buf)
		}
		w.buf = nil
		// 历史记好、连接让出来，再把终态帧发出去：
		// 客户端收到终态帧马上发下一轮时，这一轮的账已经结完、日志也写好了
		w.sess.completeGeneration(w.gen, w.responseID, w.output)
		w.sess.releaseGeneration(w.gen)
		if w.terminalData != nil {
			w.writeFrame(w.terminalData)
			return
		}
		if cancelled {
			w.sendCancelled()
			return
		}
		w.sendError("server_error", "上游的响应没有正常结束，请稍后重试", "incomplete_response")
		return
	}

	body := bytes.TrimSpace(w.buf)
	if len(body) == 0 {
		w.sess.completeGeneration(w.gen, w.responseID, w.output)
		w.sess.releaseGeneration(w.gen)
		w.sendError("server_error", "网关没有拿到上游的任何响应，请稍后重试", "empty_response")
		return
	}
	if w.status >= 400 {
		w.sess.completeGeneration(w.gen, w.responseID, w.output)
		w.sess.releaseGeneration(w.gen)
		w.sendErrorBody(body)
		return
	}
	// 整份响应就是一个 response 对象：包成 response.completed
	var obj struct {
		ID     string            `json:"id"`
		Object string            `json:"object"`
		Output []json.RawMessage `json:"output"`
	}
	if json.Unmarshal(body, &obj) == nil && obj.Object == "response" {
		w.responseID = obj.ID
		w.output = obj.Output
		w.terminal = true
		w.sess.completeGeneration(w.gen, w.responseID, w.output)
		w.sess.releaseGeneration(w.gen)
		data, err := json.Marshal(map[string]any{"type": "response.completed", "response": json.RawMessage(body)})
		if err != nil {
			return
		}
		w.writeFrame(data)
		return
	}
	w.terminal = true
	w.sess.completeGeneration(w.gen, w.responseID, w.output)
	w.sess.releaseGeneration(w.gen)
	w.writeFrame(body)
}

// sendErrorBody 把上游的错误响应转成错误帧。
func (w *wsResponseWriter) sendErrorBody(body []byte) {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	msg := truncate(string(body), 500)
	errType := "server_error"
	code := ""
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
		errType = e.Error.Type
		code = e.Error.Code
	}
	if errType == "" {
		errType = "server_error"
	}
	w.sendError(errType, msg, code)
}

// sendError 发一个错误帧。收尾的帧不能跟着生成的取消走，用自己的超时上下文。
func (w *wsResponseWriter) sendError(errType, message, code string) {
	data, err := json.Marshal(map[string]any{
		"type": "error", "code": code, "message": message, "param": nil,
		"error": map[string]any{"type": errType, "message": message, "code": code},
	})
	if err != nil {
		return
	}
	w.writeFrame(data)
}

// sendCancelled 取消时给客户端一个终态帧，客户端才知道这轮结束了。
func (w *wsResponseWriter) sendCancelled() {
	resp := map[string]any{}
	if len(w.lastResp) > 0 {
		json.Unmarshal(w.lastResp, &resp)
	}
	if resp["id"] == nil {
		resp["id"] = w.responseID
	}
	if resp["object"] == nil {
		resp["object"] = "response"
	}
	resp["status"] = "cancelled"
	data, err := json.Marshal(map[string]any{"type": "response.completed", "response": resp})
	if err != nil {
		return
	}
	w.writeFrame(data)
}

// sseEventEnd 找下一个完整 SSE 事件的边界，返回内容和分隔符长度。
func sseEventEnd(buf []byte) (int, int) {
	at := bytes.Index(buf, []byte("\n\n"))
	crlf := bytes.Index(buf, []byte("\r\n\r\n"))
	switch {
	case at >= 0 && (crlf < 0 || at < crlf):
		return at, 2
	case crlf >= 0:
		return crlf, 4
	}
	return -1, 0
}

// sseData 取一个 SSE 事件里的 data 内容（多行 data 用换行拼起来）。
func sseData(raw []byte) []byte {
	var data []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		after, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		after = bytes.TrimPrefix(after, []byte(" "))
		if data != nil {
			data = append(data, '\n')
		}
		data = append(data, after...)
	}
	return data
}
