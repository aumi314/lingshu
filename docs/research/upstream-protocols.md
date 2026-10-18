# 上游协议支持查证

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
