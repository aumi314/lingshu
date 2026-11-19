package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// keyIDs 读一个渠道当前的 key id。
func (e *env) keyIDs(channelID int64) []int64 {
	e.t.Helper()
	rows, err := e.store.Pool.Query(context.Background(), `SELECT id FROM channel_keys WHERE channel_id=$1 ORDER BY id`, channelID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// bridge403Channels 把 std-msg 放到两个 Messages-only 渠道：渠道 1 优先级高、只认 Claude Code，渠道 2 兜底。
func (e *env) bridge403Channels() ([]int64, int64) {
	e.t.Helper()
	base := strings.TrimSuffix(e.channelBase(1), "/v1")
	e.exec(`UPDATE channels SET base_url_responses='', base_url_messages=$1, priority=10 WHERE id=1`, base+"/anthropic")
	e.exec(`UPDATE channels SET base_url_messages=$1, priority=0 WHERE id=2`, base+"/second/anthropic")
	e.exec(`INSERT INTO channel_models (channel_id, model_id) VALUES (1, 2) ON CONFLICT DO NOTHING`)
	return e.keyIDs(1), e.keyIDs(2)[0]
}

// bridge403Upstream 让高优先级渠道按客户端类型返回 403，兜底渠道正常回复。
func (e *env) bridge403Upstream(primary, backup *atomic.Int32) {
	e.t.Helper()
	e.upstream = func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/second/anthropic/"):
			backup.Add(1)
			bridgeText(w, "from backup", "end_turn")
		case strings.HasPrefix(r.URL.Path, "/anthropic/"):
			primary.Add(1)
			status(403, `{"type":"error","error":{"type":"authentication_error","message":"claude code only"}}`)(w, r)
		default:
			e.t.Errorf("不该打到上游路径 %s", r.URL.Path)
		}
	}
}

// 桥接请求撞上只认原生客户端的渠道：换到兜底渠道，且原渠道的 key 仍可用。
func TestBridged403SwitchesChannelWithoutInvalidatingKey(t *testing.T) {
	e := newEnv(t)
	ids1, _ := e.bridge403Channels()
	primary, backup := &atomic.Int32{}, &atomic.Int32{}
	e.bridge403Upstream(primary, backup)
	resp := e.post("/v1/responses", `{"model":"std-msg","input":"hi"}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || primary.Load() != 1 || backup.Load() != 1 {
		t.Fatalf("应由兜底渠道完成：%d 高优先级 %d 次 兜底 %d 次", resp.StatusCode, primary.Load(), backup.Load())
	}
	for _, id := range ids1 {
		if !e.handler.Keys.Usable(id) || e.handler.Keys.Status(id).State != "ok" {
			t.Errorf("被桥接 403 的 key %d 不该失效：%+v", id, e.handler.Keys.Status(id))
		}
	}
}

// 高优先级渠道有两把 key 时，桥接 403 只试一把就整条跳过，不遍历同渠道其他 key。
func TestBridged403SkipsOtherKeysInChannel(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO channel_keys (channel_id, api_key) VALUES (1, 'key-extra')`)
	ids1, _ := e.bridge403Channels()
	primary, backup := &atomic.Int32{}, &atomic.Int32{}
	e.bridge403Upstream(primary, backup)
	resp := e.post("/v1/responses", `{"model":"std-msg","input":"hi"}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || primary.Load() != 1 || backup.Load() != 1 {
		t.Fatalf("高优先级渠道只该收到一次请求：%d 高优先级 %d 次 兜底 %d 次", resp.StatusCode, primary.Load(), backup.Load())
	}
	for _, id := range ids1 {
		if !e.handler.Keys.Usable(id) || e.handler.Keys.Status(id).State != "ok" {
			t.Errorf("桥接 403 不该动渠道里的 key %d：%+v", id, e.handler.Keys.Status(id))
		}
	}
}

// 原生 Messages 的 403 仍按 key 失效处理，防止上面放宽的规则误伤真被封的 key。
func TestNativeMessages403StillInvalidatesKey(t *testing.T) {
	e := newEnv(t)
	id := e.keyIDs(2)[0]
	e.upstream = status(403, `{"type":"error","error":{"type":"authentication_error","message":"banned key"}}`)
	resp := e.post("/v1/messages", `{"model":"std-msg","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`, nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("原生 403 应原样返回，实际 %d", resp.StatusCode)
	}
	if st := e.handler.Keys.Status(id); st.State != "invalid" || !strings.Contains(st.Reason, "403") {
		t.Errorf("原生 403 的 key 应标为失效：%+v", st)
	}
}
