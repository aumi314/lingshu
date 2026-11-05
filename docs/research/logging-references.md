# 日志设计参考调研：五个网关项目

调研日期：2026-11-05
用途：为「灵枢 · Lingshu」日志重设计（调用日志 / 操作日志二分）提供对照事实。已定规则见 `docs/decisions.md` 的「日志体系重构（2026-11-04 确认）」一节，本报告只提供事实与取舍依据。

参考仓库 commit（`git -C <repo> log -1 --format='%h %cs'`）：

| 仓库 | commit | 日期 |
|---|---|---|
| new-api | `19d435e` | 2026-10-15 |
| sub2api | `698d14246` | 2026-10-15 |
| CLIProxyAPI | `5fc580b1` | 2026-10-15 |
| litellm | `d553123` | 2026-10-15 |
| bifrost | `77c1259` | 2026-10-15 |

快照前提（影响结论边界）：

- litellm 这份快照没有 `litellm/proxy/db_logs.py`，SpendLogs 权威 schema 在根目录 `schema.prisma:720-763`；UI 源码不在工作树，UI 行号取自 `git show HEAD:<path>`。
- CLIProxyAPI 无前端源码，管理面板是外部仓库下载的单文件 `management.html`（`internal/managementasset/updater.go:32`）。
- bifrost 的 `ui/` 只有 `package.json`，前端源码不在仓库，UI 类问题按 API 契约推断并标注 unverified。

引用约定：路径相对各自的 `references/<repo>/`。

---

## A. 调用 / 用量日志

### A1 表结构、快照还是 ID、删除后的旧日志

| 项目 | 主表 | 身份字段存法 | 删除 key / group / channel 后 |
|---|---|---|---|
| new-api | `logs`，`model/log.go:57-79` | `username`/`token_name`/`model_name`/`group` 是请求时快照列（`model/log.go:63-65,74`）；`channel_id` 只存 ID，`channel_name` 标 `gorm:"->"` 属只读虚拟列，不落库（`model/log.go:71-72`） | 无外键、无 CASCADE（`model/main.go:411,445-471`）；删渠道后旧日志渠道名回填为空串（`model/log.go:538-560`） |
| sub2api | `usage_logs`，`backend/ent/schema/usage_log.go:20` | user / api_key / account / group / channel 全存 ID；模型名、倍率、计费模式是快照（`backend/ent/schema/usage_log.go:41-66,79-122`） | DB 层 user/key/account 是 `ON DELETE CASCADE`（`backend/migrations/001_init.sql:136-138`），group 是 `SET NULL`（`backend/migrations/003_subscription.sql:57`），ent schema 未声明 OnDelete，双轨不一致（`backend/ent/schema/usage_log.go:193-219`）；`channel_id` 无外键无快照（`backend/migrations/084_channel_billing_model_source.sql:5`） |
| CLIProxyAPI | 不落盘，内存队列 `internal/redisqueue/queue.go:10-11,226-241` | 无表结构概念，JSON 事件流 `internal/redisqueue/plugin.go:179-239` | 进程重启即丢；无历史可查 |
| litellm | `LiteLLM_SpendLogs`，`schema.prisma:720-763`，主键 `request_id`（一请求一行） | user / team_id / organization_id / end_user 全是裸 ID（`schema.prisma:737,742-744`）；`api_key` 存 SHA-256 哈希（`schema.prisma:723`、`litellm/proxy/utils.py:7400-7406`）；model 是名字快照 | 无外键、无 relation（`schema.prisma:720-763`）；key / team 是软删，搬到 `LiteLLM_DeletedVerificationToken`（`schema.prisma:604`）、`LiteLLM_DeletedTeamTable`（`schema.prisma:251`），旧日志成孤儿行 |
| bifrost | `logs`，`framework/logstore/tables.go:303` | ID + Name 双写快照：`SelectedKeyID/Name`（`tables.go:319-320`）、`VirtualKeyID/Name`（`:322-323`）、`UserID/UserName`（`:336-337`）、Team/Customer/BusinessUnit/Project 同理（`:338-345`），名字取自请求上下文（`plugins/logging/main.go:2161-2183`） | 无外键，删实体不回写日志，旧行保留 ID+Name 指向已删对象；仅在筛选下拉里隐藏已删 VK（`transports/bifrost-http/handlers/logging.go:371-376`） |

补充：sub2api 的软删对象在旧日志里会「变无名」——User 侧解析显式 `SkipSoftDelete` 保留「已删用户」（`backend/internal/repository/usage_log_repo_query.go:365-379`），Key 与 Group 侧未穿透（`usage_log_repo_query.go:381-394,411-424`），UI 显示 `-`（`frontend/src/components/admin/usage/UsageTable.vue:47-53,120-125`）。

### A2 耗时指标、在哪测量、非流式如何处理

- **new-api**：总耗时 `use_time` 取整秒（`time.Now().Unix() - StartTime.Unix()`，`service/text_quota.go:231`；错误日志同构 `service/relay_error.go:96`）；首字延迟 `frt` 毫秒存在 `other.frt`（`service/log_info_generate.go:110`）。测点在 SSE 首个非 `[DONE]` data 块（`relay/helper/stream_scanner.go:272`），另有 responses-websocket、image、realtime、cohere、aws、cloudflare 各自埋点（`relay/responses_websocket.go:394`、`relay/channel/openai/relay_image.go:291`、`relay/channel/openai/relay_realtime.go:117`、`relay/channel/cohere/relay-cohere.go:120`、`relay/channel/aws/relay-aws.go:296`、`relay/channel/cloudflare/relay_cloudflare.go:69`），赋值有「只记第一次」保护（`relay/common/relay_info.go:990-995`）。非流式初值是 `startTime.Add(-time.Second)`，即 `frt ≈ -1000`（`relay/common/relay_info.go:635`），UI 只在 `is_stream` 时展示首字（`web/src/features/usage-logs/components/timing-metrics-cell.tsx:72,95`、`web/src/features/usage-logs/components/dialogs/details-dialog.tsx:743-745`）。
- **sub2api**：`duration_ms` + `first_token_ms`（`backend/ent/schema/usage_log.go:129-134`），写入点 `backend/internal/service/gateway_usage_billing.go:1273,1313-1314`（OpenAI 通道同构 `backend/internal/service/openai_gateway_usage.go:446-447`）。总耗时测点 `time.Since(startTime)`（`backend/internal/service/gateway_forward.go:903`）；首字测点在 SSE 首个非 `[DONE]` 数据块（`backend/internal/service/gateway_upstream_response.go:1107-1111`）。非流式 `first_token_ms` 留 NULL，因为赋值只发生在 `if reqStream` 分支（`backend/internal/service/gateway_forward.go:817-891`）。
- **CLIProxyAPI**：`latency_ms` + `ttft_ms`（`internal/redisqueue/plugin.go:130-131`）。TTFT 三段式：起表 `StartResponseTTFT` → 首字节 `MarkFirstResponseByte` 或首个 token 事件落值 → 无 token 事件时退化为 first-packet（`internal/runtime/executor/helps/usage_helpers.go:409-418,447-482,484-500,724-737`）。非流式用 `TrackHTTPClient` 包 transport，首次 Read 触发首字节标记，因响应体整体到达，TTFT ≈ 总耗时（`usage_helpers.go:353-380,739-758`）；流式用 `TrackHTTPClientRoundTripOnly` 只留首包兜底，TTFT 由协议感知解析器显式标记（`usage_helpers.go:357-364`、`internal/runtime/executor/helps/chat_ttft_helpers.go:115`、`claude_ttft_helpers.go:103`、`gemini_ttft_helpers.go:87`、`responses_ttft_helpers.go:110`）。
- **litellm**：`startTime` / `endTime` / `completionStartTime` / `request_duration_ms`（`schema.prisma:728-731`）。`completionStartTime` 取 `kwargs["completion_start_time"]`，缺失时退化为 end_time（`litellm/proxy/spend_tracking/spend_tracking_utils.py:534`），非流式显式补成 end_time（`litellm/litellm_core_utils/litellm_logging.py:2572-2574`）。首字延迟三套算法：logging 层 `completion_start_time - start_time`（`litellm_logging.py:6356-6372`）、日聚合 SQL `AVG(EXTRACT(epoch FROM (completionStartTime-startTime)))`（`litellm/proxy/proxy_server.py:16090,16113`）、列表排序用 CASE 把非流式行置 NULL（`litellm/proxy/spend_tracking/spend_management_endpoints.py:2997-3000`）。
- **bifrost**：三个口径 `Latency` / `UpstreamLatency` / `OverheadLatency`（总 = upstream + overhead，`framework/logstore/tables.go:387-389`），provider 层各自 `time.Since(startTime)`（`core/bifrost.go:830`），回填日志行 `plugins/logging/main.go:2216-2241`，trace 结束后按 root span 覆写（`plugins/logging/main.go:2868-2903`）。TTFT 不入库，只落在 trace 属性 `bifrost.response.time_to_first_token_ms`（`core/schemas/trace.go:1219-1221`、`framework/tracing/tracer.go:861-873`）。非流式在 PreLLMHook 不写库、由 PostLLMHook 一次性 INSERT（`plugins/logging/main.go:1895,1937`）；流式只有最终 chunk 或错误才落库（`plugins/logging/main.go:2149-2160`）。

### A3 吞吐 tokens/sec

- **new-api**：后端不算，前端现算 `completion_tokens / use_time`（`use_time` 是整秒），条件 `useTime > 0 && completion_tokens > 0`（`web/src/features/usage-logs/components/columns/common-logs-columns.tsx:702-705`）。分母是总耗时，未扣首字时间。计算本身无最小 token 阈值，但配色阈值有：只有 `completion_tokens >= 100` 才按吞吐着色，档位 30 / 15 t/s（`web/src/features/usage-logs/lib/format.ts:217-223,228-234`）。
- **sub2api**：查询时算 `output_tokens * 1000.0 / NULLIF(duration_ms, 0)`，出 P5/P10/P50/AVG（`backend/internal/repository/ops_repo_dashboard.go:63-82`，公式在 `:73`）。过滤条件 `output_tokens > 0 AND duration_ms > 0`、排除图片计费、只取 `sync/stream/ws_v2/cyber` 四种 request_type（`ops_repo_dashboard.go:77-81`）。用总耗时，未扣首字，注释写明「与运维 Token 请求统计一致：使用完整耗时」（`frontend/src/utils/latencyHealth.ts:73-84`）。
- **CLIProxyAPI**：未实现。`tokens_per_second|tokensPerSecond|token_rate|output_tps|gen_speed` 全仓零命中；外部面板是否自算，本仓库无依据，标 unverified。
- **litellm**：未实现 tps 字段。最接近的指标是「每 token 平均延迟」`AVG(endTime-startTime)/NULLIF(completion_tokens,0)`（`litellm/proxy/proxy_server.py:16226`），是吞吐的倒数、含首字等待，且该查询排除缓存命中行（`proxy_server.py:16229`）。无最小 token 阈值分支。
- **bifrost**：`SUM(completion_tokens) / (SUM(latency)/1000)`（`framework/logstore/rdb.go:2224-2229`，语义注释 `:2231-2235`），只统计 `status='success'` 且 `latency > 0` 的行（`rdb.go:2250-2258`）。分母是整段 latency（含 TTFT、排队、overhead），因此数值系统性低于真实 decode 速度。无阈值分支；postgres + 物化视图 + bucket≥3600 秒时走 matview（`rdb.go:2236-2244`）。API：`/api/logs/histogram/throughput`（`transports/bifrost-http/handlers/logging.go:1418,1433`）。

### A4 缓存字段与命中率

| 项目 | 缓存字段 | 命中率 |
|---|---|---|
| new-api | `other.cache_tokens` / `cache_ratio`（`service/log_info_generate.go:106-107`）、`cache_creation_tokens` + ratio（`service/text_quota.go:498-501`）、5m/1h 分项（`service/text_quota.go:502-509`）、归一 `cache_write_tokens`（`service/text_quota.go:510-516`）、`image_cache_tokens`（`service/log_info_generate.go:346`） | 未计算、未展示。`hasAnyCacheTokens` 只判断是否走过缓存计价（`web/src/features/usage-logs/lib/format.ts:336-340`） |
| sub2api | `cache_creation_tokens` / `cache_read_tokens` / `cache_creation_5m_tokens` / `cache_creation_1h_tokens` 及四项 cost（`backend/ent/schema/usage_log.go:83-104`）、`cache_ttl_overridden`（`:181-182`） | 用量侧只给总量 `SUM(cache_creation+cache_read)`（`backend/internal/repository/usage_log_repo_stats.go:26,65`）。命中率只存在于渠道监控探针：`cache_read / (input + cache_creation + cache_read)`（`backend/internal/repository/channel_monitor_v2_repo.go:1346,1350-1355`） |
| CLIProxyAPI | `cache_read_tokens` / `cache_creation_tokens` / `cached_tokens`，外加布尔 `cache_read_tokens_present` 标记上游是否真的上报（`internal/redisqueue/plugin.go:225-234,112`） | 未实现，`hit_rate|cacheHitRate|cache_ratio` 零命中。token 分桶归一为互斥的 uncached / cache_read / cache_write 并做求和校验（`sdk/cliproxy/usage/accounting.go:26-49,52-79`） |
| litellm | SpendLogs 无独立缓存 token 列，折进 `metadata.additional_usage_values`（`spend_tracking_utils.py:752-780`）；`cache_hit` 是字符串列（`schema.prisma:739`），`cache_key` 单列（`:740`） | 无 `cache_hit_rate` 字段、无公式。只有 hit/miss 二值筛选 `LOWER(cache_hit)='true'`（`spend_management_endpoints.py:2910-2912`） |
| bifrost | `cache_debug` JSON（`framework/logstore/tables.go:384`，结构 `core/schemas/bifrost.go:2017-2039`）+ `CachedReadTokens` 反规范化列（`tables.go:456`） | 后端给三个数：`CacheHitRateTotalRequests`（分母=已完成请求数）、`DirectCacheHits`、`SemanticCacheHits`（`tables.go:260-262`，SQL `framework/logstore/rdb.go:1855-1866,1941-1953`），百分比留给前端 |

### A5 重试与失败

- **new-api**：每次失败尝试单独记一条 type=5 错误日志（`service/relay_error.go:76-98` → `model/log.go:278-336`），成功后再记一条 type=2 消费日志（`service/text_quota.go:531-544` → `model/log.go:339-405`），一次请求可产生多行。错误文本过 `MaskSensitiveErrorWithStatusCode`（`service/relay_error.go:97`）。错误行带 `error_type/error_code/status_code/request_path`，放在 public 层，普通用户可见（`service/relay_error.go:83-88`）。重试链落在 `other.admin_info.use_channel` 数组（`service/log_info_generate.go:73`），前端拼成 ` → ` 链（`common-logs-columns.tsx:403-410`），该字段对普通用户不可见（`model/log_other.go:246-252`）。
- **sub2api**：一次客户端请求一条 usage_log，且只在成功拿到上游响应后写（`backend/internal/handler/gateway_handler.go:601-629`，failover 路径同样只对成功 result 提交 `:967-995`）。usage_logs 表本身没有 error/status 字段（字段全集 `backend/ent/schema/usage_log.go:35-188`），失败走独立 `ops_error_logs` 表（`backend/migrations/033_ops_monitoring_vnext.sql:69-113`）；上游每次失败尝试追加一条 `OpsUpstreamErrorEvent`（`backend/internal/service/ops_upstream_context.go:484-514`，调用点 `backend/internal/service/gateway_forward.go:852-863`），错误正文入库前截断（`gateway_forward.go:844-850`，`LogUpstreamErrorBodyMaxBytes` 默认 2048）。用户可看自己的失败请求，走独立接口并强制按本人过滤（`backend/internal/service/ops_service.go:704-731`）。
- **CLIProxyAPI**：每次上游尝试各出一条记录——reporter 在 executor 入口创建、`once.Do` 保证一个 reporter 只发一条，重试在 conductor 层每轮重新进 executor（`internal/runtime/executor/helps/usage_helpers.go:536-544,565-572`、`sdk/cliproxy/auth/conductor_execution.go:139-160,199-220,254-290`）。双 ID：`request_id`（父请求）+ `execution_id`（单次执行 UUID）（`internal/redisqueue/plugin.go:68-76,192-194`、`sdk/cliproxy/usage/manager.go:26-29`）。失败体 `fail{status_code, body}`，状态码缺失回落 500（`internal/redisqueue/plugin.go:241-256`）。usage 记录里没有 attempt 序号字段，只能靠同一 `request_id` 下的 `execution_id` 数量推断尝试次数。
- **litellm**：每个客户端请求只写一行，上游重试不单独成行；重试次数进 metadata 的 `attempted_retries` / `max_retries`（`litellm/router.py:8175-8176,8238,8268`，声明 `litellm/proxy/_types.py:3673-3674`）。失败走 `async_post_call_failure_hook`，置 `metadata["status"]="failure"` 并写 `error_information`（error_code / error_message / traceback），状态判定 `spend_tracking_utils.py:1940-1951`，错误脱敏含 prompt 泄漏清洗与 IP 掩码（`spend_tracking_utils.py:1313-1366,1638-1705`）。另有 `LiteLLM_ErrorLogs` 表（`schema.prisma:778`），本快照无任何写入方，只有读取方（`spend_management_endpoints.py:1059,1185`），属死表。
- **bifrost**：同一 provider 内重试不新增行，行上 `number_of_retries` 记次数（`core/bifrost.go:7110`，`:7169,7313` 修正为 attempts-1）；跨 provider / model 的 fallback 每次一行，`fallback_index` 递增、子行 `parent_request_id` 指向根请求（`core/bifrost.go:5927,6146`、`plugins/logging/main.go:1868-1882`）。每次尝试的细节在 `attempt_trail` JSON：`Attempt/KeyID/KeyName/FailReason/FailureClass/StatusCode/RetryAfter/TriggeredRotation`（`core/schemas/bifrost.go:720-729`，写入 `plugins/logging/writer.go:1257-1259`）。错误存 `error_details` JSON，落盘前过 `sanitizeErrorForLogging`（`framework/logstore/tables.go:405`、`plugins/logging/main.go:159`）。

### A6 UI

- **new-api**：默认列 Time（含类型徽章）、Channel（仅 admin）、User（仅 admin）、Token、Model、Stream、Tokens、Cost、Timing、Details（`web/src/features/usage-logs/components/columns/common-logs-columns.tsx:352-891`，admin 列在 `:385` 内 push）。一格多值：Details 列由 `buildDetailSegments` 产多段，表格只渲染第 1 段并追加 `+N`（`common-logs-columns.tsx:113-131,826-867`）；Tokens 列第一行 prompt/completion、第二行 Cache↓/↑（`:734-760`）；渠道名与 ID 合并（`:411-415`）。筛选：类型 / 模型 / 分组 / Token 名 / 用户名(admin) / Channel ID(admin) / Request ID / Upstream Request ID / 时间范围（`web/src/features/usage-logs/components/common-logs-filter-bar.tsx:325-484`），后端过滤 `model/log.go:487-518`，username 与 channel 仅 admin 生效（`controller/log.go:32-35`）。顶部 Usage / RPM / TPM 三徽章（`web/src/features/usage-logs/components/common-logs-stats.tsx:51-107`），RPM/TPM 只统计最近 60 秒（`model/log.go:722-724`）。详情弹窗见 `web/src/features/usage-logs/components/dialogs/details-dialog.tsx:660-760`，admin 专属 Request Conversion（`:610-621`）、reject_reason（`:847-853`）、request_policy 决策（`:804-809`）。**用户看不到什么由服务端裁剪**：`formatUserLogs` 清空 `ChannelName` 并按 user 级裁剪 `other`（`model/log.go:114-121`），`formatLogOtherJSON` 在 user 级删除 `admin_info/root_info/audit_info` 及 legacy 敏感键（`model/log_other.go:232-267`）；IP 仅在用户开启 `RecordIpLog` 时记录（`model/log.go:296-306,356-365`）；普通用户查询有时间跨度上限（`controller/log.go:42-55`）。
- **sub2api**：管理端列含 user、created_at 恒显，`reasoning_effort/request_id/upstream_request_id/user_agent` 默认隐藏并按版本链式升级（`frontend/src/views/admin/UsageView.vue:632-658`）；用户端无 user 列与 account/上游信息（`frontend/src/views/user/UsageView.vue:713-731`）。一格多值：模型格按 `→` 拆映射链、`↳` 标上游模型、mismatch 徽标（`frontend/src/components/admin/usage/UsageTable.vue:55-91`）；tokens 格合并 input/output + cache read/creation + 图片 token（`:148-194`）；latency 格合并 TTFT + 总耗时 + tok/s + 严重度色条（`:236-255`）。顶部 4 卡：请求数 / token 总量（含 cache 分解）/ 费用（含账号成本与标准价划线）/ 平均耗时（`frontend/src/components/admin/usage/UsageStatsCards.vue:1-89`），统计接口带 30s 进程内缓存（`backend/internal/handler/admin/usage_query_cache.go:10-11`）。无独立详情弹窗，用 tooltip 展示 token 明细，导出 XLSX（`frontend/src/views/admin/UsageView.vue:600-625`）。**用户看不到的字段由 DTO 差异决定**：`upstream_model / upstream_reasoning_effort / upstream_response_model / channel_id / model_mapping_chain / upstream_request_id / billing_tier / account_rate_multiplier / account_stats_cost / account` 只在管理端 DTO（用户 DTO `backend/internal/handler/dto/types.go:593-674`，管理端追加 `:677-710`）；用户能看到自己的 IP、UA、session_id（`:655-660`）。
- **CLIProxyAPI**：本仓库无前端。管理 API 有 `usage-statistics-enabled`、`api-key-usage`、`usage-queue`、日志列表/删除（`internal/api/server_management.go:64-66,92-93,105-106`），v8 版另有 `/v8/management/observability/usage/queue` 等（`internal/api/server_management_v8.go:35-41`）。`api-key-usage` 只给成功/失败计数与按分钟桶的 RecentRequests，不含 token（`internal/api/handlers/management/api_key_usage.go:12-16,58-117`）。TUI 6 个 tab 里没有 usage tab（`internal/tui/app.go:14-21,83-87`）。
- **litellm**：默认列 Time、Type、Status、Session ID、Request ID、Cost、Duration (s)、TTFT (s)、Team Name、Key Hash、Key Alias、Model、Tokens、Internal User、End User、Tags（`ui/litellm-dashboard/src/components/logs/request/RequestLogsTableColumns.tsx`），没有 tokens/sec 列、没有缓存列。筛选含 Status、Cache hit/miss、凭证类型、span type、Team ID、Key Alias、Model、Internal User、End User、错误码下拉（`ui/litellm-dashboard/src/components/logs/request/RequestLogsFilters.tsx:28-60`）。分页 `page/page_size`（默认 50、上限 1000），计数封顶 10000 并用 `total_is_capped` 标注（`spend_management_endpoints.py:3013-3025`）。**列表接口不返回 messages/response/proxy_server_request**（列白名单 `_SPEND_LOG_LIST_COLUMNS`，`spend_management_endpoints.py:109-120`），请求体响应体要单独打 `/spend/logs/ui/{request_id}` 并做归属校验，不通过返 403（`:3390-3396,4915-4920,4922-4943`）。admin 看全部（`is_admin_view_safe`，`:4847-4861`），普通用户只看自己与可管理 team（`read_scope_sql`，`:4952-4964`）。
- **bifrost**：前端源码不在仓库，默认列与展开行为标 unverified。API 路由全表可核实（`transports/bifrost-http/handlers/logging.go:379-424`）：`/api/logs`、`/api/logs/{id}`、`/api/logs/stats`、`/api/logs/histogram/*`、`/api/logs/rankings`、`/api/logs/dashboard`、`/api/logs/filterdata`、`/api/logs/dropped`、`DELETE /api/logs`。顶部统计字段 = `SearchStats`：`total_requests / success_rate / user_facing_success_rate / average_latency / total_tokens / total_cost` 加缓存三项（`framework/logstore/tables.go:250-263`）。分页默认 limit 50、上限 1000（`transports/bifrost-http/handlers/logging.go:772,780`）。OSS 内无 admin / 普通用户分支，行级可见性靠上游注入的 `QueryScope`（`framework/queryscope/queryscope.go:99-115`），日志读路径统一走 `scopedLogsDB`（`framework/logstore/visibility.go:27-33`）。

### A7 规模

| 项目 | 索引 | 分页 | 保留 / 清理 | 写入方式 |
|---|---|---|---|---|
| new-api | `idx_created_at_id`、`idx_user_id_id`、`idx_created_at_type`、`index_username_model_name`、`idx_logs_request_id`、`idx_logs_upstream_request_id`（`model/log.go:58-78`） | offset/limit（`model/log.go:519-527`）；用户查询 total 截断到 10000（`:581,588`） | 管理员手动触发 system task `log_cleanup`，分批删（`model/log.go:771-800`、`service/system_task.go:340-410`）；ClickHouse 走 TTL（`model/main.go:422-443`） | 同步单条 `LOG_DB.Create`（`model/log.go:99-106`），失败只写系统日志；无异步队列 |
| sub2api | 单列 + `(user_id,created_at)` / `(api_key_id,created_at)` / `(group_id,created_at)` 复合（`backend/ent/schema/usage_log.go:222-238`） | offset/limit，弱筛选时用估算总数避免大表 COUNT（`backend/internal/repository/usage_log_repo_query.go:149-152,197-244`） | 默认保留 90 天（`backend/internal/config/config.go:2402`），聚合服务定期删明细（`backend/internal/service/dashboard_aggregation_service.go:360-406`）；分区表迁移在表未分区时直接跳过（`backend/migrations/035_usage_logs_partitioning.sql:20-26`） | 有界 worker 池（128 worker / 16384 队列 / 5s 超时，`backend/internal/service/usage_record_worker_pool.go:17-34,155-194`）+ 仓储批量（`backend/internal/repository/usage_log_repo_insert.go:91-101,153-215`）；幂等靠 `(request_id, api_key_id)` 唯一索引 + `ON CONFLICT DO NOTHING`（`backend/migrations/027_usage_billing_consistency.sql:32-33`） |
| CLIProxyAPI | 无索引 | usage queue 只有 `count`、FIFO pop，无游标（`internal/api/handlers/management/usage.go:30-54`、`internal/redisqueue/queue.go:199-224`） | 保留窗口默认 60s、上限 3600s（`internal/redisqueue/queue.go:10-11`），不落盘；请求日志文件按目录总大小删最旧（`internal/logging/log_dir_cleaner.go:74-157`） | 内存队列 + 后台 goroutine 派发（`sdk/cliproxy/usage/manager.go:410-431`），订阅缓冲 256、满则踢订阅者（`internal/redisqueue/queue.go:141-160`） |
| litellm | `startTime`、`startTime+request_id`、`end_user`、`session_id`、`litellm_call_id`、`api_key+startTime`（`schema.prisma:757-762`） | `page/page_size` + `LIMIT/OFFSET`（`spend_management_endpoints.py:3045-3048`），只有 session 聚合路径有 keyset cursor（`:2601-2610`） | 后台 cron `SpendLogCleanup`，保留期 `maximum_spend_logs_retention_period`（`litellm/proxy/proxy_server.py:8074-8093`、`litellm/proxy/_types.py:2835-2838`），批量/轮次/时长/超时四上限，Pod 锁保证多副本只跑一个（`litellm/proxy/spend_tracking/spend_log_cleanup.py:129-133`） | 异步批量：请求入内存队列（`litellm/proxy/utils.py:7484-7512`），阈值 100 条、轮询 2s、队列上限 64MB（`litellm/constants.py:1843-1845`）；单批按字节与行数双预算切分，毒行二分隔离（`litellm/proxy/db/spend_log_batching.py:1-20`、`litellm/proxy/utils.py:8183-8230`） |
| bifrost | 大量 `(维度, timestamp)` 复合索引与部分索引（`framework/logstore/tables.go:307,319,322,326,331,336,338,340,342,344,403,455`），`inc_number` 是数据库分配的单调游标（`tables.go:305,3181-3187`） | offset/limit + `(AfterTimestamp, AfterID)` keyset 游标（`tables.go:197-212`） | `RetentionDays` 默认 365 天（`framework/logstore/config.go:15`、`framework/logstore/cleaner.go:16`），每 24h 带 15~30min 抖动跑一次、单次删 5000 行（`cleaner.go:13-37,173-186`）；ClickHouse 走 TTL（`framework/logstore/clickhousemigrate.go:140-146,291-308`） | 单 `batchWriter` goroutine，默认 5s / 1000 行 / 300MB / 队列 10000（`framework/logstore/config.go:27-31`、`plugins/logging/writer.go:113-121`），瞬时失败重试 3 次；队列满直接丢弃并计数（`plugins/logging/writer.go:787-790`），暴露为 `/api/logs/dropped`（`plugins/logging/main.go:1256-1257`） |

---

## B. 操作 / 审计日志

### B1 是否有审计表、schema

| 项目 | 审计表 | 关键字段 |
|---|---|---|
| new-api | 有，`audit_logs`，`model/audit_log.go:26-46` | `event_id`（唯一）、`user_id`、`username`、`actor_role`、`category`、`action`、`token_ref`、`auth_method`、`ip`、`user_agent`、`method`、`route`、`status`、`success`、`request_id`、`content`、`other`(JSON)；类别 login/security/operation/access_token（`model/audit_log.go:17-22`） |
| sub2api | 有，`audit_logs`，raw SQL 建表 `backend/migrations/180_audit_logs.sql:8-35`，Go 结构 `backend/internal/service/audit_log.go:43-61` | `actor_user_id`（可空）、`actor_email`、`actor_role`、`auth_method`、`credential_masked`、`action`、`method`、`path`、`request_id`、`client_ip`、`user_agent`、`request_body`、`status_code`、`latency_ms`、`extra`(JSONB)；索引 `(created_at DESC, id DESC)`、`(actor_user_id, created_at)`、action、client_ip（`:28-35`） |
| CLIProxyAPI | 无。`audit|operation_log|action_log|audit_log` 在 `internal/ sdk/ cmd/` 零命中 | — |
| litellm | 有，`LiteLLM_AuditLog`，`schema.prisma:852-862` | `id`、`updated_at`、`changed_by`、`changed_by_api_key`、`action`、`table_name`、`object_id`、`before_value`(JSON)、`updated_values`(JSON) |
| bifrost | OSS 版无。`audit_logs` 被列为 enterprise-only schema 路径（`transports/bifrost-http/lib/config_test.go:19742`），仓库内只有常量 `EntityAuditLog`（`core/schemas/visibility.go:25`） | — |

### B2 记录哪些动作、刻意不记录哪些

- **new-api**：记录登录成功（`controller/user.go:128-144`）、管理/root 写操作兜底（`middleware/audit.go:124-179`）、handler 内精细埋点（`controller/audit.go:144-160`）、用户自身安全操作（`controller/audit.go:164-185`）、API token 增删改与密钥查看（`middleware/audit.go:210-256`）、面板访问令牌（`middleware/audit.go:265-319`）、额度调整（`controller/user_quota.go:13-44`）、渠道/兑换码/模型元数据（`controller/channel.go:513,835,866,1258`、`controller/redemption.go:118,226`）。**刻意不记**：登录失败（`controller/user.go:128` 注释只记录成功）；请求体、响应体、原始 URL/query、凭据（`model/audit_log.go:72-73` 明文声明，响应体只缓存 64KB 且仅用于解析 success 字段 `middleware/audit.go:16-23,113-117`）。
- **sub2api**：记录所有 POST/PUT/PATCH/DELETE + 敏感 GET 白名单（`backend/internal/server/middleware/audit_log.go:166-187`，白名单 `:111-121`）。动作名命中 `auditActionOverrides` 用固定名，否则按 method + 路由模板自动推导 `admin.accounts.update` 式命名（`audit_log.go:124-145,326-353`）。**刻意不记**：非白名单 GET（`:178-183`）；token 刷新成功，只记失败当攻击信号（`:215-218`）；凭证型请求体整体不入库（`:149-160,193-194`）；未过认证的 401/403，因中间件挂在认证之后（`:162-165`、`backend/internal/server/routes/admin.go:27-31`），例外是 `/auth` 组无前置认证，登录失败照记（`backend/internal/server/routes/auth.go:29-32`）。
- **CLIProxyAPI**：无审计记录。最接近的三处：配置热重载把变更打到 Info（`internal/watcher/config_reload.go:125-135`）、凭据字段变化打到 Debug（`internal/watcher/clients.go:240-248`）、管理端写操作只有普通 logrus 输出且无操作者标识（`internal/api/handlers/management/auth_files_provider_oauth.go:46-440`）。
- **litellm**：action 枚举 `created / updated / deleted / blocked / unblocked / rotated / kill_switch_fired`（`litellm/proxy/_types.py:3339`），覆盖 Key 增改轮删（`litellm/proxy/hooks/key_management_event_hooks.py:76-88,135-155,207-228,268-290`）、Team 成员变更、User、Model、密码、Proxy 设置、Cache settings、Config override、Policy 拦截、kill switch（`litellm/proxy/management_endpoints/model_management_endpoints.py:1348,1516,1545` 等）。**刻意不记**：LLM 调用本身（走 SpendLogs）、预算计数器的每次增减、读操作。写入还额外要求 `premium_user is True`，社区版即使配了 `store_audit_logs` 也不落库（`litellm/proxy/management_helpers/audit_logs.py:31-45,213-215`）。
- **bifrost**：只记录数据面请求（LLM/MCP/A2A 调用）。管理动作（改 provider、改 VK、改预算、改路由规则）在 OSS 不留操作痕迹；唯一近似痕迹是配置哈希 `config_hash`（`framework/configstore/tables/key.go:30`、`framework/configstore/tables/virtualkey.go:319`），用于判断配置是否变更。`routing_engine_logs` 属请求级「为什么这么路由」说明（`core/bifrost.go:7276-7292`）。

### B3 写入方式、变更成功但日志缺失是否可能

| 项目 | 机制 | 变更成功而日志缺失 |
|---|---|---|
| new-api | 业务提交后单独往 LOG_DB 插一行，同步执行，不在业务事务内（`model/audit_log.go:74-136`）。触发点两类：handler 内埋点（`controller/audit.go:144-185`）与 admin 鉴权兜底中间件（`middleware/audit.go:101-179`，设计意图是新增管理接口自动留痕）；handler 已埋点则置 `ContextKeyAuditLogged` 去重（`middleware/audit.go:130-133`） | 会。写失败只 `logger.LogError`，不回滚业务（`model/audit_log.go:134-136`）；LOG_DB 为 nil 同样只记 error（`:115-118`） |
| sub2api | HTTP 中间件在 `c.Next()` 之后构造记录，异步入队 + 内存队列批量落库（`backend/internal/server/middleware/audit_log.go:207-295`、`backend/internal/service/audit_log_service.go:12-16`，容量 4096 / 批 100 / 1s flush） | 会。队列满即丢弃并计数（`audit_log_service.go:70-88`），flush 失败只打 WARN 不重试（`:130-152`），崩溃丢内存队列。唯一强制落库路径是「清空审计日志后的留痕」，失败即报错（`:100-128`） |
| litellm | `asyncio.create_task` fire-and-forget，不在业务事务内（`litellm/proxy/management_helpers/audit_logs.py:158-160`、`litellm/proxy/hooks/key_management_event_hooks.py:76,135`），DB 写入无显式事务（`audit_logs.py:236-241`） | 会。失败不阻塞主流程，只打日志（`audit_logs.py:243-246` 注释 "Non-Blocking Exception"）；部分调用点挂 done_callback 显式告警（`litellm/proxy/management_endpoints/cache_settings_endpoints.py:276-286`） |
| bifrost | 审计能力在 enterprise 版，OSS 无此链路 | — |
| CLIProxyAPI | 无审计库、无审计文件追加，只有 logrus（`internal/config/config.go:59-60`、`internal/logging/global_logger.go:220`） | — |

### B4 before/after、diff、密钥脱敏

- **new-api**：不记完整 diff，多数只记变化字段名 `changed_fields`（`controller/channel.go:1233-1254`、`controller/token.go:482-504`），状态迁移记 `from/to`（`controller/token.go:479-480`）；额度覆盖显式记前后值（`controller/user_quota.go:72-74`）。密钥脱敏：渠道更新只记字段名 `"key"`，不记内容（`controller/channel.go:1233,1247-1249`）；查看渠道密钥这个动作被记、密钥本身不记（`controller/channel.go:512-516`）；token 密钥查看只记 id/name（`middleware/audit.go:225-228`）；访问令牌只存 sha256 指纹（`model/audit_log.go:61-70`）。
- **sub2api**：没有 before/after 或 diff，只存脱敏后的请求体，结构体无 old/new 字段（`backend/internal/service/audit_log.go:43-61`）。请求体仅 JSON 递归按敏感键擦除，非 JSON、超限、不可解析一律不存（`audit_log.go:176-208,210-236`）；敏感键走精确清单 + 账号凭证子键 + 支付密钥三份并集，再叠加 password/secret/token/apikey/privatekey/otp 等模糊子串（`audit_log.go:122-172`、`backend/internal/service/account_credentials_redact.go:5-15`、`backend/internal/service/payment_config_providers.go:113-119`）。请求头凭证保留前 6 后 4，中间 `****`，长度 ≤14 整体 `****`（`audit_log.go:238-249`）。extra 只允许白名单标量键，字符串截断 128 字符（`backend/internal/server/middleware/audit_log.go:51-87`）。列表接口不返回 request_body，只有详情接口给（`backend/internal/repository/audit_log_repo.go:281-282`）。
- **litellm**：`before_value` / `updated_values` 存整行 JSON 字符串（`litellm/proxy/management_helpers/audit_logs.py:219-224`）；key 更新 before=旧行全量、after=请求体，key 删除 before=删除前行、after=`"{}"`（`key_management_event_hooks.py:135-155,268-290`）；字段级 diff 在前端做（`ui/litellm-dashboard/src/components/logs/audit/AuditLogDrawer/AuditLogDrawer.tsx:80-118`）。密钥：spend log 的 `api_key` 是 SHA-256（`litellm/proxy/utils.py:7400-7406`），audit 的 `changed_by_api_key` schema 注释即 api key hash（`schema.prisma:856`）。**反例**：key 创建审计把明文密钥写进审计表（`key_management_event_hooks.py:76-88` 写 `response.model_dump_json()`，而 `GenerateKeyResponse.key` 是明文 `sk-…`，`litellm/proxy/_types.py:1197-1198`）；同仓库的 cache settings 与 config override 审计做了值抹除（`cache_settings_endpoints.py:263-273`、`litellm/proxy/management_endpoints/config_override_endpoints.py:72-82`）。
- **bifrost**：无 before/after、无 diff，仓库里唯一的 Diff 是 MCP OAuth 的 `DiffersFrom` 配置比较（`framework/configstore/rdb.go:8841`）。密钥不进日志，只存 key 的 ID 与名称（`framework/logstore/tables.go:319-320`）；内容脱敏用 `redaction_data` 瞬态 + `redaction_mapping` 加密可逆映射，随行删除（`tables.go:436,439-440`）；`content_hidden` 行永不再下发内容（`tables.go:418`）。
- **CLIProxyAPI**：只有内存 diff + 日志里的 `prefix: A -> B` 形式，不属审计记录（`internal/watcher/diff/auth_diff.go:24-42`）；`proxy_url` 走 `formatProxyURL` 脱敏，注释写明敏感数据永不打印（`auth_diff.go:11-12,31-36`）。OAuth token 只落 SHA256 指纹（`internal/runtime/executor/helps/usage_helpers.go:148,192-209`、`internal/redisqueue/plugin.go:134,212`）。**反例**：客户端 API key 在 usage 记录里是明文 `api_key`（`internal/redisqueue/plugin.go:157,189`、`usage_helpers.go:88,296-306`）。

### B5 系统动作（无人类 actor）如何表示

- **new-api**：actor 用整数 `actor_role`：0=未知、1=普通用户、10=管理员、100=root（`common/constants.go:199-201`）。非法或缺失角色置 0 且只对 root 可见（`model/audit_log.go:102-107`，查询过滤 `:149-151`）。没有独立的 "system" 字符串 actor。
- **sub2api**：`actor_user_id` 允许为空（`backend/internal/service/audit_log.go:46`），但审计中间件只覆盖 HTTP 请求，后台定时任务根本不进这张表。系统动作另有专用载体：`payment_audit_logs` 的 operator 默认 `"system"`（`backend/ent/schema/payment_audit_log.go:31-47`）、`usage_cleanup_tasks` 带 created_by / canceled_by / canceled_at（`backend/ent/schema/usage_cleanup_task.go:39-48`）。
- **litellm**：`changed_by` = user_api_key_dict.user_id 或 `litellm_proxy_admin_name`（`litellm/proxy/management_helpers/audit_logs.py:58-66`），支持 `litellm-changed-by` 头代操作但需 metadata 授权（`audit_logs.py:47-56`）。系统动作没有独立 actor 类型，cache settings 写法是 `litellm_changed_by or user_id or admin_name`（`litellm/proxy/management_endpoints/cache_settings_endpoints.py:320`）。
- **bifrost**：没有 actor 抽象，归属只能靠 `UserID/UserName`、`VirtualKeyID/Name`、Team/Customer/BusinessUnit/Project 等维度，全部来自请求上下文快照（`framework/logstore/tables.go:336-345`、`plugins/logging/main.go:2181-2183`）；后台批量结算时这些字段可空（`framework/logstore/tables.go:336`、`framework/jobaccounting/types.go:156-167`）。全仓无 `"system"` actor 常量。
- **CLIProxyAPI**：无此概念，`Record` 里最接近的标识是 `AuthIndex` / `AccessProvider` / `Source`（`sdk/cliproxy/usage/manager.go:42-50`）。

### B6 余额 / 额度变更与流水表的关系

- **new-api**：额度调整在事务内 `SELECT ... FOR UPDATE` 后更新 `users.quota`，返回不可变快照 `{UserID, Username, Before, After}`（`model/user_quota_adjustment.go:16-23,25-86`）。审计 action 为 `user.quota_add / quota_subtract / quota_override`，带 `target_user_id/mode/requested_quota` 与成功后的 `from/to/quota`，失败时带 `failure_reason`（`controller/user_quota.go:13-77`）。同时另写一条 usage log type=1，归属被操作用户而非操作者（`controller/user_quota.go:80-81` → `model/log.go:170-206`）。充值走 `RecordTopupLog`，含 `payment_method/callback_payment_method/server_ip/node_name/version`（`model/log.go:252-278`）。
- **sub2api**：没有通用账本，余额是 `users.balance` 字段，扣费直接 UPDATE（`backend/internal/repository/usage_billing_repo.go:244-275`），Key 配额同表更新（`:417-468`）。用量侧记录 `actual_cost`（本次实扣）+ `total_cost`（标准价）+ 倍率快照（`backend/ent/schema/usage_log.go:105-122`）。幂等由窄表 `usage_billing_dedup(request_id, api_key_id, request_fingerprint)` 承担（`backend/migrations/071_add_usage_billing_dedup.sql:1-13`）。金额统一量化到 8 位小数（`backend/internal/service/usage_billing.go:60-103`）。唯一带 after 快照的流水是返利账本（`backend/migrations/134_affiliate_ledger_audit_snapshots.sql:4-23`）。管理员手工改余额不产生数值型审计，只在 request_body 留脱敏入参。
- **litellm**：没有 ledger 表，额度是「当前值」字段（`schema.prisma:13,190,502`），批量改成员预算只写一条 `updated` 审计，before/after 是成员预算快照（`litellm/proxy/management_helpers/bulk_team_member_budgets.py:239-241`）。预算预留与实际费用的差额在内存与 Redis 计数器里对账，不落表（`litellm/proxy/spend_tracking/budget_reservation.py:362-390,1119-1183`），即缺少可审计的额度流水。
- **bifrost**：预算是计数器不是流水表，`governance_budgets` 存 `MaxLimit / ResetDuration / LastReset / CurrentUsage`，可挂 Team / VirtualKey / Provider / Model / Customer（`framework/configstore/tables/budget.go:40-46,102-106`）。事实流水是 `logs.cost`（`framework/logstore/tables.go:398`），日志行只记命中了哪些预算与限流 ID（`:445-446`），计数由治理侧用同一份 cost 独立更新（`plugins/governance/tracker.go:168-177`），两者无外键、无跨表事务，靠 requestID + nonce 对齐（`plugins/governance/tracker.go:126-146`）。
- **CLIProxyAPI**：无计费、无余额、无用户额度概念（`sdk/cliproxy/usage/`、`internal/redisqueue/`、`internal/api/handlers/management/` 内 `billing|usd|price|cost` 零命中）；`quota` 指上游厂商配额窗口（`internal/api/handlers/management/plugin_quota.go:120-132`）。

### B7 终端用户能否看到自己的操作日志

- **new-api**：能。`GET /api/audit/self`（UserAuth，`router/api-router.go:321`），管理员另有 `GET /api/audit`（AdminAuth + `authz.AuditRead`，`:320`）。self 视图强制 `filter.UserId = 自己`、`SelfView = true`（`controller/access_token.go:345-357`），并按 user 级抹掉 `RootInfo/AdminInfo/AuditInfo`（`model/audit_log.go:187-201`）；非 root 查询整体只保留 `actor_role IN (1,10)`（`:149-151`）；前端 self 视图没有 username、category、token_ref、request_id 筛选（`web/src/features/usage-logs/audit/components/audit-log-filter-bar.tsx:143-185`）。**副作用**：管理操作的审计行归属操作者而非目标用户（`controller/audit.go:148-158`），被管理员改额度的用户在审计页看不到那条记录，只能在 usage log 的 topup 行看到（`controller/user_quota.go:80-81`）。
- **sub2api**：不能。审计读取接口只注册在 admin 分组（`backend/internal/server/routes/admin.go:153-158`），handler 注释标明仅管理员可见（`backend/internal/handler/admin/audit_log_handler.go:16-17`）。但用户自己的改密码、改资料、TOTP 开关、step-up、创建/删除 Key 会被同一审计中间件记入（`backend/internal/server/routes/user.go:25-26`），即「记了但用户看不到」。用户能看的只有自己的用量与失败请求（`backend/internal/handler/usage_handler.go:206-220,398`、`backend/internal/service/ops_service.go:710-715`）。
- **litellm**：spend log 侧管理员看全部，普通用户只看自己的与可管理 team 的行（`spend_management_endpoints.py:4885-4913,4945-4964`），详情端点再做归属校验（`:4922-4943`）。audit log 的读取端 handler 在本快照找不到，`"/audit"`、`"/audit/{id}"` 只出现在路由白名单（`litellm/proxy/_types.py:980-981`），读取端归未开源部分，标未找到。UI 侧非 premium 显示预览图并禁用查询（`ui/litellm-dashboard/src/components/logs/audit/AuditLogsPanel.tsx:100-130`）。
- **bifrost**：OSS 无内置 RBAC，`/api/logs` 是管理面 API（`transports/bifrost-http/handlers/logging.go:379-424`），行级可见性完全依赖上游注入的 `QueryScope`，provider 缺失即等于不过滤（`framework/queryscope/queryscope.go:99-115`）。OSS 内置的可见性开关只有全局 `client.hidden_request_types`（`docs/architecture/framework/log-store.mdx:30-62`）。
- **CLIProxyAPI**：用量数据对普通 API 调用方不可见，只对管理面可见（管理密钥鉴权、失败次数超限封 IP，`internal/api/redis_queue_protocol.go:90-105`、`internal/api/server_management.go:64-66`）。

### B8 审计 UI

- **new-api**：列 Time、Username（仅非 accessOnly）、Event、IP、Client(user_agent)、Method、Route、HTTP、Result、Details(dialog)（`web/src/features/usage-logs/audit/components/audit-log-columns.tsx:36-182`）。单行摘要 = headline（本地化标签）+ identifier（如 `(ID: 12)`）+ description（两行截断），生成逻辑四段式 headline / summary / identifier / description（`web/src/features/usage-logs/audit/lib/audit-details.ts:243-386`），token 类动作有专门文案表（`:38-64`）。筛选：结果、类别、token_ref、request_id、username、时间范围（`audit-log-filter-bar.tsx:92-202`）。
- **sub2api**：列 created_at、actor、action、status_code、latency_ms、client_ip、actions（`frontend/src/views/admin/AuditLogView.vue:466-473`）。单行摘要在详情弹窗：状态徽标 + action + method/path + 时间/耗时/request_id + actor(email/role) + auth_method + 掩码凭证 + client_ip + UA + 脱敏 body（`AuditLogView.vue:177-250`）。筛选：q（path/action/actor_email 模糊）、actor_email、action、client_ip、method、auth_method、success(2xx/3xx vs ≥400) + 时间范围（`AuditLogView.vue:5-71`、`backend/internal/repository/audit_log_repo.go:125-176`）。清空需现场 TOTP，admin API key 禁止清空，清完写留痕（`backend/internal/handler/admin/audit_log_handler.go:107-168`）。
- **litellm**：列 Timestamp / Action / Table / Object ID / Changed By / API Key (Hash)（`ui/litellm-dashboard/src/components/logs/audit/AuditLogsTableColumns.tsx:53-107`），表名映射 Keys/Teams/Users/Organizations/Models/Agents（`:21-28`）。查询参数 page/page_size(50)、search、object_id、changed_by、object_key_hash、object_team_id、action、table_name、sort_by=updated_at desc（`AuditLogsPanel.tsx:70-84`）。抽屉展示 before/after 差异（`AuditLogDrawer.tsx:80-118`）。
- **bifrost / CLIProxyAPI**：无审计界面。bifrost 的 `audit_logs` 是 enterprise-only 路径（`transports/bifrost-http/lib/config_test.go:19742`），CLIProxyAPI 的面板在外部仓库（`internal/config/config_defaults.go:4`、`internal/managementasset/updater.go:32`）。

---

## C. 坑与早期设计错误的修补

**new-api**

1. `channel_name` 不落库（`gorm:"->"`，`model/log.go:72`），删渠道后历史日志渠道名变空（`model/log.go:538-560`）。
2. 用户日志 total 被截断到 10000（`model/log.go:581,588`）。
3. `SumUsedQuota` / `SumUsedToken` 的 `logType` 形参从未被使用，恒按 type=2 统计，controller 传入的 type 被丢弃（`model/log.go:680,717-718`、`controller/log.go:212`）。
4. 非流式 `frt` 是 -1000 的哨兵负值（`relay/common/relay_info.go:635`），漏判 `is_stream` 的展示会显示负数。
5. ClickHouse 无自增 id，用 `assignDisplayLogIds` 伪造展示 id，用户查询更用 `startIdx+i+1` 造假 id（`model/log.go:108-121,545-547`）。
6. 排序语义两套：管理端 `created_at desc, id desc`（`model/log.go:519-523`），用户端 `id desc`（`:581-585`）。
7. `use_time` 整秒截断（`service/text_quota.go:231`），亚秒请求耗时记为 0，连带 tps 为空。
8. tps 分母未扣首字时间（`common-logs-columns.tsx:702-705`）。
9. 审计写入不在业务事务内，可能丢日志（`model/audit_log.go:134-136`）；audit_logs 永不清理，与 usage logs 清理体系脱钩（`:249-250`）。
10. legacy 敏感字段顶层泄漏的历史包袱：写侧拒绝 `SetPublic` 写这些键，读侧仍要做归一化迁移与剥离（`model/log_other.go:50-71,204-227,246-258`）。
11. 枚举与表语义不一致：`LogTypeManage(3)` 实际写进 audit_logs（`model/log.go:182-193`），前端仍把 type=3/7 列为可选项并标 Deprecated（`web/src/features/usage-logs/constants.ts:108-119`）。

**sub2api**

12. schema 与真实表结构双轨漂移：ent schema 缺 `request_type / service_tier / session_id / upstream_request_id / first_token_ms` 等列，实际列由 SQL 迁移单独加（`backend/migrations/061_add_usage_log_request_type.sql`、`070_add_usage_log_service_tier.sql`、`187_add_usage_log_session_id.sql`、`232_add_usage_log_upstream_request_id.sql`），写入走 raw SQL（`backend/internal/repository/usage_log_repo_query.go:22`）。
13. 列名设计错误的修复史：自动命名生成了 `cache_creation5m_tokens` 这类错误列名，009 迁移回填、054 迁移删旧列（`backend/migrations/009_fix_usage_logs_cache_columns.sql:1-37`、`backend/migrations/054_drop_legacy_cache_columns.sql:1-14`）。
14. 「已扣费但无 usage_log」的对账缺口（注释引用 issue #3656）：best-effort 队列满曾直接丢弃，改为阻塞等待 + 失败同步兜底 + `ON CONFLICT` 兜重复（`backend/internal/repository/usage_log_repo_insert.go:200-207`、`backend/internal/service/gateway_usage_billing.go:626-638`、`backend/internal/service/usage_record_worker_pool.go:21-23`）。
15. 「append-only」与实际能力矛盾：schema 注释声明只追加，仓储提供单条 DELETE、服务层有 `UsageService.Delete`，且无 HTTP 路由调用（`backend/ent/schema/usage_log.go:19` vs `backend/internal/repository/usage_log_repo_query.go:90-93`、`backend/internal/service/usage_service.go:284-290`）。
16. 死表 `billing_usage_entries`：027 迁移建的「计费账本」无任何代码引用，也未被后续迁移删除（`backend/migrations/027_usage_billing_consistency.sql:36-48`）。
17. 唯一索引静默吞行：`(request_id, api_key_id)` 唯一 + `ON CONFLICT DO NOTHING`，客户端复用 request_id 时新行被静默丢弃（`backend/migrations/027_usage_billing_consistency.sql:9-33`）。
18. 分区表形同虚设：表未分区时迁移直接 NOTICE 跳过（`backend/migrations/035_usage_logs_partitioning.sql:20-26`）。
19. 软删对象在历史日志里「变无名」：Key / Group 解析未穿透软删除（`backend/internal/repository/usage_log_repo_query.go:381-394,411-424`）。
20. 审计日志可丢：队列满静默丢弃、flush 失败不重试、崩溃丢内存队列（`backend/internal/service/audit_log_service.go:83-87,144-150`）。
21. `channel_id` 无外键、无快照，渠道改名或删除后历史日志无法还原渠道名（`backend/migrations/084_channel_billing_model_source.sql:5`）。
22. 重放 / 重试特性整体删表：retry 端点下线时 DROP `ops_retry_attempts` 与 request_body/retry_count 等列（`backend/migrations/136_remove_ops_retry_replay.sql:5-16`）。

**CLIProxyAPI**

23. 默认拿不到任何用量数据：`usage-statistics-enabled` 默认 false（`internal/config/parse.go:29`、`config.example.yaml:1146`），home 模式却强制打开（`cmd/server/main.go:380,631`）。
24. 读 usage-queue 会消费掉数据（pop 非 peek），多消费者互抢（`internal/api/handlers/management/usage.go:36`、`internal/redisqueue/queue.go:199-224`）。
25. 消费者断开即静默丢数据：订阅缓冲 256 满则删除并关闭订阅者（`internal/redisqueue/queue.go:141-160`），且队列不落盘。
26. 客户端 API key 明文进队列（`internal/redisqueue/plugin.go:157`）；上游错误体全文进 `fail.body`（`internal/redisqueue/plugin.go:243-245`）。
27. TUI 里存在画饼死键：i18n 定义了 `usage_title / usage_total_reqs / usage_rpm / usage_tpm / usage_req_by_hour`，Go 代码零引用（`internal/tui/i18n.go:191-209,348-361`）。
28. 缓存读用 present 标志位而非数值差异区分「上游没报」与「命中为 0」，消费端必须特判（`internal/redisqueue/plugin.go:112,231`）。
29. 流式日志分块通道满即丢块（`internal/logging/request_logger_streaming.go:94-98`）；日志保留无时间维度，只按目录总大小删最旧（`internal/logging/log_dir_cleaner.go:74-157`）。

**litellm**

30. `LiteLLM_ErrorLogs` 是死表：有读方（`spend_management_endpoints.py:1059,1185`、`proxy_server.py:16430`），写方全仓为零。
31. key 创建审计落明文密钥（`litellm/proxy/hooks/key_management_event_hooks.py:76-88` + `litellm/proxy/_types.py:1197-1198`）。
32. 队列超预算丢最旧日志：64MB 预算内保留最新，超出丢最旧（`litellm/proxy/utils.py:7484-7512`、`litellm/constants.py:1844`），高峰期会丢计费行，只留一条 error 日志。
33. metadata JSON 当垃圾桶：`error_information / usage_object / guardrail_information / cost_breakdown / additional_usage_values` 全塞这里（`litellm/proxy/_types.py:3631-3684`），筛选靠 `metadata->>'...'` 走 JSONB 且无索引（`spend_management_endpoints.py:2935-2946`）。
34. 敏感信息落库：`store_prompts_in_spend_logs` 打开后 prompt / response / 请求体全文入库（`schema.prisma:746-747,753`）。
35. 审计时间戳不可靠：`updated_at` 被 Prisma `@updatedAt` 每次写刷新，key 配置变更时间另存 `settings_updated_at`（`litellm/proxy/management_helpers/key_settings_audit.py:5-15`）。
36. 非流式 TTFT 恒为 NULL（`completionStartTime == endTime`），聚合时静默排除（`litellm/proxy/proxy_server.py:16095-16096`）。
37. `cache_hit` 是字符串列，历史大小写与空值导致 `LOWER(cache_hit)='true'` 统计偏差（`spend_management_endpoints.py:2910-2912`）。
38. total 被封顶 10000（`spend_management_endpoints.py:3013-3025`）；深翻页退化为 `LIMIT/OFFSET`（`:3045-3048`）。

**bifrost**

39. 吞吐系统性偏低：分母用整段 latency，未扣首字与排队（`framework/logstore/rdb.go:2224-2229,2250-2258`）。
40. 缓存命中率分母易误读：分母是「已完成请求数」，direct / semantic 命中数分别给，百分比留给前端（`framework/logstore/tables.go:260-262`、`framework/logstore/rdb.go:1866,1950-1951`）。
41. `UserFacingSuccessRate` 边界漏计，代码里自己标了 known tradeoff（`framework/logstore/rdb.go:1907-1908`）。
42. 日志页看不到中间态：正常请求只写一行（PreLLMHook 不落库、流式非末块不落库，`plugins/logging/main.go:1895,2149-2160`），processing 行只由兜底清扫插入（`plugins/logging/writer.go:713-760`）。
43. 写队列满即丢弃，只在 `/api/logs/dropped` 暴露计数（`plugins/logging/writer.go:787-790`）。
44. 落库耗时口径有两套来源：PostLLMHook 先写 provider 层估算值，trace 结束后按 root span 覆写（`plugins/logging/main.go:2864-2903`）。
45. TTFT 不入库，首字延迟分析只能走 trace 属性（`core/schemas/trace.go:1219-1221`）。
46. hybrid 模式下详情页拿不到完整负载，payload 在对象存储，列表页只能靠 `content_summary` 兜底（`framework/logstore/tables.go:407`、`framework/logstore/payload.go:19-40`）。
47. 删除实体不清理日志，旧行保留 ID+Name 快照指向已删实体（`transports/bifrost-http/handlers/logging.go:371-376`）。
48. 清理按天跑，缩短保留期后最多等一天；单轮 6 小时上限且与 MCP 日志共享时间配额（`framework/logstore/cleaner.go:13-37`）。
49. 审计能力整体缺失：before/after、diff、管理操作审计、审计 UI 都在闭源 enterprise 版（`transports/bifrost-http/lib/config_test.go:19742`）。

---

## 值得借鉴 / 应避免

**值得借鉴**

1. **ID + 名称双写快照**（bifrost `framework/logstore/tables.go:319-320,336-345`）：名称取自请求上下文快照而非查询时 join，删实体后历史日志仍可读；new-api 混用两种做法，渠道名因 `gorm:"->"` 落不了库，删渠道即变空（`model/log.go:72`）。
2. **服务端按角色裁剪日志字段，而不是前端隐藏**（new-api `model/log.go:114-121` + `model/log_other.go:232-267`；sub2api DTO 分层 `backend/internal/handler/dto/types.go:593-674,677-710`）：灵枢的用户端视图应在后端裁掉渠道、上游模型、计费档位等。
3. **`other` JSON 分 public / admin / root 三层可见性**（new-api `model/log_other.go:39-44`）：一个日志表服务三种视图，敏感诊断字段与用户字段共用一行存储。
4. **重试链与失败尝试单独留痕，但归到同一请求**（new-api 每次失败一条 type=5 错误日志 + 成功后一条 type=2 消费日志 `service/relay_error.go:76-98`；bifrost 同 provider 重试记 `number_of_retries`、跨 provider fallback 每次一行 `core/bifrost.go:7110,5927`）：一行一请求满足统计口径，一次请求多行满足排障口径，两者可共存。
5. **首字延迟与非流式显式分开**（sub2api 非流式 `first_token_ms` 留 NULL `backend/internal/service/gateway_forward.go:817-891`；litellm 非流式 `completionStartTime` 退化为 end_time `spend_tracking_utils.py:534`）：new-api 用 -1000 哨兵值（`relay/common/relay_info.go:635`）是应避免的做法。
6. **异步批量写 + 队列上限 + 丢弃计数**（bifrost `framework/logstore/config.go:27-31` + `/api/logs/dropped` `plugins/logging/main.go:1256-1257`；litellm 字节与行数双预算 `litellm/constants.py:1843-1845`）：灵枢写调用日志应走队列，同时把丢弃量暴露成指标，否则计费行静默丢失。
7. **调用日志与操作日志分开、保留策略独立**（new-api `model/audit_log.go:24-25,249-250`；sub2api 审计独立表 + 独立保留期 180 天 `backend/internal/service/setting_features.go:262-271`）：审计表不能挂在调用日志的清理链路上。
8. **审计用「变化字段名 + 状态迁移前后值」而不是全量 diff**（new-api `controller/channel.go:1233-1254`、`controller/token.go:479-480`）：litellm 存整行 JSON（`audit_logs.py:219-224`）会让审计表体积和泄密面一起放大。
9. **额度调整返回不可变前后快照**（new-api `model/user_quota_adjustment.go:16-23,25-86`，行锁 + `from/to`）：sub2api 直接 UPDATE `users.balance`（`backend/internal/repository/usage_billing_repo.go:244-275`）在并发扣费下缺少可核对的账。
10. **敏感键脱敏清单集中管理并覆盖三处来源**（sub2api 精确清单 + 账号凭证子键 + 支付密钥三份并集 `backend/internal/service/audit_log.go:122-172`）：CLIProxyAPI 把客户端 API key 明文放进队列（`internal/redisqueue/plugin.go:157`）、litellm 把明文 sk- 写进审计表（`key_management_event_hooks.py:76-88`），都是同一类失误。

**应避免**

1. **tps 分母不扣首字时间**：new-api（`common-logs-columns.tsx:702-705`）、sub2api（`frontend/src/utils/latencyHealth.ts:73-84`）、bifrost（`framework/logstore/rdb.go:2224-2229`）三家都是这样，数值系统性偏低；若灵枢要显示「输出速度」，分母应是 `总耗时 − 首字延迟`，并对输出 token 少于阈值的行不给值。附带一条：new-api 还按 30 / 15 t/s 给行着色（`web/src/features/usage-logs/lib/format.ts:217-223`），推理模型首字天然慢，这类阈值会持续误报。
2. **缓存命中率口径分歧**：new-api 完全没有（`web/src/features/usage-logs/lib/format.ts:336-340`），sub2api 只在渠道监控里算（`channel_monitor_v2_repo.go:1346`），litellm 只有二值 hit/miss（`spend_management_endpoints.py:2910-2912`），bifrost 把分母定成「已完成请求数」且百分比留给前端（`framework/logstore/tables.go:260-262`）。灵枢要先把分母定成 `cache_read / (input + cache_creation + cache_read)` 这种 token 口径，再固定在服务端算。
3. **审计与业务不同事务、且允许静默丢失**：new-api（`model/audit_log.go:134-136`）、sub2api（`backend/internal/service/audit_log_service.go:83-87,144-150`）、litellm（`audit_logs.py:243-246`）三处都是「写失败只记一条 error 日志」。灵枢若要求「改余额一定有审计」，这两件事需要同事务或同队列可靠投递。
4. **保留期清理与审计链脱钩**：new-api 的 audit_logs 无 TTL（`model/audit_log.go:249-250`）；bifrost 的清理按天跑且与 MCP 日志共享时间配额（`framework/logstore/cleaner.go:26-37`）。灵枢的两类日志要各自定保留期。
5. **静默吞行与静默封顶**：sub2api 唯一索引 + `ON CONFLICT DO NOTHING` 会吞掉复用 request_id 的新行（`backend/migrations/027_usage_billing_consistency.sql:9-33`），new-api 用户日志 total 截断 10000（`model/log.go:581,588`），litellm total 也封顶 10000（`spend_management_endpoints.py:3013-3025`）。这些都要以显式标志（如 `total_is_capped`）呈现给用户。
6. **schema 与真实表结构双轨**：sub2api 的 ent schema 缺多列，实际列靠 SQL 迁移补（`backend/migrations/061_add_usage_log_request_type.sql` 等），写入走 raw SQL（`backend/internal/repository/usage_log_repo_query.go:22`）。灵枢从第一天起让迁移文件与结构定义保持单一来源。
7. **敏感信息落进日志表**：litellm 的 prompt / response 全文入库（`schema.prisma:746-747`）、CLIProxyAPI 的明文 API key 与上游错误体进队列（`internal/redisqueue/plugin.go:157,243-245`）。灵枢的调用日志要默认不落原始请求体与响应体。
8. **用哨兵负值表达「无首字延迟」**：new-api 的 `frt = -1000`（`relay/common/relay_info.go:635`）一旦漏判就会显示负数，改用 NULL 加 `is_stream` 判断更稳。
9. **把 attempt 序号藏进 ID 计数**：CLIProxyAPI 的 usage 记录没有 attempt 字段，只能靠同一 `request_id` 下数 `execution_id`（`internal/redisqueue/plugin.go:192-194`）。灵枢若要展示重试链，直接存结构化的 `attempts` 数组或 `attempt_count` 列。
10. **管理操作不留痕**：bifrost OSS 版改 provider / VK / 预算都不留操作痕迹，只靠 `config_hash` 间接判断（`framework/configstore/tables/key.go:30`）。灵枢的用户创建/禁用、余额调整、充值入账、API key 增删、模型价格变更、渠道变更、系统设置变更都应落审计行。
