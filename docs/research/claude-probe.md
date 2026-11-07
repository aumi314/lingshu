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

## 五、官方对照（2026-11-07，OpenRouter 锁定 Anthropic）

OpenRouter 上 claude-opus-5.5 的服务方是 Anthropic、AWS Bedrock、Google Vertex、Azure、Claude Platform on AWS，都是官方部署，上下文 100 万 token。请求体加 `"provider": {"only": ["anthropic"], "allow_fallbacks": false}` 可锁定 Anthropic。它把响应改成自己的格式，只能对照模型行为（答案、推理量、指令），不能对照协议格式。**花费高（Opus 输出 $20 / 百万 token），用前先跟用户说明预计花费。**

用检测 12 原样的推理题和指令题重放（各 1 次，经 Chat 接口、`reasoning.effort: high`）：

| 题 | 官方 Opus 5.5 | kinght claude0.8 |
|---|---|---|
| 递推难题（答案 74） | 74，推理 10878 token | 590，推理 356 |
| 子集计数（答案 501） | 501，推理 2792 | 490，推理 2193 |
| 系统提示“只回 vvv” | vvv | 2+3 = 5 |
| 系统提示要求的结尾标记 | 有 | 有 |

官方两道难题都对、守住系统提示；kinght 都错，递推题推理量约为官方的 1/30，系统提示没守住，再加上拒绝约 10 万 token 的请求（检测 12）。判断：kinght 的 Opus 被降了推理强度或不是满血模型。每题只 1 次，样本不够改判定规则（推理答错仍记“需关注”），先记录；有更多官方样本后再把推理和指令层级的判定收紧。

更正：Opus 5.5 上下文是 100 万 token，检测 10（20 万）和检测 12（10 万）的大海捞针失败都是 kinght 拒绝长请求，不是超出模型上限。

## 六、kirisame 号池基准（2026-11-07）

用户朋友组的号池（魔改 sub2api，模拟云工作站的高频请求），渠道 23「kirisame-claude0.7」，恢复后测一次完整档和官方逐题对照。

| 题（检测 12 原题） | 官方 Opus（OpenRouter） | kirisame（2 次） | kinght（已停用） |
|---|---|---|---|
| 递推难题（答案 74） | 74，推理 10878 | 74、74，推理 7436、7548 | 590，推理 356 |
| 子集计数（答案 501） | 501，推理 2792 | 501、501，推理 3024、3827 | 490，推理 2193 |
| 系统提示“只回 vvv” | vvv | vvv | 2+3 = 5 |
| 结尾标记 | 有 | 有 | 有 |

完整档（检测 33）100 分：协议格式 12 / 12 符合官方；新出的两道难题都对（推理 6112、2103）；大海捞针约 10.3 万 token 三个编号全找到；基准请求 63 token 无注入；4 个会话一致无混池。首字约 12 秒、约 35 tok/s，比 kinght（7 秒、104 tok/s）慢。判断：目前唯一合格的 Claude 渠道；只抽样一次，需要隔段时间复测。路由暂不调整（它只在 Account Pool 分组，Third-party Relay 分组目前没有 Claude 渠道），用户说号池的事后面再谈。

## 七、官方 Claude 样本（2026-11-07，OpenRouter 锁定 Anthropic）

用 `scripts/probe/probe.py --protocol messages --provider anthropic`，请求形态和检测时的 `ProbeMessages` 相同，在 DMIT 上用渠道 12 的 key 采（key 只进环境变量）。花费约 $0.8（另有下午的逐题对照约 $0.3）。

- 分词：官方 Opus、官方 Sonnet、kirisame 三者的基准都是 42 token，12 段探针差值全部一致：分词只能分“是不是 Claude”，分不了 Opus / Sonnet；也证明 OpenRouter 报的用量可信。
- 单词指纹：Opus、Sonnet 各 195 题 × 6 次，0 错误。同家族区分作用 ≥ 0.3 的 84 道、≥ 0.5 的 67 道；例：中国菜 Opus 宫保鸡丁 / Sonnet 麻婆豆腐，运动 冰壶 / 羽毛球，美德 patience / humility。
- 难题（Opus，high）：recurrence_hard、lattice_hard、subset_mod、digit_sum 各 3 次全对；推理中位数约 981、973、746、373。题库固定种子的题偏容易：下午那道随机参数的递推题官方推理 10878、kirisame 6–7.5k，所以推理量规则只抓极端降配，主判据是答对没有。
- 这一轮是单次采样（每个型号 r1）；GPT 那边是两轮以上。以后有异常结论时，先补一轮官方样本再下结论。

