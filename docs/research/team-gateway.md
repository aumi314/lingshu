# Lingshu 团队网关深度研究

> 研究时间：2026-11-11（Asia/Shanghai）  
> 研究对象：Lingshu 从“商业中转”向“团队网关”的产品与架构演进  
> 交接用途：供主 agent 继续拆需求、改 `decisions.md`、设计迁移方案

## 结论先行

Lingshu 已经有团队网关的半套内核：用户、角色、用户分组、服务分组、API Key、模型价格、渠道路由、预扣费、结算、额度流水、调用日志和操作日志都存在。当前缺的是团队产品的“归属链”和“成本控制链”：谁拥有一个项目、哪个应用使用哪个 Key、预算由谁承担、预算触顶如何处理、管理员能看到哪些内容、一次调用的成本如何归属到团队和项目。

团队网关不应继续沿着“给每个成员发一个余额账户”的路径扩张。更稳定的模型是：

```text
组织（Organization）
  └── 团队 / 成本中心（Team）
        └── 项目 / 应用（Project）
              └── API Key / Service Account
                    ├── 模型权限与服务分组
                    ├── 预算、限流、并发
                    ├── 路由策略
                    └── 用量、成本与审计
```

个人账户余额可以作为兼容层保留，团队模式的新调用应优先按项目预算准入，再按组织策略决定是否允许个人透支或备用池支付。

报告中的外部事实来自官方文档或项目源码；“建议”是针对 Lingshu 的设计判断，不能当作已经批准的需求。

## 一、Lingshu 当前基线

### 已有能力

- 四个调用入口：Chat Completions、Messages、Responses、System One；Responses 与 Messages 双向转接。
- 用户、管理员、超级管理员角色；用户分组控制服务分组可见范围。
- 服务分组绑定渠道，模型定价独立于渠道，API Key 绑定服务分组。
- 用户余额与 reserved 预扣；API Key quota limit / quota used；失败重试、渠道冷却、并发和频率限制。
- 请求日志记录协议、模型、渠道、usage、成本、尝试次数、TTFT、总耗时、失败渠道等；操作日志记录改前改后。
- 金额使用 PostgreSQL `NUMERIC(20,10)` 与 Go decimal；发布、备份、迁移和审计已有运维合同。

证据：`docs/plan.md:10-110`、`docs/decisions.md:1-35,145-190`、`internal/store/migrations/0001_init.sql:1-150`。

### 迁移断点

当前商业中转模式的关键约束是“用户拥有余额，Key 消耗用户额度，服务分组决定倍率”。团队模式需要增加一条独立的付款归属：

```text
调用者身份 -> 项目 -> 成本中心 / 预算 -> 计费价格快照 -> 上游成本 / 内部转移价格
```

不能把 `users.balance` 改名成“团队余额”就结束。这样会导致：成员离职后历史成本归属不清、一个用户多项目无法分摊、服务账号无法脱离个人、管理员无法设置项目级硬上限。

现有 `groups` 是服务分组，不应直接改成团队。它解决“哪些模型和渠道可用”，团队解决“谁拥有预算和数据”。两者要保持正交。

## 二、真实场景矩阵

| 场景 | 真实问题 | 需要的控制 | Lingshu 产品形态 |
|---|---|---|---|
| 团队共享模型 | 多人调用同一批模型 | 统一模型目录、团队预算、成员权限 | Team + Project + shared budget |
| 部门成本分摊 | 研发、产品、运营要分账 | 成本中心、项目标签、月度结算 | Cost center + immutable cost snapshot |
| 应用生产调用 | 后端服务不能使用个人 Key | Service Account、Key 轮换、最小权限 | Project-owned service key |
| 实验项目 | 允许试错但不能拖垮全组 | 日/月预算、模型白名单、软硬阈值 | Project budget + alert + hard stop |
| 高价模型审批 | Opus / GPT 旗舰模型贵 | 模型级预算、审批或白名单 | Model policy + approval state |
| 供应商故障 | 某渠道 5xx / 超时 / 失效 | fallback、冷却、sticky session | 现有路由内核 + project policy |
| 敏感数据调用 | 代码、客户资料、内部文档进入模型 | 内容留存开关、脱敏、区域 / provider 限制 | Data policy + provider allow-list |
| 个人临时使用 | 成员需要快速试用 | 默认项目、个人额度或审批池 | Personal project as compatibility layer |
| 离职与交接 | 人员变动不能破坏生产服务 | 项目所有权、服务账号、Key rotation | Identity detached from workload |

## 三、外部产品的共同结构

### 1. OpenAI Projects：把项目当作权限和花费边界

OpenAI 官方项目模型把成员、Service Account、API Key、项目使用范围和项目用量放在一起。项目可设置月度 spend limit、告警，并可启用 hard limit；hard limit 触发后请求会因组织或项目 spend limit exceeded 被拒绝。官方同时提醒硬限制不是瞬时生效，记录花费可能略超配置值。

来源：

- [Managing projects in the API platform](https://help.openai.com/en/articles/9186755-managing-projects-in-the-api-platform)
- [Spend limits](https://developers.openai.com/api/docs/guides/spend-limits)

对 Lingshu 的启发：项目是最小的预算和权限边界；“告警”和“硬停”必须分开；预算检查要接受异步结算导致的微小超额，并把预算状态写入日志。

### 2. LiteLLM：预算对象可以复用，权限和限流分层

LiteLLM 的网关定位是团队中央服务，公开能力包括 virtual keys、spend tracking、guardrails、load balancing 和管理后台。其预算表可被组织、项目、Key、终端用户、标签和团队成员关系复用，并包含模型预算、RPM、TPM、TPD、并发和预算重置时间。

来源：

- [LiteLLM README](https://github.com/BerriAI/litellm/blob/main/README.md#why-litellm)
- `references/litellm/schema.prisma:12-41`

对 Lingshu 的启发：预算不是 Key 的一个数字字段，而是可挂在不同归属对象上的策略；预算本身应该可被多个范围引用，成本统计要能按团队、项目、Key、模型和用户切片。

### 3. Bifrost：Virtual Key 是完整的授权对象

Bifrost 的 virtual key 可以限定 provider、model、上游 Key，并绑定 team、customer 或 business unit；同时可以附加 rate limit、预算、内容日志策略。预算可挂团队、Key、provider、model 或 customer；路由规则可按 global、team、customer、virtual key 作用域匹配，支持加权目标、优先级、fallback 和首 token 超时切换。

来源：

- [Bifrost governance](https://github.com/maximhq/bifrost/blob/dev/docs/deployment-guides/config-json/governance.mdx#virtual-keys)
- [Bifrost routing rules](https://github.com/maximhq/bifrost/blob/dev/docs/deployment-guides/config-json/governance.mdx#rule-fields)

对 Lingshu 的启发：API Key 不只是“认证字符串”，应成为权限、预算、限流、数据留存和路由策略的载体。灵枢已有 Key 绑定服务分组，可以继续增加 model allow-list、内容留存策略和 project_id。

### 4. New API：完整后台产品的近邻参考

New API 已把渠道、模型映射、渠道优先级、失败重试、渠道亲和性、多密钥、额度、订阅、用量日志、缓存计费、审计、OAuth/OIDC 和 Web 控制台放在同一产品中，协议覆盖 OpenAI、Responses、Anthropic Messages、Gemini 和 WebSocket。

来源：`references/new-api/README.zh_CN.md:134-157`、`references/new-api/model/token.go:15-34`。

对 Lingshu 的启发：后台对象和用户流程可参考，协议转换边界与价格模型仍应保持 Lingshu 自己的精度和证据标准。

### 5. Sub2API / CLIProxyAPI：账号池和配额调度

Sub2API 关注上游订阅账号的分发，包含多账号、API Key、token 级计费、粘性调度、并发和限流；CLIProxyAPI 关注多 CLI 账号的 OAuth、权重、优先级、sticky session 和 cooldown。它们提供的是“上游资源池”视角，团队网关需要把这个资源池放在项目预算和用户权限之后。

来源：`references/sub2api/README.md:194-209`、`references/sub2api/backend/ent/schema/usage_log.go:122-140`、`references/CLIProxyAPI/README.md:5-9`、`references/CLIProxyAPI/docs/management-api-v8.md:67-107`。

### 6. APISIX / zconv：入口治理和协议层边界

APISIX 的 AI Proxy Multi 展示了 provider 实例、优先级、权重、会话哈希、Consumer 鉴权和 token 限流的组合；zconv 则把协议转换拆成 Direct、IR、Identity 和事件流，明确区分能力缺失、语义拒绝和容量错误。

来源：`references/apisix/docs/en/latest/plugins/ai-proxy-multi.md:80-115`、`references/apisix/docs/en/latest/plugins/ai-rate-limiting.md:1281-1323`、`references/zconv/README.md:69-85`、`references/zconv/docs/ARCHITECTURE.md:3-19`。

## 四、外部 GitHub 项目的研究价值

这些项目适合补足 references 的视角，但需要先区分成熟度和问题类型：

| 项目 | 研究价值 | 适合吸收 | 不应直接照搬 |
|---|---|---|---|
| Otari | 团队 Key、预算、用量治理 | 预算准入、Key 作用域、团队控制台 | 具体 provider 适配和许可边界需再核验 |
| Spillway | 小团队和个人开发者体验 | 每人 / 每设备 / 每 Agent 配额、超预算降级 | 本地模型 fallback 不适合所有团队 |
| Portkey Gateway | 路由和可靠性 | retry/fallback、条件路由、权重、Guardrails | SaaS 控制面与本地数据面要拆开 |
| agentgateway | Agent 网关边界 | LLM/MCP/A2A 统一入口、JWT、RBAC、CEL、OTel | 过早引入会扩大协议和运维面 |
| Helicone AI Gateway | 高性能数据面 | 低延迟、缓存、观测和 provider failover | Rust 数据面不等于 Lingshu 需要重写 |
| Go LLM Gateway | 工程栈近邻 | Go + PostgreSQL + Redis 的预算和限流实现 | 具体项目活跃度和完整性需逐项复核 |
| 2papi / Routerly | 账号池和策略路由 | 预算、模型评分、账号选择 | 商业中转与团队网关边界容易混在一起 |

外部项目的仓库状态、版本、许可证和关键功能必须在真正采纳前单独复核。本表用于研究分工，不把 README 宣传语当作验收结论。

## 五、商业和云厂商的治理模式

### OpenAI

OpenAI 的官方项目设计给出一个很清楚的两层预算：组织 spend limit + project spend limit；项目内还有成员、Service Account、Key 和 usage 视图。硬限额会拒绝请求，告警只通知不阻断。

### Microsoft Foundry / Azure

Microsoft Foundry 把成本分析、Azure Cost Management、RBAC 和项目级 Quota / AI Gateway 结合。官方文档明确指出 Azure OpenAI 当前没有类似 OpenAI 的硬预算停止能力，预算更偏告警和治理。这个差异值得 Lingshu 直接吸收：软预算和硬预算是不同产品承诺。

来源：[Plan and manage costs - Microsoft Foundry](https://learn.microsoft.com/en-us/azure/foundry/concepts/manage-costs)。

### Google Cloud / Vertex AI

Google 把 quota 绑定到 project，Cloud Billing 可按项目和服务设置预算 / spend cap；文档强调 spend cap 触发并非瞬时，实际账单可能产生少量超额。这个模型适合 Lingshu 的“按项目、按模型、按时间窗口限制”，但要把估算成本和最终成本分开。

来源：[Quota project overview](https://docs.cloud.google.com/docs/quotas/quota-project)、[Manage spend cap budgets](https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps)。

### AWS Bedrock

Bedrock 的团队成本归属依赖 IAM principal、Project、Application Inference Profile 和 tags；模型调用日志可以落 CloudWatch 或 S3，日志包含 `identity.arn`、请求、响应和 token 元数据。AWS 官方还指出，网关代调用时，如果所有请求都使用同一个网关 IAM 身份，账单无法自然归属到最终用户，需要按用户 assume role、session tag 或 RoleSessionName 做归属。

来源：[Track usage and costs in Amazon Bedrock](https://docs.aws.amazon.com/bedrock/latest/userguide/cost-management.html)、[Model invocation logging](https://docs.aws.amazon.com/bedrock/latest/userguide/model-invocation-logging.html)。

对 Lingshu 的启发：成本归属不能靠事后猜用户名，调用进入系统时就要写入不可变的 organization_id、team_id、project_id、key_id 和 policy_id。

## 六、推荐的 Lingshu 团队网关设计

### 1. 对象模型

新增对象：

```text
organizations
teams
projects
project_members
service_accounts
project_api_keys
budget_policies
rate_limit_policies
model_policies
cost_centers
```

与现有对象的关系：

- `users` 保留为登录身份。
- `user_groups` 保留为成员集合 / 组织目录，不承担预算。
- `groups` 保留为服务分组，不改名成团队。
- `api_keys` 增加可选 `project_id`；团队模式下新建 Key 必须归属项目。
- `channels`、`models`、`channel_models` 继续作为供给侧资源。
- `request_logs` 增加归属快照，保存调用发生时的项目、团队、成本中心和预算策略。

### 2. 权限模型

建议把权限拆成四层：

| 层 | 示例 | 作用 |
|---|---|---|
| 身份角色 | org_owner、team_admin、project_admin、member、auditor、billing_viewer | 谁能管理对象 |
| 资源关系 | project_member、service_account | 谁属于哪个项目 |
| 模型策略 | allowed_groups、allowed_models、denied_models | 能调用什么 |
| 运行策略 | budget、RPM、TPM、concurrency、content_logging | 调用怎样被允许 |

不要用一个 `role` 字段承载所有权限。灵枢现有 `super_admin/admin/user` 可以作为平台级兼容角色，团队模式再增加资源级关系。

### 3. 预算设计

预算分成三个独立维度：

- **软预算**：达到阈值通知，继续服务。
- **硬预算**：预扣失败后拒绝新请求。
- **授权预算**：允许某个高价模型的最大额度，不能被普通余额替代。

预算至少支持：

- 组织、团队、项目、Key、用户、模型五个挂载层级。
- 日、周、月和自定义窗口。
- USD 成本和 token 数两种计量。
- 预算重置时间和时区。
- 预算告警阈值，例如 50%、80%、100%。
- 并发、RPM、TPM 独立于金额预算。
- 预算快照写入每次请求，避免规则修改后历史成本重新解释。

准入计算：

```text
effective_policy = min(
  organization_policy,
  team_policy,
  project_policy,
  key_policy,
  model_policy
)
```

这里的 `min` 只适用于硬上限。允许模型集合应做交集，明确拒绝集合优先；告警策略则合并，不能简单取最小。

### 4. 成本分摊

每次请求至少记录：

```text
organization_id
team_id
project_id
cost_center_id
user_id
service_account_id
api_key_id
model_id
channel_id
pricing_snapshot_id
budget_policy_id
estimated_cost
final_cost
```

要区分：

- 上游真实成本
- 团队内部结算成本
- 对成员展示的成本
- 预算占用成本

同一次调用可以只有一笔上游成本，但被多个内部报表切片。切片不能重复扣款。

### 5. 内容与审计

团队模式需要两条日志链：

1. **操作审计**：谁创建项目、改预算、改模型权限、生成或撤销 Key。
2. **调用审计**：谁在何时通过哪个项目调用哪个模型、走哪条渠道、花费多少、是否重试。

原始请求 / 回复不能默认无限保存。建议增加：

- retention policy
- content logging on/off
- redaction policy
- project 级敏感内容开关
- 管理员读取原文的独立权限和审计
- 加密存储与异步归档

灵枢已有“原始内容以后存云盘”的方向，但团队模式应该先把归属、留存期限、读取审计设计好，再接存储后端。

### 6. 路由与预算的关系

路由顺序不应只看渠道优先级。建议按以下顺序：

```text
1. 身份和项目权限
2. 模型与服务分组允许集合
3. 预算 / 限流 / 并发预检
4. 选择渠道与上游 Key
5. 首事件前重试和 fallback
6. 实际 usage 结算
7. 写调用审计与成本快照
```

供应侧故障不能消耗用户预算；已发出首字节后的断流要按现有规则处理，不能因为换渠道而重复计费。

### 7. 管理后台

建议新增“团队”顶层入口，但内部按工作流组织：

- 组织概览：预算、余额、项目、活跃 Key、错误率。
- 团队：成员、项目、月度成本、权限模板。
- 项目：模型白名单、服务分组、Key、预算、限流、日志策略。
- 成本：按团队 / 项目 / 用户 / 模型 / 渠道筛选，显示估算与最终差异。
- 审计：预算、权限、Key、内容读取和配置变更。
- 策略：模型目录、默认预算模板、昂贵模型审批。

后台首页不要先堆统计卡片，管理员的第一任务通常是回答三个问题：哪个项目在花钱、哪个项目快超额、哪个调用失败正在影响用户。

## 七、实施顺序

### 阶段 0：模式和数据契约

先确认 `company_team_gateway` 的正式语义，补 `decisions.md`：组织、团队、项目、Service Account、预算、成本中心、日志保留、个人余额兼容策略。

验收：不改线上数据，能画出实体关系、权限矩阵、预算优先级和迁移回滚边界。

### 阶段 1：项目归属和 Key

新增 Organization / Team / Project；给现有用户建立默认个人项目；现有 API Key 迁移到默认项目；新 Key 支持 project_id 和 service account。

验收：调用日志能按项目查询；个人模式行为完全不变；Key 脱离个人用户后仍可轮换和撤销。

### 阶段 2：预算和限流

先实现项目级日 / 月 USD 预算和硬停，再加 RPM、TPM、并发。复用现有 reserved 预扣事务，预算占用与最终结算在同一事务里处理。

验收：并发请求不能突破硬预算；失败渠道不消耗预算；预算规则修改不改变历史账单；达到软阈值触发一次通知。

### 阶段 3：团队成本分摊

增加成本中心、成员和项目维度报表；保留价格与倍率快照，区分上游成本和内部结算价。

验收：同一调用在团队、项目、用户维度汇总总额一致；重试不重复计费；汇率变化不重算历史。

### 阶段 4：企业身份和治理

接入 OIDC / SAML 之一，先做 OIDC；增加 auditor、billing_viewer、project_admin；增加 Key rotation、审批和访问审计。

验收：成员离职后项目服务账号仍可用；普通管理员看不到未授权项目；审计记录能定位操作者和目标对象。

### 阶段 5：内容与高级路由

最后做原文留存、敏感内容策略、模型审批、路由规则、fallback policy、MCP / A2A 入口。

验收：原文访问有独立权限和审计；敏感项目可禁用内容保存；策略变更有版本和回滚。

## 八、独到判断

### 1. 团队网关的核心不是“多人共享余额”

多人共享余额只是展示层。真正要解决的是资源归属：一个调用到底属于哪个团队、项目和应用，谁有权改预算，谁能看到内容，谁承担成本。

### 2. 项目比用户更稳定

用户会加入、离开和换岗位；生产应用和项目不会因为人员变动消失。把 Key 绑定个人，短期简单，长期会把生产依赖绑在离职账号上。Service Account 应该在第二阶段就出现。

### 3. 预算是准入系统，不是月底报表

月底才统计花费已经晚了。预算必须进入请求前检查和预扣事务，同时保留软预算告警，允许团队在风险可控时继续使用。

### 4. 最高价值的日志字段是“归属快照”

token 数、耗时和成本都已有价值，但如果没有 project_id、cost_center_id、policy_id，日志只能回答“发生了什么”，回答不了“谁应该负责”。

### 5. 高价模型需要“能力权限”和“钱权限”同时通过

成员能看到 Claude Opus，不代表项目预算允许使用；项目有钱，也不代表成员可以调用。模型白名单和预算策略应当是两个独立门槛。

### 6. 自研网关的壁垒是策略一致性

上游适配会越来越多，真正难的是跨协议、跨渠道、跨重试的成本和审计一致性。Lingshu 已经把预扣、结算、usage 和失败重试放在一起，这是团队网关最值得保留的核心。

## 九、三种未来剧本

### 最可能

Lingshu 先保留商业中转模式，增加个人项目兼容层和项目级预算，逐步把用户余额迁移为组织 / 项目预算。服务分组继续负责供给和模型权限，团队对象负责归属和成本。

### 最危险

直接把现有用户分组改成团队，把用户余额改成团队余额，继续用个人 API Key 代表服务。这样可以很快出页面，但会在项目共享、离职交接、审计和成本分摊时产生结构性返工。

### 最乐观

Lingshu 形成一套轻量的、可自托管的团队 AI 控制面：协议和计费一致，项目预算可解释，Key 和 Service Account 可轮换，渠道质量经过检测，日志能支撑复盘和成本治理。它不追求替代 LiteLLM 的所有 provider，而是把“可靠路由 + 可解释计费 + 团队归属”做深。

## 十、交接清单

主 agent 接手时应先确认：

1. 公司团队网关是否仍与商业中转共用同一个部署，还是独立模式 / 独立实例。
2. 团队预算是 USD 成本、内部余额，还是两者同时存在。
3. 是否需要 OIDC，还是先用本地用户和 Service Account。
4. 是否允许管理员读取原始请求和回复，保存多久，谁审批。
5. 项目、团队、服务分组和用户分组的边界是否按本报告定义。
6. 第一阶段只做项目预算，还是同时做团队成本中心和模型审批。

## 信息来源

### Lingshu 本地源码与文档

- `docs/plan.md`
- `docs/decisions.md`
- `internal/store/migrations/0001_init.sql`
- `internal/api/`
- `internal/store/`
- `internal/relay/`

### 已核验的开源项目

- [New API](https://github.com/QuantumNous/new-api)
- [LiteLLM](https://github.com/BerriAI/litellm)
- [Bifrost](https://github.com/maximhq/bifrost)
- [Sub2API](https://github.com/Wei-Shaw/sub2api)
- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)
- [Apache APISIX](https://github.com/apache/apisix)
- [zconv](https://github.com/LIghtJUNction/zconv)

### 外部产品和云厂商官方资料

- [OpenAI project management](https://help.openai.com/en/articles/9186755-managing-projects-in-the-api-platform)
- [OpenAI spend limits](https://developers.openai.com/api/docs/guides/spend-limits)
- [Microsoft Foundry cost management](https://learn.microsoft.com/en-us/azure/foundry/concepts/manage-costs)
- [Google Cloud quota projects](https://docs.cloud.google.com/docs/quotas/quota-project)
- [Google Cloud spend caps](https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps)
- [AWS Bedrock cost management](https://docs.aws.amazon.com/bedrock/latest/userguide/cost-management.html)
- [AWS Bedrock invocation logging](https://docs.aws.amazon.com/bedrock/latest/userguide/model-invocation-logging.html)

> 链接访问时间：2026-11-11。GitHub 项目版本为本地 `references` 工作区在本次研究开始前更新后的状态。外部 GitHub 项目的具体版本、许可证和功能仍应在采纳前单独复核。

## 方法论说明

本报告采用横纵分析法：纵向从 Lingshu 的商业中转阶段追踪到团队网关迁移，横向比较开源实现、商业控制面和云厂商治理模型，最后提炼可落地的对象、预算、权限、成本和实施顺序。
