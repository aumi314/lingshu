package bridge

import "strings"

// 下面这些表按 Anthropic 官方文档整理，来源：
//   effort 档位：https://platform.claude.com/docs/en/build-with-claude/effort
//   thinking 支持：https://platform.claude.com/docs/en/build-with-claude/thinking-troubleshooting
// 官方文档里 max 和 xhigh 按型号分开列，所以按型号记，不做统一降级。

// claudeEffortSupport 是各型号支持的 effort 档位。
// 官方 max 覆盖：Fable 5.1、Mythos 5.1、Fable 5、Mythos 5、Mythos Preview、
// Opus 5.5、Opus 5、Opus 4.8、Opus 4.7、Opus 4.6、Sonnet 5.5、Sonnet 5、
// Sonnet 4.6、Haiku 5.5。
// 官方 xhigh 覆盖：Fable 5.1、Mythos 5.1、Fable 5、Mythos 5、Opus 5.5、
// Opus 5、Opus 4.8、Opus 4.7、Sonnet 5.5、Sonnet 5、Haiku 5.5。
// 所以 Opus 4.6、Sonnet 4.6、Mythos Preview 有 max 没有 xhigh。
var claudeEffortSupport = map[string][]string{
	"claude-fable-5-1":      {"low", "medium", "high", "xhigh", "max"},
	"claude-mythos-5-1":     {"low", "medium", "high", "xhigh", "max"},
	"claude-fable-5":        {"low", "medium", "high", "xhigh", "max"},
	"claude-mythos-5":       {"low", "medium", "high", "xhigh", "max"},
	"claude-mythos-preview": {"low", "medium", "high", "max"},
	"claude-opus-5-5":       {"low", "medium", "high", "xhigh", "max"},
	"claude-opus-5":         {"low", "medium", "high", "xhigh", "max"},
	"claude-opus-4-8":       {"low", "medium", "high", "xhigh", "max"},
	"claude-opus-4-7":       {"low", "medium", "high", "xhigh", "max"},
	"claude-opus-4-6":       {"low", "medium", "high", "max"},
	"claude-sonnet-5-5":     {"low", "medium", "high", "xhigh", "max"},
	"claude-sonnet-5":       {"low", "medium", "high", "xhigh", "max"},
	"claude-sonnet-4-6":     {"low", "medium", "high", "max"},
	"claude-haiku-5-5":      {"low", "medium", "high", "xhigh", "max"},
}

// claudeAdaptiveModels 是支持 thinking.type=adaptive 的型号。
// Opus 4.5 一族只支持旧的手动预算思考，本层不发 budget_tokens，所以不列。
var claudeAdaptiveModels = map[string]bool{
	"claude-fable-5-1":      true,
	"claude-mythos-5-1":     true,
	"claude-fable-5":        true,
	"claude-mythos-5":       true,
	"claude-mythos-preview": true,
	"claude-opus-5-5":       true,
	"claude-opus-5":         true,
	"claude-opus-4-8":       true,
	"claude-opus-4-7":       true,
	"claude-opus-4-6":       true,
	"claude-sonnet-5-5":     true,
	"claude-sonnet-5":       true,
	"claude-sonnet-4-6":     true,
	"claude-haiku-5-5":      true,
}

// 关思考的三种方式：
//
//	disabled：官方接受 thinking.type=disabled。
//	between：官方只接受 thinking.type=between_tools（Sonnet 5.5）。
//	always：思考关不掉，给 none 就是错，直接报错。
const (
	thinkingOffDisabled = "disabled"
	thinkingOffBetween  = "between_tools"
	thinkingOffAlways   = "always_on"
)

// claudeThinkingOff 是各型号关思考的方式。
var claudeThinkingOff = map[string]string{
	"claude-fable-5-1":      thinkingOffAlways,
	"claude-mythos-5-1":     thinkingOffAlways,
	"claude-fable-5":        thinkingOffAlways,
	"claude-mythos-5":       thinkingOffAlways,
	"claude-mythos-preview": thinkingOffAlways,
	"claude-opus-5-5":       thinkingOffAlways,
	"claude-sonnet-5-5":     thinkingOffBetween,
	"claude-opus-5":         thinkingOffDisabled,
	"claude-haiku-5-5":      thinkingOffDisabled,
	"claude-opus-4-8":       thinkingOffDisabled,
	"claude-opus-4-7":       thinkingOffDisabled,
	"claude-sonnet-5":       thinkingOffDisabled,
	"claude-opus-4-6":       thinkingOffDisabled,
	"claude-sonnet-4-6":     thinkingOffDisabled,
}

// normalizeModel 把上游模型名收拾成查表用的名字：
// 去掉厂商前缀（anthropic/claude-opus-5.5），点号换横线（claude-opus-5.5 → claude-opus-5-5）。
func normalizeModel(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ReplaceAll(name, ".", "-")
}

// knownClaudeModel 找出已知型号（带日期后缀也算同一个型号），返回标准名。
func knownClaudeModel(name string) (string, bool) {
	normalized := normalizeModel(name)
	best := ""
	for candidate := range claudeEffortSupport {
		if normalized == candidate || strings.HasPrefix(normalized, candidate+"-") {
			if len(candidate) > len(best) {
				best = candidate
			}
		}
	}
	return best, best != ""
}

// effortSupported 判断某型号支不支持这一档 effort。
func effortSupported(model, effort string) bool {
	name, ok := knownClaudeModel(model)
	if !ok {
		return false
	}
	for _, supported := range claudeEffortSupport[name] {
		if supported == effort {
			return true
		}
	}
	return false
}

// effortPlan 是这次请求实际要发给上游的思考设置。
type effortPlan struct {
	thinking     map[string]any // Anthropic 的 thinking
	outputConfig map[string]any // Anthropic 的 output_config
}

// planEffort 把 Responses 的 reasoning.effort 转成 Anthropic 的思考设置。
// 不做静默降级：型号或档位不支持就报错，让调用方知道而不是悄悄少想。
func planEffort(model, effort string) (effortPlan, error) {
	switch effort {
	case "none":
		return planNoThinking(model)
	case "low", "medium", "high", "xhigh", "max":
		name, known := knownClaudeModel(model)
		if !known {
			return effortPlan{}, newError(CodeUnsupportedEffort, "reasoning.effort",
				"模型 %s 的推理强度无法确认，暂时只接受 effort=none", model)
		}
		if !claudeAdaptiveModels[name] {
			return effortPlan{}, newError(CodeUnsupportedEffort, "reasoning.effort",
				"模型 %s 只支持旧的手动预算思考，本转接发不了 budget_tokens，请改用 effort=none", name)
		}
		if !effortSupported(name, effort) {
			return effortPlan{}, newError(CodeUnsupportedEffort, "reasoning.effort",
				"模型 %s 不支持推理强度 %s（支持 %s）", name, effort, strings.Join(claudeEffortSupport[name], "/"))
		}
		return effortPlan{
			thinking:     map[string]any{"type": "adaptive"},
			outputConfig: map[string]any{"effort": effort},
		}, nil
	default:
		// minimal 这类 Anthropic 没有的档位不猜，往下压就是偷偷降级。
		return effortPlan{}, newError(CodeUnsupportedEffort, "reasoning.effort",
			"推理强度 %s 不能转成 Messages 的 effort（支持 none、low、medium、high、xhigh、max）", effort)
	}
}

// planNoThinking 处理 effort=none：思考要真的关掉。
// 官方文档里 Opus 5.5、Fable、Mythos 一族思考永远开着，
// 请求里写 thinking.disabled 会被 400 拒，所以这里明确报错而不是偷偷改成 low。
func planNoThinking(model string) (effortPlan, error) {
	name, known := knownClaudeModel(model)
	if !known {
		// 型号不认识，没法确认能不能关，按关掉处理，真不行上游会报错。
		return effortPlan{thinking: map[string]any{"type": "disabled"}}, nil
	}
	switch claudeThinkingOff[name] {
	case thinkingOffAlways:
		return effortPlan{}, newError(CodeUnsupportedEffort, "reasoning.effort",
			"模型 %s 的思考关不掉（官方文档：始终开启），请把 reasoning.effort 改成 low 或更高", name)
	case thinkingOffBetween:
		// Sonnet 5.5 只认 between_tools，官方说明它只在 high 及以下可用，
		// 这里不发 output_config.effort，用模型自己的默认档（high）。
		return effortPlan{thinking: map[string]any{"type": "between_tools"}}, nil
	default:
		return effortPlan{thinking: map[string]any{"type": "disabled"}}, nil
	}
}

// describeEffort 把实际发出去的思考设置写成人话，进诊断。
func describeEffort(plan effortPlan) string {
	thinking := strField(plan.thinking, "type")
	if plan.outputConfig == nil {
		if thinking == "disabled" {
			return "关闭思考"
		}
		return thinking
	}
	return thinking + " + " + strField(plan.outputConfig, "effort")
}
