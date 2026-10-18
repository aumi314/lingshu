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

var channelTemplates = []ChannelTemplate{
	{ID: "deepseek", Name: "DeepSeek", BaseURLChat: "https://api.deepseek.com", BaseURLResponses: "https://api.deepseek.com",
		HasModelList: true, Note: "按量计费，同一地址支持 chat 和 responses；也支持 messages（https://api.deepseek.com/anthropic）"},
	{ID: "moonshot", Name: "Kimi（Moonshot 中国站）", BaseURLChat: "https://api.moonshot.cn/v1",
		HasModelList: true, Note: "流式 usage 需要 include_usage，网关会自动加；也支持 messages（https://api.moonshot.cn/anthropic）"},
	{ID: "zhipu", Name: "智谱 GLM（中国站）", BaseURLChat: "https://open.bigmodel.cn/api/paas/v4",
		PresetModels: []string{"GLM-5.3", "GLM-5.3-Flash"}, Note: "没有模型列表接口，已预置常用模型"},
	{ID: "mimo", Name: "Xiaomi MiMo", BaseURLChat: "https://api.xiaomimimo.com/v1",
		HasModelList: true, Note: "Token Plan 也用这个地址"},
	{ID: "stepfun", Name: "StepFun 阶跃星辰", BaseURLChat: "https://api.stepfun.com/v1",
		HasModelList: true, Note: "Step Plan 订阅地址是 https://api.stepfun.com/step_plan/v1"},
	{ID: "dashscope", Name: "阿里百炼 Qwen", BaseURLMessages: "https://dashscope.aliyuncs.com/apps/anthropic",
		PresetModels: []string{"qwen3.8-max", "qwen3.8-flash"}, Note: "Anthropic 兼容地址，没有模型列表接口，已预置常用模型"},
	{ID: "custom", Name: "自定义", HasModelList: true, Note: "按上游文档填写各协议的地址"},
}
