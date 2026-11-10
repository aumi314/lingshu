package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func reverseText(w http.ResponseWriter, text string) {
	q, _ := json.Marshal(text)
	sse(w,
		`{"type":"response.created","response":{"id":"resp_reverse","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_up","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"item_id":"msg_up","content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		fmt.Sprintf(`{"type":"response.output_text.delta","output_index":0,"item_id":"msg_up","content_index":0,"delta":%s}`, q),
		fmt.Sprintf(`{"type":"response.output_text.done","output_index":0,"item_id":"msg_up","content_index":0,"text":%s}`, q),
		fmt.Sprintf(`{"type":"response.content_part.done","output_index":0,"item_id":"msg_up","content_index":0,"part":{"type":"output_text","text":%s,"annotations":[]}}`, q),
		fmt.Sprintf(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_up","role":"assistant","status":"completed","content":[{"type":"output_text","text":%s,"annotations":[]}]}}`, q),
		fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_reverse","status":"completed","output":[{"type":"message","id":"msg_up","role":"assistant","status":"completed","content":[{"type":"output_text","text":%s,"annotations":[]}]}],"usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":10}}}}`, q))
}

const reverseRequest = `{"model":"std-chat","max_tokens":256,"stream":true,"messages":[{"role":"user","content":"hello"}]}`

func TestMessagesResponsesStreamingBillingAndCapabilities(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+upstreamKey || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Anthropic-Version") != "" || r.Header.Get("Anthropic-Beta") != "" {
			t.Errorf("upstream request: %s %v", r.URL, r.Header)
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["model"] != "up-chat" || b["stream"] != true || b["store"] != false {
			t.Errorf("upstream body=%v", b)
		}
		for _, item := range b["input"].([]any) {
			m := item.(map[string]any)
			switch m["type"] {
			case "message":
				if m["role"] != "user" || m["content"] == nil {
					t.Errorf("invalid Responses message: %v", m)
				}
			default:
				t.Errorf("invalid Responses input item: %v", m)
			}
		}
		reverseText(w, "first line\n第二行\\literal")
	}
	resp := e.post("/v1/messages?beta=true", reverseRequest, map[string]string{"Anthropic-Version": "2023-06-01", "Anthropic-Beta": "interleaved-thinking-2025-05-14"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	reader := usage.NewSSEReader(strings.NewReader(string(body)))
	starts, stops := 0, 0
	var used map[string]any
	for {
		ev, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err = json.Unmarshal(ev.Data, &v); err != nil {
			t.Fatal(err)
		}
		typ := v["type"].(string)
		if !strings.Contains(string(ev.Raw), "event: "+typ) {
			t.Fatalf("missing named event: %s", ev.Raw)
		}
		switch typ {
		case "message_start":
			starts++
			m := v["message"].(map[string]any)
			if m["model"] != "std-chat" {
				t.Fatalf("client model=%v", m)
			}
		case "message_delta":
			used = v["usage"].(map[string]any)
		case "message_stop":
			stops++
		}
	}
	if starts != 1 || stops != 1 || used["input_tokens"] != float64(60) || used["cache_read_input_tokens"] != float64(40) || used["output_tokens"] != float64(50) {
		t.Fatalf("lifecycle=%d/%d usage=%v %s", starts, stops, used, body)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "success" || l.InputTokens != 60 || l.CacheReadTokens != 40 || l.OutputTokens != 50 || l.ReasoningTokens != 10 {
		t.Fatalf("log=%+v", l)
	}
	var proto, up, cost string
	var count, write int
	err := e.store.Pool.QueryRow(context.Background(), `SELECT protocol,upstream_protocol,cost::text,cache_write_tokens,(SELECT count(*) FROM request_logs) FROM request_logs WHERE request_id=$1`, resp.Header.Get("X-Lingshu-Request-Id")).Scan(&proto, &up, &cost, &write, &count)
	if err != nil || proto != "messages" || up != "responses" || cost != "0.0001640000" || write != 0 || count != 1 {
		t.Fatalf("billing %s/%s %s write=%d count=%d err=%v", proto, up, cost, write, count, err)
	}
	// 存储层只给原生协议，能转接的 Messages 入口由网关按调用设置的开关补上
	ps, err := e.store.ModelProtocols(context.Background(), 1, "std-chat")
	if err != nil || strings.Contains(strings.Join(ps, ","), "messages") {
		t.Fatalf("protocols=%v %v", ps, err)
	}
	if got := strings.Join(withBridgedProtocols(ps, store.DefaultCallLimits), ","); !strings.Contains(got, "messages") {
		t.Fatalf("开着转接时应补上 messages 入口: %s", got)
	}
	off := store.DefaultCallLimits
	off.BridgeMessagesToResponses = false
	if got := strings.Join(withBridgedProtocols(ps, off), ","); strings.Contains(got, "messages") {
		t.Fatalf("关掉转接后不应再有 messages 入口: %s", got)
	}
	models, err := e.store.PricedModels(context.Background(), []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.Name == "std-chat" {
			if strings.Contains(strings.Join(m.NativeProtocols, ","), "messages") || strings.Join(m.BridgedProtocols, ",") != "messages" {
				t.Fatalf("capabilities=%+v", m)
			}
		}
	}
}

func TestMessagesResponsesNativePreference(t *testing.T) {
	e := newEnv(t)
	_, err := e.store.Pool.Exec(context.Background(), `UPDATE channels SET base_url_messages=base_url_responses WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/v1/messages" || r.Header.Get("X-Api-Key") != upstreamKey {
			t.Errorf("native request %s", r.URL)
		}
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		if v["native_field"] != "keep" {
			t.Errorf("native field lost: %v", v)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"type":"message","id":"native","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	}
	resp := e.post("/v1/messages", `{"model":"std-chat","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"native_field":"keep"}`, nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"id":"native"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if l.Status != "success" {
		t.Fatalf("%+v", l)
	}
}

func TestMessagesResponsesErrorsAndRetries(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			sse(w, `{"type":"response.created","response":{"id":"resp_failed","status":"in_progress","output":[]}}`, `{"type":"error","message":"busy"}`)
			return
		}
		reverseText(w, "ok")
	}
	resp := e.post("/v1/messages", reverseRequest, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || calls.Load() != 2 || !strings.Contains(string(b), "message_stop") {
		t.Fatalf("retry calls=%d %d %s", calls.Load(), resp.StatusCode, b)
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"bad upstream-secret"}}`)
	}
	resp = e.post("/v1/messages", reverseRequest, nil)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || strings.Contains(string(b), upstreamKey) || !strings.Contains(string(b), `"type":"error"`) {
		t.Fatalf("normalization %d %s", resp.StatusCode, b)
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
}

func TestMessagesResponsesTruncationAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			e := newEnv(t)
			var calls atomic.Int32
			stopped := make(chan struct{})
			e.upstream = func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				sse(w, `{"type":"response.created","response":{"id":"resp_cut","status":"in_progress","output":[]}}`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_cut","role":"assistant","content":[]}}`, `{"type":"response.content_part.added","output_index":0,"item_id":"msg_cut","content_index":0,"part":{"type":"output_text","text":""}}`, `{"type":"response.output_text.delta","output_index":0,"item_id":"msg_cut","content_index":0,"delta":"partial"}`)
				if cancel {
					<-r.Context().Done()
				}
				close(stopped)
			}
			resp := e.post("/v1/messages", reverseRequest, nil)
			if cancel {
				buf := make([]byte, 1)
				resp.Body.Read(buf)
				resp.Body.Close()
			} else {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if !strings.Contains(string(b), "event: error") || strings.Contains(string(b), "message_stop") {
					t.Fatalf("truncation %s", b)
				}
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream did not cancel")
			}
			l := e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
			want := "upstream_error"
			if cancel {
				want = "client_cancelled"
			}
			if calls.Load() != 1 || l.Status != want {
				t.Fatalf("calls=%d log=%+v", calls.Load(), l)
			}
			if !cancel {
				var cost string
				e.store.Pool.QueryRow(context.Background(), `SELECT cost::text FROM request_logs WHERE request_id=$1`, resp.Header.Get("X-Lingshu-Request-Id")).Scan(&cost)
				if cost != "0.0000000000" {
					t.Fatalf("failure charged=%s", cost)
				}
			}
		})
	}
}

func TestMessagesResponsesNonStreamingAndJSONUpstream(t *testing.T) {
	for _, jsonUp := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonUp), func(t *testing.T) {
			e := newEnv(t)
			e.upstream = func(w http.ResponseWriter, r *http.Request) {
				if !jsonUp {
					reverseText(w, "hello")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"resp_json","object":"response","status":"completed","output":[{"type":"message","id":"msg_json","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":2}}`)
			}
			resp := e.post("/v1/messages", strings.Replace(reverseRequest, `"stream":true`, `"stream":false`, 1), nil)
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var v map[string]any
			json.Unmarshal(b, &v)
			if resp.StatusCode != 200 || v["type"] != "message" || v["stop_reason"] != "end_turn" || v["model"] != "std-chat" {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
		})
	}
}

func TestMessagesResponsesRejectsBeforeCallingUpstream(t *testing.T) {
	e := newEnv(t)
	var called atomic.Int32
	e.upstream = func(w http.ResponseWriter, r *http.Request) { called.Add(1); reverseText(w, "unexpected") }
	bad := `{"model":"std-chat","max_tokens":100,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":"output"}]}]}`
	resp := e.post("/v1/messages", bad, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || called.Load() != 0 || !strings.Contains(string(b), "invalid_request_error") {
		t.Fatalf("%d calls=%d %s", resp.StatusCode, called.Load(), b)
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	var balance, reserved string
	e.store.Pool.QueryRow(context.Background(), `SELECT balance::text,reserved::text FROM users WHERE id=1`).Scan(&balance, &reserved)
	if balance != "10.0000000000" || reserved != "0.0000000000" {
		t.Fatalf("rejection changed balance/reserve %s/%s", balance, reserved)
	}
}

func TestMessagesResponsesReasoningReplayPinsOrigin(t *testing.T) {
	e := newEnv(t)
	const reasoning = `{"type":"reasoning","id":"rs_replay","summary":[{"type":"summary_text","text":"Check the file"}],"encrypted_content":"opaque-final"}`
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"type":"response.created","response":{"id":"resp_replay","status":"in_progress"}}`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_replay","summary":[]}}`, `{"type":"response.output_item.done","output_index":0,"item":`+reasoning+`}`, `{"type":"response.completed","response":{"id":"resp_replay","status":"completed","output":[`+reasoning+`],"usage":{"input_tokens":10,"output_tokens":3}}}`)
	}
	resp := e.post("/v1/messages", strings.Replace(reverseRequest, `"stream":true`, `"stream":false`, 1), nil)
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("first=%v", result)
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	// 给另一原生 Messages 渠道更高优先级，回放仍应固定在原 Responses 渠道。
	_, err := e.store.Pool.Exec(context.Background(), `INSERT INTO channel_models(channel_id,model_id) VALUES(2,1);UPDATE channels SET priority=100 WHERE id=2`)
	if err != nil {
		t.Fatal(err)
	}
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("reasoning replay changed channel: %s", r.URL)
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "opaque-final") || strings.Contains(string(raw), "lingshu.responses.v1") {
			t.Errorf("reasoning replay body=%s", raw)
		}
		reverseText(w, "continued")
	}
	request := map[string]any{"model": "std-chat", "max_tokens": 100, "messages": []any{map[string]any{"role": "assistant", "content": result["content"]}, map[string]any{"role": "user", "content": "continue"}}}
	raw, _ := json.Marshal(request)
	resp = e.post("/v1/messages", string(raw), nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("replay %d %s", resp.StatusCode, b)
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	_, err = e.store.Pool.Exec(context.Background(), `UPDATE channels SET enabled=false WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		t.Error("unavailable origin must not fall back with encrypted state")
	}
	resp = e.post("/v1/messages", string(raw), nil)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("disabled origin %d %s", resp.StatusCode, b)
	}
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
}
