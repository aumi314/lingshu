package api

// ChannelTemplate 是新建渠道时的预设：填好地址和协议，没有模型列表接口的上游预置常用模型。
// 协议按 docs/decisions.md 的 MVP 选择；上游实际还支持的其他协议写在 Note 里，管理员可以自己加。
type ChannelTemplate struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	BaseURLChat      string   `json:"base_url_chat"`
	BaseURLMessages  string   `json:"base_url_messages"`
	BaseURLResponses string   `json:"base_url_responses"`
	HasModelList     bool     `json:"has_model_list"` // 上游有没有 /models 接口
	PresetModels     []string `json:"preset_models"`  // 没有 /models 接口时预置的模型名
	Note             string   `json:"note"`
}

// 协议地址都经过 2026-10-18 实测（灵枢冒烟测试，三种协议 × 各家模型，流式 usage 都有），见 docs/research/upstream-protocols.md。
var channelTemplates = []ChannelTemplate{
	{ID: "deepseek", Name: "DeepSeek", BaseURLChat: "https://api.deepseek.com", BaseURLMessages: "https://api.deepseek.com/anthropic",
		BaseURLResponses: "https://api.deepseek.com", HasModelList: true, Note: "按量计费，三种协议都支持"},
	{ID: "moonshot", Name: "Kimi（Moonshot 中国站）", BaseURLChat: "https://api.moonshot.cn/v1", BaseURLMessages: "https://api.moonshot.cn/anthropic",
		BaseURLResponses: "https://api.moonshot.cn/v1", HasModelList: true,
		Note: "三种协议都支持（实测 kimi-k3、kimi-k2.7-code 都能走 responses）；限流按累计充值分档，最低档每分钟 3 次"},
	{ID: "zhipu", Name: "智谱 GLM（中国站）", BaseURLChat: "https://open.bigmodel.cn/api/paas/v4", BaseURLMessages: "https://open.bigmodel.cn/api/anthropic",
		BaseURLResponses: "https://open.bigmodel.cn/api/v1", PresetModels: []string{"GLM-5.3", "GLM-5.3-FlashX"},
		Note: "三种协议都支持，要用 2026-10-16 之后新建的标准 Key；之前的老 Key（标着“兼容”）调 messages / responses 会优先扣 Coding Plan 额度，套餐到期就失败。没有模型列表接口，已预置常用模型"},
	{ID: "mimo", Name: "Xiaomi MiMo", BaseURLChat: "https://api.xiaomimimo.com/v1", BaseURLMessages: "https://api.xiaomimimo.com/anthropic",
		BaseURLResponses: "https://api.xiaomimimo.com/v1", HasModelList: true, Note: "三种协议都支持；Token Plan 也用这些地址"},
	{ID: "stepfun", Name: "StepFun 阶跃星辰", BaseURLChat: "https://api.stepfun.com/v1", BaseURLMessages: "https://api.stepfun.com",
		BaseURLResponses: "https://api.stepfun.com/v1", HasModelList: true,
		Note: "三种协议都支持（官方文档写 responses 只支持 step-5-preview、step-3.7-flash）；Step Plan 订阅地址是 https://api.stepfun.com/step_plan/v1"},
	{ID: "dashscope", Name: "阿里百炼 Qwen", BaseURLChat: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		BaseURLMessages: "https://dashscope.aliyuncs.com/apps/anthropic", BaseURLResponses: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		PresetModels: []string{"qwen3.8-max", "qwen3.8-flash"},
		Note:         "三种协议都支持（responses 官方未写，实测可用）；没有模型列表接口，已预置常用模型"},
	{ID: "custom", Name: "自定义", HasModelList: true, Note: "按上游文档填写各协议的地址"},
}
