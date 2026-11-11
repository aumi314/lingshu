package relay

import (
	"net/http"
	"strings"
)

// CollapseDoubleV1 把 /v1/v1/… 当成 /v1/… 处理。有的客户端把接口地址填成 …/v1，又自己再拼一层 /v1/…
// （2026-11-11 cctest 的 Claude、GPT 都因此请求成 /v1/v1/… 被 404），用户不用自己排查地址。
func CollapseDoubleV1(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/v1/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/v1")
			r2.URL.RawPath = ""
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}
