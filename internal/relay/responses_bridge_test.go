package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

func bridgeText(w http.ResponseWriter, text, stop string) {
	quoted, _ := json.Marshal(text)
	sse(w,
		`{"type":"message_start","message":{"id":"msg_up","type":"message","role":"assistant","content":[],"model":"std-msg","usage":{"input_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, quoted),
		`{"type":"content_block_stop","index":0}`,
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":50}}`, stop),
		`{"type":"message_stop"}`)
}

func TestResponsesMessagesStreamingAndRawBilling(t *testing.T) {
	e := newEnv(t)
	_, err := e.store.Pool.Exec(context.Background(), `UPDATE models SET cache_write_price=3 WHERE id=2`)
	if err != nil {
		t.Fatal(err)
	}
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anthropic/v1/messages" || r.Header.Get("x-api-key") != upstreamKey || r.Header.Get("Authorization") != "" {
			t.Errorf("bad upstream %s %v", r.URL.Path, r.Header)
		}
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		if v["stream"] != true || v["model"] != "std-msg" {
			t.Errorf("body=%v", v)
		}
		bridgeText(w, "first line\n第二行\\literal", "end_turn")
	}
	resp := e.post("/v1/responses", `{"model":"std-msg","stream":true,"input":"hello","reasoning":{"effort":"none"}}`, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	reader := usage.NewSSEReader(strings.NewReader(string(body)))
	var completed map[string]any
	for {
		ev, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(ev.Data, &v); err != nil {
			t.Fatalf("invalid SSE JSON %s: %v", ev.Data, err)
		}
		if v["type"] == "response.completed" {
			completed = v["response"].(map[string]any)
		}
	}
	if completed == nil {
		t.Fatalf("missing completed: %s", body)
	}
	u := completed["usage"].(map[string]any)
	if u["input_tokens"] != float64(600) || u["output_tokens"] != float64(50) {
		t.Fatalf("client usage=%v", u)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "success" || l.InputTokens != 100 || l.CacheReadTokens != 200 || l.OutputTokens != 50 {
		t.Fatalf("log=%+v", l)
	}
	var protocol, upstream, operation string
	var writeTokens int
	var cost string
	err = e.store.Pool.QueryRow(context.Background(), `SELECT protocol,upstream_protocol,operation,cache_write_tokens,cost::text FROM request_logs WHERE request_id=$1`, resp.Header.Get("X-Lingshu-Request-Id")).Scan(&protocol, &upstream, &operation, &writeTokens, &cost)
	if err != nil {
		t.Fatal(err)
	}
	if protocol != "responses" || upstream != "messages" || operation != "generation" || writeTokens != 300 || cost != "0.0011200000" {
		t.Fatalf("protocol=%s/%s operation=%s write=%d cost=%s", protocol, upstream, operation, writeTokens, cost)
	}
}

func TestResponsesMessagesJSONAndExplicitCompaction(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		bridgeText(w, "keep marker A-812\nfile ready", "end_turn")
	}
	resp := e.post("/v1/responses", `{"model":"std-msg","input":"hi"}`, nil)
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result["status"] != "completed" {
		t.Fatalf("response=%v", result)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/responses/compact", e.handler.Serve(usage.Responses))
	server := httptest.NewServer(mux)
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/v1/responses/compact", strings.NewReader(`{"model":"std-msg","input":"conversation"}`))
	req.Header.Set("Authorization", "Bearer "+e.key)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result = nil
	json.NewDecoder(response.Body).Decode(&result)
	if response.StatusCode != 200 || result["object"] != "response.compaction" {
		t.Fatalf("%d %v", response.StatusCode, result)
	}
	output := result["output"].([]any)
	if len(output) != 1 || output[0].(map[string]any)["type"] != "compaction" {
		t.Fatalf("output=%v", output)
	}
	l := e.waitLog(response.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "success" {
		t.Fatalf("log=%+v", l)
	}
	var operation string
	e.store.Pool.QueryRow(context.Background(), `SELECT operation FROM request_logs WHERE request_id=$1`, response.Header.Get("X-Lingshu-Request-Id")).Scan(&operation)
	if operation != "compaction" {
		t.Fatal(operation)
	}
	replay, _ := json.Marshal(map[string]any{"model": "std-msg", "input": append(output, map[string]any{"role": "user", "content": "continue"})})
	response = e.post("/v1/responses", string(replay), nil)
	b, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("replay %d %s", response.StatusCode, b)
	}
}

func TestResponsesMessagesFirstErrorRetriesAndNormalizes(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			sse(w, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
			return
		}
		bridgeText(w, "ok", "end_turn")
	}
	resp := e.post("/v1/responses", `{"model":"std-msg","stream":true,"input":"hi"}`, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if calls.Load() != 2 || !strings.Contains(string(body), "response.completed") {
		t.Fatalf("calls=%d %s", calls.Load(), body)
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad parameter upstream-secret"}}`)
	}
	resp = e.post("/v1/responses", `{"model":"std-msg","input":"hi"}`, nil)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || strings.Contains(string(body), upstreamKey) || !strings.Contains(string(body), "invalid_request_error") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestResponsesMessagesTruncationAndContextErrors(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		sse(w, `{"type":"message_start","message":{"usage":{"input_tokens":1}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`)
	}
	resp := e.post("/v1/responses", `{"model":"std-msg","stream":true,"input":"hi"}`, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if calls.Load() != 1 || !strings.Contains(string(body), "response.failed") || strings.Contains(string(body), "response.completed") {
		t.Fatalf("calls=%d %s", calls.Load(), body)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "upstream_error" {
		t.Fatal(l.Status)
	}
	for _, input := range []string{`{"model":"std-msg","store":false,"previous_response_id":"unknown","input":"delta"}`, `{"model":"std-msg","context_management":{},"input":"hi"}`} {
		resp = e.post("/v1/responses", input, nil)
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
	}
}

func TestResponsesMessagesCancelPropagates(t *testing.T) {
	e := newEnv(t)
	cancelled := make(chan struct{})
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"message_start","message":{"usage":{"input_tokens":1}}}`)
		<-r.Context().Done()
		close(cancelled)
	}
	resp := e.post("/v1/responses", `{"model":"std-msg","stream":true,"input":"hi"}`, nil)
	b := make([]byte, 1)
	resp.Body.Read(b)
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not cancelled")
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "client_cancelled" {
		t.Fatalf("status=%s", l.Status)
	}
}

func TestResponsesMessagesSendsTextBeforeUpstreamCompletes(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	defer close(release)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"message_start","message":{"usage":{"input_tokens":1}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"early"}}`)
		<-release
		sse(w, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`, `{"type":"message_stop"}`)
	}
	resp := e.post("/v1/responses", `{"model":"std-msg","input":"hello","stream":true}`, nil)
	defer resp.Body.Close()
	got := make(chan string, 1)
	go func() {
		reader := usage.NewSSEReader(resp.Body)
		for {
			ev, err := reader.Next()
			if err != nil {
				got <- "EOF"
				return
			}
			if strings.Contains(string(ev.Data), "response.output_text.delta") {
				got <- string(ev.Data)
				return
			}
		}
	}()
	select {
	case event := <-got:
		if !strings.Contains(event, "early") {
			t.Fatal(event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("text buffered until message end")
	}
}

func TestResponsesMessagesJSONUpstream(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"type":"message","id":"msg_json","model":"std-msg","role":"assistant","content":[{"type":"text","text":"json body"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`)
	}
	resp := e.post("/v1/responses", `{"model":"std-msg","input":"hello"}`, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "json body") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestResponsesMessagesCompactionRejectsTruncatedSummary(t *testing.T) {
	e := newEnv(t)
	_, err := e.store.Pool.Exec(context.Background(), `UPDATE groups SET retry_count=0 WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	e.upstream = func(w http.ResponseWriter, r *http.Request) { bridgeText(w, "partial summary", "max_tokens") }
	resp := e.post("/v1/responses", `{"model":"std-msg","stream":true,"input":[{"type":"message","role":"user","content":"history"},{"type":"compaction_trigger"}]}`, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 || strings.Contains(string(b), "response.completed") || strings.Contains(string(b), "encrypted_content") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "upstream_error" {
		t.Fatal(l.Status)
	}
	var cost string
	e.store.Pool.QueryRow(context.Background(), `SELECT cost::text FROM request_logs WHERE request_id=$1`, resp.Header.Get("X-Lingshu-Request-Id")).Scan(&cost)
	if cost != "0.0000000000" {
		t.Fatal(cost)
	}
}
