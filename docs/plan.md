# 灵枢 · Lingshu MVP 实施计划

状态：**已确认**（2026-10-18），当前进度见第 7 节。现役部署为 DMIT 洛杉矶服务器，入口 `https://gateway.piqode.com`；源码与构建在本机 WSL。标准渠道模板已发布 DMIT，详见 `docs/deploy.md`。模型可用性按“定价记录存在即配置完成”处理；schema 12、TypeSafe / Nace 仅显示 System One 协议地址及简化说明，以及新建渠道默认标准预设均已发布 DMIT。
多支付应用已发布 DMIT；EPay 已通过真实付款并开放。注册管理已发布 DMIT，线上数据库为 schema 14；注册默认仍关闭。实例级 API 接入地址设置已发布 DMIT。
模型图标（2026-10-24 ✅ 已发布 DMIT）：管理员定价和用户模型页复用厂商图标，按模型名称识别，已补齐 Grok 和 Muse；按 DMIT 现有 27 个模型检查，两张表均显示 27 个厂商图标。前端类型检查、构建及本地浏览器排版检查已通过。DMIT 程序哈希、健康检查和公网页面资源均已核对，发布凭证见 `docs/deploy.md`。
2026-10-25 项目资料整理：维护环境统一为本机 WSL 与 DMIT，部署说明集中到 `docs/deploy.md`，删除过期部署文件并更新引用。本次仅整理维护资料、注释和模板说明，无数据库或计费行为变更。
依据：`docs/decisions.md`（已定决策）、`docs/research/upstream-protocols.md`（上游实测）。

部署使用模式（2026-10-25）：系统设置显示自用、公司团队网关、商业中转；现阶段仅公司团队网关可选。设置持久化到现有 settings 表，并由管理接口校验。默认公司团队网关，无数据库迁移。
本文件只写"怎么做"；"做什么、为什么"以 `decisions.md` 为准，两者冲突时先改 `decisions.md`。

---

## 1. 整体结构

一个 Go 二进制 `lingshu`，一个进程，三类路由：

| 路径 | 用途 | 鉴权 |
|---|---|---|
| `/v1/chat/completions`、`/v1/messages`、`/v1/responses`、`/v1/systemone` | 网关透传 | API key（`Authorization: Bearer` 或 `x-api-key`） |
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
│   ├── relay/              # 四个协议的透传：改写请求、转发；聊天协议支持流式
│   │   └── usage/          # 各协议 usage 解析和统一口径
│   ├── router/             # 选渠道、重试、渠道冷却
│   ├── billing/            # 算钱、结算、准入检查
│   ├── payments/           # 五种支付适配、报价、官方验签和查单
│   ├── auth/               # 登录会话、API key 校验
│   └── api/                # /api 下的管理端、用户端接口
├── web/                    # React 前端
├── scripts/                # 冒烟测试等脚本（读 .env）
├── deploy/                 # DMIT 的 systemd、Caddy、PostgreSQL 容器和备份配置
└── docs/
```

## 3. 数据表

金额统一 `NUMERIC(20,10)`，单位 USD。价格单位是"每百万 token 的 USD"。

| 表 | 关键字段 | 说明 |
|---|---|---|
| `users` | username、password_hash、role（admin/user）、balance、enabled | 余额允许为负（小额透支）；角色只控制后台权限 |
| `groups` | name、ratio、enabled、is_public | 服务分组，决定倍率、渠道归属和可用范围 |
| `user_groups` | name、is_default | 用户分组，例如 default、internal |
| `user_group_members` | user_id、user_group_id | 用户所属的用户分组 |
| `group_user_groups` | group_id、user_group_id | 限定范围的服务分组允许使用的用户分组 |
| `models` | name（站内标准名，唯一）、input / output / cache_read / cache_write 价格、price_rules | 存在定价记录即可供已启用渠道映射调用，零价有效 |
| `channels` | name、template、api_key、priority、enabled、base_url_chat / base_url_messages / base_url_responses / base_url_systemone | 填了哪个协议的地址，就算支持哪个协议 |
| `channel_groups` | channel_id、group_id | 渠道属于哪些服务分组 |
| `channel_models` | channel_id、model_id、upstream_name | 名称映射；upstream_name 为空表示和标准名相同 |
| `api_keys` | user_id、name、key_hash、key_prefix、group_id、quota_limit、quota_used、enabled、last_used_at | 只存哈希，完整 key 创建时只显示一次 |
| `request_logs` | 见下 | 一个客户端请求一行 |
| `balance_adjustments` | user_id、amount、balance_after、operator_id、note、topup_order_id | 管理员调整及自动充值流水，订单关联唯一 |
| `payment_config_versions` / `payment_services` | provider、config、secrets / enabled、version_id | 不可变商户配置版本与当前服务开关 |
| `topup_orders` | user_id、provider、version_id、environment、credit_usd、amount、currency、unit_price、status | 报价、支付会话及到账状态 |
| `settings` | key、value | 显示汇率（CNY/USD）、用户充值开关、注册方式、实例级 API Base URL |

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
| systemone | `input_tokens` | — | — | `output_tokens` |

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
- 429 / 5xx / 超时 / 连接失败 / 404 → 重试并累计失败；401 / 402 / 403 → 重试并立刻冷却（key 失效或上游欠费）；其余 400 类 → 不重试，原样返回。
- 同一渠道连续失败 3 次 → 冷却 5 分钟。冷却中的渠道排到最后而不是剔除：全部渠道都在冷却时仍然要试。
- 等上游响应头的时限：流式 60 秒、非流式 5 分钟，超时换渠道。
- 流式请求先读到上游第一个事件再给客户端回响应头：第一个事件就是错误时还能换渠道。
- 上游错误响应里如果出现渠道 key，替换成 `***` 再返回给用户和写日志。
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
- 管理端：用户（建、角色、改余额、用户分组成员）、用户分组管理、服务分组（倍率、启停、可用用户范围）、模型和价格、渠道（模板、拉取模型、名称映射、冒烟测试）、全站日志。
- 用户端：登录、余额、可用分组及模型和价格、key 管理、自己的日志（按时间、模型筛选，显示汇总金额）。

内置渠道模板列出各上游的默认地址、协议和模型列表；没有 `/models` 的上游可以由模板预置模型，或由管理员手动输入。
验证：在浏览器里走完整流程：建用户 → 设余额 → 用模板建渠道 → 映射模型 → 冒烟测试 → 用户登录 → 建 key → 调用 → 看到日志和扣费。

### 阶段 5：部署与验收
做：本机 WSL 编译后上传 DMIT；PostgreSQL 容器；systemd 服务；Caddy HTTPS 入口。安装、发布、备份和恢复按 `docs/deploy.md` 执行。
验证：HTTPS 正常；流式输出不攒包；核对协议、用量、计费和备份恢复，验收记录见第 7 节。

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
| 3 路由与重试 | ✅ 完成（2026-10-18） |
| 4 前端 | ✅ 完成（2026-10-18） |
| 5 部署与验收 | ✅ DMIT 验收完成（2026-10-23） |

### 阶段 1 验收记录（2026-10-18）

- 单元测试：14 个真实上游样本的 usage 解析；9 个网关测试（模拟上游）覆盖模型名改写、未知字段保留、`include_usage` 代加与剥离、`?beta=true` 转发、换入口提示、上游错误原样返回、截断识别、客户端断开取消上游、`/v1/models`。
- 冒烟测试 `scripts/smoke.py`：7 个模型 × 流式/非流式，21 项全部通过，日志 usage 与客户端收到的一致。
- Claude Code（干净配置）经灵枢调 `qwen3.8-max`：工具调用正常；提示缓存生效（第 1 次写入 15825 token，第 2 次全部命中）。
- 实测发现并修复：Claude Code 可能同时带 `x-api-key` 和 `Authorization` 两个不同的 key，网关改为逐个尝试。
- 已知：Claude Code 不认识非 Claude 的模型名，会提示按 200k 上下文处理，可以用它文档里的环境变量设置上下文窗口；Codex 也会对非官方模型名采用默认参数。以后写接入说明时要提到。

### 阶段 2 验收记录（2026-10-18）

- 单元测试：算钱（含缓存读写、分组倍率、极小金额不丢精度）、准入（余额为 0 / 为负、key 额度用完）、估算量级。
- 网关测试（模拟上游）：成功扣余额并累加 key 额度；倍率生效；余额不足 / 额度用完 / 无分组权限被拒且不请求上游；透支一次后下一次被拒；上游 429、流截断、流里报错都不扣费；用户断开按估算扣费；上游没给 usage 时按估算扣费。
- 真实冒烟：21 项全部通过，每笔扣费 = 用量 × 价格快照 × 倍率，余额减少总额与日志扣费合计精确一致。
- 真实断开测试（Qwen、GLM）：记为 client_cancelled，按估算扣费。
- 已知局限：用户中途断开时，输入按请求内容估算，不含上游自己加的隐藏提示词，会偏低（实测 Qwen 估 14，完整请求上游报 68）。金额很小，先接受。

### 阶段 3 验收记录（2026-10-18）

- 单元测试：错误分类、冷却（连续 3 次、401 立刻、成功清零、到期恢复）、冷却渠道排到最后。
- 网关测试（模拟上游）：5xx 换到兜底渠道且兜底收到自己的上游模型名、只扣一次费；400 不重试；401 渠道只被请求 1 次就冷却；连续 3 次 503 后冷却；全部失败返回最后一个错误、日志记下每个渠道的原因、不扣费；第一个事件就是错误的流换渠道；发出内容后断流不重试；响应头超时换渠道；同优先级随机分流；`previous_response_id` 发回原渠道，原渠道停用时提示开新对话；上游回显的 key 被遮掉。
- 真实环境：加一个 key 填错、优先级更高的 DeepSeek 渠道，第 1 次请求 401 后自动切到正常渠道（attempts=2），后两次直接走正常渠道；用户三次都是 200。
- 重构后真实冒烟 21 项全部通过，余额与扣费一致。
- 顺带修复：阶段 1 的请求改写会修改原始请求，重试时会漏掉 usage 块的剥离，改为每次尝试从原始请求重新生成。

### 阶段 4 验收记录（2026-10-18）

- 后端 `/api`：登录会话（cookie，30 天，HttpOnly + SameSite=Lax，HTTPS 下自动加 Secure）、偏好、改密码、key 管理、模型价格、日志与汇总；管理端用户 / 余额（加减或直接设为，记流水）/ 授权分组 / 分组 / 模型 / 渠道（模板、拉取模型、名称映射、冒烟测试）/ 全站日志 / 汇率。9 个接口测试：登录与会话失效、偏好校验、改密码后其他设备下线、key 只显示一次与分组权限、普通用户访问管理接口 403、余额调整与流水、渠道 key 不出服务器、用户日志不含渠道信息、静态页面与缓存头。
- 前端：React + TypeScript + Vite + Ant Design 6，构建产物嵌入 Go 二进制（约 13MB）。用户端：概览（余额、今日 / 本月花费、接入地址）、API keys、模型与价格、账户日志（原“使用记录”）、设置（语言 中文/English、货币 美元/人民币、主题 跟随系统/浅色/深色，存在账号上）。管理端：用户、分组、模型、渠道、全站日志、系统设置。
- 无头浏览器验收：11 个页面无前端报错或废弃警告；端到端走完"建用户 → 设余额 $5 → 添加站内模型 → 自定义模板建渠道 → 拉取模型并映射 → 冒烟测试通过 → 新用户登录建 key → 用 key 调网关 → 日志可见、余额正确扣成 $4.9940"；切换英文 + 人民币 + 深色正常。
- 端到端使用本地模拟上游；真实上游的转发与计费已在阶段 1–3 验证。
- 已知：服务端返回的错误提示只有中文，英文界面下错误信息仍是中文。

### 阶段 5 DMIT 验收记录（2026-10-23）

- DMIT 洛杉矶部署：`lingshu.service`、`lingshu-db.service`、PostgreSQL 容器及备份 timer；Caddy 提供 `https://gateway.piqode.com`。公网 HTTPS、管理员登录、Secure / HttpOnly 会话 cookie、首页和构建资源验证通过。
- 9 个启用渠道、12 个模型映射，三种聊天协议共 **36/36** 个真实流式测试通过，均上报完整用量。
- 公网正式调用：Chat 流式、Messages 与 Responses 非流式均 HTTP 200。Chat 收到 259 个 SSE 事件和 `[DONE]`，首事件 0.415 秒，事件最大间隔 0.371 秒，作为本轮样本记录。
- 三笔日志按 token × 价格 × 分组倍率核对，总扣费 `0.0003729000 USD`，与测试账号余额减少、key 已用额度精确一致。测试用户与 key 已停用，会话已撤销，日志保留。
- 日常备份完成恢复演练，当时 schema 9 的 15 张表逐表行数一致。当前版本、schema、后续发布和备份记录统一见 [现役部署](deploy.md)。

## MVP 之后（2026-10-19 → 10-21）

团队正式使用前的加固和运营工具。决策细节见 `docs/decisions.md` 对应小节。

DeepSeek 峰谷配置复核（2026-10-23 ✅ DMIT 修正）：原生产配置只有周末谷价，已补齐夜间 18:00–09:00 和午间 12:00–14:00 两条谷价；周一至周五仅 09:00–12:00、14:00–18:00 为高峰，周末全天低谷。基础价与谷价均保持原值。管理接口及库内回读为三条，计费规则边界测试通过，无需发布程序。备份及修改记录见 [现役部署](deploy.md)。

系统设置标签页（2026-10-23，✅ 已发布 DMIT）：复用定价页的顶部标签交互，运营设置 / 支付设置分开切换，默认运营设置，URL `?tab=payments` 支持直达和刷新保留；支付配置按需加载，切换保留表单。页面使用与定价页一致的内容宽度。前端类型检查/构建、中英文、默认标签、切换与刷新、375px 布局检查通过。无后端或数据库改动，保留现有支付配置。

多支付服务（2026-10-23，✅ DMIT 应用已发布；DMIT EPay 真实付款验收通过）：五种适配、支付配置与版本、USD 充值订单、原子入账及唯一流水、钱包报价和分页记录、管理员验收入口已实现。新增 schema 13；旧库余额和流水保留，重复迁移通过。独立库完整测试、支付并发 race 检测、前端类型检查和构建通过。协议模拟覆盖五家支付创建、错误响应及签名、金额/币种、环境、税费、重复通知；数据库和接口覆盖并发扣费、事务回滚、超时、提前通知、查单补齐、旧凭据和关闭后到账。Pancake 目录读取和显式创建/发布商品均有测试。DMIT 用现有 aumif 完成 EPay CNY 6.70 → USD 1 的真实付款，通知自动入账且唯一流水、重复核对不重复加钱，已开放 DMIT 充值；其余四种未做真实付款。使用及限制见 [支付接入](payments.md)，发布和真实验收状态见 [现役部署](deploy.md)。

EPay 环境选择（2026-10-24 ✅ 发布到 DMIT）：EPay 表单固定显示“生产”，保存和验证请求均提交生产环境；后端拒绝 EPay 测试环境配置，其他支付服务的测试环境和管理员“测试支付”验收入口保留。`make test`、支付/API 测试及前端构建通过；无数据库迁移，未改商户配置。该次发布程序 SHA-256 为 `2eb1ccb6f5266fe8dc1685fe30dd92a39f44020f3620edde1004e876d27fdffa`，DMIT 服务、公网健康检查及前端资源均通过，现役版本与发布步骤见 [现役部署](deploy.md)。

注册管理（2026-10-24，✅ 已发布 DMIT）：系统设置增加手动添加、注册码注册和暂未开放的邮箱注册选项。管理员可批量生成 1–100 个一次性注册码、查看状态并撤销未用码；明文只在生成时返回，数据库仅存哈希。注册码注册会创建普通账号、加入 default 用户分组并自动登录，核销与账号、会话创建在同一事务内；手动模式关闭自助注册。新增 schema 14，邮箱注册仍不可用。全量后端测试、前端类型检查与构建、Go 程序构建通过；DMIT 公网注册状态为关闭，现役运行基线见 [现役部署](deploy.md)。

System One 接入（2026-10-22 本地完成，2026-10-23 ✅ 发布到 DMIT）：迁移 `0009` 新增独立渠道地址，加入 TypeSafe / Nace 模板、`/v1/systemone` 透传与用量解析、模型拉取和非流式冒烟测试；管理端及用户端展示协议入口。全量 Go 测试、前端类型检查和 `make build` 通过（构建使用本机已有的 WSL Node 22.22.3）。已验证真实响应样本、鉴权和失败不扣费、529 重试与只结算一次、完整 / 缺失用量、渠道配置读写；两家真实上游的 `noul` / `choice` / `score` 请求经本地最终构建成功，日志金额与输入单价精确一致。2026-10-23 DMIT 发布后 schema 为第 9 版；模板与入口已上线，未自动创建生产渠道。浏览器检查模板切换、地址预填、渠道测试和用户模型页通过。详情及复验步骤见 [System One 接入记录](research/systemone.md)。

标准渠道模板（2026-10-23 ✅ 发布到 DMIT）：新增一个根地址输入，自动生成 Chat、Messages、Responses 三个 Base URL；同一地址展开逻辑供保存、冒烟测试和模型拉取使用，编辑可还原根地址，转为自定义时保留展开地址。四项根地址单测、全量 Go 测试、前端类型检查和构建通过。DMIT 用渠道 15 已保存的 key 对 `api.kinght.top` 三个完整 URL 实际请求均 HTTP 200。发布前备份，DMIT 健康检查和公网资源均通过；无数据库迁移。现役版本与运维步骤见 [现役部署记录](deploy.md)。

TypeSafe / Nace 模板显示（2026-10-23 ✅ 发布到 DMIT）：模板选择器说明只显示 `System One`；协议地址区只显示 System One 地址行，隐藏空的 Chat、Messages、Responses 行；通用切换提示也不显示。其他模板和后端协议处理不变。`go test ./internal/api`、前端类型检查、前端构建和后端构建通过；无数据库迁移。现役版本与运维步骤见 [现役部署记录](deploy.md)。

渠道冒烟测试交互（2026-10-23 ✅ 发布到 DMIT）：点击“测试”先选协议，按模型单测或批量测试所选协议下的全部模型；弹窗展示单项结果。每次最多批量 20 个模型，后端并发限制保持 3。前端构建和 `go test ./internal/api` 通过；无数据库迁移。现役版本与运维步骤见 [现役部署记录](deploy.md)。

系统设置板块（2026-10-23 ✅ 发布到 DMIT）：运营设置提供“显示汇率”；支付设置提供默认关闭的“开放用户充值”开关，保存到现有 settings 表，五种支付接入进度见本节开头。汇率和开关可独立保存，兼容旧客户端只提交汇率。独立测试库运行 `make test`、前端类型检查与构建、程序构建通过；DMIT 用现有 aumif 账户验证设置接口返回汇率 6.7、充值关闭，临时验证会话已删除。无数据库迁移。现役版本与运维步骤见 [现役部署记录](deploy.md)。

API 接入地址（2026-10-24 ✅ 已发布 DMIT）：运营设置可编辑当前部署的完整 API Base URL；默认由该实例 `LINGSHU_PUBLIC_URL` 加 `/v1` 得出，保存后写入现有 settings。API keys 与概览页面从用户接口读取同一值并生成各协议地址；支付回调仍使用独立的 `LINGSHU_PUBLIC_URL` 根地址。DMIT 显示 `https://gateway.piqode.com/v1`。现役版本与运维步骤见 [现役部署记录](deploy.md)。

用户钱包入口及消费统计（2026-10-24 ✅ 发布到 DMIT）：用户区第五项新增“钱包充值”，页面顶部并排展示当前余额和累计消费金额，累计金额按当前用户全部请求日志汇总，移动端上下排列；沿用货币偏好和显示汇率，普通用户与管理员均可访问。进入时刷新余额和累计消费。全量 Go 测试、前端类型检查和构建通过；累计汇总测试确认覆盖所有分页并按用户隔离。无 schema 变化；现役版本与运维步骤见 [现役部署记录](deploy.md)。

| 项 | 状态 | 说明 |
|---|---|---|
| 登录防护 | ✅ | 按用户名 / IP 计数，失败后工作量证明人机验证，再多锁 15 分钟；新密码至少 12 位；失败写服务日志 |
| 渠道页 | ✅ | 厂商图标、去掉 key 列、ID 列、拖拽排序、余额估算、标准多协议根地址模板 |
| 手机端 | ✅ | 侧边菜单收进抽屉、表格左右滑动、卡片两列 |
| 余额估算 | ✅ | 每个渠道对应一个上游账户；按上游成本扣估算余额；充值、校准、流水；DeepSeek / Kimi 每小时自动校准 |
| 标志 | ✅ | 玉琮俯视（`docs/design/`） |
| 定价页 | ✅ | 模型定价 + 分组设置合一；"渠道负责拿到算力，定价负责怎么分发" |
| 价格规则 | ✅ | 按输入长度、生效日期、周末和 UTC+8 时段 |
| 美国模型 | ✅ | Claude、GPT、Gemini 4 Argon、Grok 4.7、Muse Wonder 共 11 个，支持经 OpenRouter 接入；Claude / GPT 长上下文阶梯已配，后三个待查 |
| 渠道代理 | ✅ | 每个渠道可单独配 socks5 / http 代理 |
| 告警通知 | 待做 | 服务宕机、上游余额低于提醒线、用量异常 → 邮件 |
| 接入说明 | 待做 | 建 key、Claude Code / Codex / Cherry Studio 配置、常见坑 |
| 操作记录 | 待做 | 谁在什么时候改了价格、开关、渠道、余额（kimi-k2.7-code 被停用查不到是谁） |
| 角色和用户分组拆分 | ✅ | 角色只管后台权限；用户分组控制服务分组访问。旧逐用户授权迁移为用户分组规则；服务分组支持启停及所有用户 / 指定用户分组范围 |


### 用户编辑与改名（2026-10-23）

用户表右侧统一“编辑”入口；编辑窗口支持修改用户名、留空不变的密码重置、用户分组和单独确认的额度调整，余额流水保留。PATCH `/api/admin/users/{id}` 支持用户名和用户分组更新，服务端清理用户名首尾空白、拒绝空用户名，重复用户名返回 HTTP 409；用户资料、密码和用户分组在同一数据库事务更新。无数据库迁移。前端类型检查/构建和 Linux amd64 程序构建通过；本轮未运行测试套件。

用户启停与流水隔离（2026-10-25 ✅ 已发布 DMIT）：列表显示状态标签；编辑弹窗中资料、启停状态与可选额度调整统一由右下角“提交”保存，取消不生效，提交停用需确认，不能停用自己。PATCH `/api/admin/users/{id}` 新增可选 `balance_adjustment`（mode 为 add/set、amount 为十进制字符串、currency 为 USD/CNY、note 为备注）；不传则不调余额，人民币由后端 decimal 换算。资料、余额、流水与会话失效在同一事务内处理，设为余额按锁定后的实际余额算差额，零差额不记流水。保留流水查询的请求隔离、加载、空记录和失败重试。无数据库迁移。独立测试库 `make test`、前端类型检查与构建通过；回归覆盖统一保存、失败回滚、币种换算、留空与零额、重名校验、并发扣减，以及浏览器中的取消、确认、失败保留草稿、中英文和手机布局。浏览器脚本 `web/tests/users.browser.cjs` 需在可解析 playwright 包且已安装 Chromium 的 Node 环境运行，会自行启停本地 Vite，也可通过 `USERS_TEST_URL` 指定已有本地服务。未修改线上账号状态；现役发布凭证见 `docs/deploy.md`。

Anthropic Platform 官方模板（2026-10-24 ✅ 已发布 DMIT）：新增 Anthropic 官方 API 预设，根地址为 `https://api.anthropic.com`，只提供 Messages 协议和官方模型列表获取。当前没有创建渠道或保存 API key；管理员选用模板后再自行填写凭据和映射模型。无数据库迁移。

Google Gemini 官方渠道（2026-10-24 ✅ 已发布 DMIT）：新增 Google AI Studio 预设，走 Gemini OpenAI 兼容 Chat Completions，支持模型列表。DMIT 新增启用渠道 `Google AI Studio`，映射站内零价模型 `gemini-3.8-flash`（仅 Chat、Official API 分组）。无数据库迁移。

### 统一账户日志（2026-10-25 ✅ 已发布 DMIT）

全站日志与用户“账户日志”统一查询调用、在线充值及额度调整，复用原请求表、余额流水及订单，在数据库合并分页。type 支持 all/request/topup/adjustment；不传保持旧接口语义。独立 total 控制分页，消费和 tokens 仅统计调用，充值到账和额度净调整独立计算。普通用户强制限定自身并隐藏上游信息，订单只返回摘要。历史零额与极小金额保留，详情提高精度。无数据库迁移，也不修改扣费、入账事务。独立测试库 make test、类型检查、前端构建及管理员中文／成员英文手机浏览器回归通过；测试覆盖分页、分类、隔离、重复通知、订单脱敏及延迟响应。浏览器脚本为 web/tests/activity-logs.browser.cjs，运行条件同用户编辑回归脚本。

统一日志现役提交、备份及只读登录验收见 `docs/deploy.md`；未制造生产充值或余额调整，验证会话已清理。
