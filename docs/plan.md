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
│   ├── relay/              # 四个协议的透传及 Messages / Responses 双向转接
│   │   ├── bridge/         # Responses → Messages 转换和事件状态机
│   │   ├── messagesbridge/ # Messages → Responses 转换与事件状态机
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
| `groups` | name、ratio、enabled、is_public、retry_count、is_collection | 服务分组，决定倍率、渠道归属、可用范围和失败重试次数；集合不挂渠道，按模型走成员分组 |
| `collection_groups` | collection_id、group_id | 集合包含哪些普通分组 |
| `user_groups` | name、is_default | 用户分组，例如 default、internal |
| `user_group_members` | user_id、user_group_id | 用户所属的用户分组 |
| `group_user_groups` | group_id、user_group_id | 限定范围的服务分组允许使用的用户分组 |
| `models` | name（站内标准名，唯一）、input / output / cache_read / cache_write 价格、price_rules、fast_multiplier | 存在定价记录即可供已启用渠道映射调用，零价有效 |
| `channels` | name、template、api_key、priority、enabled、base_url_chat / base_url_messages / base_url_responses / base_url_systemone | 地址表示上游原生能力；Messages 与 Responses 地址互相提供转接 |
| `channel_groups` | channel_id、group_id | 渠道属于哪些服务分组 |
| `channel_models` | channel_id、model_id、upstream_name | 名称映射；upstream_name 为空表示和标准名相同 |
| `api_keys` | user_id、name、key_hash、key_prefix、group_id、quota_limit、quota_used、enabled、expires_at、last_used_at | 只存哈希，完整 key 创建时只显示一次 |
| `request_logs` | 见下 | 一个客户端请求一行 |
| `projects` / `project_members` / `project_groups` | 池子余额与冻结、每周上限、邀请码；成员与审批状态；开放分组 | 项目额度：项目 key（`api_keys.project_id`）从项目池扣，日志记 `project_id` |
| `request_captures` / `conversation_blobs` | 骨架、回复 / 去重的内容块 | 对话留存：每次调用一条骨架，消息和 system、tools 按 SHA-256 去重存块（gzip） |
| `balance_adjustments` | user_id、amount、balance_after、operator_id、note、topup_order_id | 管理员调整及自动充值流水，订单关联唯一 |
| `payment_config_versions` / `payment_services` | provider、config、secrets / enabled、version_id | 不可变商户配置版本与当前服务开关 |
| `topup_orders` | user_id、provider、version_id、environment、credit_usd、amount、currency、unit_price、status | 报价、支付会话及到账状态 |
| `sessions` | token_hash、user_id、expires_at | 登录会话，只存 token 的 SHA-256 |
| `registration_codes` / `redeem_codes` | code_hash、使用 / 撤销时间 | 注册码、兑换码，明文只在生成时显示 |
| `operation_logs` | 操作人、受影响用户、动作、对象快照、改前 / 改后 | 操作日志，与业务改动同事务写入 |
| `probe_runs` | channel_id、model、baseline、preset、status、report | 模型检测记录 |
| `settings` | key、value | 汇率及模式、充值开关、注册方式、API Base URL、部署模式、预扣与限流参数等 |

`request_logs` 字段：request_id、user_id、api_key_id、group_id、protocol、upstream_protocol、operation、conversion_diagnostics、model、upstream_model、channel_id、stream、status（success / client_cancelled / upstream_error / rejected）、http_status、error、input / cache_read / cache_write / output / reasoning tokens、usage_estimated、价格与倍率快照、cost、attempts、首字节耗时、总耗时、读体与发往上游时刻、upstream_response_id、client_ip、reasoning_effort、service_tier、failed_channel_ids、channel_key_id、failed_key_ids、upstream_header_ms（上游响应头到达时刻）、group_name 快照（集合记“集合 › 成员”）、created_at。

## 4. 关键实现规则

### 透传与转接
- Responses 与 Messages 可以双向直接转换；两方向映射规则分别见 `decisions.md` 对应小节；压缩和 WebSocket 能力以 Responses 入口为准。下列原样保留规则用于原生透传。
- 请求体只改三处：`model` 换成渠道的上游名；chat 流式请求加 `stream_options.include_usage: true`；模型没开放 Fast 时去掉 `service_tier`。都是计费正确的前提，所以不做“请求透传”开关。其余字段原样保留，包括网关不认识的字段（用原始 JSON 改写，不先反序列化成结构体）。
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
- 候选渠道 = 属于 key 所选分组（集合按模型取第一个提供它的成员分组）∩ 有可服务入口的地址（Messages / Responses 互相转接，各受调用设置里的开关控制）∩ 有这个模型 ∩ 启用；冷却中的渠道排在后面。
- 先用最高优先级一档，档内按会话固定渠道（渠道亲和）；失败后先试同档其他渠道，再往下一档。渠道级失败重试次数按服务分组设置（0–4，默认 2），换 key 不占次数；总时限 2 分钟。
- 只在还没给客户端发出第一个字节之前重试。
- 429 / 5xx / 超时 / 连接失败 / 404 → 重试并累计失败；401 / 402 / 403 → 重试并立刻冷却（key 失效或上游欠费）；其余 400 类 → 不重试，原样返回。
- 同一渠道连续失败 3 次 → 冷却 5 分钟。冷却中的渠道排到最后而不是剔除：全部渠道都在冷却时仍然要试。
- 等上游响应头的时限：流式 30 秒（2026-11-11 由 60 秒缩短）、非流式 5 分钟，超时换渠道。
- 流式请求先读到上游第一个事件再给客户端回响应头：第一个事件就是错误时还能换渠道。
- 上游错误响应里如果出现渠道 key，替换成 `***` 再返回给用户和写日志。
- 带 `previous_response_id` 的请求：按日志里的 `upstream_response_id` 找到原渠道，只发往原渠道。

## 5. 当前状态（2026-11-11）

线上 `https://gateway.piqode.com`（DMIT）当前应用 `eaa6b55`（2026-11-11）。近期上线：服务分组集合（#72）、转接兼容默认 Codex / Claude Code 并补提示缓存（#73，Claude Code → GPT 已真实验收，第二轮约八成输入命中缓存）、调用设置标签与渠道亲和（#74）、API key 过期时间（#76）、上游卡死时换渠道（#78 重试总时限 2 分钟、#79 等响应头 30 秒）。Codex → Claude 转接的真实调用和缓存还没验收：只有 Messages 地址的 Claude 渠道（kirisame 号池授权过期、Anthropic Platform 余额不足）当时都不可用。

| 模块 | 现在有什么 |
|---|---|
| 网关 | chat / messages / responses / systemone 四个入口透传，Messages / Responses 双向转接（应急开关在系统设置「调用设置」）；优先级分档、渠道亲和、按分组次数换渠道重试、冷却；预扣费、按用户的并发和频率限制；GPT Fast 档位计费 |
| 计费 | 模型四项单价 + 价格规则（长上下文、日期、时段、周末）× 分组倍率（集合按实际成员的倍率）；汇率手动或实时（Frankfurter） |
| 渠道 | 标准模板与各厂商官方模板（含 OpenRouter、Gemini、Anthropic、TypeSafe / Nace、OpenCode Zen、NVIDIA）、渠道代理、按需余额查询、24 小时连接状态、冒烟测试 ；一个渠道多把 key（上限 50，会话固定、限流 / 失效自动换 key）、渠道备注；模型映射识别厂商前缀和版本号分隔符（待 PR 合并发布） |
| 用户 | 超级管理员 / 管理员 / 成员；用户分组控制服务分组可见范围；注册码注册；API key 可选过期时间与点击行编辑、用户编辑右侧抽屉（PR #76 已发布）；登录防护（频率限制 + 工作量证明） |
| 钱包 | 五家支付适配（EPay 已真实开放）、兑换码独立开关（同步钱包入口与兑换接口）、用 API key 查余额（`/v1/usage`） |
| 日志 | 调用日志（耗时拆分、IP、推理强度、换渠道失败记录）+ 操作日志（同事务写入、改前改后对比）；对话留存按成功写入字节记录、读取失败可重试、下载包含截断标记（待 PR 合并） |
| 模型检测 | 管理端页面，快速 / 完整两档（完整档含大海捞针），4 个会话抓号池混用；GPT 和官方样本比（gpt-6.1-sol、gpt-6-astra、gpt-6.1-terra、gpt-6-luna，Codex 环境）；claude-opus-5-5 和官方样本比（OpenRouter 锁定 Anthropic 采的 Opus / Sonnet）并查协议 |
| 运维 | CI + 合并即自动发布（破坏性迁移暂停待确认）、发布前备份 + 每日分层备份、`main` 分支保护（Rulesets「分支保护」，2026-11-07） |

## 6. 下一步

不再区分使用模式，功能都是可开关的组件（2026-11-11，见 `decisions.md`「核心加组件，不分模式」）。候选方向，顺序和范围待和用户对齐后写进 `decisions.md`：

- **模型检测完善**
  - 鹈鹕只作参考、不进页面检测（xhigh 单次也只有约三分之一真实蹬踏，见 `research/model-probe.md`）。
  - 第三方检测站（hvoy、ztest、cctest）的抓包在 `references/probe-sites/`，结论见 `research/model-probe.md`。借鉴项还剩：更逼真的 Claude Code / Codex 伪装、Claude 服务端工具（web_search、code_execution）和图片输入。数字序列指纹已评估，分不清具体型号，不接入（见 `research/model-probe.md`）。
  - 知识时效题库：提示词要求直接回答、禁止联网搜索、禁止参考对话；用官方账号采集四个型号的样本。
  - 官方 API 环境（无 Codex 注入提示词）的样本：luna 在 API 环境下距离 0.44，贴近阈值。
  - 更可靠的推理强度降级检测（现有 low / high 对比只作参考）。新增候选项“Juice”：官方 Codex 一律拒答，回答了就说明不是官方环境（咔咔 3/3 回答 48），见 `research/model-probe.md`。
  - Claude 官方样本已采（Opus、Sonnet 各一轮，OpenRouter 锁定 Anthropic）；需要时补第二轮；Sonnet 单独检测、知识时效题未做。
- **对话留存**：第一版已做（系统设置「调用设置」开关，默认关；管理员在全站日志请求详情里看和下载）。后续：按会话浏览、成员看自己的、批量导出、保留期限与清理、密钥打码、告知用户。
- **项目额度**：已做（规则见 `decisions.md`「项目额度」），组件默认关闭，在「系统设置 → 主设置 → 项目」开启，「用户」里逐个打开“允许创建项目”；成员侧边栏「我的项目」。后续候选：成员单人上限、余额不足提醒、按项目看调用日志。研究见 `research/team-gateway.md`。
- **自有号池**：现用 sub2api 反代质量不够；作为灵枢的兄弟项目单独做，不急，灵枢只把它当渠道接。
- **Claude 渠道**：kirisame（渠道 23，Account Pool）2026-11-11 返回 `OAuth refresh failed`（号池授权过期，待朋友处理）；Anthropic Platform 余额不足；现役是 Relay-claude 分组的超超 claude（检测 67 完整档 94 分）。任一只有 Messages 地址的 Claude 渠道恢复后，补做 Codex → Claude 转接的真实调用和缓存命中验收。
- **等响应头时限复核**：2026-11-11 起日志记 `upstream_header_ms`，多数中转要等首字才回响应头（上线首半小时中位数 7.3 秒）。几天后看被判超时、换渠道后成功的比例，误伤明显就把 30 秒调到 45 秒。
- **运维缺口**：异地备份、告警通知（见 `decisions.md`「待定」）。
