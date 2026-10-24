package api

// ChannelTemplate 是新建渠道时的预设：填好地址和协议，没有模型列表接口的上游预置常用模型。
// 协议按 docs/decisions.md 的 MVP 选择；上游实际还支持的其他协议写在 Note 里，管理员可以自己加。
type ChannelTemplate struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	BaseURLChat      string   `json:"base_url_chat"`
	BaseURLMessages  string   `json:"base_url_messages"`
	BaseURLResponses string   `json:"base_url_responses"`
	BaseURLSystemOne string   `json:"base_url_systemone"`
	HasModelList     bool     `json:"has_model_list"` // 上游有没有 /models 接口
	PresetModels     []string `json:"preset_models"`  // 没有 /models 接口时预置的模型名
	ProxyURL         string   `json:"proxy_url"`      // 需要走代理的上游，套模板时自动填上
	Note             string   `json:"note"`
}

// 聊天协议地址见 docs/research/upstream-protocols.md；System One 地址于 2026-10-22 实测，见 docs/research/systemone.md。
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
	{ID: "minimax", Name: "MiniMax（中国站）", BaseURLChat: "https://api.minimaxi.com/v1", BaseURLMessages: "https://api.minimax.cn/anthropic",
		BaseURLResponses: "https://api.minimaxi.com/v1", HasModelList: true,
		Note: "三种协议都支持；注意 messages 的域名是 api.minimax.cn，和另外两个不同。MiniMax-M3.1-Flash-Preview 只在 M Plan 订阅里，按量用 MiniMax-M3.1-Flash"},
	{ID: "volcengine", Name: "火山方舟（豆包）", BaseURLChat: "https://ark.cn-beijing.volces.com/api/v3",
		BaseURLMessages: "https://ark.cn-beijing.volces.com/api/compatible", BaseURLResponses: "https://ark.cn-beijing.volces.com/api/v3", HasModelList: true,
		Note: "三种协议都支持；上游模型名带版本日期（如 doubao-seed-2-1-pro-260915），建议映射成不带日期的站内名。缓存另按小时收存储费，灵枢不计"},
	{ID: "hunyuan", Name: "腾讯混元（TokenHub 中国站）", BaseURLChat: "https://tokenhub.tencentmaas.com/v1",
		BaseURLMessages: "https://tokenhub.tencentmaas.com", BaseURLResponses: "https://tokenhub.tencentmaas.com/v1", HasModelList: true,
		Note: "腾讯云 TokenHub，三种协议都支持（2026-10-21 实测 hy4-preview，流式都带用量）；同一个账号还能调 GLM、DeepSeek、Kimi、MiniMax 等模型。新 key 刚开通时可能短暂返回 402“service ID 不存在”，过一会儿再测"},
	{ID: "openrouter", Name: "OpenRouter", BaseURLChat: "https://openrouter.ai/api/v1", BaseURLMessages: "https://openrouter.ai/api",
		BaseURLResponses: "https://openrouter.ai/api/v1", HasModelList: true, ProxyURL: "socks5://127.0.0.1:10808",
		Note: "三种协议都支持。从中国访问 Claude、GPT 等模型会被按地区拒绝，需要走代理（已预填本机出境代理）。模型名带厂商前缀，如 anthropic/claude-opus-5.5"},
	{ID: "openai-platform", Name: "OpenAI Platform", BaseURLChat: "https://api.openai.com/v1", BaseURLResponses: "https://api.openai.com/v1",
		HasModelList: true, Note: "OpenAI 官方 Platform API；支持 Chat Completions 和 Responses，不提供 Anthropic Messages 协议"},
	{ID: "typesafe", Name: "TypeSafe", BaseURLSystemOne: "https://api.typesafe.ai/v1", HasModelList: true,
		Note: "Jev 决策模型，使用 System One 协议（state + questions），不支持聊天或流式输出；输出 token 免费，模型价格在定价页设置"},
	{ID: "nace", Name: "Nace", BaseURLSystemOne: "https://console.nace.ai/v1", HasModelList: true,
		Note: "Drex 决策模型，使用 System One 协议（state + questions），不支持聊天或流式输出；输出 token 免费，模型价格在定价页设置"},
	{ID: "standard", Name: "标准模板", HasModelList: true, Note: "填写根地址，自动生成 Chat、Messages 和 Responses 三种协议地址"},
	{ID: "custom", Name: "自定义", HasModelList: true, Note: "按上游文档填写各协议的地址"},
}
