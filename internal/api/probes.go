package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/billing"
	"github.com/aumi314/lingshu/internal/probe"
	"github.com/aumi314/lingshu/internal/relay"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
)

// 模型检测（docs/decisions.md「模型检测」）：选渠道和模型发起检测，后台运行，结果存进 probe_runs。

const maxRunningProbes = 5

type probeView struct {
	store.ProbeRun
	Progress *[2]int `json:"progress,omitempty"` // 进行中：已完成 / 总请求数
}

func (s *Server) withProgress(r store.ProbeRun) probeView {
	v := probeView{ProbeRun: r}
	if p, ok := s.probeProgress.Load(r.ID); ok && r.Status == "running" {
		pp := p.([2]int)
		v.Progress = &pp
	}
	return v
}

// probeableBaselines 是能发起检测的有样本型号：GPT 指纹辨别用 Chat 对照官方样本；
// Claude 只有在检测名单里的才走 Messages（Sonnet 的样本只用来识别“拿 Sonnet 冒充 Opus”）。
func probeableBaselines() []string {
	var out []string
	for _, m := range probe.BaselineModels() {
		if probe.Family(m) == "gpt" || probe.IsProtocolModel(m) {
			out = append(out, m)
		}
	}
	return out
}

func (s *Server) listProbeBaselines(w http.ResponseWriter, r *http.Request) {
	// protocol_models：没有官方样本、只做协议层检测的型号（Claude，需要渠道有 Messages 地址）
	writeJSON(w, http.StatusOK, map[string]any{"models": probeableBaselines(), "protocol_models": probe.ProtocolModels})
}

func (s *Server) listProbes(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.ListProbeRuns(r.Context(), 50)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]probeView, len(runs))
	for i, run := range runs {
		out[i] = s.withProgress(run)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getProbe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetProbeRun(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withProgress(run))
}

func (s *Server) startProbe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ChannelID int64  `json:"channel_id"`
		Model     string `json:"model"`    // 站内模型名
		Baseline  string `json:"baseline"` // 对照的官方型号；空表示和站内模型同名
		Preset    string `json:"preset"`   // quality / fingerprint
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Preset != probe.ModeQuality && in.Preset != probe.ModeFingerprint {
		writeError(w, http.StatusBadRequest, "preset 必须是 quality 或 fingerprint")
		return
	}
	if in.Baseline == "" {
		in.Baseline = in.Model
	}
	protocol := probe.IsProtocolModel(in.Baseline)
	known := protocol
	for _, m := range probeableBaselines() {
		known = known || m == in.Baseline
	}
	if !known {
		writeError(w, http.StatusBadRequest, "没有 "+in.Baseline+" 的官方样本，请选一个对照型号："+strings.Join(append(probeableBaselines(), probe.ProtocolModels...), "、"))
		return
	}
	channels, err := s.Store.ListChannels(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var upstream, channelName string
	var selected *store.Channel
	for _, ch := range channels {
		if ch.ID != in.ChannelID {
			continue
		}
		selected = ch
		channelName = ch.Name
		for _, m := range ch.Models {
			if m.ModelName == in.Model {
				upstream = m.UpstreamName
				if upstream == "" {
					upstream = m.ModelName
				}
			}
		}
	}
	if selected == nil {
		writeError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if upstream == "" {
		writeError(w, http.StatusBadRequest, "这个渠道没有映射模型 "+in.Model)
		return
	}
	if protocol && selected.BaseURLMessages == "" {
		writeError(w, http.StatusBadRequest, in.Model+" 的检测走 Messages 协议，这个渠道没有配置 Messages 地址")
		return
	}
	if !protocol && in.Preset == probe.ModeQuality && selected.BaseURLResponses == "" {
		writeError(w, http.StatusBadRequest, "GPT 渠道质量测试需要 Responses 地址")
		return
	}
	if !protocol && in.Preset == probe.ModeFingerprint && selected.BaseURLChat == "" {
		writeError(w, http.StatusBadRequest, "GPT 模型指纹辨别需要 Chat 地址")
		return
	}
	// 渠道质量测试必须用真实客户端的完整伪装（中转认出检测流量就能专门转给好的上游），服务器没有对应模板就不发起，不静默退回简版
	if in.Preset == probe.ModeQuality {
		family, client := "gpt", "Codex"
		if protocol {
			family, client = "claude", "Claude Code"
		}
		if _, ok := s.Relay.Disguise.Template(family); !ok {
			writeError(w, http.StatusBadRequest, "服务器没有 "+client+" 伪装模板，渠道质量测试要用真实客户端的请求形态才测得准。配置方法见 docs/deploy.md「模型检测伪装配置」；可以先跑模型指纹辨别")
			return
		}
	}
	secret, err := s.Store.ChannelSecret(r.Context(), selected.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.probeAdmission.Lock()
	defer s.probeAdmission.Unlock()
	total, channelCount, err := s.Store.CountRunningProbesForChannel(r.Context(), in.ChannelID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if total >= maxRunningProbes || channelCount > 0 {
		writeError(w, http.StatusTooManyRequests, "已有检测在进行，等它跑完再发起")
		return
	}
	run := store.ProbeRun{ChannelID: &in.ChannelID, ChannelName: channelName, Model: in.Model, UpstreamModel: upstream, Baseline: in.Baseline, Preset: in.Preset}
	if err := s.Store.CreateProbeRun(r.Context(), &run); err != nil {
		writeStoreError(w, err)
		return
	}
	s.probeProgress.Store(run.ID, [2]int{0, 0})
	go s.runProbe(run, secret)
	writeJSON(w, http.StatusOK, map[string]any{"id": run.ID})
}

// runProbe 在后台执行检测并写入结果；和发起请求的连接无关，管理员关掉页面也会跑完。
func (s *Server) runProbe(run store.ProbeRun, ch *store.Channel) {
	defer s.probeProgress.Delete(run.ID)
	timeout := 15 * time.Minute
	if probe.NormalizeMode(run.Preset) == probe.ModeFingerprint {
		timeout = 60 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// 一次检测用几个会话编号：号池按会话路由，多个会话才能抽查到不同的上游
	sessions := make([]string, probe.Sessions)
	for i := range sessions {
		sessions[i] = relay.NewProbeSession()
	}
	// 这个渠道不支持推理强度 none 时，后面的请求直接用 low，免得几百道指纹题每道都先失败一次。
	// 明确返回 400 立刻认定；只是空流要几乎全军覆没（至少 5 次且占 80%）才认定——kinght 这类渠道约 7% 的流会随机断，不能因此整次降档
	var noneUnsupported atomic.Bool
	var noneTried, noneEmpty atomic.Int32
	// 质量测试用标准 Responses，指纹辨别用标准 Chat；Claude 两种模式都用 Messages。
	gpt, protocolName := s.Relay.ProbeChat, "chat"
	if probe.NormalizeMode(run.Preset) == probe.ModeQuality {
		gpt, protocolName = s.Relay.ProbeResponses, "responses"
	}
	if probe.IsProtocolModel(run.Baseline) {
		protocolName = "messages"
	}
	caller := func(ctx context.Context, req probe.Request) probe.Call {
		if probe.IsProtocolModel(run.Baseline) {
			return s.Relay.ProbeMessages(ctx, ch, run.UpstreamModel, sessions[req.Session%len(sessions)], req)
		}
		if req.Effort == "none" && noneUnsupported.Load() {
			req.Effort = "low"
			c := gpt(ctx, ch, run.UpstreamModel, sessions[req.Session%len(sessions)], req)
			c.EffortSent = "low"
			return c
		}
		c := gpt(ctx, ch, run.UpstreamModel, sessions[req.Session%len(sessions)], req)
		if req.Effort == "none" {
			tried := noneTried.Add(1)
			switch c.NoneRejectedBy {
			case "400":
				noneUnsupported.Store(true)
			case "empty":
				if empty := noneEmpty.Add(1); empty >= 5 && empty*5 >= tried*4 {
					noneUnsupported.Store(true)
				}
			}
		}
		return c
	}
	spec := probe.Spec{Baseline: run.Baseline, UpstreamModel: run.UpstreamModel, Mode: probe.NormalizeMode(run.Preset)}
	if spec.Mode == probe.ModeQuality {
		if tp, ok := s.Relay.Disguise.Template(probe.Family(run.Baseline)); ok {
			spec.Disguise, spec.Template, spec.TemplateHash, spec.TemplateTokens = true, tp.Name, tp.Hash, tp.Tokens
		}
	}
	report, err := probe.Run(ctx, caller, spec, func(done, total int) {
		s.probeProgress.Store(run.ID, [2]int{done, total})
	})
	if report != nil {
		report.Protocol = protocolName
	}
	finish := context.Background()
	if err != nil {
		if e := s.Store.FinishProbeRun(finish, run.ID, "failed", nil, err.Error(), decimal.Zero, nil); e != nil {
			slog.Error("写检测结果失败", "id", run.ID, "err", e)
		}
		return
	}
	raw, _ := json.Marshal(report)
	status := "done"
	if report.StopReason != "" {
		status = "failed"
	}
	if e := s.Store.FinishProbeRun(finish, run.ID, status, report.Score, report.Verdict, s.probeCost(finish, run.Model, report.Calls), raw); e != nil {
		slog.Error("写检测结果失败", "id", run.ID, "err", e)
	}
}

// probeCost 按站内模型的基础价估算本次检测的花费（上游实际怎么收费取决于渠道）。
func (s *Server) probeCost(ctx context.Context, model string, calls []probe.Call) decimal.Decimal {
	models, err := s.Store.ListModels(ctx)
	if err != nil {
		return decimal.Zero
	}
	for _, m := range models {
		if m.Name != model {
			continue
		}
		prices := billing.Prices{Input: m.InputPrice, Output: m.OutputPrice, CacheRead: m.CacheReadPrice, CacheWrite: m.CacheWritePrice}
		total := decimal.Zero
		for _, c := range calls {
			u := usage.Usage{Input: c.InputTokens - c.CachedTokens, CacheRead: c.CachedTokens, Output: c.OutputTokens}
			total = total.Add(billing.Cost(u, prices, decimal.NewFromInt(1)))
		}
		return total
	}
	return decimal.Zero
}
