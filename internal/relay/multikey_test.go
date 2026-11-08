package relay

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 渠道多 key（docs/decisions.md「渠道多 key 与渠道备注」）：渠道 1 上放三把 key，模拟上游按收到的 key 决定怎么回。

func usedKey(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// withKeys 给渠道 1 再加两把 key（原有一把 upstreamKey），返回 key → id。
func (e *env) withKeys() map[string]int64 {
	e.t.Helper()
	e.exec(`INSERT INTO channel_keys (channel_id, api_key, sort_order) VALUES (1, 'key-b', 1), (1, 'key-c', 2)`)
	ids := map[string]int64{}
	rows, _ := e.store.Pool.Query(context.Background(), `SELECT api_key, id FROM channel_keys WHERE channel_id = 1`)
	for rows.Next() {
		var k string
		var id int64
		rows.Scan(&k, &id)
		ids[k] = id
	}
	rows.Close()
	return ids
}

// keyLog 读日志里记的最终 key 和失败过的 key。
func (e *env) keyLog(requestID string) (int64, []int64, int) {
	e.t.Helper()
	e.waitLog(requestID)
	var keyID *int64
	var failed []int64
	var attempts int
	e.store.Pool.QueryRow(context.Background(), `SELECT channel_key_id, failed_key_ids, attempts FROM request_logs WHERE request_id = $1`, requestID).Scan(&keyID, &failed, &attempts)
	if keyID == nil {
		return 0, failed, attempts
	}
	return *keyID, failed, attempts
}

// 同一会话固定用同一把 key；不同会话分散到不同 key。
func TestMultiKeyStickyBySession(t *testing.T) {
	e := newEnv(t)
	e.withKeys()
	var mu sync.Mutex
	var last string
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = usedKey(r)
		mu.Unlock()
		okUpstream(w, r)
	}
	distinct := map[string]bool{}
	for i := range 12 {
		sess := map[string]string{"Session-Id": "sess-" + strconv.Itoa(i)}
		var first string
		for j := range 2 {
			resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, sess)
			resp.Body.Close()
			mu.Lock()
			if j == 0 {
				first = last
			} else if last != first {
				t.Errorf("会话 %d 两次请求用了不同的 key：%s / %s", i, first, last)
			}
			mu.Unlock()
		}
		distinct[first] = true
	}
	if len(distinct) < 2 {
		t.Errorf("12 个会话都落在同一把 key 上，没有分散：%v", distinct)
	}
}

// 固定的那把 key 被限流：同一个请求在本渠道换一把成功（不去兜底渠道），日志记下失败的 key；
// 这把 key 冷却中，同一会话的下一个请求直接用别的 key。
func TestMultiKey429SwitchesKeyInChannel(t *testing.T) {
	e := newEnv(t)
	ids := e.withKeys()
	sess := map[string]string{"Session-Id": "sess-429"}
	var mu sync.Mutex
	var sticky string
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if sticky == "" {
			sticky = usedKey(r)
		}
		mu.Unlock()
		okUpstream(w, r)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, sess)
	resp.Body.Close()
	_, backupHits := e.withBackup(func(w http.ResponseWriter, r *http.Request) {
		if usedKey(r) == sticky {
			w.Header().Set("Retry-After", "120")
			status(429, `{"error":{"message":"rate limited"}}`)(w, r)
			return
		}
		okUpstream(w, r)
	}, okUpstream)
	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, sess)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	keyID, failed, attempts := e.keyLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if resp.StatusCode != 200 || backupHits.Load() != 0 || attempts != 2 || keyID == ids[sticky] || len(failed) != 1 || failed[0] != ids[sticky] {
		t.Fatalf("应在本渠道换 key 成功：%d 兜底 %d 次 尝试 %d 最终 key %d 失败 %v（被限流的 %d）", resp.StatusCode, backupHits.Load(), attempts, keyID, failed, ids[sticky])
	}
	if st := e.handler.Keys.Status(ids[sticky]); st.State != "cooling" {
		t.Errorf("被限流的 key 应在冷却中：%+v", st)
	}
	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, sess)
	resp.Body.Close()
	if _, _, attempts := e.keyLog(resp.Header.Get("X-Lingshu-Request-Id")); attempts != 1 {
		t.Errorf("冷却中的 key 应排到后面，下一个请求一次成功，实际尝试 %d 次", attempts)
	}
}

// 401：这把 key 标为失效，请求由别的 key 完成。
func TestMultiKey401MarksKeyInvalid(t *testing.T) {
	e := newEnv(t)
	ids := e.withKeys()
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if usedKey(r) == upstreamKey {
			status(401, `{"error":{"message":"invalid api key"}}`)(w, r)
			return
		}
		okUpstream(w, r)
	}
	for i := range 6 { // 几个会话里总有落在 upstreamKey 上的
		resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, map[string]string{"Session-Id": "s" + strconv.Itoa(i)})
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("应由别的 key 完成，实际 %d", resp.StatusCode)
		}
	}
	if st := e.handler.Keys.Status(ids[upstreamKey]); st.State != "invalid" || !strings.Contains(st.Reason, "401") {
		t.Errorf("返回 401 的 key 应标为失效：%+v", st)
	}
}

// 5xx 是渠道整体的问题：只试一把 key 就换兜底渠道，不把每把 key 都试一遍。
func TestMultiKey5xxSkipsRestOfChannel(t *testing.T) {
	e := newEnv(t)
	e.withKeys()
	primaryHits, backupHits := e.withBackup(status(500, `{"error":{"message":"boom"}}`), okUpstream)
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 || primaryHits.Load() != 1 || backupHits.Load() != 1 {
		t.Errorf("渠道 500 应直接换兜底：%d 主 %d 次 备 %d 次", resp.StatusCode, primaryHits.Load(), backupHits.Load())
	}
}

// 续接对话：previous_response_id 发回原来那把 key，即使换了会话编号（固定的 key 会不一样）。
func TestMultiKeyPreviousResponsePinsKey(t *testing.T) {
	e := newEnv(t)
	ids := e.withKeys()
	var mu sync.Mutex
	var hits []string
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		k := usedKey(r)
		mu.Lock()
		hits = append(hits, k)
		mu.Unlock()
		sse(w, `{"type":"response.completed","response":{"id":"resp_`+k+`","usage":{"input_tokens":1,"output_tokens":1}}}`)
	}
	resp := e.post("/v1/responses", `{"model":"std-chat","stream":true}`, map[string]string{"Session-Id": "first"})
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	e.waitLog(resp.Header.Get("X-Lingshu-Request-Id"))
	orig := hits[0]
	// 找一个固定 key 和 orig 不同的会话
	var keyID int64
	e.store.Pool.QueryRow(context.Background(), `SELECT id FROM api_keys LIMIT 1`).Scan(&keyID)
	other := ""
	for i := 0; i < 100 && other == ""; i++ {
		s := "other-" + strconv.Itoa(i)
		sticky := strconv.FormatInt(keyID, 10) + "\x00" + s
		best, bestW := "", uint64(0)
		for k, id := range ids {
			if w := keyWeight(sticky, id); w > bestW {
				best, bestW = k, w
			}
		}
		if best != orig {
			other = s
		}
	}
	resp = e.post("/v1/responses", `{"model":"std-chat","stream":true,"previous_response_id":"resp_`+orig+`"}`, map[string]string{"Session-Id": other})
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 || hits[1] != orig {
		t.Errorf("续接对话应发回原 key %s，实际 %v", orig, hits)
	}
}

// manyKeys 让渠道 1 一共有 n 把 key（原有的 upstreamKey 加 n-1 把）。
func (e *env) manyKeys(n int) {
	e.t.Helper()
	for i := 1; i < n; i++ {
		e.exec(`INSERT INTO channel_keys (channel_id, api_key, sort_order) VALUES (1, $1, $2)`, "key-"+strconv.Itoa(i), i)
	}
}

// 最后一个渠道（免费 key 聚合类）：限流时一直换 key，不受 10 把的上限，直到 key 用完；
// 之后所有 key 都在冷却，直接回 429 和 Retry-After，不再去上游白挨限流。
func TestLastChannelRotatesAllKeysThenRetryAfter(t *testing.T) {
	e := newEnv(t)
	e.manyKeys(12)
	var hits atomic.Int32
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		status(429, `{"error":{"message":"rate limited"}}`)(w, r)
	}
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	_, failed, attempts := e.keyLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if resp.StatusCode != 429 || attempts != 12 || len(failed) != 12 || hits.Load() != 12 {
		t.Fatalf("最后一个渠道应把 12 把 key 都试一遍：%d 尝试 %d 失败 key %d 上游 %d 次", resp.StatusCode, attempts, len(failed), hits.Load())
	}
	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
	if resp.StatusCode != 429 || ra < 1 || ra > 60 || !strings.Contains(string(body), "秒后恢复") || hits.Load() != 12 {
		t.Errorf("key 全在冷却时应直接回 429 + Retry-After，不碰上游：%d Retry-After %q %s 上游 %d 次", resp.StatusCode, resp.Header.Get("Retry-After"), body, hits.Load())
	}
}

// 后面还有别的渠道时，同一渠道最多连换 10 把 key 就去下一个渠道。
func TestKeyRotationCapWhenOtherChannelExists(t *testing.T) {
	e := newEnv(t)
	e.manyKeys(12)
	primaryHits, backupHits := e.withBackup(status(429, `{"error":{"message":"rate limited"}}`), okUpstream)
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	_, _, attempts := e.keyLog(resp.Header.Get("X-Lingshu-Request-Id"))
	if resp.StatusCode != 200 || primaryHits.Load() != 10 || backupHits.Load() != 1 || attempts != 11 {
		t.Errorf("换 10 把 key 后应去兜底渠道：%d 主 %d 次 备 %d 次 尝试 %d", resp.StatusCode, primaryHits.Load(), backupHits.Load(), attempts)
	}
}

// 服务分组的失败重试次数只计渠道级失败：设为 0 时主渠道 500 就直接返回，不换兜底。
func TestGroupRetryCountZero(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE groups SET retry_count = 0 WHERE id = 1`)
	primaryHits, backupHits := e.withBackup(status(500, `{"error":{"message":"boom"}}`), okUpstream)
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 500 || primaryHits.Load() != 1 || backupHits.Load() != 0 {
		t.Errorf("重试 0 次：%d 主 %d 次 备 %d 次", resp.StatusCode, primaryHits.Load(), backupHits.Load())
	}
}

// 没有别的渠道可换：渠道级失败后等 1 秒同渠道再试（偶发 524、抖动）；重试设为 0 时不再试。
func TestSingleChannel5xxRetriesSameChannel(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			status(524, `timeout`)(w, r)
			return
		}
		okUpstream(w, r)
	}
	start := time.Now()
	resp := e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 || hits.Load() != 2 || time.Since(start) < sameChannelRetryDelay {
		t.Errorf("应等一下同渠道重试成功：%d 上游 %d 次 用时 %v", resp.StatusCode, hits.Load(), time.Since(start))
	}
	e.exec(`UPDATE groups SET retry_count = 0 WHERE id = 1`)
	hits.Store(0)
	resp = e.post("/v1/chat/completions", `{"model":"std-chat"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != 524 || hits.Load() != 1 {
		t.Errorf("重试 0 次时不再试：%d 上游 %d 次", resp.StatusCode, hits.Load())
	}
}
