# 灵枢 · Lingshu 现状与计划

一页看懂项目：怎么搭的（第 1–4 节）、现在做到哪（第 5 节）、接下来做什么（第 6 节）。
“做什么、为什么”以 `docs/decisions.md` 为准，两者冲突时先改 `decisions.md`。完成的工作在第 5 节只留一句话，验收细节和发布证据追加到 `docs/history.md`，这份文件不再写流水账。

---

## 1. 整体结构

一个 Go 二进制 `lingshu`，一个进程，三类路由（另有 `/v1/usage` 供工具用 API key 查余额）：

| 路径 | 用途 | 鉴权 |
|---|---|---|
| `/v1/chat/completions`、`/v1/messages`、`/v1/responses`、`/v1/systemone` | 网关透传 | API key（`Authorization: Bearer` 或 `x-api-key`） |
| `/api/...` | 管理端和用户端的 JSON 接口 | 登录会话（cookie） |
| `/` 其余路径 | 前端页面（构建产物嵌入二进制） | 前端自己判断登录状态 |

技术选择：
- Go 标准库 `net/http` 做路由；`pgx` 连 PostgreSQL；`shopspring/decimal` 算钱；`bcrypt` 存密码。依赖越少越好。
- 数据库迁移：SQL 文件嵌入二进制，启动时按顺序执行。
- 前端：React + TypeScript + Vite + Ant Design（管理后台以表格、表单为主，Ant Design 现成组件最全）。
- 本地开发库：单独起一个 `lingshu-postgres` 容器（端口 55432），不碰本机已有的 lobe 数据库。镜像 `postgres:17-alpine`。

## 2. 代码目录

```text
lingshu/
├── cmd/lingshu/            # main：启动服务、建管理员等子命令
├── internal/
│   ├── config/             # 读环境变量
│   ├── store/              # 数据库访问 + migrations/*.sql
│   ├── relay/              # 四个协议的透传及 Responses → Messages 转接
│   │   ├── bridge/         # Responses → Messages 转换和事件状态机
│   │   └── usage/          # 各协议 usage 解析和统一口径
│   ├── router/             # 选渠道、重试、渠道冷却
│   ├── probe/              # 模型检测：检测题和官方样本（data/，编译进程序）、出题、评判
│   ├── billing/            # 算钱、结算、准入检查
│   ├── payments/           # 五种支付适配、报价、官方验签和查单
│   ├── auth/               # 登录会话、API key 校验
│   └── api/                # /api 下的管理端、用户端接口
├── web/                    # React 前端
├── scripts/                # 冒烟测试等脚本（读 .env）
│   └── probe/              # 模型检测本机脚本 probe.py、鹈鹕腿部测量 legs.js（检测题和官方样本在 internal/probe/data/）
├── deploy/                 # DMIT 的 systemd、Caddy、PostgreSQL 容器和备份配置
└── docs/
```

## 3. 数据表

金额统一 `NUMERIC(20,10)`，单位 USD。价格单位是"每百万 token 的 USD"。下表只列核心表和关键字段，完整结构以 `internal/store/migrations/` 为准。

| 表 | 关键字段 | 说明 |
|---|---|---|
| `users` | username、password_hash、role（super_admin / admin / user）、balance、reserved（预扣冻结）、enabled、note、preferences | 余额允许为负（小额透支）；角色只控制后台权限 |
| `groups` | name、ratio、enabled、is_public | 服务分组，决定倍率、渠道归属和可用范围 |
| `user_groups` | name、is_default | 用户分组，例如 default、internal |
| `user_group_members` | user_id、user_group_id | 用户所属的用户分组 |
| `group_user_groups` | group_id、user_group_id | 限定范围的服务分组允许使用的用户分组 |
| `models` | name（站内标准名，唯一）、input / output / cache_read / cache_write 价格、price_rules、fast_multiplier | 存在定价记录即可供已启用渠道映射调用，零价有效 |
| `channels` | name、template、api_key、priority、enabled、base_url_chat / base_url_messages / base_url_responses / base_url_systemone | 地址表示上游原生能力；Messages 地址同时提供 Responses 转接 |
| `channel_groups` | channel_id、group_id | 渠道属于哪些服务分组 |
| `channel_models` | channel_id、model_id、upstream_name | 名称映射；upstream_name 为空表示和标准名相同 |
| `api_keys` | user_id、name、key_hash、key_prefix、group_id、quota_limit、quota_used、enabled、last_used_at | 只存哈希，完整 key 创建时只显示一次 |
| `request_logs` | 见下 | 一个客户端请求一行 |
| `balance_adjustments` | user_id、amount、balance_after、operator_id、note、topup_order_id | 管理员调整及自动充值流水，订单关联唯一 |
| `payment_config_versions` / `payment_services` | provider、config、secrets / enabled、version_id | 不可变商户配置版本与当前服务开关 |
| `topup_orders` | user_id、provider、version_id、environment、credit_usd、amount、currency、unit_price、status | 报价、支付会话及到账状态 |
| `sessions` | token_hash、user_id、expires_at | 登录会话，只存 token 的 SHA-256 |
| `registration_codes` / `redeem_codes` | code_hash、使用 / 撤销时间 | 注册码、兑换码，明文只在生成时显示 |
| `operation_logs` | 操作人、受影响用户、动作、对象快照、改前 / 改后 | 操作日志，与业务改动同事务写入 |
| `probe_runs` | channel_id、model、baseline、preset、status、report | 模型检测记录 |
| `settings` | key、value | 汇率及模式、充值开关、注册方式、API Base URL、部署模式、预扣与限流参数等 |

`request_logs` 字段：request_id、user_id、api_key_id、group_id、protocol、upstream_protocol、operation、conversion_diagnostics、model、upstream_model、channel_id、stream、status（success / client_cancelled / upstream_error / rejected）、http_status、error、input / cache_read / cache_write / output / reasoning tokens、usage_estimated、价格与倍率快照、cost、attempts、首字节耗时、总耗时、读体与发往上游时刻、upstream_response_id、client_ip、reasoning_effort、service_tier、failed_channel_ids、group_name 快照、created_at。

## 4. 关键实现规则

### 透传与转接
- Responses 可直接转到 Messages；具体工具、思考、压缩和 WebSocket 规则见 `decisions.md`「Responses → Messages」。下列原样保留规则用于原生透传。
- 请求体只改两处：`model` 换成渠道的上游名；chat 流式请求加 `stream_options.include_usage: true`。其余字段原样保留，包括网关不认识的字段（用原始 JSON 改写，不先反序列化成结构体）。
- 请求头：换掉鉴权头，`User-Agent`、`anthropic-version`、`anthropic-beta` 等原样转发。
- 流式：收到上游一个事件就立刻转发并 flush，不攒包。
- 客户端没要 `include_usage`、是网关加上的：转发时去掉"`choices` 为空且只带 usage"的那一块。
- 出错时按入口协议的格式返回错误（OpenAI 格式或 Anthropic 格式），用户的 SDK 才能正常解析。

### usage 统一口径
| 入口协议 | 输入（不含缓存） | 缓存读 | 缓存写 | 输出 |
|---|---|---|---|---|
| chat | `prompt_tokens` − 缓存读 − 缓存写 | `prompt_tokens_details.cached_tokens` | `prompt_tokens_details.cache_write_tokens`（Moonshot） | `completion_tokens` |
| messages | `input_tokens` | `cache_read_input_tokens` | `cache_creation_input_tokens` | `output_tokens` |
| responses | `input_tokens` − `cached_tokens` | `input_tokens_details.cached_tokens` | — | `output_tokens` |
| systemone | `input_tokens` | — | — | `output_tokens` |

- 流里每个字段取最后出现的值，不累加。
- 拿不到 usage：输入用请求体估算，输出用已转发内容估算，`usage_estimated = true`。估算规则：中日韩文字每字 1 token，其他字符每 4 字节 1 token（各家分词器都不是 tiktoken，引入它不会更准）。

### 计费
- 准入：可用余额（余额 − 冻结）够预扣、key 启用、key 未超限额、用户仍有权使用 key 所在分组、未超并发和频率限制，否则直接拒绝并提示原因（规则见 `decisions.md`「预扣费与调用限制」）。余额或额度不足返回 402（不用 429，否则 Claude Code 等客户端会反复重试）。
- 结算：cost = Σ(各项 token × 单价 / 1e6) × 分组倍率。一个请求只结算一次，在一个数据库事务里同时扣用户余额、加 key 的已用额度、写日志。
- 按 `decisions.md` 的流式规则：用户断开按已生成部分扣；上游出错或截断不扣；失败的尝试不扣。

### 路由
- 候选渠道 = 属于 key 所选分组 ∩ 有可服务入口的地址（Responses 包括 Messages 转接）∩ 有这个模型 ∩ 启用；冷却中的渠道排在后面。
- 先用最高优先级一档，档内随机；失败后先试同档其他渠道，再往下一档。最多 3 次，总时限 60 秒。
- 只在还没给客户端发出第一个字节之前重试。
- 429 / 5xx / 超时 / 连接失败 / 404 → 重试并累计失败；401 / 402 / 403 → 重试并立刻冷却（key 失效或上游欠费）；其余 400 类 → 不重试，原样返回。
- 同一渠道连续失败 3 次 → 冷却 5 分钟。冷却中的渠道排到最后而不是剔除：全部渠道都在冷却时仍然要试。
- 等上游响应头的时限：流式 60 秒、非流式 5 分钟，超时换渠道。
- 流式请求先读到上游第一个事件再给客户端回响应头：第一个事件就是错误时还能换渠道。
- 上游错误响应里如果出现渠道 key，替换成 `***` 再返回给用户和写日志。
- 带 `previous_response_id` 的请求：按日志里的 `upstream_response_id` 找到原渠道，只发往原渠道。

## 5. 当前状态（2026-11-09）

线上 `https://gateway.piqode.com`（DMIT）已支持开放注册及协议同意，用户列表、渠道协议标签、日志刷新、Agent 配置提示词和模型检测并发 5 个已发布，Responses → Messages 转接（PR #58）与首轮安全加固（PR #63）也已合并发布（最新应用 `22e1f60`，2026-11-09 11:06，含引用内容下载超时换渠道修复；Caddy 站点防护，02:13），转接已通过 Codex 工具、压缩及续接验收；渠道列表的协议列表头改回“协议”，转接标签改成更弱的视觉层级（PR 待合并）。

| 模块 | 现在有什么 |
|---|---|
| 网关 | chat / messages / responses / systemone 四个入口透传；优先级分档、换渠道重试、冷却；预扣费、按用户的并发和频率限制；GPT Fast 档位计费 |
| 计费 | 模型四项单价 + 价格规则（长上下文、日期、时段、周末）× 分组倍率；汇率手动或实时（Frankfurter） |
| 渠道 | 标准模板与各厂商官方模板（含 OpenRouter、Gemini、Anthropic、TypeSafe / Nace、OpenCode Zen、NVIDIA）、渠道代理、按需余额查询、24 小时连接状态、冒烟测试 ；一个渠道多把 key（上限 50，会话固定、限流 / 失效自动换 key）、渠道备注 |
| 用户 | 超级管理员 / 管理员 / 成员；用户分组控制服务分组可见范围；注册码注册；登录防护（频率限制 + 工作量证明） |
| 钱包 | 五家支付适配（EPay 已真实开放）、兑换码、用 API key 查余额（`/v1/usage`） |
| 日志 | 调用日志（耗时拆分、IP、推理强度、换渠道失败记录）+ 操作日志（同事务写入、改前改后对比） |
| 模型检测 | 管理端页面，快速 / 完整两档（完整档含大海捞针），4 个会话抓号池混用；GPT 和官方样本比（gpt-6.1-sol、gpt-6-astra、gpt-6.1-terra、gpt-6-luna，Codex 环境）；claude-opus-5-5 和官方样本比（OpenRouter 锁定 Anthropic 采的 Opus / Sonnet）并查协议 |
| 运维 | CI + 合并即自动发布（破坏性迁移暂停待确认）、发布前备份 + 每日分层备份、`main` 分支保护（Rulesets「分支保护」，2026-11-07） |

## 6. 下一步

商业中转模式告一段落（2026-11-07 用户决定）。候选方向，顺序和范围待和用户对齐后写进 `decisions.md`：

- **模型检测完善**
  - 鹈鹕只作参考、不进页面检测（xhigh 单次也只有约三分之一真实蹬踏，见 `research/model-probe.md`）。
  - 多抓几轮 hvoy（`references/hvoy/`），对照着补全我们自己的检测维度；题目仍用自己的、随机抽。
  - 知识时效题库：提示词要求直接回答、禁止联网搜索、禁止参考对话；用官方账号采集四个型号的样本。
  - 官方 API 环境（无 Codex 注入提示词）的样本：luna 在 API 环境下距离 0.44，贴近阈值。
  - 更可靠的推理强度降级检测（现有 low / high 对比只作参考）。新增候选项“Juice”：官方 Codex 一律拒答，回答了就说明不是官方环境（咔咔 3/3 回答 48），见 `research/model-probe.md`。
  - Claude 官方样本已采（Opus、Sonnet 各一轮，OpenRouter 锁定 Anthropic）；需要时补第二轮；Sonnet 单独检测、知识时效题未做。
- **公司团队网关**：例如按组共用金额池，按开源通用场景设计，细节后面再议。
- **自有号池**：现用 sub2api 反代质量不够；作为灵枢的兄弟项目单独做，不急，灵枢只把它当渠道接。
- **Claude 渠道**：kirisame（渠道 23，Account Pool 分组）测出和官方满血 Opus 一致，是目前唯一合格的 Claude 渠道，但较慢、只测过一次；Third-party Relay 分组没有 Claude 渠道。分组和路由怎么配，等用户谈号池时定。
- **GPT 兜底**：只有 ai8 一个渠道，出问题没有自动切换；找到质量合格的第二个渠道后挂低一档。
- **运维缺口**：异地备份、告警通知（见 `decisions.md`「待定」）。
