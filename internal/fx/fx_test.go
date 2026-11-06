package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
)

func TestFetch(t *testing.T) {
	for body, want := range map[string]string{
		`{"amount":1.0,"base":"USD","date":"2026-11-05","rates":{"CNY":6.6684}}`:   "6.6684",
		`{"amount":1.0,"base":"USD","date":"2026-11-05","rates":{"CNY":6.668449}}`: "6.6684",
		`{"rates":{"EUR":0.9}}`: "",
		`{"rates":{"CNY":-1}}`:  "",
		`<html>moved</html>`:    "",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		rate, _, err := Fetch(context.Background(), srv.Client(), srv.URL)
		srv.Close()
		if want == "" {
			if err == nil {
				t.Errorf("%s 应报错，得到 %s", body, rate)
			}
			continue
		}
		if err != nil || rate.String() != want {
			t.Errorf("%s: 应为 %s，得到 %s %v", body, want, rate, err)
		}
	}
}

func TestTooFar(t *testing.T) {
	d := decimal.RequireFromString
	if TooFar(d("6.7"), d("6.6684")) || !TooFar(d("6.7"), d("7.5")) || !TooFar(d("6.7"), d("6.0")) || TooFar(decimal.Zero, d("6.7")) {
		t.Error("10% 阈值判断不对")
	}
}
