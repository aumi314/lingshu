package relay

import "net/http"

// 对话留存（docs/decisions.md「对话留存」）：开关打开时，入口给 ResponseWriter 套一层，边转发边记下底层 ResponseWriter 成功写入的内容。
// HTTP 留存客户端侧回复；WebSocket 留存转成帧之前的 SSE（之后补发的终态不在此处）。

const maxCapturedResponse = 8 << 20 // 回复最多记 8 MiB，超出部分不存并标记截断

type captureWriter struct {
	http.ResponseWriter
	buf       []byte
	truncated bool
}

func (w *captureWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	p = p[:n]
	if room := maxCapturedResponse - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(len(p), room)]...)
		if len(p) > room {
			w.truncated = true
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return n, err
}

// Unwrap 让 http.NewResponseController 找到底层的 Flush（流式要逐个事件推给客户端）。
func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
