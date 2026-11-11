package relay

import "net/http"

// 对话留存（docs/decisions.md「对话留存」）：开关打开时，入口给 ResponseWriter 套一层，边转发边记下客户端实际收到的内容。
// 普通响应、流式、两个转接方向、WebSocket 最终都经过同一个入口，套一层就都记得到。

const maxCapturedResponse = 8 << 20 // 回复最多记 8 MiB，超出部分不存并标记截断

type captureWriter struct {
	http.ResponseWriter
	buf       []byte
	truncated bool
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if room := maxCapturedResponse - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(len(p), room)]...)
		if len(p) > room {
			w.truncated = true
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return w.ResponseWriter.Write(p)
}

// Unwrap 让 http.NewResponseController 找到底层的 Flush（流式要逐个事件推给客户端）。
func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
