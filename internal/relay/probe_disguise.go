package relay

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// ProbeDisguise 是模型检测的伪装配置：让检测请求看起来和真实的 Claude Code / Codex 客户端一样，
// 中转认不出检测流量、没法专门转给好的上游（docs/decisions.md「模型检测」）。
// 内容来自真实客户端的抓包（references/probe-sites/build_disguise.py 生成），含 Anthropic / OpenAI 客户端原文，
// 不进公开仓库，部署时放在服务器上，用 LINGSHU_PROBE_DISGUISE_FILE 指定；没有配置时退回简版伪装。
type ProbeDisguise struct {
	Claude struct {
		Headers   map[string]string `json:"headers"`
		System    []map[string]any  `json:"system"` // 文本里的 {{MODEL_NAME}} {{MODEL_ID}} 按检测的模型替换
		Tools     []map[string]any  `json:"tools"`
		MaxTokens int               `json:"max_tokens"`
	} `json:"claude"`
	Codex struct {
		Headers          map[string]string `json:"headers"`
		Input            json.RawMessage   `json:"input"` // 用户问题之前的 input 项（工具、开发者说明、环境）；{{DATE}} 按当天替换
		Body             map[string]any    `json:"body"`  // 其余请求体字段，如 tool_choice、parallel_tool_calls、text
		ReasoningContext string            `json:"reasoning_context"`
	} `json:"codex"`

	installationID string // 一个灵枢进程像一台装了 Codex 的机器
}

// LoadProbeDisguise 读取伪装配置；Claude 和 Codex 两部分至少要有一部分完整。
func LoadProbeDisguise(path string) (*ProbeDisguise, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d ProbeDisguise
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("伪装配置格式不对: %w", err)
	}
	if len(d.Claude.System) == 0 && len(d.Codex.Input) == 0 {
		return nil, fmt.Errorf("伪装配置里 Claude 和 Codex 都是空的")
	}
	if len(d.Codex.Input) > 0 {
		var items []any
		if err := json.Unmarshal(d.Codex.Input, &items); err != nil {
			return nil, fmt.Errorf("伪装配置的 codex.input 不是数组: %w", err)
		}
	}
	d.installationID = NewProbeSession()
	return &d, nil
}

// noToolsSuffix 接在不需要工具的检测题后面：完整伪装带着客户端的整套工具，模型遇到算术题会去调 Bash / exec 算
// （2026-11-11 官方 sol 和 kirisame 的 Opus 都这样），这是真实客户端的正常行为，不是渠道的问题。只在发给上游时加，报告里记原题。
const noToolsSuffix = "\n\nAnswer directly; do not use any tools."

// directPrompt 返回发给上游的题面：有伪装工具、检测题自己没声明工具时加上 noToolsSuffix。
func (d *ProbeDisguise) directPrompt(prompt string, hasProbeTools, hasDisguiseTools bool) string {
	if hasDisguiseTools && !hasProbeTools {
		return prompt + noToolsSuffix
	}
	return prompt
}

func (d *ProbeDisguise) hasClaude() bool { return d != nil && len(d.Claude.System) > 0 }
func (d *ProbeDisguise) hasCodex() bool  { return d != nil && len(d.Codex.Input) > 0 }

var claudeIDPattern = regexp.MustCompile(`^claude-([a-z]+)-(\d+)-(\d+)`)

// claudeDisplayName 把 claude-opus-5-5 写成 Opus 5.5（Claude Code 系统提示里的写法）；认不出就原样返回。
func claudeDisplayName(id string) string {
	m := claudeIDPattern.FindStringSubmatch(id)
	if m == nil {
		return id
	}
	return strings.ToUpper(m[1][:1]) + m[1][1:] + " " + m[2] + "." + m[3]
}

// claudeSystem 返回伪装的系统提示块（深拷贝、填好模型名），后面接检测自己的系统指令。
func (d *ProbeDisguise) claudeSystem(model, extra string) []map[string]any {
	var out []map[string]any
	raw, _ := json.Marshal(d.Claude.System)
	s := strings.NewReplacer("{{MODEL_NAME}}", claudeDisplayName(model), "{{MODEL_ID}}", model).Replace(string(raw))
	json.Unmarshal([]byte(s), &out)
	if extra != "" {
		out = append(out, map[string]any{"type": "text", "text": extra})
	}
	return out
}

// codexInput 返回伪装的 input 前缀（填好日期），后面接检测的系统指令（开发者消息）和问题。
func (d *ProbeDisguise) codexInput(system, prompt string, now time.Time) []any {
	var items []any
	s := strings.ReplaceAll(string(d.Codex.Input), "{{DATE}}", now.Format("2006-01-02"))
	json.Unmarshal([]byte(s), &items)
	if system != "" {
		items = append(items, map[string]any{"type": "message", "role": "developer", "content": []map[string]any{{"type": "input_text", "text": system}}})
	}
	return append(items, map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}})
}

// codexSessionMeta 生成 Codex 每个请求带的会话元数据（和真实 Codex 0.160 同样的字段）：请求头 x-codex-turn-metadata 和请求体 client_metadata 共用。
func (d *ProbeDisguise) codexSessionMeta(session, model, effort string, now time.Time) (turnMeta string, clientMeta map[string]any) {
	turn := NewProbeSession()
	window := session + ":0"
	meta := map[string]any{
		"installation_id": d.installationID, "session_id": session, "thread_id": session, "agent_name": "/root", "turn_id": turn,
		"window_id": window, "window_number": 0, "context_window_id": NewProbeSession(), "request_kind": "turn", "root_turn_id": turn,
		"thread_source": "user", "turn_trigger": "exec", "sandbox": "seccomp", "sandbox_mode": "read-only", "auto_review_enabled": false,
		"node_repl_auto_review_required": true, "node_repl_disabled": false, "turn_started_at_unix_ms": now.UnixMilli(),
		"analytics_enabled": true, "model": model,
	}
	if effort != "" {
		meta["reasoning_effort"] = effort
	}
	raw, _ := json.Marshal(meta)
	return string(raw), map[string]any{"x-codex-window-id": window, "turn_id": turn, "x-codex-turn-metadata": string(raw),
		"thread_id": session, "x-codex-installation-id": d.installationID, "root_turn_id": turn, "session_id": session}
}
