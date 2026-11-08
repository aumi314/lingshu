# 历史记录

已完成工作的验收记录、发布证据和被取代的旧决策，原文搬运，供追溯。日常开发不需要读；现状看 `docs/plan.md`，现行决策看 `docs/decisions.md`，运维看 `docs/deploy.md`。

---

## 一、开发与验收记录（原 `docs/plan.md`）

### 原文件头的状态摘要

状态：**已确认**（2026-10-18），当前进度见第 7 节。现役部署为 DMIT 洛杉矶服务器，入口 `https://gateway.piqode.com`；源码与构建在本机 WSL。标准渠道模板已发布 DMIT，详见 `docs/deploy.md`。模型可用性按“定价记录存在即配置完成”处理；schema 12、TypeSafe / Nace 仅显示 System One 协议地址及简化说明，以及新建渠道默认标准预设均已发布 DMIT。
多支付应用已发布 DMIT；EPay 已通过真实付款并开放。注册管理已发布 DMIT，线上数据库为 schema 14；注册默认仍关闭。实例级 API 接入地址设置已发布 DMIT。
模型图标（2026-10-24 ✅ 已发布 DMIT）：管理员定价和用户模型页复用厂商图标，按模型名称识别，已补齐 Grok 和 Muse；按 DMIT 现有 27 个模型检查，两张表均显示 27 个厂商图标。前端类型检查、构建及本地浏览器排版检查已通过。DMIT 程序哈希、健康检查和公网页面资源均已核对，发布凭证见 `docs/deploy.md`。
2026-10-25 项目资料整理：维护环境统一为本机 WSL 与 DMIT，部署说明集中到 `docs/deploy.md`，删除过期部署文件并更新引用。本次仅整理维护资料、注释和模板说明，无数据库或计费行为变更。
依据：`docs/decisions.md`（已定决策）、`docs/research/upstream-protocols.md`（上游实测）。

部署使用模式（2026-10-26 ✅ 已发布）：系统设置显示自用、公司团队网关、商业中转；现阶段仅公司团队网关可选。设置持久化到现有 settings 表，并由管理接口校验。默认公司团队网关，无数据库迁移。前后端构建、发布程序哈希、公网健康页和新前端资源核对通过；DMIT 发布证据见 `docs/deploy.md`。
管理列表 ID（2026-10-26 ✅ 已发布）：用户和服务分组列表已增加与渠道页一致的 ID 列，三处表头统一为 `ID`，显示真实记录 ID。类型检查、前端及程序构建通过；DMIT 登录后页面、显示值与接口 ID 一致性、运行程序及公网资源哈希已核对。仅前端展示，无接口或数据库变更；发布凭证见 `docs/deploy.md`。

### 5. 分阶段

每个阶段结束都能实际跑起来验证，验证通过才进入下一阶段。

#### 阶段 0：骨架
做：Go 模块、目录结构、配置读取、数据库迁移、`lingshu admin create` 子命令、`/healthz`；本地 Postgres 容器；`make dev / test / build`。
验证：`make test` 通过；服务启动后 `/healthz` 返回 200；数据库表全部建好。

#### 阶段 1：透传跑通
做：三个入口的透传（单渠道，不重试）；API key 鉴权；usage 解析；写请求日志。渠道和模型先用 SQL 种子数据配置（管理界面在阶段 4）。
验证：
- 单元测试：用实测抓到的真实 SSE 数据做测试样本，覆盖 7 个模型的 usage 解析。
- 冒烟脚本 `scripts/smoke`：7 个模型，流式 / 非流式各调一次，日志里的 usage 和直连上游拿到的一致。
- Claude Code 用灵枢地址 + key 调 `qwen3.8-max`，能正常对话、能用工具。

#### 阶段 2：计费
做：价格、分组倍率、结算、准入、key 限额、断开 / 上游出错规则、usage 估算兜底。
验证：
- 单元测试：算钱（含缓存）、余额透支、限额拒绝。
- 用模拟上游（`httptest`）测：中途断开、中途出错、没有 usage 三种情况下的扣费是否符合规则。
- 抽查日志：usage × 单价 × 倍率 = 实际扣费。

#### 阶段 3：路由与重试
做：多渠道、优先级分档、档内随机、错误分类重试、渠道冷却、`previous_response_id` 粘渠道。
验证：模拟上游测 429 / 500 / 400 / 首字节后断流 / 冷却恢复；真实环境里故意把主渠道 key 填错，请求自动切到兜底渠道。

#### 阶段 4：前端
做：
- 管理端：用户（建、角色、改余额、用户分组成员）、用户分组管理、服务分组（倍率、启停、可用用户范围）、模型和价格、渠道（模板、拉取模型、名称映射、冒烟测试）、全站日志。
- 用户端：登录、余额、可用分组及模型和价格、key 管理、自己的日志（按时间、模型筛选，显示汇总金额）。

内置渠道模板列出各上游的默认地址、协议和模型列表；没有 `/models` 的上游可以由模板预置模型，或由管理员手动输入。
验证：在浏览器里走完整流程：建用户 → 设余额 → 用模板建渠道 → 映射模型 → 冒烟测试 → 用户登录 → 建 key → 调用 → 看到日志和扣费。

#### 阶段 5：部署与验收
做：本机 WSL 编译后上传 DMIT；PostgreSQL 容器；systemd 服务；Caddy HTTPS 入口。安装、发布、备份和恢复按 `docs/deploy.md` 执行。
验证：HTTPS 正常；流式输出不攒包；核对协议、用量、计费和备份恢复，验收记录见第 7 节。

### 6. 项目代码约定（确认后写进 AGENTS.md）

- 目录按第 2 节，新增顶层目录要先更新本文件。
- 金额只用 decimal，禁止 float。
- 密钥只放 `.env` 或数据库，不进 git、不进日志、不打印。日志里的 key 只显示前缀。
- 改协议处理或计费逻辑，必须带测试；测试样本优先用真实抓到的上游数据。
- 每个阶段完成后，更新 `decisions.md`（如有新决策）和本文件的进度。

### 7. 进度

| 阶段 | 状态 |
|---|---|
| 0 骨架 | ✅ 完成（2026-10-18） |
| 1 透传 | ✅ 完成（2026-10-18） |
| 2 计费 | ✅ 完成（2026-10-18） |
| 3 路由与重试 | ✅ 完成（2026-10-18） |
| 4 前端 | ✅ 完成（2026-10-18） |
| 5 部署与验收 | ✅ DMIT 验收完成（2026-10-23） |

#### 阶段 1 验收记录（2026-10-18）

- 单元测试：14 个真实上游样本的 usage 解析；9 个网关测试（模拟上游）覆盖模型名改写、未知字段保留、`include_usage` 代加与剥离、`?beta=true` 转发、换入口提示、上游错误原样返回、截断识别、客户端断开取消上游、`/v1/models`。
- 冒烟测试 `scripts/smoke.py`：7 个模型 × 流式/非流式，21 项全部通过，日志 usage 与客户端收到的一致。
- Claude Code（干净配置）经灵枢调 `qwen3.8-max`：工具调用正常；提示缓存生效（第 1 次写入 15825 token，第 2 次全部命中）。
- 实测发现并修复：Claude Code 可能同时带 `x-api-key` 和 `Authorization` 两个不同的 key，网关改为逐个尝试。
- 已知：Claude Code 不认识非 Claude 的模型名，会提示按 200k 上下文处理，可以用它文档里的环境变量设置上下文窗口；Codex 也会对非官方模型名采用默认参数。以后写接入说明时要提到。

#### 阶段 2 验收记录（2026-10-18）

- 单元测试：算钱（含缓存读写、分组倍率、极小金额不丢精度）、准入（余额为 0 / 为负、key 额度用完）、估算量级。
- 网关测试（模拟上游）：成功扣余额并累加 key 额度；倍率生效；余额不足 / 额度用完 / 无分组权限被拒且不请求上游；透支一次后下一次被拒；上游 429、流截断、流里报错都不扣费；用户断开按估算扣费；上游没给 usage 时按估算扣费。
- 真实冒烟：21 项全部通过，每笔扣费 = 用量 × 价格快照 × 倍率，余额减少总额与日志扣费合计精确一致。
- 真实断开测试（Qwen、GLM）：记为 client_cancelled，按估算扣费。
- 已知局限：用户中途断开时，输入按请求内容估算，不含上游自己加的隐藏提示词，会偏低（实测 Qwen 估 14，完整请求上游报 68）。金额很小，先接受。

#### 阶段 3 验收记录（2026-10-18）

- 单元测试：错误分类、冷却（连续 3 次、401 立刻、成功清零、到期恢复）、冷却渠道排到最后。
- 网关测试（模拟上游）：5xx 换到兜底渠道且兜底收到自己的上游模型名、只扣一次费；400 不重试；401 渠道只被请求 1 次就冷却；连续 3 次 503 后冷却；全部失败返回最后一个错误、日志记下每个渠道的原因、不扣费；第一个事件就是错误的流换渠道；发出内容后断流不重试；响应头超时换渠道；同优先级随机分流；`previous_response_id` 发回原渠道，原渠道停用时提示开新对话；上游回显的 key 被遮掉。
- 真实环境：加一个 key 填错、优先级更高的 DeepSeek 渠道，第 1 次请求 401 后自动切到正常渠道（attempts=2），后两次直接走正常渠道；用户三次都是 200。
- 重构后真实冒烟 21 项全部通过，余额与扣费一致。
- 顺带修复：阶段 1 的请求改写会修改原始请求，重试时会漏掉 usage 块的剥离，改为每次尝试从原始请求重新生成。

#### 阶段 4 验收记录（2026-10-18）

- 后端 `/api`：登录会话（cookie，30 天，HttpOnly + SameSite=Lax，HTTPS 下自动加 Secure）、偏好、改密码、key 管理、模型价格、日志与汇总；管理端用户 / 余额（加减或直接设为，记流水）/ 授权分组 / 分组 / 模型 / 渠道（模板、拉取模型、名称映射、冒烟测试）/ 全站日志 / 汇率。9 个接口测试：登录与会话失效、偏好校验、改密码后其他设备下线、key 只显示一次与分组权限、普通用户访问管理接口 403、余额调整与流水、渠道 key 不出服务器、用户日志不含渠道信息、静态页面与缓存头。
- 前端：React + TypeScript + Vite + Ant Design 6，构建产物嵌入 Go 二进制（约 13MB）。用户端：概览（余额、今日 / 本月花费、接入地址）、API keys、模型与价格、账户日志（原“使用记录”）、设置（语言 中文/English、货币 美元/人民币、主题 跟随系统/浅色/深色，存在账号上）。管理端：用户、分组、模型、渠道、全站日志、系统设置。
- 无头浏览器验收：11 个页面无前端报错或废弃警告；端到端走完"建用户 → 设余额 $5 → 添加站内模型 → 自定义模板建渠道 → 拉取模型并映射 → 冒烟测试通过 → 新用户登录建 key → 用 key 调网关 → 日志可见、余额正确扣成 $4.9940"；切换英文 + 人民币 + 深色正常。
- 端到端使用本地模拟上游；真实上游的转发与计费已在阶段 1–3 验证。
- 已知：服务端返回的错误提示只有中文，英文界面下错误信息仍是中文。

#### 阶段 5 DMIT 验收记录（2026-10-23）

- DMIT 洛杉矶部署：`lingshu.service`、`lingshu-db.service`、PostgreSQL 容器及备份 timer；Caddy 提供 `https://gateway.piqode.com`。公网 HTTPS、管理员登录、Secure / HttpOnly 会话 cookie、首页和构建资源验证通过。
- 9 个启用渠道、12 个模型映射，三种聊天协议共 **36/36** 个真实流式测试通过，均上报完整用量。
- 公网正式调用：Chat 流式、Messages 与 Responses 非流式均 HTTP 200。Chat 收到 259 个 SSE 事件和 `[DONE]`，首事件 0.415 秒，事件最大间隔 0.371 秒，作为本轮样本记录。
- 三笔日志按 token × 价格 × 分组倍率核对，总扣费 `0.0003729000 USD`，与测试账号余额减少、key 已用额度精确一致。测试用户与 key 已停用，会话已撤销，日志保留。
- 日常备份完成恢复演练，当时 schema 9 的 15 张表逐表行数一致。当前版本、schema、后续发布和备份记录统一见 [现役部署](deploy.md)。

### MVP 之后（2026-10-19 → 10-21）

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

用户备注与弹窗精简（2026-11-01 ✅ 已发布 DMIT）：用户编辑弹窗在密码下方新增「用户备注」（迁移 `0016_user_note` 给 `users` 加 `note` 列，上限 500 字，服务端去空白，空串清空），只由管理端用户列表接口返回，`/api/me` 不含，成员看不到。额度调整区的「备注」改名「额度备注」，并去掉弹窗内的「最近调整」流水列表（流水照旧写库，仍可在账户日志与全站日志查看，接口保留）。`GOFLAGS=-count=1 make test` 全绿（新增 `TestUserNote` 覆盖写入、去空白、清空、超长拒绝和不泄漏给成员），前端类型检查与构建、Go 程序构建通过；迁移在本地库重复执行确认幂等。已发布 DMIT，线上 schema 升到 `0016_user_note`，公网首页与入口资源哈希与干净构建一致，发布凭证见 [现役部署](deploy.md)。

上游余额查询（2026-10-27 ✅ 已发布 DMIT）：取消上游余额估算，渠道列表「额度」列改为按需查询。删除成本系数、充值 / 校准流水、每小时自动校准和逐请求上游成本；迁移 0015 删除 `upstream_accounts`、`upstream_account_entries` 两表及 `channels.upstream_account_id`、`request_logs.upstream_account_id`、`request_logs.upstream_cost`，渠道新增 `balance_detail` / `balance_checked_at` / `balance_error`。查询支持 DeepSeek、Moonshot、OpenRouter 三个模板，结果按厂商原始币种保存，不做汇率换算；失败保留上一次的数字并记错误。全量 Go 测试、前端类型检查与构建、本地浏览器检查（列表、弹窗、手机宽度）通过；DMIT 迁移到 `0015_channel_balance`，生产实测三个渠道查询成功，发布凭证见 [现役部署](deploy.md)。

| 项 | 状态 | 说明 |
|---|---|---|
| 登录防护 | ✅ | 按用户名 / IP 计数，失败后工作量证明人机验证，再多锁 15 分钟；新密码至少 12 位；失败写服务日志 |
| 渠道页 | ✅ | 厂商图标、去掉 key 列、ID 列、拖拽排序、额度查询、标准多协议根地址模板 |
| 手机端 | ✅ | 侧边菜单收进抽屉、表格左右滑动、卡片两列 |
| 余额查询 | ✅ | 点开渠道额度按需查一次；DeepSeek、Moonshot、OpenRouter 有接口，其余显示「—」；只显示，不参与计费 |
| 标志 | ✅ | 玉琮俯视（`docs/design/`） |
| 定价页 | ✅ | 模型定价 + 分组设置合一；"渠道负责拿到算力，定价负责怎么分发" |
| 价格规则 | ✅ | 按输入长度、生效日期、周末和 UTC+8 时段 |
| 美国模型 | ✅ | Claude、GPT、Gemini 4 Argon、Grok 4.7、Muse Wonder 共 11 个，支持经 OpenRouter 接入；Claude / GPT 长上下文阶梯已配，后三个待查 |
| 渠道代理 | ✅ | 每个渠道可单独配 socks5 / http 代理 |
| 告警通知 | 待做 | 服务宕机、上游余额低于提醒线、用量异常 → 邮件 |
| 接入说明 | 待做 | 建 key、Claude Code / Codex / Cherry Studio 配置、常见坑 |
| 操作记录 | 待做 | 谁在什么时候改了价格、开关、渠道、余额（kimi-k2.7-code 被停用查不到是谁） |
| 角色和用户分组拆分 | ✅ | 角色只管后台权限；用户分组控制服务分组访问。旧逐用户授权迁移为用户分组规则；服务分组支持启停及所有用户 / 指定用户分组范围 |


#### 用户编辑与改名（2026-10-23）

用户表右侧统一“编辑”入口；编辑窗口支持修改用户名、留空不变的密码重置、用户分组和单独确认的额度调整，余额流水保留。PATCH `/api/admin/users/{id}` 支持用户名和用户分组更新，服务端清理用户名首尾空白、拒绝空用户名，重复用户名返回 HTTP 409；用户资料、密码和用户分组在同一数据库事务更新。无数据库迁移。前端类型检查/构建和 Linux amd64 程序构建通过；本轮未运行测试套件。

用户启停与流水隔离（2026-10-25 ✅ 已发布 DMIT）：列表显示状态标签；编辑弹窗中资料、启停状态与可选额度调整统一由右下角“提交”保存，取消不生效，提交停用需确认，不能停用自己。PATCH `/api/admin/users/{id}` 新增可选 `balance_adjustment`（mode 为 add/set、amount 为十进制字符串、currency 为 USD/CNY、note 为备注）；不传则不调余额，人民币由后端 decimal 换算。资料、余额、流水与会话失效在同一事务内处理，设为余额按锁定后的实际余额算差额，零差额不记流水。保留流水查询的请求隔离、加载、空记录和失败重试。无数据库迁移。独立测试库 `make test`、前端类型检查与构建通过；回归覆盖统一保存、失败回滚、币种换算、留空与零额、重名校验、并发扣减，以及浏览器中的取消、确认、失败保留草稿、中英文和手机布局。浏览器脚本 `web/tests/users.browser.cjs` 需在可解析 playwright 包且已安装 Chromium 的 Node 环境运行，会自行启停本地 Vite，也可通过 `USERS_TEST_URL` 指定已有本地服务。未修改线上账号状态；现役发布凭证见 `docs/deploy.md`。

Anthropic Platform 官方模板（2026-10-24 ✅ 已发布 DMIT）：新增 Anthropic 官方 API 预设，根地址为 `https://api.anthropic.com`，只提供 Messages 协议和官方模型列表获取。当前没有创建渠道或保存 API key；管理员选用模板后再自行填写凭据和映射模型。无数据库迁移。

Google Gemini 官方渠道（2026-10-24 ✅ 已发布 DMIT）：新增 Google AI Studio 预设，走 Gemini OpenAI 兼容 Chat Completions，支持模型列表。DMIT 新增启用渠道 `Google AI Studio`，映射站内零价模型 `gemini-3.8-flash`（仅 Chat、Official API 分组）。无数据库迁移。

#### 统一账户日志（2026-10-25 ✅ 已发布 DMIT）

全站日志与用户“账户日志”统一查询调用、在线充值及额度调整，复用原请求表、余额流水及订单，在数据库合并分页。type 支持 all/request/topup/adjustment；不传保持旧接口语义。独立 total 控制分页，消费和 tokens 仅统计调用，充值到账和额度净调整独立计算。普通用户强制限定自身并隐藏上游信息，订单只返回摘要。历史零额与极小金额保留，详情提高精度。无数据库迁移，也不修改扣费、入账事务。独立测试库 make test、类型检查、前端构建及管理员中文／成员英文手机浏览器回归通过；测试覆盖分页、分类、隔离、重复通知、订单脱敏及延迟响应。浏览器脚本为 web/tests/activity-logs.browser.cjs，运行条件同用户编辑回归脚本。

统一日志现役提交、备份及只读登录验收见 `docs/deploy.md`；未制造生产充值或余额调整，验证会话已清理。

日志体系重构 · 第一次发布（2026-11-05 ✅ 已发布 DMIT）：全站日志与账户日志分为「调用日志」（默认）和「操作日志」两个标签，`?tab=operations` 可直达并在刷新后保留。调用日志列为时间、（用户）、Key、服务分组、模型、（渠道）、状态、输入 / 输出（次行缓存命中率）、首字 · tok/s（次行总耗时）、花费；汇总为花费、请求数、输入 / 输出 tokens、整体缓存命中率。操作日志暂含充值和额度调整（接口新增 `type=balance`），第二次发布接入全部管理写操作。迁移 `0017_log_group_name` 补服务分组名快照。`make test` 全绿（新增分组删除后保留名称、缓存汇总、balance 筛选断言），前端类型检查与构建通过，浏览器回归 `web/tests/activity-logs.browser.cjs` 已按新交互重写并通过（运行需 `NODE_PATH` 指向含 playwright 的 node_modules 且浏览器版本匹配）。发布凭证见 `docs/deploy.md`。待办：从备份补回 1995 条已丢失分组名的历史日志（待确认）。

日志体系重构 · 第二次发布（2026-11-05 ✅ 已发布 DMIT）：新增 `operation_logs`（迁移 0018），每次状态变更与业务改动同一事务写一行；变更列表由改前 / 改后快照比对得出（`internal/store/operations.go`），密钥类字段只标“已更换”，没有变化的保存不记。覆盖用户、用户分组、服务分组、模型定价、渠道、Key、系统设置、支付配置、注册码、改密码、注册、余额调整、充值到账（后者强制记为系统）。操作人由登录中间件放入 context（`store.WithActor`）。Key 的名称 / 启停 / 分组与限额改为同一事务。成员只见影响自己的记录，用户备注在服务端剔除。防漏记测试 `TestOperationLogCoverage` 扫描全部写路由，未登记也未豁免即失败；`TestOperationLogRulesAndVisibility` 覆盖无变化不记、失败回滚不记、成员可见性和筛选；`store.TestTopupCreditOperation` 覆盖系统入账与重复通知。前端操作日志标签显示操作人、操作、对象、变更摘要，点开看逐字段改前改后，可按对象类型筛选。`make test`、类型检查、构建与浏览器回归通过。发布凭证见 `docs/deploy.md`。

日志细节补充（2026-11-05 ✅ 已发布 DMIT）：调用日志「模型」列升级为「请求」（模型 + 协议 · 流式 · 推理强度），新增访问者 IP（管理员列表、成员详情），表格过宽时表内左右滑动；输出速度改按可见 token 且排除一次性到达；模型定价表加 ID 列。修正取 IP 只信回环地址导致线上全为 Caddy 容器 IP 的问题。迁移 0019。`make test`、类型检查、构建及浏览器回归（连跑 3 次）通过，发布凭证见 `docs/deploy.md`。

CI 与自动发布（2026-11-05 ✅ 已启用）：PR 和推送到 main 跑 Go 测试（临时 PostgreSQL）、`go vet`、前端类型检查与构建、三个浏览器回归；main 上 CI 通过后 GitHub 构建并发布 `main-latest`（成品、哈希、清单）；DMIT `lingshu-deploy.timer` 每分钟拉取，单独备份、替换、健康检查、失败回滚，破坏性迁移暂停等确认，纯文档 / CI 改动不发布。首次 `f2d9211`（21 秒，含等待流式请求结束）与 `0e92d08`（3 秒）自动发布均核对通过。手动发布保留为备用。见 `docs/deploy.md`「自动发布」。

概览页改版（2026-11-05 ✅ 已发布 DMIT）：余额卡带充值 / 联系管理员与低余额提醒；「开始使用」四步自动打勾、完成后收起；「接入」按工具分 Magpie（一键导入）、Claude Code、Codex、SDK，配置按官方文档核对；创建 key 弹窗可连同 key 导入 Magpie。内容在 `web/src/guide.ts`，以后改为后台自定义。新增 `web/tests/overview.browser.cjs`。发布凭证见 `docs/deploy.md`。

商业中转定位（2026-11-05 ✅ 已发布 DMIT）：现有功能（预付余额、在线充值、分组倍率计费、调用 / 操作日志）作为「商业中转」模式的第一个阶段性成果；系统设置中商业中转为当前模式，公司团队网关标“开发中”。迁移 0020。同次发布用户列表启用状态筛选。下一阶段：公司团队网关专用功能，需求待确认（预算替代余额、部门 / 项目成本分摊、企业账号登录、原始内容留存、DMIT 是否长期作为商业中转实例）。

用户页与用户分组入口（2026-11-05 ✅ 已发布 DMIT）：用户表改为 ID、用户（含备注）、角色标签、可用服务分组、余额、最近调用、状态；角色改到编辑弹窗并可预览可用服务分组；用户分组管理挪到「定价 → 服务分组」。管理端用户列表新增 `last_used_at`。无迁移。`make test`、构建、两个浏览器回归通过；`web/tests/users.browser.cjs` 按 11-01 改版后的页面重写。发布凭证见 `docs/deploy.md`。待办：按用户实际需要把部分服务分组改为“指定用户分组”（线上目前全部对所有用户开放）。

混元 Hy4 正式版（2026-11-05 ✅ 线上生效）：新增站内模型 `hy4`，价格按中国站刊例价 ¥6 / ¥18 / 缓存命中 ¥0.3 除以现行汇率 6.7，得 $0.895522 / $2.686567 / $0.044776，缓存写入 0，与 `hy4-preview` 同价。映射到 Hunyuan 渠道（渠道 11，Official API 分组），同名映射上游模型 `hy4`。经网关实测 chat 与 messages 两种协议都成功，上游模型为 hy4，26 输入 / 16 输出 token 扣费 $0.0000662686，与价格换算一致；验证用临时 key 已删除。`hy4-preview` 保留，上游仍标 online。无代码改动、无迁移、无发布。

服务分组 Relay-GPT 下线（2026-11-05 ✅ 线上生效）：该分组已无渠道，第三方中转渠道与自用 key 均已迁到 `Third-party Relay`，随即删除分组。删除前把 lsh 的 key「dddd」从 `Relay-GPT` 迁到 `Third-party Relay`——`api_keys.group_id` 外键没有级联，key 仍指向已删分组会导致删除被拒。删后现存 5 个分组：default、Official API、Cloud Providers、Account Pool、Third-party Relay；该 key 倍率由 0.2 变为 1。历史请求日志的 `group_id` 按外键置空，金额、倍率、模型保留。无代码改动、无迁移、无发布。

超级管理员（2026-11-06 ✅ 已发布 DMIT）：`287eb11` 含迁移 0024（删旧角色 CHECK 约束），被自动发布判为破坏性而暂停，经用户确认放行；发布前备份 `lingshu-deploy-20261106T043214Z-287eb11.dump`。核对：schema 0024，aumif（id 1）为超级管理员，另两个已停用的管理员不变，`/healthz` 正常、无 ERROR、发布后调用成功。若需回滚到旧版本，旧程序不认 `super_admin`，需先把 id 1 改回 `admin`。随后修正破坏性判断，只把删表、删列、`TRUNCATE`、`DELETE FROM` 算作破坏性（见 `docs/decisions.md`「CI 与自动发布」）。

GPT Fast 计费（2026-11-06 ✅ 已发布 DMIT）：模型加「Fast 倍率」（迁移 0025，`models.fast_multiplier` 和 `request_logs.service_tier`）。chat / responses 请求写 `service_tier` 为 fast 或 priority 时，开放了的模型按“规则价（含长上下文）× 倍率”计费，被上游降级按标准价；没开放的去掉档位，按标准处理；Ultrafast 一律去掉。预扣同样乘倍率。成员价格页显示 Fast 价，调用日志标档位。规则见 `docs/decisions.md`「GPT Fast 计费」。新增 `TestGPTFastTier`，覆盖未开放、Fast 加长上下文、responses 流式、降级、标准请求、Ultrafast；`web/tests/models.browser.cjs` 加 Fast 价展示和管理端清空倍率。`0a8050d` 自动发布：新的破坏性判断下 0025 不算破坏性，没有暂停；发布前备份 `lingshu-deploy-20261106T054552Z-0a8050d.dump`。随后用临时管理员会话经管理接口给 4 个 GPT 模型（id 12、16、17、18）开 ×2，与 OpenAI 官方一致；操作日志只记了 Fast 倍率一项变化，价格和长上下文规则不变。真实调用核对（gpt-6-luna，Official API 分组，OpenAI Platform 渠道，临时 key 限额 $0.01，用后删除）：chat 写 `priority`，上游返回 priority，记 fast，扣 $0.000006；不写档位，上游返回 default，扣 $0.000003，用量同为 10 / 4 token，正好两倍；responses 流式写 `fast`，上游返回 priority，按 Fast 计费。

调用耗时拆分（2026-11-06 ✅ 已发布 DMIT）：日志新增读体和发往上游时刻（迁移 0026）；首字改为从发给上游算起；详情拆成读体、网关处理、首字、转发、总耗时；列表在读体达到 0.5 秒时提示。新增 `TestTimingBreakdown`（客户端上传中途停 200ms，上游 150ms 后才回首字）；`web/tests/activity-logs.browser.cjs` 加了拆分显示和旧日志兼容。口径见 `docs/decisions.md`「日志体系重构」。`f8ca3b3` 自动发布，发布前备份 `lingshu-deploy-20261106T063332Z-f8ca3b3.dump`。上线后 8 条真实流式请求（gpt-6.1-sol，chat）：读体 0–56ms，网关处理 44–352ms（多数 50ms 左右），首字 2.4–20.8s，转发 6.8–42.5s。读体很小，是因为请求先经 Cloudflare，Cloudflare 收完整个请求体才转给源站，用户自己的上传时间算不到我们这边；所以灵枢的读体只反映 Cloudflare 到 DMIT 这一段。另有 4 条的转发几乎等于首字，说明上游把内容攒好一次性发出，和 11-05 观察到的中转渠道行为一致。

备份清理与保留策略（2026-11-06）：每日备份由“最近 14 份”改为分层保留（14 天每天、8 周每周、6 个月每月）；发布脚本发布成功后旧版本只留最近 5 套，并清掉暂停 / 放行标记；只改 `deploy/` 不再触发发布。保留策略表见 `docs/deploy.md`「备份、恢复与回滚」。只改 `deploy/` 的 `3f77a27` 按新规则没有发布。新脚本手动装到 DMIT，哈希与仓库一致，原脚本留在 `/var/backups/lingshu-*.script-before-20261106`。清理前用发布前备份 `lingshu-deploy-20261106T063332Z-f8ca3b3.dump` 恢复到临时库演练：哈希一致，21 张表行数与线上一致（调用日志少的 55 条是备份之后的新请求），演练库用后删除。清理范围：
- `/opt/lingshu` 删掉 50 套旧版本，留最近 5 套，778MB 降到 88MB。
- `/var/backups/lingshu` 删掉 10-23 至 10-25 的手动备份和 `.env` 副本（25 个文件，对应 schema 14 及以前，已被后续备份覆盖）。
- 删除 2 个旧恢复演练库，以及 287eb11 的暂停 / 放行标记。
- 手动跑一次新备份脚本，每日备份按日去重后剩 11-01 至 11-06 每天一份；发布前备份 10 份；目录 4MB；磁盘可用从 7.4G 升到 8.1G。
- 安装基线 `/var/backups/lingshu-dmit-copy-20261023/` 保留。

待定：异地备份。

充值并入操作日志（2026-11-06）：日志只留调用日志、操作日志两个标签；发起在线充值记“发起充值”，订单状态实时关联（迁移 0027 补记已有订单）；去掉 `GET /api/admin/topups`。顺带修了按“兑换码”筛选操作日志报 400 的问题：接口白名单漏了 `redeem_code`。测试：`TestTopupOrdersInOperationLog` 取代原订单筛选测试；`activity-logs.browser.cjs` 改为检查只有两个标签、发起充值的实时状态和旧链接。规则见 `docs/decisions.md`「充值订单与兑换码」。

渠道连接状态（2026-11-06）：渠道列表去掉模型列，加“连接状态”（最近 24 小时成功率与次数、首字和速度中位数、缓存命中率、冷却中）；调用日志新增 `failed_channel_ids`（迁移 0028），换渠道成功的请求也把前面失败的渠道计入失败。测试：`TestChannelStatsCountFailover`（换渠道、断流、原样 400、用户断开、首字 / 速度 / 缓存口径）；新增 `web/tests/channels.browser.cjs` 并加入 CI。口径见 `docs/decisions.md`「渠道连接状态」。下一步：渠道质量与智商。

OpenCode Zen 原生渠道（2026-11-07 ✅ 已发布 DMIT，PR #29 接替 Codex 的 #21）：内置 `opencode-zen` 模板和独立 Go TLS 客户端，直连官方 Chat 入口；免费模型补充编码上下文、占位工具和会话，非流式客户端收到完整 Chat JSON，沿用原有路由、计费和日志。合并 main 后用户新给的 key 实测上游 10 个免费模型全部免费通过（此前 429 是旧 key 被限流）。按用户要求先只上 `deepseek-v4.1-flash-free`（每把 key 一个渠道、专用分组；第二把 key 上游报 401，后缀比另两把少一位），经网关临时 key 实测流式、非流式都成功，费用 0，临时 key 已删。线上现状（2026-11-07 用户自行调整后核对）：只留一个渠道 `OpenCode Zen`（id 32，第一把 key，Cloud Providers 分组），映射 `deepseek-v4.1-flash-free`（零价）和站内 `GLM-5.3-Flash` ← 上游 `glm-5.3-flash-free`（站内按 GLM-5.3-Flash 原价收费）；我建的另两个渠道和 `OpenCode Free` 分组已被用户删除。接入细节见 [OpenCode Zen](research/opencode-zen.md)。

模型检测 · 试验阶段（2026-11-06）：本机脚本 `scripts/probe/probe.py` 按 `internal/probe/data/suite.json`（第 1 版）检测分词指纹、单词指纹、数学题、大海捞针、鹈鹕（结构检查表和腿部测量 `legs.js`）。用用户的官方账号（本机 Magpie `codex/*`）给 gpt-6.1-sol、gpt-6-astra、gpt-6.1-terra、gpt-6-luna 建了两轮样本（`internal/probe/data/baselines/`；第二轮未跑大海捞针，astra 第二轮的鹈鹕因额度中断）。单词指纹和最难的数学题都能把 terra、luna 和 sol、astra 分开，sol 和 astra 分不开；可疑阈值 0.40。中转 咔咔 的 sol、astra 未见掺水。题库已扩到 195 题（第 2 版），四个型号都跑完新题（含补跑的希腊字母、表情符号两题），答案分布在 `fingerprint-distributions.json`。待办：推理强度对鹈鹕腿的影响（high / xhigh）。口径见 `docs/decisions.md`「模型检测」。

模型检测页面 · 第一版（2026-11-07）：管理端新增「模型检测」，选渠道和模型发起检测，后台运行，逐项给出结论和请求质量观察（迁移 0029 `probe_runs`；`internal/probe`、`relay.ProbeChat`、`/api/admin/probes`）。检测题和官方样本从 `scripts/probe/` 挪到 `internal/probe/data/`，编译进程序，本机脚本读同一份。测试：`internal/probe` 单元测试（和脚本算法对齐、模拟上游识别真 sol / terra 冒充 / astra 冒充）、`TestProbeRun`（发起到入库、请求伪装、工具调用分片拼接、不写调用日志、重启标失败）、`web/tests/probes.browser.cjs`。待办：知识时效题库和样本；上线后在 咔咔 上实测，确认注入的提示词不会让指纹误判。

模型检测 · 实测与改进（2026-11-07）：线上检测 咔咔 的 gpt-6.1-sol，第一次最像 luna、复测像 sol，基准输入不同，确认号池里有上游拿 luna 冒充 sol（用户自行处理该渠道）；对照组发现 OpenAI Platform 的 gpt-6-luna 映射到 gpt-5.6-luna（用户已改）。改进：一次检测用 4 个会话编号并分会话评判、报告上游混用；工具调用题用 `none` 推理强度；推理强度两档比较改为参考（官方校准见 `docs/decisions.md`）。新增 `TestRunCatchesMixedPool`。

NVIDIA 模板（2026-11-07）：新增 `nvidia` 模板（只有 Chat，`https://integrate.api.nvidia.com/v1`，可拉取模型列表）；NVIDIA、OpenCode Zen 加上渠道图标（lobehub-icons 1.95.1）。已发布 DMIT（d8da83b），线上 NVIDIA 渠道（id 31）原来没选模板，只把模板字段改为 `nvidia` 以显示图标。

模型检测加回档位（2026-11-07）：发起检测可选快速（指纹 40 题 × 4 次）或完整（区分度 ≥ 0.3 的指纹题全上 × 6 次），检测记录加“档位”列；浏览器测试改为选完整档。已发布 DMIT（66d44c9）。

待办：GitHub 给 `main` 设分支规则（禁删、禁强推、必须走 PR、要求两项 CI 通过、审批数 0、管理员仅 PR 可跳过），已向用户建议、未设置。已确认只改文档的 PR 也会跑 CI（PR #32），要求检查通过不会卡住文档合并。

> 注：上面最后一条“GitHub 给 main 设分支规则”已于 2026-11-07 由用户在 Rulesets 里设好（规则名「分支保护」）。

### 上游中文报错导致日志丢失（2026-11-07 ✅ 已发布 DMIT，PR #35）

上游报错按字节截断会切在汉字中间，写请求日志被 PostgreSQL 拒绝，整笔结算回滚：日志丢失、预扣冻结不释放。14 天内发生 3 次（均 cost=0，没有算错账），最近两次是 hvoy 测 claude-fable-5-1 时渠道 kirisame-claude0.7 返回中文 429。修复：`relay.truncate` 用 `strings.ToValidUTF8` 去掉残字节和上游原文的非法字节；新增 `TestLongChineseUpstreamErrorIsLogged`（修复前复现失败）。`558c089` 06:34 UTC 自动发布，发布前备份 `lingshu-deploy-20261107T063407Z-558c089.dump`；健康检查正常，aumif 卡住的冻结 $0.8256 随重启清零，发布后无 ERROR。

### 大海捞针与 Claude 协议检测（2026-11-07）

完整档加大海捞针（GPT、Claude 都做，编号和句子每次随机，提示词只存开头和长度）；claude-opus-5-5 开放协议层检测（`relay.ProbeMessages` 伪装 Claude Code 走 Messages，`internal/probe/protocol.go` 评判协议规范、推理、工具、指令、文档与原样输出）。上线前在 DMIT 用 kinght claude0.8 实测：协议特征、PDF、尖括号原样、工具调用都正常；thinking 签名回传（普通多轮、工具调用循环，原样 / 篡改 / 伪造 / 删除）全部 200 且输入 token 不变，判断依据不足，未上线该项。官方渠道 18 余额不足，kirisame 号池 503。测试：`TestNeedleInFullPreset`、`TestProtocolCheckForClaude`、`TestProbeMessagesDisguiseAndParse`（按 kinght 真实响应构造）、`probes.browser.cjs` 加 Messages 渠道和 Claude 选项。

线上验收（`ca5bdb5`，含 PR #39 大海捞针按分词器定长度、记下 Claude 流中报错）：
- 首轮（`dfd5152`，检测 10）Claude 大海捞针达 20.1 万 token，上游回 200 后在流中报错，检测误判为编号没找到、协议规范连带不通过，由 #39 修正。
- 检测 11，kinght gpt-0.16 · gpt-6.1-sol 完整档 93 分：大海捞针 92577 token 三个编号全找到；结构化输出遇渠道 Cloudflare 524；会话 1 基准 28 token、其余 4391，渠道混了官方 API 和 Codex 号池两种上游；535 个流里 29 个没正常结束。
- 检测 12，kinght claude0.8 · claude-opus-5-5 完整档 67 分：协议规范通过（11 个响应全部符合官方格式）；大海捞针（约 10 万 token）上游流中报 “Upstream access forbidden”，渠道不支持长上下文；推理两次 0/2、系统提示“只回某词”两次都没守住，没有官方样本，暂记需关注。
- 本地 Codex 0.160（Responses 协议，`model_auto_compact_token_limit=2000`）经灵枢触发两次自动压缩均成功：压缩请求走 `/v1/responses`、带 `remote_compaction_v2`，不调 `/responses/compact`，灵枢透传即可。Codex 启动时会请求 `gpt-5.6-luna`、`gpt-5.6-terra`，站内没有这两个模型，返回 404，不影响主流程。

### 按质量调整 GPT 路由（2026-11-07）

Third-party Relay 分组原来 kinght gpt-0.16、咔咔-gpt0.18、ai8-gpt-0.15 都在优先级 0，同档随机，约三分之一请求落到咔咔。检测结论：ai8 两次 92、93 分（真 sol，但每个请求多注入约 1.4 万 token）；kinght 93、83、79 分（号池混用，部分会话像 luna / terra）；咔咔两次像 luna。经管理接口改为 ai8 优先级 10、咔咔停用（操作日志 76、77），kinght 保留 0 档兜底；改前后逐字段对比只有这两处变化。停用后 gpt-6-sol 只剩 kinght（14 天 2 次调用），其余 GPT 模型都有 ai8。随后用户把 kinght gpt-0.16、kinght claude0.8 也停用：GPT 只走 ai8（没有兜底渠道），gpt-6-sol 没有可用渠道；Third-party Relay 分组不再提供 Claude，Official API（Anthropic Platform 余额不足）和 Account Pool（kirisame 号池无可用账号）也暂时不可用。

### 检测里的断流记成失败（2026-11-07）

用户在 kinght gpt-0.16 上跑完整档，进度长时间显示 0%：536 个请求里 38 个流一个字没回、约 2 分钟后断开，占住 6 个并发位，整次检测用了约 11 分钟（93 分），不是卡死。但这些空流原先被当成正常回答，空答案算进了单词指纹。改为：Chat / Messages 检测请求的流既没有结束标记也没有结束原因时记成失败；检测记录列表的进度从取整百分比改为“已完成 / 总数”。新增 `TestProbeHungStreamIsError`。

### 三个 GPT 中转的检测复盘与删除 dong（2026-11-07）

用户按检测结论给渠道命名（ai8“缓存偏低”、kinght“混池波动”、dong“偶发降智”）后请求复核，汇总 14 条检测（剔除 2 条渠道额度用完的 0 分、1 条被发布打断）：
- kinght gpt-0.16（83、79、93、93、100、93）：混池属实，基准输入 token 在 22 / 28 / 4227 / 4391 / 22427 / 29980 之间跳；中午一次分词 0/12、零宽水印，一次部分会话像 luna / terra；下午后稳定但每次 19–38 个流卡住。保留为兜底（优先级 7）。
- ai8 gpt0.15（92、93）：模型正常；同口径检测缓存 66–73%（另两家 83–97%），真实流量约 90%；每个请求多注入约 1.4 万 token；14 号检测时渠道余额用完全部 403。保留为主渠道（优先级 10）。
- dong gpt0.15（86、79、93）：指纹三次都离官方 sol 0.54–0.61（正常 0.25–0.30），注入量与官方 Codex 一致，排除注入影响；推理强度反常（low 推理 1034–1263 token，high 只有 425–437）；推理题 3/4、2/4、4/4；首字约 7 秒。判断不是原版 sol 或推理强度不受控，用户删除该渠道（操作日志 101）。

### Claude 检测接入官方样本（2026-11-07）

`scripts/probe/probe.py` 加 `--protocol messages`（Claude Code 请求形态）、`--provider`（OpenRouter 锁定服务方）、`--name`、`--math-kinds/-repeats/-effort`，key 可走环境变量 `PROBE_KEY`。在 DMIT 用 OpenRouter（锁定 Anthropic）采 claude-opus-5-5（分词、单词指纹 195 × 6、难题 4 类 × 3）和 claude-sonnet-5-5（分词、单词指纹），约 $0.8。检测按家族分开比（`Family`、`FamilyModels`、`SeparatingPrompts(…, family)`，GPT 题池仍 74 道）；Claude 有样本时加分词、单词指纹，推理按官方正确率和推理量判，指令层级守不住直接不通过；接口只列出能正确检测的型号（GPT + Claude 检测名单）。测试：`TestClaudeWithBaseline`、`TestJudgeThinkingWithBaseline`，原协议检测测试改用测试专用的无样本型号。

上线（`c58945a`）后两次快速档验证：检测 34 kirisame 100 分（分词 12/12、指纹最像 Opus 0.19、4 个会话一致、推理 2/2、守住系统提示，基准 42 token 与官方相同）；检测 35 kinght claude0.8 57 分不达标——协议格式 186 个响应全部符合（hvoy 给它 100%），但分词只有 3/12 和官方一致、指纹离 Opus 0.80 且一个会话像 Sonnet、推理 1/2、系统提示没守住、基准 68 token。说明协议层全过不代表是原样的 Opus，智能层把它拦下了。

### 厂商筛选、公告、USDT 占位（2026-11-07）

模型定价加厂商筛选（`vendorOf`，`?vendor=`）；运营设置加公告（`announcement` 设置项，`/api/me/endpoints` 返回，概览页顶部显示，邮箱可点）；支付设置加 USDT（BEpusdt）“规划中”占位。测试：`TestAnnouncement`（默认空、去空白、成员可读、超 500 字拒绝、成员不能改、留空不显示），`models.browser.cjs` 加厂商筛选，`overview.browser.cjs` 加公告。

### 模型定价筛选改为搜索框加厂商下拉（2026-11-07）

厂商分段按钮在十几个厂商时出横向滚动条，改成工具栏：搜索框（模型名或厂商名）、厂商下拉（图标 + 数量，`?vendor=` 保留）、添加模型按钮靠右。`models.browser.cjs` 改为验证搜索、下拉选项顺序和数量、网址保留、清空恢复。

### 渠道多 key 与渠道备注（2026-11-07）

迁移 0030 新建 `channel_keys`（现有 key 搬成第一把，`channels.api_key` 保留不删）、`channels.note`、`request_logs.channel_key_id / failed_key_ids`。网关按“灵枢 key + 会话编号”做最高随机权重哈希固定 key（`relay/keys.go`），`previous_response_id` 回原 key；`router.KeyHealth` 管单把 key 的冷却（429，按 Retry-After，最多 10 分钟）和失效（401 / 402 / 403，30 分钟后再试），5xx / 超时仍是渠道级、跳过同渠道其余 key；原 `router.Order` 由 `orderAttempts` 取代后删除。OpenCode 免费模型会话按 key 区分。管理端 key 列表（掩码、状态、24 小时次数、备注、启停），批量添加分追加 / 覆盖，服务端自动去重（覆盖时相同 key 沿用原记录），上限 50；冒烟测试可选 key；渠道备注显示在列表名称下方。测试：`TestKeyHealth`、`TestMultiKey*`（会话固定、429 本渠道换 key、401 标失效、5xx 跳过渠道、续接回原 key）、`TestChannelKeysSave`、`channels.browser.cjs`；测试环境加触发器把 `channels.api_key` 同步成一把 key。期间 WSL 重启清空 `/tmp`，未提交的改动丢失后重做，`AGENTS.md` 改为工作区放 `~/worktrees/`。

### 调用日志加峰值 RPM / TPM、响应时间中位数和快捷时间段（2026-11-07）

`store.requestLoad` 按调用日志的筛选算最忙那一分钟的请求数和 token 数、成功请求的首字和总耗时中位数，随汇总返回（`peak_rpm`、`peak_tpm` 及发生时间、`median_first_token_ms`、`median_total_ms`）。日志页汇总卡片 6 张（宽屏一行 6 张、中屏 3 张、手机 2 张，数字不折行），日期旁加今日 / 本周 / 本月 / 近 7 天 / 近 30 天。测试：`TestLogLoadMetrics`，`activity-logs.browser.cjs` 加新卡片和快捷时间段（含手机宽度不撑宽页面）。

### 服务分组失败重试次数与按原因重试（2026-11-07）

迁移 0031 给 `groups` 加 `retry_count`（0–4，默认 2，原行为不变；保存不带字段时保留原值，接口返回数据库里的实际值）。`relay.attemptAll` 改为按失败原因走：key 级失败换 key 不占次数（后面还有渠道时同渠道最多 10 把，最后一个渠道不限），渠道级失败占次数、换渠道或等 1 秒同渠道再试；所有 key 限流冷却时直接回 429 + Retry-After（`router.KeyHealth.MinCooldown`）。删除不再使用的 `router.MaxAttempts`。服务分组页加“失败重试”列和编辑项。用户指出最初“同渠道最多 2 把 key”的设计会坑免费 key 聚合渠道，改为上面的规则。测试：`TestLastChannelRotatesAllKeysThenRetryAfter`、`TestKeyRotationCapWhenOtherChannelExists`、`TestGroupRetryCountZero`、`TestSingleChannel5xxRetriesSameChannel`、`TestGroupRetryCount`、`models.browser.cjs`。

### 兑换码并入支付设置（2026-11-07）

系统设置去掉“兑换码”标签，兑换码收成一张卡片放在“支付设置”第二张（第一张为“在线支付”）；旧链接 `?tab=redeem` 落到支付设置并滚到兑换码卡片。开关“开放用户充值”改名“开放在线支付”并注明不影响兑换码；兑换码说明加“可以放到第三方发卡平台售卖（例如 wzyp.cn）”。只改前端和文档，无接口或数据库变更。测试：`redeem.browser.cjs`（合并后的两张卡片、旧链接、第三方平台说明，原有生成和兑换流程）。

### 分词固定偏移与 none 降档（2026-11-08）

- 超超 claude 检测 62 分词 5/12 判不通过，逐段看是 5 段一致、7 段恰好 +28：分词器一致，是部分请求被固定注入，属误报。对照所有分词不通过的记录，只有它是“0 和固定数”两种，其余（kinght、咔咔、codezhongzhuan、zoowyoo 等）零散不一致。新增 `fixedOffset`，这种情况记需关注。
- pikaqiu gpt 测 astra（检测 59）指纹 444 题全部 HTTP 400“Unsupported value: 'none'”，原重试条件只认报错里的 reasoning 字样，没重试；aresai gpt 测 astra（检测 60）上游把 Chat 转成 Responses，none 时回空流（对方后台显示 0 token）。改为两种都换 low 重试、按规则决定是否整次降档，指纹大部分降档时只作参考。测试：`TestTokensFixedOffsetIsInjection`、`TestProbeChatRetriesUnsupportedNone`、`TestProbeChatRetriesNoneOnEmptyStream`、`TestFingerprintLoweredEffortIsOnlyAdvisory`。
- 本轮复核四个 GPT 渠道：超超 gpt 100（未挂分组，没生效）；pikaqiu 100、93、79（一次会话像 luna，偶发混池）；aresai 93、79、86、93、93（指纹一直离官方 0.46–0.60，两次有会话像 terra）；code 86、86、86、93（指纹稳定是 sol，推理常 3/4）。aresai、pikaqiu 是官方 API 环境（基准 11 token），和 Codex 环境的官方样本有环境差，需要补官方 API 环境样本（渠道 16 OpenAI Platform）。

## 二、部署与发布证据（原 `docs/deploy.md`）

### 改为自动发布前的手动发布记录

2026-11-05 发布并复核（概览页改版）：DMIT 已发布提交 `b5090dcb1881e60bdaa1f28b8e368ea7192a0505`，运行中程序 SHA-256 为 `995c0f0635ac55fb8830db57aa06dd3b8cf036f2e012109afc7da837200e8f29`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T160124Z.dump`，SHA-256 为 `ab5105544efe475be997872e7e65a9e12e143fc319029ba90cb2e23b62080194`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-b5090dc`。只改前端，无迁移，schema 仍为 `0020_commercial_relay_mode`；回滚换回 `prev-b5090dc` 程序即可。内网与公网健康检查通过，公网入口资源 `index-DRFHDpk-.js` 与干净构建产物 SHA-256 `54ea3d0fd1851a35d5c296af7f8fc9f935b3e7b5abbec83227362855b7c184c0` 一致，服务日志无 ERROR。压缩成品上传约 9 分钟；部署方式拟改为 GitHub Actions 构建、DMIT 下载成品（仓库为公开仓库，下载无需 token），待确认。

2026-11-05 发布并复核（商业中转定位、用户筛选）：DMIT 已发布提交 `17e48474ff30321d33fe3d8661d424f53c85d149`（含 `5b70ebf` 用户启用状态筛选），运行中程序 SHA-256 为 `697774a77a95b8a17208223c80bc3d105e15686b2699f5ec1ac32b2f0c84d9ba`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T152552Z.dump`，SHA-256 为 `50be89fb5dcaabf842802ccc4cca7c86ea1ee047b57e3ae3928a90929a845355`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-17e4847`。

迁移 `0020_commercial_relay_mode` 后 schema 为 `0020_commercial_relay_mode`；线上 `deployment_mode` 原为未设置（按默认显示公司团队网关），现为 `commercial_relay`。回滚换回 `prev-17e4847` 程序即可，旧程序不认识该值时页面仍显示公司团队网关，不影响功能。内网与公网健康检查通过，公网入口资源 `index-CFF1fsTx.js` 与干净构建产物 SHA-256 `e45edd97b13e3e6af9008379b4b33b6cd758d7e0de872b8a7557d28351937ae2` 一致，服务日志无 ERROR。`5b70ebf` 单独上传的成品未安装，已删除，合并到本次一起发布，只重启一次。

2026-11-05 发布并复核（用户页与用户分组入口）：DMIT 已发布提交 `4b2736262197116eb4afb521b690888741b6d89e`，运行中程序 SHA-256 为 `a2afd6aaf36ffa6330facac0b55c88763cbe7d2fa751d6075b0209cd62ded542`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T145456Z.dump`，SHA-256 为 `e337e4a69775880f3cc991a5d8bfb05db26228a861aa1f9091cdd39525d7428d`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-4b27362`。无数据库迁移，schema 仍为 `0019_log_request_details`；回滚换回 `prev-4b27362` 程序即可。内网与公网健康检查通过，公网入口资源 `index-DkalBhoK.js` 与干净构建产物 SHA-256 `8b1556d50caca9e07ce5e9ffb46c46506dcab53f4dd30c9ffb1ed8d20d3baabf` 一致，服务日志无 ERROR。压缩成品约 5.3MB，上传约 7.5 分钟。

2026-11-05 发布并复核（请求 IP、请求列、速度口径）：DMIT 已发布提交 `88d8823f8fedf9a353e60acd693904664acb285e`（含 `6106589` 速度口径修正），运行中程序 SHA-256 为 `32388c364777dbd88580bf6ec62e8d01ed4e58a95d3b520b9474de6e1b974322`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T141959Z.dump`，SHA-256 为 `b01ab8a8427efb98a25a69a95b6361224c543ed5bca1a5f88f9a84d473f6b145`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-88d8823`。

迁移 `0019_log_request_details` 给 `request_logs` 加 `client_ip`、`reasoning_effort`，线上 schema 升到 `0019_log_request_details`。发布后第一条新请求记下公网 IP 与推理强度 `high`，确认 Caddy 经 Docker 网桥转发的 `X-Real-IP` 已被采信（此前全部记为 `172.18.0.4`，登录防护也按此计数）。公网入口资源 `index-CoclI6Bs.js` 与干净构建产物 SHA-256 `6b2bdabb4967d2da1322caff342c27b42b8c6bc584d2c19591087196e15d2d90` 一致，服务日志无 ERROR。0019 是纯增列迁移，回滚换回 `prev-88d8823` 程序即可。

传输备注：本次 WSL → DMIT 上行很慢，scp 13MB 成品中途断开过一次；改为本地 `gzip -9`（约 5.3MB）后上传，耗时约 16 分钟，服务器端解压后核对哈希一致。

2026-11-05 发布并复核（操作日志）：DMIT 已发布提交 `4ac5a7a56f887be66100e8b6c86262d2512166bc`，运行中程序 SHA-256 为 `e1541a729f2ac256267e7a44bbe838474008c4066f73e815c32e347d5c43ed86`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T104022Z.dump`，SHA-256 为 `b24c51029a7f7410cde04ed9e15d4e2f426cbf93ef5a9d00b7e9afaa583d006b`，`pg_restore --list` 可读（19 张表数据）。旧程序、配置与发布标记保留为 `prev-4ac5a7a`。

迁移 `0018_operation_logs` 新建操作日志表并回填历史余额流水，线上 schema 由 `0017_log_group_name` 升到 `0018_operation_logs`；10 条 `balance_adjustments` 全部回填，逐条核对无遗漏。服务启动、内网与公网健康检查通过，服务日志无 ERROR。公网入口资源 `index-YxoBAqJh.js` 与干净构建产物 SHA-256 `856a68a982db7fea5e48bf66d48c9cfd57148109849c34ce09c91df467d3a3db` 一致。上一版发布后的 72 条新请求均写入了分组名快照。

0018 只新建表并回填，旧程序不读该表：回滚只需换回 `prev-4ac5a7a` 程序。回滚后新产生的操作不会被记录，再次升级时表和已有记录保留。

2026-11-05 发布并复核：DMIT 已发布提交 `186bcc883cee3e4d073c9900deacf41ad65e8ae6`，运行中程序 SHA-256 为 `2c43284acb99cc22d694bef72fc8e451ce80a14cb81f80f7c57d33c6a939f81e`，属主保持 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T092701Z.dump`，SHA-256 为 `813a62c93e41922a54deddafbefc79176e8e431435732153d81ab48bdb7d6410`，`pg_restore --list` 可读（19 张表数据）。旧程序、配置与发布标记保留为 `prev-186bcc8`。

本次日志分为调用日志 / 操作日志两个标签：迁移 `0017_log_group_name` 给 `request_logs` 加服务分组名快照并按现有分组回填，线上 schema 由 `0016_user_note` 升到 `0017_log_group_name`。回填时 3185 条请求日志中 1190 条补上分组名；1995 条的 `group_id` 早已因分组删除置空（倍率快照 0.2、0.1、1 各有一批，2026-10-28 至 11-05），新迁移无法补回，可按 request_id 从删除前的每日备份恢复。旧进程等待进行中请求结束后退出，新进程启动、内网健康检查通过，服务日志无 ERROR。公网首页引用的 `index-B0Hsk727.js` 与干净构建产物 SHA-256 `4b0617cd224271183e28d25205035751f71f9894cc534714e8292f4c4d36a4fa` 一致。

0017 是纯增列迁移，旧程序不读该列：回滚只需换回 `prev-186bcc8` 程序，不需要数据库恢复。

运维备注：DMIT 的 SSH 对短时间内重复建连会直接断开（`Connection closed`），发布时用 `ControlMaster` 开一条主连接复用。本次从 WSL 直连 `gateway.piqode.com:443` 出现 TLS 握手失败（`SSL_ERROR_SYSCALL`），从 DMIT 经 Cloudflare 访问正常，公网核对改在 DMIT 上执行。

2026-11-01 发布并复核：DMIT 已发布提交 `a716f27161e93c0fca6c99fb2cce890ad43c0131`，运行中程序 SHA-256 为 `51becc46e164ec1614d46c6e72acb05b69d0593f1ffe3b627e1c780441b83f2e`，服务及内外网健康检查通过。发布前备份 `/var/backups/lingshu/lingshu-20261101T151309Z.dump`，SHA-256 为 `da3a5cfeda473da641ce9abe6a3bbf7bae21ed8f8b73cbb687d86b2b63ed17c7`，已通过 `pg_restore --list`。旧程序、配置与发布标记保留为 `prev-a716f27`。

本次新增管理员用户备注：迁移 `0016_user_note` 给 `users` 加 `note TEXT NOT NULL DEFAULT ''`，线上 schema 由 `0015_channel_balance` 升到 `0016_user_note`；该字段只在管理端用户列表返回，`/api/me` 不含。用户编辑弹窗同时把额度区的「备注」改名「额度备注」，并去掉「最近调整」列表（流水仍写库，可在账户日志与全站日志查看）。发布后用临时管理员会话只读核对：`/api/admin/users` 返回 `note` 字段、`/api/me` 不含该字段，会话已删除。公网首页与入口资源 `index-HpP6EULs.js` 的 SHA-256 为 `367755f967692605d101c7470865b5045b9210dec3a3001dcaace4e0ee230f74`，与干净构建产物逐字节一致。复核公网时注意：用 Python 默认 `Python-urllib` UA 访问首页会得到 403（Cloudflare），需换浏览器 UA。

0016 是纯增列迁移，旧程序不读该列：回滚只需换回 `prev-a716f27` 程序，不需要数据库恢复。

构建备注：本次 `npm ci` 因 registry 单包下载耗时过长（个别包 10–30 分钟）中断，改为复用已安装的 `web/node_modules`，并在复制前后校验 `package-lock.json` 与 `node_modules/.package-lock.json` 的 SHA-256 完全一致；构建仍取自干净的 detached worktree（提交 `a716f27`），worktree 除 `node_modules`、`web/dist` 外无改动。

2026-10-27 发布并复核：DMIT 已发布提交 `030f94751af51f2509d936f2ae51b0c250319698`，运行中程序 SHA-256 为 `493daa23191fcafd438c48493a1754c971898dd3d12b90f72b15be683e1efbe7`，服务及内外网健康检查通过。发布前备份 `/var/backups/lingshu/lingshu-20261027T155013Z.dump`，SHA-256 为 `f07af5f6a9aea19f561e994adf03ec529aa4ff87a59e7829fc82f8e3b46e26df`，已通过 `pg_restore --list`。旧程序、配置与发布标记保留为 `prev-030f947`。

本次取消上游余额估算，改为渠道「额度」按需查询：迁移 0015 删除 `upstream_accounts`、`upstream_account_entries` 两表及 `channels.upstream_account_id`、`request_logs.upstream_account_id`、`request_logs.upstream_cost`，渠道新增 `balance_detail` / `balance_checked_at` / `balance_error`。线上 schema 由 14 升到 `0015_channel_balance`，19 个渠道保留。用临时管理员在生产实测 DeepSeek、Moonshot、OpenRouter 三个渠道查询成功（¥8.93、¥71.345474、$9.97802656），无余额接口的智谱返回 400；验证账号与会话已删除。公网资源 `index-DhYQCzPW.js` 的 SHA-256 为 `cb18849d99b4cf4e728d0f97a2c324373d6790dd42d76251280970793950bf12`，与干净构建产物一致。

0015 是删表删字段的不可逆变更：旧程序依赖已删除的表和列，回滚不能只换程序，必须恢复发布前备份并接受备份之后的请求日志丢失；保留的 `prev-030f947` 只用于同一 schema 下的紧急替换。

2026-10-26 发布并复核：DMIT 已发布提交 `5f0ee4f475debb41d4e120f2e92cec32ea1dca81`，运行中程序 SHA-256 为 `d3e759a08f960cd6959f83187d868786a22dbe75c84225c92142cf2e8fc928d7`，服务及内外网健康检查通过。发布前备份 `/var/backups/lingshu/lingshu-20261026T234615Z.dump`，SHA-256 为 `dd22f00caa17cd4e5d94555528f5d75f5ba2ed7e68f80284055aa76515a98e92`，已通过 `pg_restore --list`。旧程序、配置与发布标记保留为 `prev-5f0ee4f`。前端资源 `index-Br285SK3.js` 的 SHA-256 为 `3cbe34a584e4dff178591701323cc0b22a691ad54bf7462edc77599c8d23fd9d`，与已推送提交的干净构建产物一致。

本次在用户和服务分组列表新增 ID 列，并将渠道列表表头统一为 `ID`。前端类型检查、前端及程序构建通过；登录后的三处页面已只读核对，用户及服务分组的显示 ID 与接口返回值一致，无接口或数据库变更。系统使用模式仍默认且仅开放“公司团队网关”。

2026-10-25 发布并复核：DMIT 应用正常，公网 `/healthz`、账户日志页与前端资源均返回 HTTP 200。应用版本为 `a08594cd18bbf66d6623d825952a0f6fab4d6e6a`，程序 SHA-256 为 `603c3d888ba62659bd4cd673c75d9a3ba0c68e30b5fcaf9ede4dcb0b2e2da696`，与 `/opt/lingshu/release.json` 一致。本次无数据库迁移。

该版本将模型调用、在线充值和额度调整统一纳入全站日志及用户账户日志，分页总数和各类金额独立统计。发布前备份为 `/var/backups/lingshu/lingshu-20261026T040328Z.dump`（UTC 时间），SHA-256 为 `8357fd0f9ec49cdf72cc98619ee8ad76dd2340511688fdc276f9fe7a25ddc311`，已通过 `pg_restore --list`；旧程序、配置及发布标记以 `prev-a08594c` 后缀留存。前端资源为 `index-D5w4AcIX.js`，公网内容 SHA-256 为 `08656f8a28bb9c1922f1c0fb3d516e342fd67d37acb21b49abc1d8b2d1b47d31`，与干净检出构建产物一致。

使用已有账号的短期验证会话，只读验证管理员全站日志与用户账户日志页面，以及全部、充值、额度调整和调用筛选，响应与页面均通过；验证会话已删除。未新增充值、未调整真实用户余额。

### DMIT 历史验收证据

#### 初次上线与恢复演练（2026-10-23）

初始数据库快照时间为 `2026-10-23T08:45:17.337778Z`，使用同一事务快照核对 15 张表：1 个用户、2 把 API key、11 个渠道、23 个模型、24 条渠道模型映射、43 条请求日志。随后从干净 worktree 发布 System One 提交 `4bef2a13d8bbd7ef654bb788aa2a26643ec74859`，程序 SHA-256 为 `f94184bf62c270543405752757025f91931b3c6d306d96d5fe026f05a66bdbfd`，当时为 schema 9。

- 公网 HTTPS、管理员登录、Secure / HttpOnly 会话 cookie、首页和构建资源通过。TypeSafe / Nace 模板可用，当轮没有自动创建对应生产渠道。
- 9 个启用渠道、12 个模型映射，三种聊天协议共 36/36 个真实流式测试通过，全部上报完整用量。公网 Chat 流式、Messages 和 Responses 非流式均 HTTP 200；Chat 样本含 259 个 SSE 事件及 `[DONE]`，首事件 0.415 秒、事件最大间隔 0.371 秒。
- 三笔调用总扣费 `0.0003729000 USD`，与 token × 价格 × 分组倍率、测试余额减少和 key 已用额度精确一致。验收账号已停用，会话撤销，测试 key 已停用；测试日志保留。
- 日常备份完成恢复演练，schema 9 的 15 张表逐表行数一致；验证库 `lingshu_restore_verify_20261023` 和 `lingshu_restore_release_4bef2a1` 曾保留供复核，2026-11-06 清理备份时删除。这是历史版本的恢复证据。
- 既有容器 ID、启动时间及重启计数保持不变，只对 Caddy 平滑 reload。数据库回环监听、应用网桥监听和三个服务 / timer 的开机启动验证通过。

DMIT 初始安装前备份和验收 JSON 保存在 root 私有目录 `/var/backups/lingshu-dmit-copy-20261023/`，包括原 Caddyfile、UFW 规则、既有数据库备份、快照 manifest、容器基线、协议计费与恢复演练结果。归档及数据库含密钥，保留在主机私有目录，不进入 Git。

#### 真实支付（2026-10-24 UTC）

多支付版本 `ba139c4b7b5df55fd8c81d39581b380bfd3bdbe2` 在 DMIT 完成 schema 13 迁移，原有用户、模型及余额调整流水完整保留。发布前备份为 `/var/backups/lingshu/lingshu-20261024T045936Z-pre-ba139c4.dump`，SHA-256 `80ff538eb4c01182b7bdff4919ce442b96398a9054536ccd3369bac193f87afc`；旧程序和标记为 `/opt/lingshu/lingshu.prev-ba139c4`、`/opt/lingshu/release.prev-ba139c4.json`。

使用现有管理员验收订单 `149dd1a4f9e707e1610e21d2317e3ca5`，报价 CNY 6.70、到账 USD 1，通知地址为 `https://gateway.piqode.com/api/payments/epay/1/prod/notify`。用户实际付款后，`2026-10-24T05:04:08Z` 收到有效成功通知并自动入账；独立向 ZPAY 查单确认成功、订单一致、实付 CNY 6.70。余额从 `$9.8493093969` 增至 `$10.8493093969`，仅一条 USD 1 流水，operator 为空；连续两次核对无重复入账。

付款后备份 `/var/backups/lingshu/lingshu-20261024T050524Z-post-epay-paid.dump`，SHA-256 `6e06cbae717fa3d0d02e323cc4c1778e1accb7f9f2881951205123178cd8ec1f`，通过 `pg_restore --list`。验收后通过管理接口开启 EPay 及充值总开关，配置版本为 2、通知版本为 1，钱包显示支付宝；这些是验收时的配置与余额，当前值以管理接口为准。Stripe、Creem、Waffo、Pancake 只有协议和 SDK 模拟测试记录，真实付款尚待验收。

#### DeepSeek 峰谷规则配置（2026-10-23）

DMIT 模型 ID 1 `deepseek-v4.1-flash` 在保留周末谷价的基础上补齐 UTC+8 18:00–09:00 和 12:00–14:00 两条谷价。通过管理接口保存，接口与数据库回读均为三条，`TestDeepSeekOffPeak` 边界测试通过。基础输入/输出/缓存读/缓存写价格为 0.30/1.20/0.006/0，谷价为 0.15/0.60/0.003/0（USD / 百万 token），按请求开始时间匹配，历史账单不追溯。

改前备份 `/var/backups/lingshu/lingshu-20261024T051740Z-pre-deepseek-time-rules.dump`，SHA-256 `1f9800dcf05cbb34570a69ed3df4dbee579eaa0c0e57607b1d282454211cf7e6`，通过 `pg_restore --list`；同前缀 `-model-before.json` / `-model-after.json` 保留修改记录。

#### 注册管理（2026-10-24）

`59d8759e87155ff453301db82572d3de67c5b592` 发布时完成 schema 14 迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T094052Z.dump`，SHA-256 `9c57620331c300caccb16cc9b9f07d6f3bc996f48fa800bfc7f62412f0460648`，通过 `pg_restore --list`；旧程序与标记为 `/opt/lingshu/lingshu.prev-59d8759`、`/opt/lingshu/release.prev-59d8759.json`。当次服务、内部及公网健康检查通过，公网注册状态为 `{"enabled":false}`，未生成注册码或新增账户。

## 三、被取代的决策（原 `docs/decisions.md`）

### 上游账户（2026-10-19，2026-10-27 已取消）

以下为历史决策，已被上方「上游余额查询」取代，保留供追溯。

用户提出估算各厂商账号余额，作为运维工具，和站内金额完全隔离。

- 余额记在"上游账户"上（你在厂商那里的账号）。2026-10-20 改为**并入渠道页**：一个渠道一把 key，对应唯一一个账户（同一账号开两把 key 没有意义，限流和余额都按账号算）。渠道列表有"余额"列，点开是余额面板（充值、校准、流水、设置）；删除渠道时余额估算和流水一起删。以后一个渠道支持多把 key 时，每把 key 各对应一个账户。
- 站内统一美元（用户决定）：录入和显示按当前用户的货币偏好换算，库里存 USD；充值、校准流水另存输入时的原始金额和币种。改系统汇率会让人民币账户显示的余额跟着变，下次校准纠正。
- 上游成本 = 站内基础价（不乘分组倍率）× 账户的成本系数，默认 1。以后有需要再加按模型单独设上游价。
- 上游成本按上游的计费方式记，和是否向用户收费无关：上游中途断流、没给 usage 但已经开始输出时，按估算算成本（用户不扣）。成本记在 `request_logs.upstream_cost`，同时扣账户估算余额。
- 校准：把估算余额改成真实余额，差额记为"偏差"，并显示距上次校准的估算花费。成本系数默认 1，也是调平系数：由人或 AI 看流水里的偏差手动调，不做自动建议（用户决定，别搞复杂）。
- 自动校准：DeepSeek（`/user/balance`，取 `total_balance`）、Kimi（`/v1/users/me/balance`，取 `available_balance`，含代金券）每小时用关联渠道的 key 查一次；`moonshot.cn` 记人民币，其他域名记美元。其他厂商只能手动校准。
- 提醒线目前只在页面标红；邮件等通知和"服务挂了提醒"一起做。

### 统一账户日志（2026-10-25 确认，2026-11-04 被「日志体系重构」取代）

> 调用与资金混在一张表的展示方式已废弃，以下仅作历史记录，现行规则见下一节。

- 全站日志与用户账户日志统一展示模型调用、已入账在线充值及人工额度调整；用户只能查看自己，钱包和用户编辑窗口的流水保留。
- 复用请求记录、余额流水和关联订单，在数据库合并排序分页，不新增日志副本或迁移。未传 type 的日志接口保持原请求查询行为。
- 消费、请求及 tokens 只统计调用，充值到账和额度净调整独立统计，分页总数不再使用请求数。充值详情只公开订单摘要，禁止返回支付配置和收银台数据。

### 部署使用模式的原始版本（2026-10-25，2026-11-05 已调整）

- 系统设置展示自用、公司团队网关、商业中转三种模式；现阶段仅允许选择公司团队网关。API 同样拒绝其他模式值，避免绕过界面限制。未配置时默认公司团队网关。

### 充值订单单独成页（2026-11-06，同日被“充值并入操作日志”取代）

- 充值订单从「系统设置 → 支付设置」挪到「全站日志 → 充值订单」（仅管理员）：设置页只放设置。可按时间、用户、状态（待付款 / 已到账 / 失败 / 已过期）筛选。成功到账的订单同时出现在操作日志的“充值到账”；成员仍在「钱包充值」看自己的订单。

### MVP 范围与初始上游（2026-10-18）

**做**
- 网关：三个入口透传、key 鉴权、按优先级选渠道并重试、结算扣费、每次请求一条日志（用户、key、模型、渠道、token、金额、耗时、成败）。
- 管理端：用户（创建、角色、余额、用户分组成员）；用户分组（名称）；服务分组（倍率、启停、所有用户 / 指定用户分组、渠道归属）；模型（标准名 + 4 个价格）；渠道（模板、地址、key、协议、优先级、所属服务分组、拉取模型、名称映射、冒烟测试）；全站日志。
- 用户端：登录、余额、可用分组及其模型和价格、key 管理（名称、分组、限额）、自己的日志。

**初始 MVP 未做**：接入教程、统计图表（日志能筛选、能看汇总金额就够）、真实充值、注册、邮箱、人机验证、SSO、原始内容留存、跨协议转换、安全加固。

**初始 MVP 上游**：DeepSeek、Kimi、GLM、Qwen、Xiaomi MiMo、StepFun。后续上游扩展见下方记录。
- 模型（站内标准名 → 上游模型名）：`deepseek-v4.1-flash` → `deepseek-flash`；`GLM-5.3`；`GLM-5.3-FlashX`；`mimo-v2.6-pro`；`kimi-k3`；`kimi-k2.7-code`；`step-5-preview`；`qwen3.8-max`；`qwen3.8-flash`。没写箭头的，上游名和标准名相同。
- 2026-10-20 加入：`MiniMax-M3.1-Flash`（MiniMax 渠道）、`doubao-seed-2-1-pro` → `doubao-seed-2-1-pro-260915`（火山方舟渠道 volces）。豆包没有国际站价，按中国站价换算；缓存存储费（按小时）不计。`GLM-5.3-FlashX`、`kimi-k2.7-code` 于 2026-10-18 加入；kimi-k2.8 没有按量计费接口，不接。
- 2026-10-21 加入美国模型的站内价（官方价，USD / 每百万 token，输入 / 输出 / 缓存命中 / 缓存写入）：`claude-fable-5-1` 10 / 50 / 0.25 / 12.5；`claude-opus-5-5` 4 / 20 / 0.2 / 5；`claude-sonnet-5-5` 2 / 10 / 0.2 / 2.5；`gpt-6-astra` 10 / 50 / 1 / 12.5；`gpt-6-sol` 2 / 10 / 0.2 / 2.5；`gpt-6.1-sol` 2 / 10 / 0.1 / 2.5（原为 cctq 中转渠道填的 0.3 / 1 / 0 / 0.1，按用户要求改为官方价）；`gpt-6-luna` 0.1 / 0.5 / 0.01 / 0.125。来源：Anthropic 定价文档（Claude Code 内置资料，2026-10-06）与 OpenAI 模型页（用户委托查证）。DeepSeek 峰谷价（2026-10-23 调整）：工作日仅北京时间 09:00–12:00、14:00–18:00 为高峰价，其余时段与周末全天为低谷价，低谷为高峰价 5 折。按自然星期和时段计价。基础价 0.3 / 1.2 / 0.006 / 0 为高峰价，低谷价 0.15 / 0.6 / 0.003 / 0，由“仅周末”规则和两个低谷时段组成。阶梯价（2026-10-21 配好）：GPT 四款"输入超过 272K"整单输入和缓存 ×2、输出 ×1.5（gpt-6-sol 4 / 15 / 0.4 / 5，gpt-6.1-sol 4 / 15 / 0.2 / 5，gpt-6-astra 20 / 75 / 2 / 25，gpt-6-luna 0.2 / 0.75 / 0.02 / 0.25）；`claude-haiku-5-5` 基础 0.1 / 0.5 / 0.01 / 0.125，"输入超过 100K" 0.5 / 2.5 / 0.05 / 0.625。**推算值**：GPT 长上下文档的缓存写入（官方只给"未缓存输入价 × 1.25"的规则）、Haiku 5.5 的缓存命中和写入（按 Anthropic 通用的 0.1× / 1.25× 推算），官方页面没有直接印出这几个数。
- 2026-10-21 经 OpenRouter 接入（渠道 #12，优先级 -1）：Claude Fable 5.1 / Opus 5.5 / Sonnet 5.5 / Haiku 5.5、GPT-6 Sol / 6.1 Sol / Astra / Luna，以及新加的三个，2026-10-21 按厂商官方价（用户委托查证）：`gemini-4-argon` 2 / 10 / 0.1 / 0 为限时价，规则"2026-12-01 起"4 / 20 / 0.2 / 0（Google 官方：12-01 起翻倍）；`grok-4.7` 2 / 6 / 0.5 / 0，规则"输入 ≥ 200K"（填 input_above 199999）整单 4 / 12 / 1 / 0，上下文 500K；`muse-wonder`（官方 ID `muse-wonder-1.0`，OpenRouter 上是 `meta/muse-wonder`）4 / 20 / 0.2 / 0。三家都没有文档化的缓存写入费。gpt-6.1-sol 同时挂在 cctq（优先级 0）上，OpenRouter 作兜底。
- Kimi 账号目前处于 Moonshot 限流 Tier0（RPM 3、并发 1），多人同时用会 429；累计充值 ¥50 升到 Tier1（RPM 100）。用户决定不单独处理，随用量充值自然升档。2026-10-19 已升到 Tier1（并发 15、RPM 100）。

### 中转分组合并（2026-11-05 确认）

- `Relay-GPT`（倍率 0.2）分组下已无渠道：四个第三方中转渠道（dongtoken、kinght claude0.8、kinght gpt-94分、咔咔-gpt0.18）和自用 key 都已迁到 `Third-party Relay`，该分组随之删除。
- lsh 的 key「dddd」原本挂在 `Relay-GPT`，删除前迁到 `Third-party Relay`，沿用该组 1 倍率。原来 0.2 倍率的折扣随分组删除取消。
- 分组删除后，历史请求日志的 `group_id` 按外键规则置空，金额、倍率、模型等字段保留。

### 混元 Hy4 正式版定价（2026-11-05）

- 腾讯混元 Hy4 正式版（2026-11-04 发布）上线后与 preview 同价：中国站 ¥6 / ¥18 / 缓存命中 ¥0.3，按现行汇率 6.7 换算成站内价 $0.895522 / $2.686567 / $0.044776，缓存写入记 0。
- 正式版作为独立模型 `hy4` 加入，映射到 Hunyuan 渠道（同名映射上游模型 `hy4`）。`hy4-preview` 保留：上游 `/v1/models` 仍标 online，用户侧两个模型并存。


## 邮箱注册开发前的注册约定（被 2026-11-08 决策取代）

- 系统设置新增“注册管理”。注册开放程度默认“仅手动添加”；管理员可切换到“注册码注册”。“开放邮箱注册”作为未开放选项展示，前后端都不允许启用。
- 注册码由管理员批量生成，每批 1–100 个；每个注册码只能注册一个普通用户。注册码明文只在生成时返回，数据库只保存哈希；管理端可查看可用、已使用、已撤销状态并撤销未使用码。
- 开启注册码模式后，登录页显示注册入口。用户用注册码、用户名和至少 12 位密码注册，成功后自动登录并加入默认用户分组；注册码核销、创建用户、创建会话在同一事务完成。关闭该模式后拒绝自助注册，已有账号及注册码记录保留。
- 邮箱注册、邮箱验证和邮件发送不在本次实现范围内。
- 设置接口支持分别保存汇率或支付开关；旧客户端只提交汇率时保留支付开关状态。


## 邮箱注册与 Outlook 主备发信验收（2026-11-08）

- 独立工作区 `~/worktrees/lingshu-email-registration`、分支 `codex/email-registration`；新增迁移 `0032_email_registration`，旧用户邮箱保持空值，用户名密码登录不变。三种注册模式互斥，邮箱模式需要至少一个启用且授权检查通过的发件账号。
- Graph 按账号缓存 Access Token 并串行刷新，轮换 Refresh Token 写回数据库；缓存与写回均核对配置，旧授权检查结果不会覆盖新配置。按主备顺序发送；429 遵守 Retry-After，超时及结果不明不自动重发，202 只标记“已提交”。
- 验证码数据库原子抢占发送资格，随机 6 位、哈希保存、15 分钟有效、60 秒冷却、5 次错误上限；并发注册仅一次成功，用户创建、默认分组、审计和会话同事务。用户名冲突回滚后仍可使用验证码。
- 独立本地验收库通过管理接口导入私有文件第 3～7 个 Outlook 账号，一主四备；五个账号逐一授权检查、各向 `support@piqode.com` 发一封明确标注的测试邮件，Graph 全部返回 202，五个账号的已发送记录均核对通过。收件端验收独立于 Graph 接受结果。
- 受控 Outlook 收件箱真实收到验证码，完成普通用户注册、默认分组、自动登录及退出后的用户名密码登录；最终二进制重新检查五个账号授权通过。测试凭据已通过管理接口删除，测试用户停用，会话与验证码已清除，验证服务已停止；独立测试库和工作区保留复核现场，生产未改动。
- 浏览器回归覆盖邮箱发码/PoW/倒计时/自动进入控制台、注册码与手动模式、用户名密码登录、发信账号管理、授权状态门禁、凭据不回填，以及中文/英文 375px 手机布局；CI 已加入该回归。前端类型检查与构建通过，现有 bundle 大小提示仍在。
- 发布状态：分支开发验收完成，交付 PR 与 CI；由维护者决定合并，合并后沿用自动发布，生产账号导入和开放步骤见 `deploy.md`「邮箱注册与 Outlook 发信」。


## 开放注册与可选验证限制（2026-11-08）

- 用户调整需求：管理员手动添加保持原样；“开放注册”作为总开关，开启后显示“注册码验证”“邮箱认证”可选限制。无验证限制时仅填写用户名和密码；两项都选时需要同时满足，使用同一注册表单。
- 取代此前 manual / code / email 三种互斥注册方式。旧配置保留原有开放程度和验证要求，升级不自动扩大注册范围。
- 独立 PostgreSQL 库验证通过：总开关与限制配置、旧模式回退、无验证注册、单项注册回归、双项限制事务回滚、错误验证码计数、已关闭接口拒绝和无发信授权仍能关闭注册。全量 Go 测试中与改动无关包通过，修正审计字段新增后的断言后 API/store 新鲜全量通过；go vet 与格式检查通过。
- 关闭总开关保留验证条件偏好，再次开启时沿用原要求；发信账号失效时仍可关闭注册。无需新增 SQL 迁移，生产设置在本次 PR 合并前保持原状。
- 前端 TypeScript 与生产构建通过；浏览器回归覆盖关闭注册、四种验证组合、旧配置兼容、发信账号操作、用户名密码登录及中英文手机布局。现有构建包体积提示保留。按 neat-freak 核对现行决策、部署操作和文档链接，交付 PR，生产未变更。


## 旧版 PiQode 协议同步与注册同意（2026-11-08）

- 只读核对旧站 `options`：`legal.user_agreement` 为 4016 字符、`legal.privacy_policy` 为 1374 字符；原文取到项目外文件，未改写条款，不放入公开仓库。
- 新增公告下方两份协议的管理设置、公开阅读页及注册同意校验；生产原文导入在功能合并发布后通过管理接口进行，并核对内容一致。
- 新增迁移 `0033_legal_terms`，给用户添加可空同意版本与时间字段；独立 PostgreSQL 全量 Go 测试、go vet、格式与差异检查通过。原文保存不修改空白，公开读取与保存内容一致；缺少同意或版本过期在核销注册码/验证码前拒绝。
- 前端类型检查与构建通过，全部 9 项浏览器回归通过；包含必选勾选框、链接保留注册输入、协议读取失败及重试、原文纯文本渲染、后台分别保存、中英文和手机布局。后端空白判定与前端一致，保存原文不 trim；文档链接与规则一致性检查完成。


## 2026-11-08：协议范围更新

原有“MVP 只做同协议透传、跨协议以后处理”的决策由 Responses → Messages 方案取代。原生入口继续保留；本期只新增 Responses 客户端到 Messages 上游，按现有渠道顺序自动选择，其他方向单独规划。实施和验收在 codex/responses-messages 独立工作区完成，PR 待用户决定合并。


## 2026-11-08：Responses → Messages、显式压缩和客户端 WebSocket

- 分支 `codex/responses-messages` 基于 `102e740`，独立 worktree；增加直接转换器、按上游协议路由与原始 Messages 用量结算、显式摘要和自包含回灌、连接内 WS 续接。模型/渠道标明 Responses（转接），日志增加上游协议、用途和转换诊断。
- 新增迁移 `0034_log_upstream_protocol.sql`，只添加日志字段、按旧 protocol 回填 upstream_protocol；用户余额、定价和生产渠道配置不变。
- Codex CLI 0.160.0 在本机隔离库、固定渠道 23 完成 HTTP/WS 的真实 exec_command、custom apply_patch、读回及二次修改；手动和低阈值自动摘要后恢复随机标记与文件阶段；OpenAI 名称分支的 compaction_trigger、legacy compact JSON 回灌、签名思考回灌、取消、重连和失效引用后完整重发通过。完整证据和字段语义见 [转接验收](research/responses-messages.md)。
- 最终真实验收隔离账本 110 条：105 success、2 cancelled、2 upstream_error、1 rejected；成本 $1.4786164，按四桶单价重算差异 0。加前期压缩实验约 $0.057872，累计约 $1.5365；预算 USD 10。两条取消使用现行估算规则，失败不扣费。真实 Codex turn/interrupt 返回 interrupted；显式 response.cancel 返回 cancelled。
- 全量独立 PostgreSQL 测试、go vet、前端构建、10 个浏览器脚本通过；转接/WS 纯测试另有 race 检查。Vite 单包超 1600 kB 的体积提示保留，构建成功。原生 Chat/Messages/Responses/System One 回归保持通过。
- 交付状态：PR 待合并；生产未部署。规则与协议说明已对齐，记忆只读；独立 worktree 和验收现场保留供复核。

## 2026-11-08：四处后台列表点击行编辑

- 渠道、模型定价、用户和服务分组列表移除行内“编辑”按钮，点击行内非操作区域打开原有编辑窗口；可编辑行显示指针并支持 Enter / Space。
- 行内测试、删除、启停、额度查看和拖拽各自独立，弹出层事件与文本选择不连带打开编辑。普通管理员仍不能编辑超级管理员账号。
- 前端类型检查和构建通过；channels / models / users 三个浏览器脚本通过，覆盖按钮消失、行点击、键盘、删除取消、启停、真实拖拽排序、用户权限、中英文及手机布局。现有 Vite 包体积提示保留。
- 基于 main `102e740` 的独立分支 `codex/row-click-edit`，不包含协议转接 PR #58；交付 PR，生产未发布，工作区保留供复核。

## 2026-11-08：Agent 配置提示词更新

- 概览页“Agent 配置”替换为用户提供的英文提示词：先获取模型并自动选择最低价适用模型做小额测试，失败输出脱敏错误、诊断和网站检查项并等待修复；成功后询问目标 Agent、备份和修改用户级配置，说明重载方式并汇总。配套介绍同步为先测试再配置，地址仍按当前部署设置填入，复制按钮复用同一提示词内容。
- 前端类型检查、生产构建与 overview 浏览器回归通过，检查新提示词、部署地址、步骤顺序、工具列表、密钥占位和手机宽度；既有 Vite 包体积提示保留。初次本地回归因复用其他 worktree 的依赖未完成页面加载，改为本工作区独立安装锁定依赖后通过，依赖清单未变。
- 按 neat-freak 核对受影响文档与实现；基于 main `8d0a876` 的独立分支 `codex/agent-setup-prompt`，交付 PR，生产配置不变，复核工作区保留。

## 2026-11-08：用户分组、启停开关与日志刷新

- 用户列表“可用服务分组”改为“用户分组”，显示真实所属用户分组标签，无分组显示“无”；可用服务分组继续在编辑窗口预览。“状态”标签改为“开关”，仅提交 enabled，停用需确认，请求中按行显示加载状态，失败保留原值，保留自己与超级管理员账号的权限保护。
- 渠道列表“协议”表头改为“原生协议”；调用日志“全部状态”右侧新增刷新按钮，复用原加载流程刷新列表和统计，保留筛选与页码。全站日志与账户日志共用这一交互。
- 前端类型检查与构建通过；users、channels、activity-logs 三项浏览器回归通过，覆盖用户分组与服务分组区分、启停及确认取消、失败状态、按行加载、权限、点击开关不打开编辑、中英文、手机布局，以及刷新请求保持模型、状态、日期与页码。既有 Vite 包体积提示保留。
- 按 neat-freak 核对受影响决策与代码；基于 main `8d0a876` 的独立分支 `codex/user-list-controls`，只交付 PR，生产保持现状，复核工作区保留。此前四处点击行编辑已通过 PR #59 发布为 `8d0a876` 并完成线上验收。

## 2026-11-08：模型检测并发上限调整

- 按用户要求，全站同时运行的模型检测任务上限由 2 个调整为 5 个；达到上限仍拒绝新任务，现有任务执行方式不变。
- 同步现行决策与计划状态；`git diff --check`、`go test ./internal/probe ./internal/api` 通过，待 PR 合并发布。

## 2026-11-09：首轮安全加固（PR #63，未发布）

- 独立工作区 `~/worktrees/lingshu-security-first`、分支 `codex/security-first`，基于 main `7cb680f`。登录与 challenge 分别使用每 IP 30 次/分钟、突发 10 的令牌桶，在查询数据库与 bcrypt 前拦截；429 包含 Retry-After 和等待提示。闲置 IP 桶和已消费 PoW 按期清理，现有失败锁定保留。
- 登录、注册、发码、登出和会话写接口检查 Origin / Referer / Fetch Metadata，带请求体时要求 JSON；无来源头 CLI、无体动作、模型 API 和支付通知保持原有使用方式。登出 Cookie 补齐 Secure 和 SameSite=Lax。
- Caddy 模板仅处理 gateway 站点：remote_ip 放行 Cloudflare 官方 IPv4 15 条、IPv6 7 条（2026-11-09 核对），直连与伪造转发头拒绝；添加五项安全头及旧站扫描路径规则，保留 SSE 和 WS。官方 v2.11.9 源码确认活动 HTTP-01 挑战在用户路由前处理，TLS-ALPN 位于握手层；真实证书续期留待获准发布时核对。
- 本地专用 PostgreSQL 库全量 `go test -count=1 -p 1 ./...`、`go vet ./...` 和相关 race 测试通过；覆盖限流、冷却恢复、独立 IP、清理、来源校验、登录、注册、后台操作与支付。首次共用测试库遇到并行重建 schema，改用本任务独占库后通过。
- 前端类型检查与生产构建、登录限流提示、注册、用户、协议浏览器回归通过；新增回归确认 429 提示显示后不密集重试。现有 Vite 包体积提示保留。应用提交 `c6d732c` 的 CI Go / 前端及全部浏览器回归通过。
- `python3 scripts/test-caddy-security.py` 使用固定 `caddy:2.11.9-alpine` 与本地模拟上游通过：模板 validate、真实连接来源允许/拒绝、伪造头拒绝、X-Real-IP 覆盖、安全头、扫描路径、HTTP 308、SSE 即时首事件、WS 101 及数据帧；已加入独立 CI job。不消耗真实模型额度。
- 按 neat-freak 核对 decisions、plan、deploy 与实现；初始 PR #63 单独交付，应用未发布、现役 Caddy 未修改；Caddy 的备份、候选校验、原位更新、平滑 reload、低频检查及回退步骤见 deploy。工作区和独立测试库保留复核，未修改规则或用户记忆。

- 后续用户授权处理全部未合并 PR：#62 已合并，#58 与 #63 分别完成集成检查后合并发布，保留各自验收证据。


## 2026-11-09：三个 PR 合并发布与转接产物复验

- PR #62（模型检测并发 5）、#58（Responses → Messages 转接）、#63（首轮安全加固）合并到 main。应用 `eba864f` 于 2026-11-08T18:02:18Z（北京时间 11-09 02:02）自动发布，程序 SHA256 `50b0bc0c15487fa3ca0689bf77132b9989aafc8ee859d81e51dc062c39413532`，数据库 schema 到 `0034_log_upstream_protocol`，发布前备份 `lingshu-deploy-20261108T180158Z-eba864f.dump`。
- Caddy 站点防护单独发布于 2026-11-08T18:13:39Z（北京时间 11-09 02:13），生产配置 SHA256 `fbc78bd7580f2cb7bc22d69441986962f604c1830cb695e20ae3d1a62ca57b4a`，备份 `/opt/newapi/Caddyfile.pre-lingshu-security-20261108T180518Z`。首次发布脚本用 urllib 默认 UA 被 Cloudflare 1010 拒绝并自动回滚，改用 Lingshu-release-check UA 后发布成功，未改 Cloudflare。旧站、公开 health 与内部 health 均 200；cancel 语义按源码与 mock 在生产 Caddy 2.11.4 确认通过，`flush_interval -1` 未改。
- #58 合并前后共解决 4 处冲突：渠道列表保留 main 的“原生协议”表头并保留转接标记，CI 配置保留 auth-security 浏览器回归与 Caddy 隔离防护 job 并加入转接回归；CI 的 Go 测试、前端与浏览器回归、Caddy 隔离防护三项全绿。
- 发布产物最小真实复验（隔离库 `lingshu_bridge_acceptance`、渠道 23 隔离上游，非生产路由）：用与线上一致的二进制哈希复跑 HTTP 最小文本和同一个 WS 连接内的预热、生成、续接。三条成功请求的日志均为 `protocol=responses`、`upstream_protocol=messages`、`operation=generation`，四桶合计 input 2040、cache_read 0、cache_write 0、output 83，逐条按四桶重算与 cost 列不一致条数为 0；隔离账本由 $1.4786164 增至 $1.4884364，实际花费 $0.00982。另有 1 条请求被上游内容策略拒绝，0 token、0 扣费。WS 预热不计费也不留调用日志。
- 最终低频线上检查：公开 health 与登录页 200、匿名管理接口 401；跨域、null origin、其他子域和 cross-site 请求 403，表单请求 415，同源与无来源 CLI 的损坏 JSON 400（到达解析器，未实际登录）；`/v1/responses` 带恶意 Origin 但无 key 返回 401，仍走独立鉴权。外部 curl `--noproxy '*' --resolve` 到源站 IP 为 403，附加 X-Forwarded-For、X-Real-IP、CF-Connecting-IP 伪造 Cloudflare 来源仍 403；`/.env`、`/.git/config`、`/wp-login.php`、`/actuator/health` 路径 404，HTTP 308，五项安全头取值与模板一致。运行中的二进制 SHA 与清单一致（`50b0bc0c…`），eba864f 发布前 dump 校验通过，四个服务与 timer 均 active，Caddy 配置 SHA `fbc78bd7…`，旧站 200，SSH 正常。
- 保留的既有提示：Caddy 模板校验通过但仍有一条格式 warn（Caddyfile 未按 `caddy fmt` 排版），未额外格式化配置；既有的 Vite 单包超 1600 kB 体积提示保持不变。
- 生产路由的真实模型调用未在本轮验收，本轮只做入口与鉴权的低频检查。


## 2026-11-09：渠道协议列表头与转接标签弱化

- 用户调整：渠道列表的“原生协议”列改回“协议”，转接的 Responses 保留但显示更浅。列头改为「协议 / Protocols」，只有 Messages 地址的渠道继续标出「Responses（转接）」，去掉 cyan 强调色，改为浅底细边加三级文字色（跟随明暗主题）；模型页和调用日志的转接说明不变，网关行为不变。
- 浏览器回归同步：渠道列表断言列头为「协议」、转接标签存在、标签文字色等于三级文字色且不同于原生协议标签；英文界面断言列头 Protocols 与 Responses (bridged)；浅色主题下复验同一颜色规则。
- 基于 main `7a07de7` 的独立分支 `codex/channel-protocol-label`，交付 PR 待合并；生产未改动。

- 验证：npm run build、bridge.browser.cjs（中文/英文、深色/浅色）和 channels.browser.cjs 均通过；保留既有 Vite 单包体积提示。
