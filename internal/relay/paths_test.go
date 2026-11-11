package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCollapseDoubleV1(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("messages " + r.URL.RawQuery)) })
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("models")) })
	h := CollapseDoubleV1(mux)
	for _, c := range []struct{ method, path, want string }{
		{"POST", "/v1/v1/messages?beta=true", "messages beta=true"},
		{"POST", "/v1/messages", "messages "},
		{"GET", "/v1/v1/models", "models"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != 200 || w.Body.String() != c.want {
			t.Errorf("%s %s → %d %q，期望 %q", c.method, c.path, w.Code, w.Body.String(), c.want)
		}
	}
	// 只去一层：/v1/v1/v1/… 仍是 404，不无限兼容
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/v1/v1/messages", nil))
	if w.Code != 404 {
		t.Errorf("三层 /v1 应 404，实际 %d", w.Code)
	}
}
