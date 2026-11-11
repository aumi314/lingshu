package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aumi314/lingshu/internal/probe"
)

// 模型检测：发起、后台跑完、结果入库；请求直接打到渠道、伪装成 Codex 客户端；流式工具调用分片能拼回来。
func TestProbeRun(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	ctx := context.Background()

	var mu sync.Mutex
	seen := map[string]int{} // 上游收到的会话编号、User-Agent、模型名
	only := regexp.MustCompile(`single word (\w+)`)
	marker := regexp.MustCompile(`\[\[END-[0-9a-f]+\]\]`)
	exact := regexp.MustCompile(`Use exactly (\{.*\})`)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role, Content string
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		mu.Lock()
		seen["session "+r.Header.Get("Session-Id")]++
		seen["ua "+r.Header.Get("User-Agent")]++
		seen["model "+body.Model]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v string) { fmt.Fprintf(w, "data: %s\n\n", v) }
		chunk := func(delta string) {
			send(`{"id":"chatcmpl-x","model":"up-sol","choices":[{"index":0,"delta":` + delta + `}]}`)
		}
		system, prompt := "", ""
		for _, m := range body.Messages {
			if m.Role == "system" {
				system = m.Content
			} else {
				prompt = m.Content
			}
		}
		text := "47"
		switch {
		case only.MatchString(system):
			text = only.FindStringSubmatch(system)[1]
		case marker.MatchString(system):
			text = "Hello!\n" + marker.FindString(system)
		case exact.MatchString(prompt):
			text = exact.FindStringSubmatch(prompt)[1]
		}
		if len(body.Tools) > 0 { // 工具调用分三片：名字、参数前半、参数后半
			city := strings.TrimSuffix(strings.TrimPrefix(prompt, "What is the weather in "), " right now? Use the tool.")
			name := body.Tools[0].(map[string]any)["function"].(map[string]any)["name"].(string) // 工具名每次随机，回传请求里声明的名字
			chunk(`{"tool_calls":[{"index":0,"function":{"name":"` + name + `","arguments":""}}]}`)
			chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}`)
			chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"\"` + city + `\"}"}}]}`)
		} else {
			b, _ := json.Marshal(map[string]string{"content": text})
			chunk(string(b))
		}
		send(`{"id":"chatcmpl-x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		send(`{"id":"chatcmpl-x","choices":[],"usage":{"prompt_tokens":4158,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4000}}}`)
		send("[DONE]")
	}))
	defer up.Close()
	for _, q := range []string{
		`INSERT INTO models (id, name, input_price, output_price, cache_read_price) VALUES (1, 'gpt-6.1-sol', 2, 10, 0.1)`,
		`INSERT INTO channels (id, name, api_key, base_url_chat) VALUES (1, 'relay', 'upstream-key', '` + up.URL + `')`,
		`INSERT INTO channel_models (channel_id, model_id, upstream_name) VALUES (1, 1, 'up-sol')`,
	} {
		if _, err := e.store.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	if code, _ := e.login("bob").do("POST", "/api/admin/probes", map[string]any{"channel_id": 1, "model": "gpt-6.1-sol"}, nil); code != 403 {
		t.Fatalf("普通用户不能发起检测: %d", code)
	}
	if code, body := a.do("POST", "/api/admin/probes", map[string]any{"channel_id": 1, "model": "gpt-6.1-sol", "baseline": "no-such"}, nil); code != 400 || !strings.Contains(body, "官方样本") {
		t.Fatalf("没有样本的对照型号应拒绝: %d %s", code, body)
	}
	if code, body := a.do("POST", "/api/admin/probes", map[string]any{"channel_id": 1, "model": "gpt-6-luna"}, nil); code != 400 || !strings.Contains(body, "没有映射") {
		t.Fatalf("渠道没映射的模型应拒绝: %d %s", code, body)
	}
	var started struct{ ID int64 }
	if code, body := a.do("POST", "/api/admin/probes", map[string]any{"channel_id": 1, "model": "gpt-6.1-sol", "preset": "quick"}, &started); code != 200 {
		t.Fatalf("发起检测: %d %s", code, body)
	}
	var run struct {
		Status, Verdict, Baseline, UpstreamModel string `json:"-"`
		Score                                    *int   `json:"score"`
		Cost                                     string `json:"cost"`
		Report                                   struct {
			Items        []struct{ Key, Status, Summary string } `json:"items"`
			Observations []struct{ Key, Status, Summary string } `json:"observations"`
			Calls        []json.RawMessage                       `json:"calls"`
		} `json:"report"`
	}
	var raw map[string]any
	deadline := time.Now().Add(60 * time.Second)
	for {
		raw = nil
		a.do("GET", fmt.Sprintf("/api/admin/probes/%d", started.ID), nil, &raw)
		if raw["status"] != "running" || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	b, _ := json.Marshal(raw)
	json.Unmarshal(b, &run)
	if raw["status"] != "done" || run.Score == nil || raw["baseline"] != "gpt-6.1-sol" || raw["upstream_model"] != "up-sol" {
		t.Fatalf("检测应跑完并入库: %v", raw["status"])
	}
	status := map[string]string{}
	for _, it := range append(run.Report.Items, run.Report.Observations...) {
		status[it.Key] = it.Status
	}
	for _, k := range []string{"structured", "instruction", "stream", "invisible"} {
		if status[k] != "pass" {
			t.Errorf("%s 应通过: %v", k, run.Report)
		}
	}
	for _, k := range []string{"tokens", "fingerprint", "pair", "reasoning", "injection", "mixed", "protocol"} {
		if status[k] == "" {
			t.Errorf("缺少检测项 %s", k)
		}
	}
	if len(run.Report.Calls) < 150 || run.Cost == "0" {
		t.Errorf("请求数 %d、花费 %s", len(run.Report.Calls), run.Cost)
	}
	// 上游看到 4 个会话编号（一次检测抽查几个上游）、Codex 客户端的 User-Agent 和映射后的上游模型名
	sessions, uas := 0, 0
	for k := range seen {
		if strings.HasPrefix(k, "session ") {
			sessions++
		}
		if strings.HasPrefix(k, "ua codex-tui/") {
			uas++
		}
	}
	if sessions != probe.Sessions || uas != 1 || seen["model up-sol"] != len(run.Report.Calls) {
		t.Errorf("请求伪装或模型映射不对: %v", seen)
	}
	// 列表不带完整结果；检测请求不进调用日志
	var list []map[string]any
	a.do("GET", "/api/admin/probes", nil, &list)
	if len(list) != 1 || list[0]["report"] != nil {
		t.Errorf("列表: %v", list)
	}
	var logs int
	e.store.Pool.QueryRow(ctx, `SELECT count(*) FROM request_logs`).Scan(&logs)
	if logs != 0 {
		t.Errorf("检测请求不应写调用日志: %d", logs)
	}
	// 重启时进行中的检测标成失败
	e.store.Pool.Exec(ctx, `INSERT INTO probe_runs (channel_name, model, upstream_model, baseline, preset, status) VALUES ('x', 'm', 'm', 'gpt-6.1-sol', 'quick', 'running')`)
	if err := e.store.FailStaleProbeRuns(ctx); err != nil {
		t.Fatal(err)
	}
	var running int
	e.store.Pool.QueryRow(ctx, `SELECT count(*) FROM probe_runs WHERE status = 'running'`).Scan(&running)
	if running != 0 {
		t.Errorf("重启后不应还有进行中的检测")
	}
}
