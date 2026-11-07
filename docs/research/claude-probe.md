# Claude 检测思路

目标和 GPT 一样：发现中转“掺水”（换模型、降推理、注入内容、协议转换）。现状：灵枢的模型检测只有 GPT 官方样本，Claude 还不能检测；本机也没有官方 Claude 对照组（Magpie 里只有 `codex/*`）。本文是 2026-11-07 抓包 hvoy 检测 claude-opus-5-5 后整理的思路，做法和取舍待实现时再定进 `decisions.md`。

## 一、hvoy 怎么测

一次检测 5 个请求，全部走 `/v1/messages`，伪装成 Claude Code：`User-Agent: claude-cli/…`、`x-app: cli`、Claude Code 的 beta 头；系统提示只有计费头和一句 “You are Claude Code, Anthropic's official CLI for Claude.”；`metadata.user_id` 里的 device / account / session 每次随机；`thinking: {type: adaptive, display: omitted}`、`output_config.effort: xhigh`、`tools: []`。伪装的原因：Claude 号池类中转只接受像 Claude Code 的请求，不伪装测不到真实链路。

| 请求 | 内容 | 每次是否变化 |
|---|---|---|
| 知识时效 | 4 道 2025 年 8–10 月新闻题，“序号\|答案”格式 | 从题库抽；格式示例 `1\|Alaska` 本身就是其中一题的答案 |
| PDF 读取 | 附 base64 PDF（文字 “Hvoy AI report total <随机数>”），要求只返回文字 | 数字随机 |
| 乘法 JSON | 两位数乘法，按固定 JSON 返回 | 数字随机 |
| 尖括号校验串 | 原样输出 `<随机串>`，不解释 | 串随机 |
| 自报知识截止 | 只回 `YYYY-MM` | 固定（两次都答 `2025-01`） |

报告给出 10 项（截图），大部分不是单独发题，而是从上面 5 个响应里检查出来的。对应关系是按响应内容推断的，未经 hvoy 证实：

| 报告项 | 推断的依据 |
|---|---|
| 身份一致性 | 自报截止、回答风格是否像 Claude（不确定） |
| 模型签名验证 | 响应里有 thinking 块和 `signature`（只有 Anthropic 后端能生成） |
| 模型一致性 | `message_start.message.model` 等于请求的模型 |
| 知识准确度 | 知识题答对几道 |
| 模型特性 | 自报截止 / adaptive thinking、`thinking_tokens` 等 Claude 独有行为（不确定） |
| PDF 文档识别 | PDF 里的随机数读对没有 |
| 结构化输出 | 乘法 JSON 格式和结果 |
| 协议规范性 | SSE 事件顺序：`message_start` → `content_block_start/delta/stop` → `message_delta` → `message_stop`，中间有 `ping` |
| 响应完整性 | `stop_reason`、`usage` 齐全，流正常结束 |
| 消息标识规范 | 消息编号格式 `msg_01…`（实测 `msg_011Cg…`） |

## 二、官方响应长什么样（kinght claude0.8，判定正常）

- 消息编号 `msg_011CgXGAYlRsQKtZrK78QU0F` 这类格式；`model` 原样是 `claude-opus-5-5`。
- 第一个内容块是 `thinking`，`display: omitted` 时 `thinking` 为空，只给签名。
- `usage` 带新字段：`cache_creation.ephemeral_5m_input_tokens`、`output_tokens_details.thinking_tokens`，有时有 `iterations`；`message_delta` 带 `stop_details` 和 `context_management.applied_edits`。
- 输入 token 很小（只发一句话时 82），说明这条渠道没有注入提示词。

这些新字段是协议转换的试金石：把别家模型或 OpenAI 格式转成 Anthropic 格式的中转（new-api 一类）通常只拼出基本字段，编号、thinking 块、新 usage 字段对不上。

## 三、我们怎么做（建议）

**不需要官方样本、可以先做的（协议层，计分）**

1. 协议规范：事件顺序、`ping`、消息编号格式、`model` 一致、`stop_reason`、usage 新字段齐全。
2. thinking 块：开 adaptive thinking 时必须有 thinking 块和非空签名；推理强度高的题 `thinking_tokens` 不能是 0（对应 GPT 那边“推理强度被降”的检查）。
3. **签名回传验证（比 hvoy 强，待实测）**：第二轮把第一轮的 thinking 块原样带回去。官方 API 会校验签名，伪造或被改过的签名会报错；转发真 Claude 的中转能通过，自己拼签名的过不了。还要实测：签名不同的中转之间、Bedrock / Vertex 与官方是否互认。
4. PDF document 原样转发、尖括号内容原样输出、注入检测（极短请求的输入 token 基准）——和 GPT 那边的“请求质量”同一套。

**需要官方 Claude 对照组的（后做）**

- 单词指纹：GPT 上已经证明能分型号；Claude 要用官方账号对 Opus / Sonnet / Haiku 建样本，才能抓“Sonnet 冒充 Opus”。
- 分词指纹：Anthropic 分词器和 OpenAI 不同，探针文本的 token 差值能直接区分“是不是 Claude”，同样要官方样本定基准。
- 知识时效：题目贴近各型号的知识截止期，不能带示例答案。

**不采用**

- 自报知识截止：模型自报经常不准（Opus 5.5 答 `2025-01`，却能答出 2025 年 10 月的事），只能当参考。
- 固定的格式示例里夹答案、逐字固定的题目：hvoy 的这两个问题不重复。

## 四、待定

- 官方 Claude 对照组从哪来：Claude 订阅账号（经 Magpie 之类本机网关）还是 Anthropic API key，需要用户提供。没有它，只能做第三节第一部分。
- 签名回传验证的实测结果。
