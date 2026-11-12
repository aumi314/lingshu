# Magpie 请求解析与抓取记录

核对日期：2026-11-11。源码快照：`92c798aac5f5507c3e485450c911e535ddb45889`。本文对照项目内只读快照 `references/magpie` 的源码与子系统设计文档，梳理请求解析、Routing 上下文窗口和会话/请求正文的记录路径。

## 结论

Magpie 有三类不同的请求记录：Routing trace 保存路由决策和 Prompt 结构统计；Gateway Conversations 是默认关闭、保存在本机的会话正文抓取；Request archive 是可选的单请求请求/响应归档。它们的数据内容、触发条件、保留方式和用途不同。Usage 页面里的 gateway session 则是从用量记录即时聚合的统计 projection，不保存对话正文。

## 请求解析

客户端请求进入 `serveAgent` 后，Magpie 设置客户端会话标识，处理 Responses 历史响应，然后挂载 Gateway Conversations 记录器，再运行工具循环、网关 hooks、middleware 和协议转换/路由。因此正文抓取包住一次客户端可见的请求与响应，重试不会各自产生一条 assistant 回复。[middleware.go:23-50](../../references/magpie/internal/gateway/middleware.go:23)

统一解析入口 `parseRequest` 按协议分派：

| 客户端协议 | 解析器 |
|---|---|
| Chat Completions | `readChat` |
| Responses API | `readResponsesFor` |
| Gemini | `parseGemini` |
| Anthropic Messages 及默认分支 | `readAnthropic` |

解析成功后统一经过 `afterParse`，处理 thought signature、sealed reasoning 和必需工具校验。[gateway.go:9172-9204](../../references/magpie/internal/gateway/gateway.go:9172)

Chat 解析覆盖 model、输出上限、采样参数、stream、reasoning effort、service tier、system/developer/user/assistant/tool messages、reasoning、tool calls 与结果、图片、tools、tool choice 和 web search。[chat.go:76-174](../../references/magpie/internal/gateway/chat.go:76)

Responses 解析接受字符串或结构化 `input`，并处理 `instructions`、reasoning、工具调用/结果、sealed reasoning、web search 和 developer/system 消息。[responses.go:321-380](../../references/magpie/internal/gateway/responses.go:321) 对只需提取事实的路径，解析器不保留 assistant 文本、工具参数和工具输出，减少长对话被重复解码产生的内存副本。[responses.go:323-327,391-393,454-468](../../references/magpie/internal/gateway/responses.go:323)、[parse_memo.go:10-76](../../references/magpie/internal/gateway/parse_memo.go:10)

## Routing 上下文窗口

上下文卡先从协议转换前的客户端请求体读取 prompt，把内容归为 system、tools、skills、memory、files、results、chat 等部分。初始 token 数是估算值：ASCII 每约 4 字符计一个 token，非 ASCII 字符按字符计，图片按约 1600 token 估算。[prompt.go:30-97](../../references/magpie/internal/gateway/prompt.go:30)

供应商返回 usage 后，Magpie 用最后一次有效尝试的输入、cache read 和 cache write 计数校准 prompt 总量，并按比例缩放各部分 token 数，使分项合计到上游计数。[prompt.go:1194-1218](../../references/magpie/internal/gateway/prompt.go:1194)、[gateway.go:5417-5427](../../references/magpie/internal/gateway/gateway.go:5417)

上下文窗口按模型的用户配置、模型 catalog 条目、provider 通用配置顺序读取；Routing 卡显示正在进行的请求时也会读取目标模型窗口。[fallback.go:2213-2235](../../references/magpie/internal/gateway/fallback.go:2213)、[prompt.go:1220-1244](../../references/magpie/internal/gateway/prompt.go:1220) Context 汇总在窗口未知时以 200,000 tokens 作为估算分母。[context.go:29-31,595-648](../../references/magpie/internal/gui/context.go:29)

正在进行的请求，上下文卡显示的是按当前请求体估算的数字；请求结束、拿到有效 usage 后才校准。卡片上的“窗口”来自目标模型的窗口配置（用户设置、catalog 或 provider 默认）。[prompt.go:1232-1273](../../references/magpie/internal/gateway/prompt.go:1232)

路由历史持久化 Prompt 的分项名称与 token 数，不持久化 prompt 正文；正文为上下文卡临时保留在 trace 内存中。[prompt.go:10-16,43-70](../../references/magpie/internal/gateway/prompt.go:10) 最近 10 个请求的正文在内存中保留，单个最多 32 MiB，总计最多 64 MiB，超限时丢弃最早内容。[prompt_text.go:27-67](../../references/magpie/internal/gateway/prompt_text.go:27)

## 三类记录

| 记录 | 内容 | 开关与保存方式 | 用途 |
|---|---|---|---|
| Routing trace | 路由候选、权重/顺序、重试、失败、用量、Prompt 分项统计 | 路由历史；Prompt 正文在最近请求的短期内存中，或由独立 archive 提供 | 查看请求为何走某条渠道、key/account 如何选择、Prompt 由哪些部分组成 |
| Gateway Conversations | 规范化后的输入、输出、工具调用/结果等会话 turn | Sessions 页显式开关，默认关闭；本地保存 7 天、256 MiB | 在 Sessions 页查看网关会话正文 |
| Request archive | 单次请求和响应的 headers、body、状态等 | `RequestArchive` (S3 bucket) 或单独的 `RequestArchiveLocal` 本机开关 | 打开单个请求的完整归档以便排查 |

### Gateway Conversations

它只记录客户端请求明确携带 session id 的会话，不为无 session id 的请求创建记录。[conversations.go:21-27,59-75](../../references/magpie/internal/gateway/conversations.go:21)

记录正文时，Magpie按协议将输入解析成统一 parts，将输出从完整 JSON 或 SSE 解码成 parts；Responses 流优先使用完整 output items。图片内容用占位符展示，超过输入/输出抓取上限或流不完整时会标记截断。[conversations.go:59-93,96-123,125-217](../../references/magpie/internal/gateway/conversations.go:59)

保存内容经过 secrets scrub：工具调用参数、文件内容、文本、图片分别规范化；这是已识别敏感值的脱敏，不代表任意敏感文字都会被自动移除。[conversations.go:96-149](../../references/magpie/internal/gateway/conversations.go:96)、[gateway-sessions.md:477-501](../../references/magpie/docs/subsystems/gateway-sessions.md:477)

限制和位置：输入与输出抓取临时上限各 8 MiB；单 turn 落盘最多 1 MiB，每方向有 64 KiB part 预算；每天每 session 文件最多 256 MiB；保留 7 天。内容写入 Magpie 配置目录下 `gateway-conversations/<UTC date>/<identity hash>/turns.jsonl`，目录权限 0700、文件权限 0600。[conversations.go:19,71-74](../../references/magpie/internal/gateway/conversations.go:19)、[sessions/gateway.go:27-55,89-100](../../references/magpie/internal/sessions/gateway.go:27)、[gateway-sessions.md:477-501](../../references/magpie/docs/subsystems/gateway-sessions.md:477)

停止并清除会关闭记录、删除本地抓取存储及已删除会话在 trash 中的抓取文本；generation 检查可避免在途请求完成后重新写入刚清空的记录。[sessions/gateway.go:191-205](../../references/magpie/internal/sessions/gateway.go:191)

### Request archive

Request archive 与 Gateway Conversations 是不同开关、不同格式和不同用途。归档在启用时保存单次调用的请求/响应头与正文；优先写已配置的 bucket，否则需开启本机 `RequestArchiveLocal` 才写入本地。归档关闭时不保存请求正文。[archive.go:82-115](../../references/magpie/internal/gateway/archive.go:82)、[settings.go:273-293](../../references/magpie/internal/settings/settings.go:273)

本机归档位于 Magpie 配置目录的 `request-archive/<date>/<id>.json`；本机容量默认 1 GiB，超限后移除最旧调用。[archive_local.go:3-9,30-68](../../references/magpie/internal/gateway/archive_local.go:3)

上下文卡在内存 prompt body 被丢弃后，可读取 route 所引用的请求 archive 重新解析和脱敏；响应正文也可从 archive 提供。[context_archive.go:62-150](../../references/magpie/internal/gui/context_archive.go:62)、[gateway-routing.md:49](../../references/magpie/docs/subsystems/gateway-routing.md:49)

### Usage session projection

Usage 的 gateway session 是从 usage ledger 快照聚合而成的只读视图，统计会话、模型、token、费用，不读取 Routing history 或 agent 文件，也不保存对话文本。[usage/gateway_sessions.go:16-24,38-69](../../references/magpie/internal/usage/gateway_sessions.go:16)

## 页面上的对应关系

- 路由页里工具调用次数（如 `exec command ×N`）、提示词/工具/结果分类属于 Prompt 结构分析：它展示请求所带上下文的分项 token 估算/校准结果，不表示这些项目被持久保存成了完整 transcript。
- Sessions 页的“记录网关对话”是独立的本机正文保存开关。默认关闭；开关说明中的 7 天和 256 MiB 对应 Gateway Conversations 存储约束。[gateway-sessions.md:437-475](../../references/magpie/docs/subsystems/gateway-sessions.md:437)
- 需要从历史记录重新查看单次请求全文时，走 Request archive 的设置和容量，不依赖 Gateway Conversations 开关。

## 参考文件

- `references/magpie/internal/gateway/gateway.go`
- `references/magpie/internal/gateway/chat.go`
- `references/magpie/internal/gateway/responses.go`
- `references/magpie/internal/gateway/prompt.go`
- `references/magpie/internal/gateway/prompt_text.go`
- `references/magpie/internal/gateway/conversations.go`
- `references/magpie/internal/gateway/archive.go`
- `references/magpie/internal/sessions/gateway.go`
- `references/magpie/internal/usage/gateway_sessions.go`
- `references/magpie/docs/subsystems/gateway-routing.md`
- `references/magpie/docs/subsystems/gateway-sessions.md`
