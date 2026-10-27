package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

func TestSystemOneProbeAndModelList(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing upstream bearer auth")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			io.WriteString(w, `{"models":[{"name":"jev-preview"},{"name":"jev-latest"}]}`)
		case "/v1/systemone":
			var body map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&body)
			if string(body["model"]) != `"jev-latest"` || body["state"] == nil || body["questions"] == nil || body["stream"] != nil || body["messages"] != nil {
				t.Errorf("wrong decision probe: %s", body)
			}
			io.WriteString(w, `{"model":"jev-latest","answers":{"is_urgent":{"type":"noul","noul":0.95}},"usage":{"input_tokens":283,"output_tokens":23}}`)
		default:
			t.Errorf("wrong upstream path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	var ch store.Channel
	raw, _ := json.Marshal(map[string]string{"api_key": "test-key", "base_url_systemone": up.URL + "/v1"})
	json.Unmarshal(raw, &ch)
	h := NewHandler(nil)
	names, err := h.FetchModels(context.Background(), &ch)
	if err != nil || !reflect.DeepEqual(names, []string{"jev-latest", "jev-preview"}) {
		t.Errorf("model list = %v, %v", names, err)
	}
	res := h.Probe(context.Background(), &ch, usage.Protocol("systemone"), "jev-latest")
	if !res.OK || !res.UsageReported || res.InputTokens != 283 || res.OutputTokens != 23 {
		t.Fatalf("decision probe = %+v", res)
	}
}

func TestStandardModelListUsesSingleV1Endpoint(t *testing.T) {
	var requests atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("model list path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer standard-key" {
			t.Errorf("missing standard API key")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"model-z"},{"id":"model-a"}]}`)
	}))
	defer up.Close()

	ch := &store.Channel{
		Template:         "standard",
		APIKey:           "standard-key",
		BaseURLChat:      up.URL + "/v1",
		BaseURLMessages:  up.URL,
		BaseURLResponses: up.URL + "/v1",
	}
	names, err := NewHandler(nil).FetchModels(context.Background(), ch)
	if err != nil || !reflect.DeepEqual(names, []string{"model-a", "model-z"}) {
		t.Fatalf("standard model list = %v, %v", names, err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("standard model list requests = %d, want exactly one", got)
	}
}

func TestSystemOneProbeRejectsInvalidResponse(t *testing.T) {
	for _, body := range []string{`not json`, `{}`, `{"answers":{},"usage":{"input_tokens":1,"output_tokens":0}}`, `{"answers":{"other":{"type":"noul","noul":0.95}}}`} {
		t.Run(body, func(t *testing.T) {
			up := httptest.NewServer(status(200, body))
			defer up.Close()
			var ch store.Channel
			json.NewDecoder(strings.NewReader(`{"base_url_systemone":"` + up.URL + `/v1"}`)).Decode(&ch)
			if res := NewHandler(nil).Probe(context.Background(), &ch, usage.Protocol("systemone"), "jev-latest"); res.OK {
				t.Fatalf("invalid decision response passed: %+v", res)
			}
		})
	}
}

func (e *env) enableSystemOne() {
	e.exec(`UPDATE channels SET base_url_systemone = base_url_chat, base_url_chat = '', base_url_responses = '' WHERE id = 1`)
	e.exec(`UPDATE models SET input_price = 0.06, output_price = 0 WHERE id = 1`)
}

func TestSystemOneRelayAndSettlement(t *testing.T) {
	e := newEnv(t)
	e.enableSystemOne()
	e.exec(`UPDATE groups SET ratio = 1.5 WHERE id = 1`)
	raw, err := os.ReadFile("usage/testdata/nace_systemone.json")
	if err != nil {
		t.Fatal(err)
	}
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/v1/systemone?trace=true" || r.Header.Get("Authorization") != "Bearer "+upstreamKey {
			t.Errorf("upstream request = %s, wrong auth = %v", r.URL, r.Header.Get("Authorization") != "Bearer "+upstreamKey)
		}
		var req map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&req)
		if string(req["model"]) != `"up-chat"` || string(req["state"]) != `{"message":"Help!"}` || req["questions"] == nil || string(req["custom_flag"]) != `{"keep":true}` {
			t.Errorf("request fields lost: %s", req)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Typesafe-Request-Id", "req_fixture")
		w.Write(raw)
	}
	resp := e.post("/v1/systemone?trace=true", `{"model":"std-chat","state":{"message":"Help!"},"questions":{"is_urgent":{"type":"noul","instructions":"Urgent?"}},"custom_flag":{"keep":true}}`, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != string(raw) || resp.Header.Get("X-Typesafe-Request-Id") != "req_fixture" {
		t.Fatalf("response changed: %d %s", resp.StatusCode, body)
	}
	id := resp.Header.Get("X-Lingshu-Request-Id")
	l := e.waitLog(id)
	if l.Status != "success" || l.InputTokens != 26 || l.OutputTokens != 24 || l.Stream {
		t.Fatalf("decision log = %+v", l)
	}
	want := decimal.RequireFromString("0.00000234")
	if cost, estimated := e.cost(id); !cost.Equal(want) || estimated {
		t.Errorf("cost = %s, estimated = %v; want %s", cost, estimated, want)
	}
	if !e.balance().Equal(decimal.NewFromInt(10).Sub(want)) || !e.decimal(`SELECT quota_used::text FROM api_keys WHERE user_id = 1`).Equal(want) {
		t.Error("balance or key quota differs from exact cost")
	}
	var proto string
	e.store.Pool.QueryRow(context.Background(), `SELECT protocol FROM request_logs WHERE request_id = $1`, id).Scan(&proto)
	if proto != "systemone" {
		t.Errorf("log protocol = %s", proto)
	}
	models, err := e.store.PricedModels(context.Background(), []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.Name == "std-chat" && !reflect.DeepEqual(m.Protocols, []string{"systemone"}) {
			t.Errorf("user model protocols = %v", m.Protocols)
		}
	}
}

func TestSystemOneRejectionsDoNotCallUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		header           map[string]string
		status           int
	}{
		{"stream", "/v1/systemone", `{"model":"std-chat","state":"hi","questions":{},"stream":true}`, nil, 400},
		{"wrong endpoint", "/v1/chat/completions", `{"model":"std-chat"}`, nil, 400},
		{"invalid key", "/v1/systemone", `{"model":"std-chat"}`, map[string]string{"Authorization": "Bearer invalid"}, 401},
		{"no balance", "/v1/systemone", `{"model":"std-chat"}`, nil, 402},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.enableSystemOne()
			if tc.name == "no balance" {
				e.exec(`UPDATE users SET balance = 0 WHERE id = 1`)
			}
			var hits atomic.Int32
			e.upstream = func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }
			resp := e.post(tc.path, tc.body, tc.header)
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tc.status || hits.Load() != 0 {
				t.Fatalf("rejection = %d %s, upstream hits = %d", resp.StatusCode, body, hits.Load())
			}
			if tc.name == "wrong endpoint" && !strings.Contains(string(body), "/v1/systemone") {
				t.Errorf("missing actionable endpoint: %s", body)
			}
			if tc.status != 401 {
				if cost, _ := e.cost(resp.Header.Get("X-Lingshu-Request-Id")); !cost.IsZero() {
					t.Errorf("rejected request charged %s", cost)
				}
			}
		})
	}
}

func TestSystemOneRetryAndUpstreamError(t *testing.T) {
	for _, code := range []int{422, 529} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			e := newEnv(t)
			raw, _ := os.ReadFile("usage/testdata/nace_systemone.json")
			var mapped string
			_, backupHits := e.withBackup(status(code, `{"error":{"type":"overloaded","message":"try later"}}`), func(w http.ResponseWriter, r *http.Request) {
				var req struct{ Model string }
				json.NewDecoder(r.Body).Decode(&req)
				mapped = req.Model
				w.Header().Set("Content-Type", "application/json")
				w.Write(raw)
			})
			e.exec(`UPDATE channels SET base_url_systemone = base_url_chat, base_url_chat = '', base_url_responses = '' WHERE id IN (1, 3)`)
			e.exec(`UPDATE models SET input_price = 0.06, output_price = 0 WHERE id = 1`)
			resp := e.post("/v1/systemone", `{"model":"std-chat","state":"Help!","questions":{"is_urgent":{"type":"noul","instructions":"Urgent?"}}}`, nil)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))
			if code == 422 {
				if resp.StatusCode != 422 || backupHits.Load() != 0 || !l.cost.IsZero() {
					t.Fatalf("invalid input retried or charged: %+v", l)
				}
			} else if resp.StatusCode != 200 || backupHits.Load() != 1 || mapped != "up-backup" || l.attempts != 2 || !l.cost.Equal(decimal.RequireFromString("0.00000156")) {
				t.Fatalf("failover or single settlement failed: %+v, mapped=%s, backup hits=%d", l, mapped, backupHits.Load())
			}
		})
	}
}

func TestSystemOneMissingUsageIsEstimated(t *testing.T) {
	for _, usageJSON := range []string{``, `,"usage":{"output_tokens":24}`} {
		t.Run(usageJSON, func(t *testing.T) {
			e := newEnv(t)
			e.enableSystemOne()
			e.upstream = status(200, `{"model":"drex-v2.0","answers":{"is_urgent":{"type":"noul","noul":0.7353}}`+usageJSON+`}`)
			resp := e.post("/v1/systemone", `{"model":"std-chat","state":"Help!","questions":{"is_urgent":{"type":"noul","instructions":"Urgent?"}}}`, nil)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if cost, estimated := e.cost(resp.Header.Get("X-Lingshu-Request-Id")); resp.StatusCode != 200 || !cost.IsPositive() || !estimated {
				t.Errorf("missing input usage must not be free: status=%d cost=%s estimated=%v", resp.StatusCode, cost, estimated)
			}
		})
	}
}

func TestSystemOneInvalidSuccessIsNotCharged(t *testing.T) {
	e := newEnv(t)
	e.enableSystemOne()
	e.upstream = status(200, `{"error":{"message":"not a decision"}}`)
	resp := e.post("/v1/systemone", `{"model":"std-chat","state":"Help!","questions":{"is_urgent":{"type":"noul","instructions":"Urgent?"}}}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	l := e.logRow(resp.Header.Get("X-Lingshu-Request-Id"))
	if resp.StatusCode != 502 || l.status != "upstream_error" || !l.cost.IsZero() {
		t.Errorf("invalid successful response charged: %+v", l)
	}
}
