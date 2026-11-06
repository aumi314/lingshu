# OpenCode Zen 原生渠道

本分支将 OpenCode Zen 接入灵枢 Go 进程；由灵枢直接连接官方推理服务，运行不需要 Bun、Magpie、NewAPI 或额外的转发容器。本轮只交付 PR，线上渠道仍维持原配置。

## 配置

新建渠道选择 **OpenCode Zen** 模板，填写自己的上游 API key，拉取模型并映射到已定价的站内模型。模板的 Chat Base URL 是 `https://opencode.ai/inference/openai/v1`，实际请求路径为 `/chat/completions`，模型列表为 `/models`。本期只验证 Chat，不做 Messages / Responses 的协议转换。

`deepseek-v4.1-flash-free` 使用独立站内模型名，可在定价页把四项单价设为零；创建模板不自动调整任何价格或现有渠道。其他模型需先冒烟测试，模型列表中的存在不保证该模型支持 Chat 协议。

## 请求与响应

只有显式选择 `opencode-zen` 模板且上游模型名以 `-free` 结尾时，灵枢才补充编码客户端的上下文、核心工具声明与会话信息。其他模板保持原有转发行为，OpenCode 的非免费模型保留请求体。

- 保留调用者的系统提示词、消息、工具定义、推理强度、输出上限和其他模型参数。新增的通用编码上下文使用逻辑目录 `/workspace`，不读取宿主目录、环境文件或用户资料。
- 缺少的 `bash`、`edit`、`glob`、`grep`、`read` 以明确标注不可用的工具声明补齐。已有同名工具保持原定义；如果模型实际调用补充的工具，返回错误，避免执行不存在的工具或把丢失的动作伪装成成功。
- 上游始终请求 SSE 与完整 usage。流式调用沿用现有用量解析及首字前重试规则；非流式调用收完整 SSE 后返回 Chat completion JSON，保留文本、推理、工具调用、结束原因和完整 usage。截断或错误事件不算成功。
- 会话 ID 按灵枢调用 key、渠道及客户端会话隔离并在内存中有限缓存；无显式会话时用首条用户消息归属。重启后会话 ID 可变化。项目归属用 `global`，不把服务器路径当作用户项目。
- 上游鉴权使用该渠道自己的 key；普通用户的灵枢 key 不会发给厂商。计费、冻结、分组权限、路由重试及调用日志继续走原流程。

## 验证依据

2026-11-06 同机对照中，Go 默认 TLS 和 Chrome ClientHello 的请求返回 `429 FreeUsageLimitError`；使用本地捕获的 Bun ClientHello 构造 Go uTLS 请求，直接从 DMIT 调用得到 HTTP 200、`OK`、`opencode.billing_source: free` 和零费用。因此该模板使用单独的原生 Go TLS 传输，普通渠道不受影响。该结果说明这组请求需要专属传输处理；它没有定位服务端的具体判定规则。

公开合同与模型可用性会变化。官方说明免费层面向 OpenCode 内调用，服务端限制仍可能返回 403 / 429；灵枢沿用现有错误和重试规则，不将免费模型改成收费模型兜底。当前验证范围为 `deepseek-v4.1-flash-free`。

参考来源：[官方推理 API](https://opencode.ai/v2/docs/console/api/inference/)、[模型目录](https://opencode.ai/v2/docs/console/models/)、[uTLS](https://github.com/refraction-networking/utls)。
