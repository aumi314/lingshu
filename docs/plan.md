# 灵枢 · Lingshu MVP 实施计划

状态：**已确认**（2026-10-18），当前进度见第 7 节
依据：`docs/decisions.md`（已定决策）、`docs/research/upstream-protocols.md`（上游实测）。
本文件只写"怎么做"；"做什么、为什么"以 `decisions.md` 为准，两者冲突时先改 `decisions.md`。

---

## 1. 整体结构

一个 Go 二进制 `lingshu`，一个进程，三类路由：

| 路径 | 用途 | 鉴权 |
|---|---|---|
| `/v1/chat/completions`、`/v1/messages`、`/v1/responses` | 网关透传 | API key（`Authorization: Bearer` 或 `x-api-key`） |
| `/api/...` | 管理端和用户端的 JSON 接口 | 登录会话（cookie） |
| `/` 其余路径 | 前端页面（构建产物嵌入二进制） | 前端自己判断登录状态 |

技术选择（细节默认值，有异议再改）：
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
│   ├── relay/              # 三个协议的透传：改写请求、转发、流式转发
│   │   └── usage/          # 各协议 usage 解析和统一口径
│   ├── router/             # 选渠道、重试、渠道冷却
│   ├── billing/            # 算钱、结算、准入检查
│   ├── auth/               # 登录会话、API key 校验
│   └── api/                # /api 下的管理端、用户端接口
├── web/                    # React 前端
├── scripts/                # 冒烟测试等脚本（读 .env）
├── deploy/                 # systemd unit、nginx 配置模板
└── docs/
```

## 3. 数据表

金额统一 `NUMERIC(20,10)`，单位 USD。价格单位是"每百万 token 的 USD"。

| 表 | 关键字段 | 说明 |
|---|---|---|
| `users` | username、password_hash、role（admin/user）、balance、enabled | 余额允许为负（小额透支） |
| `groups` | name、ratio、is_public | 公开分组所有人可用 |
| `user_groups` | user_id、group_id | 特殊分组的授权 |
| `models` | name（站内标准名，唯一）、input / output / cache_read / cache_write 价格、enabled | 价格只挂在这里 |
| `channels` | name、template、api_key、priority、enabled、base_url_chat / base_url_messages / base_url_responses | 填了哪个协议的地址，就算支持哪个协议 |
| `channel_groups` | channel_id、group_id | 渠道属于哪些分组 |
| `channel_models` | channel_id、model_id、upstream_name | 名称映射；upstream_name 为空表示和标准名相同 |
| `api_keys` | user_id、name、key_hash、key_prefix、group_id、quota_limit、quota_used、enabled、last_used_at | 只存哈希，完整 key 创建时只显示一次 |
| `request_logs` | 见下 | 一个客户端请求一行 |
| `balance_adjustments` | user_id、amount、balance_after、operator_id、note | 管理员改余额的流水 |
| `settings` | key、value | 汇率（CNY/USD）等 |

`request_logs` 字段：request_id、user_id、api_key_id、group_id、protocol、model、upstream_model、channel_id、stream、status（success / client_cancelled / upstream_error / rejected）、http_status、error、input / cache_read / cache_write / output / reasoning tokens、usage_estimated、价格与倍率快照、cost、attempts、首字节耗时、总耗时、upstream_response_id、created_at。

## 4. 关键实现规则

### 透传
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

- 流里每个字段取最后出现的值，不累加。
- 拿不到 usage：输入用请求体估算，输出用已转发内容估算，`usage_estimated = true`。估算规则：中日韩文字每字 1 token，其他字符每 4 字节 1 token（各家分词器都不是 tiktoken，引入它不会更准）。
- Moonshot 的 `prompt_tokens` 是否包含缓存写，阶段 2 用缓存场景实测确认。

### 计费
- 准入：用户余额 > 0、key 启用、key 未超限额、用户仍有权使用 key 所在分组，否则直接拒绝并提示原因。余额或额度不足返回 402（不用 429，否则 Claude Code 等客户端会反复重试）。
- 结算：cost = Σ(各项 token × 单价 / 1e6) × 分组倍率。一个请求只结算一次，在一个数据库事务里同时扣用户余额、加 key 的已用额度、写日志。
- 按 `decisions.md` 的流式规则：用户断开按已生成部分扣；上游出错或截断不扣；失败的尝试不扣。

### 路由
- 候选渠道 = 属于 key 所选分组 ∩ 有入口协议的地址 ∩ 有这个模型 ∩ 启用 ∩ 不在冷却中。
- 先用最高优先级一档，档内随机；失败后先试同档其他渠道，再往下一档。最多 3 次，总时限 60 秒。
- 只在还没给客户端发出第一个字节之前重试。
- 429 / 5xx / 超时 / 连接失败 / 401 / 403 → 重试；400 类 → 不重试，原样返回。
- 同一渠道连续失败 3 次 → 冷却 5 分钟。
- 带 `previous_response_id` 的请求：按日志里的 `upstream_response_id` 找到原渠道，只发往原渠道。

## 5. 分阶段

每个阶段结束都能实际跑起来验证，验证通过才进入下一阶段。

### 阶段 0：骨架
做：Go 模块、目录结构、配置读取、数据库迁移、`lingshu admin create` 子命令、`/healthz`；本地 Postgres 容器；`make dev / test / build`。
验证：`make test` 通过；服务启动后 `/healthz` 返回 200；数据库表全部建好。

### 阶段 1：透传跑通
做：三个入口的透传（单渠道，不重试）；API key 鉴权；usage 解析；写请求日志。渠道和模型先用 SQL 种子数据配置（管理界面在阶段 4）。
验证：
- 单元测试：用实测抓到的真实 SSE 数据做测试样本，覆盖 7 个模型的 usage 解析。
- 冒烟脚本 `scripts/smoke`：7 个模型，流式 / 非流式各调一次，日志里的 usage 和直连上游拿到的一致。
- Claude Code 用灵枢地址 + key 调 `qwen3.8-max`，能正常对话、能用工具。

### 阶段 2：计费
做：价格、分组倍率、结算、准入、key 限额、断开 / 上游出错规则、usage 估算兜底。
验证：
- 单元测试：算钱（含缓存）、余额透支、限额拒绝。
- 用模拟上游（`httptest`）测：中途断开、中途出错、没有 usage 三种情况下的扣费是否符合规则。
- 抽查日志：usage × 单价 × 倍率 = 实际扣费。

### 阶段 3：路由与重试
做：多渠道、优先级分档、档内随机、错误分类重试、渠道冷却、`previous_response_id` 粘渠道。
验证：模拟上游测 429 / 500 / 400 / 首字节后断流 / 冷却恢复；真实环境里故意把主渠道 key 填错，请求自动切到兜底渠道。

### 阶段 4：前端
做：
- 管理端：用户（建、改余额、授权分组）、分组、模型和价格、渠道（模板、拉取模型、名称映射、冒烟测试）、全站日志。
- 用户端：登录、余额、可用分组及模型和价格、key 管理、自己的日志（按时间、模型筛选，显示汇总金额）。

渠道模板预置 6 家的地址、协议和模型列表（智谱和阿里没有 `/models`，靠模板预置）。
验证：在浏览器里走完整流程：建用户 → 设余额 → 用模板建渠道 → 映射模型 → 冒烟测试 → 用户登录 → 建 key → 调用 → 看到日志和扣费。

### 阶段 5：部署与验收
做：腾讯云上建库建用户；本地编译后上传；systemd unit；Nginx 虚拟主机 + certbot 证书（备份现有配置，`nginx -t` 通过后再 reload；Nginx 对 `/v1/` 关闭缓冲）。
验证：HTTPS 正常；流式输出经过 Nginx 后不攒包；完成 `decisions.md` 里的 4 条 MVP 验收。

## 6. 项目代码约定（确认后写进 AGENTS.md）

- 目录按第 2 节，新增顶层目录要先更新本文件。
- 金额只用 decimal，禁止 float。
- 密钥只放 `.env` 或数据库，不进 git、不进日志、不打印。日志里的 key 只显示前缀。
- 改协议处理或计费逻辑，必须带测试；测试样本优先用真实抓到的上游数据。
- 每个阶段完成后，更新 `decisions.md`（如有新决策）和本文件的进度。

## 7. 进度

| 阶段 | 状态 |
|---|---|
| 0 骨架 | ✅ 完成（2026-10-18） |
| 1 透传 | ✅ 完成（2026-10-18） |
| 2 计费 | ✅ 完成（2026-10-18） |
| 3 路由与重试 | 下一步 |
| 4 前端 | 未开始 |
| 5 部署与验收 | 未开始 |

### 阶段 1 验收记录（2026-10-18）

- 单元测试：14 个真实上游样本的 usage 解析；9 个网关测试（模拟上游）覆盖模型名改写、未知字段保留、`include_usage` 代加与剥离、`?beta=true` 转发、换入口提示、上游错误原样返回、截断识别、客户端断开取消上游、`/v1/models`。
- 冒烟测试 `scripts/smoke.py`：7 个模型 × 流式/非流式，21 项全部通过，日志 usage 与客户端收到的一致。
- Claude Code（干净配置）经灵枢调 `qwen3.8-max`：工具调用正常；提示缓存生效（第 1 次写入 15825 token，第 2 次全部命中）。
- 实测发现并修复：Claude Code 可能同时带 `x-api-key` 和 `Authorization` 两个不同的 key，网关改为逐个尝试。
- 已知：Claude Code 不认识非 Claude 的模型名，会提示按 200k 上下文处理，可以用它文档里的环境变量设置上下文窗口；以后写接入说明时要提到。

### 阶段 2 验收记录（2026-10-18）

- 单元测试：算钱（含缓存读写、分组倍率、极小金额不丢精度）、准入（余额为 0 / 为负、key 额度用完）、估算量级。
- 网关测试（模拟上游）：成功扣余额并累加 key 额度；倍率生效；余额不足 / 额度用完 / 无分组权限被拒且不请求上游；透支一次后下一次被拒；上游 429、流截断、流里报错都不扣费；用户断开按估算扣费；上游没给 usage 时按估算扣费。
- 真实冒烟：21 项全部通过，每笔扣费 = 用量 × 价格快照 × 倍率，余额减少总额与日志扣费合计精确一致。
- 真实断开测试（Qwen、GLM）：记为 client_cancelled，按估算扣费。
- 已知局限：用户中途断开时，输入按请求内容估算，不含上游自己加的隐藏提示词，会偏低（实测 Qwen 估 14，完整请求上游报 68）。金额很小，先接受。
