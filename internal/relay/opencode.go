package relay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const openCodeTemplate = "opencode-zen"

// 免费层要求编码会话的上下文；不复制本机目录、日期或用户的环境信息。
const openCodeContext = "You are opencode, a coding assistant.\nHere is some useful information about the environment you are running in:\n<env>\n  Working directory: /workspace\n  Workspace root folder: /workspace\n  Is directory a git repo: no\n  Platform: linux\n</env>"

type openCodeRequest struct {
	stubs     map[string]bool
	toolNames map[[2]int]string
}

var openCodeSessions = struct {
	sync.Mutex
	ids map[[32]byte]string
}{ids: map[[32]byte]string{}}

func openCodeSession(seed [32]byte) string {
	openCodeSessions.Lock()
	defer openCodeSessions.Unlock()
	if id := openCodeSessions.ids[seed]; id != "" {
		return id
	}
	if len(openCodeSessions.ids) >= 1000 {
		for key := range openCodeSessions.ids {
			delete(openCodeSessions.ids, key)
			break
		}
	}
	// OpenCode 的 session 标识以倒序毫秒时间和随机后缀组成。
	id := fmt.Sprintf("ses_%012x%s", (^(uint64(time.Now().UnixMilli())*4096 + 1))&0xffffffffffff, rand.Text()[:14])
	openCodeSessions.ids[seed] = id
	return id
}

// prepareOpenCode 仅对显式选择该模板的 Chat 请求调用，保留原请求的工具和参数。
func prepareOpenCode(req *http.Request, body []byte, channelID int64) ([]byte, *openCodeRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, nil, err
	}
	state := &openCodeRequest{stubs: map[string]bool{}}
	var tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	var rawTools []json.RawMessage
	if raw := fields["tools"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &tools); err != nil {
			return nil, nil, fmt.Errorf("tools 必须是数组")
		}
		json.Unmarshal(raw, &rawTools)
	}
	names := map[string]bool{}
	for _, t := range tools {
		names[t.Function.Name] = true
	}
	for _, name := range []string{"bash", "edit", "glob", "grep", "read"} {
		if names[name] {
			continue
		}
		state.stubs[name] = true
		raw, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]any{
			"name": name, "description": "Unavailable tool. Never call this tool; only use tools provided by the caller.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}, "strict": false,
		}})
		rawTools = append(rawTools, raw)
	}
	fields["tools"], _ = json.Marshal(rawTools)
	var messages []json.RawMessage
	if err := json.Unmarshal(fields["messages"], &messages); err != nil {
		return nil, nil, fmt.Errorf("messages 必须是数组")
	}
	context, _ := json.Marshal(map[string]string{"role": "system", "content": openCodeContext})
	fields["messages"], _ = json.Marshal(append([]json.RawMessage{context}, messages...))
	fields["stream"] = json.RawMessage("true")
	var opts map[string]json.RawMessage
	json.Unmarshal(fields["stream_options"], &opts)
	if opts == nil {
		opts = map[string]json.RawMessage{}
	}
	opts["include_usage"] = json.RawMessage("true")
	fields["stream_options"], _ = json.Marshal(opts)
	if len(fields["tool_choice"]) == 0 {
		fields["tool_choice"] = json.RawMessage(`"auto"`)
	}
	seed := req.Header.Get("X-Opencode-Session")
	if seed == "" {
		seed = req.Header.Get("X-Session-Id")
	}
	if seed == "" {
		seed = string(fields["prompt_cache_key"])
	}
	if seed == "" {
		for _, raw := range messages {
			var m struct{ Role string }
			json.Unmarshal(raw, &m)
			if m.Role == "user" {
				seed = string(raw)
				break
			}
		}
	}
	// 同一调用 key、渠道和会话的后续轮次保持归属，不把两个成员的同文请求合在一起。
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", channelID, req.Header.Get("Authorization"), seed)))
	session := openCodeSession(sum)
	for _, name := range []string{"X-Opencode-Session", "X-Opencode-Session-Id", "X-Session-Affinity", "X-Session-Id"} {
		req.Header.Set(name, session)
	}
	req.Header.Del("X-Opencode-Request")
	req.Header.Set("User-Agent", "opencode/1.19.4 ai-sdk/provider-utils/4.0.23 runtime/bun/1.4.4")
	req.Header.Set("X-Opencode-Client", "cli")
	req.Header.Set("X-Opencode-Project", "global")
	req.Header.Set("Accept", "*/*")
	trace := make([]byte, 24)
	if _, err := rand.Read(trace); err != nil {
		return nil, nil, err
	}
	req.Header.Set("Traceparent", "00-"+hex.EncodeToString(trace[:16])+"-"+hex.EncodeToString(trace[16:])+"-01")
	updated, err := json.Marshal(fields)
	return updated, state, err
}

// sanitizeEvent 保留真实工具调用。模型若调用补充的占位工具，整次响应报错，避免静默丢失动作。
func (s *openCodeRequest) sanitizeEvent(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) || len(data) == 0 {
		return nil
	}
	var chunk struct {
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				ToolCalls []struct {
					Index    int                   `json:"index"`
					Function struct{ Name string } `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return err
	}
	for _, choice := range chunk.Choices {
		for _, tc := range choice.Delta.ToolCalls {
			if s.toolNames == nil {
				s.toolNames = map[[2]int]string{}
			}
			key := [2]int{choice.Index, tc.Index}
			s.toolNames[key] += tc.Function.Name
			if s.stubs[s.toolNames[key]] {
				return fmt.Errorf("OpenCode 请求了客户端未提供的工具 %s，请用支持该工具的编码客户端重试", strings.TrimSpace(s.toolNames[key]))
			}
		}
	}
	return nil
}
