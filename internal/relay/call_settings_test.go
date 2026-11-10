package relay

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// 调用设置里关掉“Codex 调 Claude”转接后，只有 Messages 地址的渠道不再接 Responses 请求，提示改用原生入口。
func TestBridgeSwitchOff(t *testing.T) {
	e := newEnv(t)
	e.upstream = func(w http.ResponseWriter, r *http.Request) { t.Error("关掉转接后不应请求上游") }
	e.exec(`INSERT INTO settings (key, value) VALUES ('bridge_responses_to_messages', 'false')`)
	resp := e.post("/v1/responses", `{"model":"std-msg","input":"hi"}`, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "/v1/messages") || strings.Contains(string(b), "/v1/responses 或") {
		t.Fatalf("应提示只能用原生的 /v1/messages: %d %s", resp.StatusCode, b)
	}
}

// 渠道亲和：两个同优先级渠道，同一会话总落在同一个渠道；不同会话分散到两个渠道；固定的渠道出故障时换另一个。
func TestChannelAffinity(t *testing.T) {
	e := newEnv(t)
	base := strings.TrimSuffix(e.channelBase(1), "/v1")
	e.exec(`INSERT INTO channels (id, name, api_key, base_url_chat) VALUES (3, 'c-twin', 'k', $1)`, base+"/twin/v1")
	e.exec(`INSERT INTO channel_groups VALUES (3, 1)`)
	e.exec(`INSERT INTO channel_models VALUES (3, 1, 'up-chat')`)
	var mu sync.Mutex
	broken := ""
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		ch := "c-chat"
		if strings.HasPrefix(r.URL.Path, "/twin/") {
			ch = "c-twin"
		}
		mu.Lock()
		fail := ch == broken
		mu.Unlock()
		if fail {
			status(http.StatusInternalServerError, `{"error":{"message":"down"}}`)(w, r)
			return
		}
		w.Header().Set("X-Test-Channel", ch)
		okUpstream(w, r)
	}
	call := func(session string) (string, int) {
		resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, map[string]string{"Session-Id": session})
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.Header.Get("X-Test-Channel"), resp.StatusCode
	}
	first, _ := call("s-fixed")
	for i := 0; i < 10; i++ {
		if got, _ := call("s-fixed"); got != first {
			t.Fatalf("同一会话第 %d 次换到了 %s（原来 %s）", i, got, first)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		ch, _ := call("s-" + strconv.Itoa(i))
		seen[ch] = true
	}
	if !seen["c-chat"] || !seen["c-twin"] {
		t.Errorf("不同会话应分散到两个渠道: %v", seen)
	}
	mu.Lock()
	broken = first
	mu.Unlock()
	if got, code := call("s-fixed"); code != http.StatusOK || got == first || got == "" {
		t.Errorf("固定的渠道出故障时应换到另一个: %s %d", got, code)
	}
}
