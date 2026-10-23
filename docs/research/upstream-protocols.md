# 上游协议支持查证

System One（TypeSafe / Nace）的协议、价格与本地验收见 [单独记录](systemone.md)。下文保留聊天协议的历史查证。

查证日期 2026-10-18，依据各家官方文档（便宜模型查证，原始报告带 URL）。文档会变，做渠道模板前以冒烟测试实测为准。

## 三个入口的支持情况

| 上游 | `/chat/completions` | `/responses` | `/v1/messages` |
|---|---|---|---|
| OpenAI | 原生 | 原生 | 不支持 |
| Anthropic | 只有兼容层，官方说不适合生产，**不用** | 无文档 | 原生 |
| OpenRouter | 原生 | 支持，但无状态：`store: true` 或带 `previous_response_id` 返回 400 | 支持，含 thinking、cache_control |
| DeepSeek | 支持（URL 没有 `/v1`） | 支持，无状态，很多字段被静默忽略 | 支持（`/anthropic` 前缀）；`cache_control` 被忽略，`budget_tokens` 被忽略 |
| Moonshot（Kimi 开放平台） | 支持 | 只有 `kimi-k3`，无状态 | 支持（`/anthropic` 前缀）；`cache_control` 只认顶层 |
| Kimi Code（订阅） | 支持 | 支持 | 支持 |

## 流式响应里的 usage

| 上游 | chat | responses | messages |
|---|---|---|---|
| OpenAI | **要加 `stream_options.include_usage`**，usage 块的 `choices` 为空数组 | 默认带（`response.completed`） | — |
| Anthropic | — | — | 默认带（`message_start` / `message_delta`） |
| OpenRouter | 默认带；usage 块里有一个 choice，不是空数组 | 默认带 | 默认带 |
| DeepSeek | 默认带 | 默认带 | 无文档 |
| Moonshot | **要加 `include_usage`** | 无文档 | 无文档 |
| Kimi Code | 无文档 | 无文档 | 无文档 |

## 对灵枢的影响

- 同一个协议不等于同样的功能：DeepSeek、Moonshot 会静默忽略部分字段（如 `cache_control`），用户发出去不报错，但效果不同。
- `previous_response_id` 引用的是某个上游账号里存的状态，只有原渠道能接，不能换渠道兜底。
- Kimi 开放平台和 Kimi Code 是两套模型名、两个域名，不能混用，要做成两个渠道模板。
- usage 是否带上，以冒烟测试实测为准；没有 usage 时按决策记录里的规则用 tokenizer 估算。

---

# 中国上游查证（MVP 用）

查证日期 2026-10-18，依据官方文档（便宜模型查证，原始报告带 URL）。

## MVP 渠道协议选择（用户决定）

| 上游 | MVP 走的协议 | Base URL（按量计费） | 有没有 `/models` |
|---|---|---|---|
| DeepSeek | chat + responses | `https://api.deepseek.com` | 有 |
| Kimi（Moonshot 中国站） | chat | `https://api.moonshot.cn/v1` | 有 |
| GLM（智谱中国站） | chat | `https://open.bigmodel.cn/api/paas/v4` | **无文档** |
| Xiaomi MiMo | chat | `https://api.xiaomimimo.com/v1` | 有 |
| StepFun | chat | `https://api.stepfun.com/v1` | 有 |
| Qwen（阿里百炼） | **messages** | `https://dashscope.aliyuncs.com/apps/anthropic`（用户指定，避开带 WorkspaceId 的新域名） | **无**（文档明说 Anthropic 端点没有） |

## 和计费、透传相关的坑

- **流式 usage 各家不一样**：
  - 阿里、Moonshot：要加 `stream_options.include_usage`。
  - StepFun：每个 chunk 都带**累计** usage，只能取最后一块，**不能累加**。
  - 智谱：文档里流式 chunk 没有 `usage` 字段，可能要靠 tokenizer 估算。以冒烟测试实测为准。
  - MiMo chat：文档没说是否需要开关。
- **智谱三个协议的 base 不同**：chat 是 `/api/paas/v4`，responses 是 `/api/v1`，anthropic 是 `/api/anthropic`。渠道要能按协议分别填地址。
- **阿里百炼**：官方文档说 `dashscope.aliyuncs.com` 已迁移到 `{WorkspaceId}.cn-beijing.maas.aliyuncs.com`。旧域名现在能不能用，以冒烟测试为准。
- **缓存计价差别大**：智谱命中约半价、自动；阿里显式缓存写入 125%、命中约 10%；MiMo 写入限时免费；StepFun 没有单独的写入价。4 个价格维度都能覆盖，写入价填 0 也合法。
- **订阅套餐的使用条款**：GLM Coding Plan 只能在指定工具里用；Kimi Code 要求保留工具真实的 User-Agent。风险用户已知悉，是否使用由用户决定。

## 实测结果（2026-10-18，本地 WSL 直连）

文档说的和实测不一致时，以实测为准。

| 上游 / 协议 | 模型 | 流式 usage 实测 |
|---|---|---|
| DeepSeek chat | `deepseek-flash` | **默认带**。最后一块带 usage，这一块 `choices` 不为空（含 `finish_reason`）。另有 `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` |
| DeepSeek responses | `deepseek-flash` | `response.completed` 带 usage；没有 `[DONE]`（Responses 协议本来就不发） |
| 智谱 chat | `glm-5.3` | **默认带**（文档没写，实测有），最后一块 |
| StepFun chat | `step-5-preview` | 几乎每块都带**累计** usage；最后一块 `choices` 为空，是总量 |
| Moonshot chat | `kimi-k3` | **不加 `include_usage` 就没有 usage**；加了以后最后一块 `choices` 为空 |
| MiMo chat | `mimo-v2.6-pro` | **默认带**，最后一块 `choices` 为空 |
| 阿里 messages | `qwen3.8-max` | `message_start` 的 `input_tokens`=7，`message_delta` 的 `input_tokens`=68。**开头的值不准，必须以 `message_delta` 为准** |

结算规则据此定为：**每个字段取流里最后出现的值**（后出现的覆盖先出现的），不累加，也不信任开头的值。

其他实测：
- 阿里旧域名 `dashscope.aliyuncs.com/apps/anthropic` 可用；`qwen3.8-max`、`qwen3.8-flash` 都能通，回复里带 thinking 块。
- 智谱模型名不区分大小写（`GLM-5.3` 和 `glm-5.3` 都行，返回的 `model` 字段原样回显）。
- DeepSeek `/models` 只有 `deepseek-flash`、`deepseek-v4-pro`；传 `deepseek-v4.1-flash` 会报错。
- MiMo 第一次给的 key 无效（401），换 key 后 `https://api.xiaomimimo.com/v1` 正常。
- 阿里渠道上没有 `MiniMax-M3`。

## 官方价格（2026-10-18 查证，每百万 token）

便宜模型从官方价格页查证，原始报告带 URL。国际站（USD）和中国站（CNY / 6.7）对比：

| 模型 | 国际站 输入 / 输出（USD） | 中国站 输入 / 输出（CNY） | 国际站 ÷ (中国站/6.7) |
|---|---|---|---|
| deepseek-flash | 0.30 / 1.20（低谷 0.15 / 0.60） | 2 / 8（低谷 1 / 4） | 1.00 |
| GLM-5.3 | 1.40 / 4.40 | 8 / 28 | 1.17 / 1.05 |
| mimo-v2.6-pro | 0.435 / 0.87 | 3 / 6 | 0.97 |
| kimi-k3 | 3.00 / 15.00 | 20 / 100 | 1.01 |
| step-5-preview | 1.00 / 2.70 | 7 / 20 | 0.96 / 0.90 |
| qwen3.8-max | 2 / 6 | 12 / 36 | 1.12 |
| qwen3.8-flash | 0.15 / 0.47 | 0.8 / 2.7 | 1.26 / 1.17 |

缓存：
- DeepSeek 缓存命中 $0.006（低谷 $0.003），不收写入费。
- GLM 命中 $0.26，写入限时免费。
- MiMo 命中 $0.0036，写入限时免费。
- Kimi 命中 $0.30，写入 $3（5 分钟）/ $6（1 小时）。
- StepFun 命中 $0.10，写入费含在输入价里；中国站 2026-10-14 起命中价从 ¥0.35 调到 ¥0.70。
- 阿里只给百分比：显式缓存写入 125%、显式命中 10%、隐式命中 20%。

DeepSeek 高峰时段：周一到周五 UTC 01:00–04:00、06:00–10:00（中国节假日除外），其余时间低谷半价。

## 补充模型（2026-10-18）

用户查证，来源为官方定价页：
- GLM-5.3-FlashX：国际站 $0.37 / $1.25，缓存命中 $0.075，缓存存储限时免费（无结束日期），无阶梯（docs.z.ai/guides/overview/pricing）；中国站 ¥2 / ¥7，命中 ¥0.57。实测 `GLM-5.3-FlashX` 和 `glm-5.3-flashx` 都能调，上游返回小写名。
- kimi-k2.7-code：国际站 $0.95 / $4.00，缓存命中 $0.19，K2 系列无缓存写入费（platform.kimi.ai/pricing）；中国站 ¥6.50 / ¥27.00，命中 ¥1.30。只有思考模式。实测可调，流式 usage 需要 include_usage（网关会自动加）。
- kimi-k2.8：开放平台不提供按量计费，只在 Kimi Code 订阅里（model ID `kimi-for-coding`）。

### Moonshot 限流按累计充值分档（platform.moonshot.cn/docs/pricing/limits）

中国站 Tier0（¥0）只有 **RPM 3、并发 1**；累计充值 ¥50 到 Tier1（RPM 100、并发 15）。国际站同样 Tier0 RPM 3，Tier1 需要累计 $10。
2026-10-18 实测当前 Kimi 账号返回 "organization max RPM: 3"，处于 Tier0。团队同时用 Kimi 时会频繁 429。

## 协议全量实测（2026-10-19，灵枢冒烟测试）

用灵枢后台的冒烟测试接口（和网关同一套拼地址、鉴权、解析代码），流式请求：

| 渠道 | chat | messages | responses | 地址（messages 填到 `/v1/messages` 之前） |
|---|---|---|---|---|
| DeepSeek | ✅ | ✅ | ✅ | chat/responses `https://api.deepseek.com`，messages `https://api.deepseek.com/anthropic` |
| 智谱 | ✅ | ✅ | ✅ | chat `https://open.bigmodel.cn/api/paas/v4`，messages `https://open.bigmodel.cn/api/anthropic`，responses `https://open.bigmodel.cn/api/v1`（和 chat 的 base 不同）。必须用 2026-10-16 之后新建的标准 Key，见下 |
| MiMo | ✅ | ✅ | ✅ | chat/responses `https://api.xiaomimimo.com/v1`，messages `https://api.xiaomimimo.com/anthropic` |
| Kimi | ✅ | ✅ | ✅ | chat/responses `https://api.moonshot.cn/v1`，messages `https://api.moonshot.cn/anthropic`；kimi-k3 和 kimi-k2.7-code 都能走 responses（文档只写了 k3） |
| StepFun | ✅ | ✅ | ✅ | chat/responses `https://api.stepfun.com/v1`，messages `https://api.stepfun.com` |
| 阿里百炼 | ✅ | ✅ | ✅ | chat/responses `https://dashscope.aliyuncs.com/compatible-mode/v1`（responses 文档未写），messages `.../apps/anthropic` |

所有通过的组合，流式响应都带 usage。
Kimi 在 Tier0（并发 1）时并行探测会全部 429；2026-10-19 用户已充值升到 Tier1（并发 15、RPM 100）。冒烟测试改为最多同时 3 个请求。

### 智谱 Key 类型（2026-10-19）

用户查证官方说明（docs.bigmodel.cn/cn/coding-plan/notice/api-key-management）：`/api/anthropic` 和 `/api/v1` 是通用端点，`/api/coding/paas/v4` 才是套餐端点。
- 2026-10-16 之后新建的 Key：按 Key 类型计费，标准 Key 调任何端点都扣按量余额。
- 之前的老 Key（标"兼容"）：账号开过 Coding Plan 时，通用端点优先扣套餐额度，套餐到期直接失败、不会转按量。

实测：老 Key 调 messages / responses / 套餐 chat 都返回"Coding Plan 套餐已到期"，只有 `paas/v4` 正常；换成新的标准 Key 后，GLM-5.3 和 GLM-5.3-FlashX 三种协议 6 项全部通过、都带 usage。线上智谱渠道已换新 Key 并开通三种协议。

## MiniMax 和火山方舟（2026-10-20）

用户在线上用"自定义"建了两个渠道，灵枢冒烟测试三种协议全部通过、都带 usage：

| 渠道 | chat / responses | messages | 模型 |
|---|---|---|---|
| MiniMax（中国站） | `https://api.minimaxi.com/v1` | `https://api.minimax.cn/anthropic`（域名不同） | MiniMax-M3.1-Flash（2026-10-19 发布）；列表里还有 MiniMax-M3 等 |
| 火山方舟 | `https://ark.cn-beijing.volces.com/api/v3` | `https://ark.cn-beijing.volces.com/api/compatible` | doubao-seed-2-1-pro-260915 |

价格（用户查证，官方来源）：
- MiniMax-M3.1-Flash：国际站 $0.15 / $0.60，缓存读 $0.015，不分档（minimax.io/blog/minimax-m31f，2026-10-19）。中国站价格页尚未更新（媒体报道 ¥1 / ¥4 / ¥0.1）。按量模型名是 `MiniMax-M3.1-Flash`；`-Preview` 只在 M Plan 订阅里。MiniMax 会自动缓存服务端提示词，实测每次约 195 token 命中缓存。
- MiniMax-M3（对照）：国际站永久五折后 ≤512K $0.30 / $1.20、>512K 翻倍，有 1.5 倍的 Priority 档。**灵枢目前没有接 M3**（需要阶梯计价）。
- doubao-seed-2-1-pro：火山方舟在线推理 ¥6 / ¥30，缓存命中 ¥1.2，缓存存储 ¥0.017 / 百万 token / 小时；0–1024K 不分档（2.0-pro 是分档的）。BytePlus 国际站暂无 2.1-pro，国际站模型名通常带 `dola-` 前缀。灵枢按 ¥/6.7 换算：$0.895522 / $4.477612 / $0.179104；缓存存储费不计（按时长计费，灵枢按请求计费）。

## 腾讯混元 TokenHub（2026-10-21 实测）

- 中国站地址 `https://tokenhub.tencentmaas.com`：chat `/v1/chat/completions`、messages `/v1/messages`（Bearer 和 x-api-key 都认）、responses `/v1/responses`，三种协议流式都带用量。`/v1/models` 可用，同一账号还能调 GLM、DeepSeek、Kimi、MiniMax 等模型（模型 ID 和各家官方不完全一样，如 `deepseek/deepseek-flash`）。
- 新 key 第一次测试时 chat 和 messages 返回 402 `401006 The service ID does not exist, or the model does not match the service`，几分钟后重测全部通过，判断是开通延迟。
- hy4-preview（腾讯混元 Hy4 preview，2026-08-28 发布，1M 上下文）价格：中国站 ¥6 / ¥18 / 缓存命中 ¥0.3；国际站 $0.834 / $2.501 / $0.042（intl.cloud.tencent.com 文档 1300/78937，2026-09-25 更新）。用户查证提供。灵枢接的是中国站，站内价按中国站人民币价 ÷ 6.7 换算（用户决定，2026-10-21）：$0.895522 / $2.686567 / $0.044776。
