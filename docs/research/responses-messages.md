# Responses → Messages 实现与验收

## 来源和版本

本期验证客户端为 Codex CLI 0.160.0，二进制 SHA-256 `12eb3e81114588aca3b7998f4f19e8997b056aca08e57a7ca7c8a3ec8c652aad`，官方源码 tag `rust-v0.160.0` 指向 `a956835d020762cb2b570053af06f643a11c0ecc`。本仓库的脱敏合成请求见 `internal/relay/testdata/codex-0.160.0/`。原始私有环境指令和鉴权头不进入仓库。

参考 new-api、CLIProxyAPI、sub2api、Magpie 的协议思路，代码独立实现；`references/` 的源码未复制。另读本机 `agent-harness/codex` 的主题研究与 0.154.0 固定源码，用来核对工具配对、压缩验收与 WS 失效引用恢复；旧版本结论分别用本期客户端捕获核实。

- Codex 0.154.0 `codex-rs/codex-api/src/endpoint/responses_websocket.rs` 解析嵌套 `error.code=previous_response_not_found`，按可重试错误处理；`core/src/client.rs` 在重试时放弃增量引用并重发完整请求。本期 0.160.0 注入失效引用后，已实际观察到不带引用的完整输入重发。
- Codex 0.160.0 普通自定义 provider 手动及低阈值自动压缩均为普通 `/responses` 摘要请求，工具声明为空，历史仍包含工具调用；转接层回放历史工具，不把它们重新开放为可执行工具。
- 名称为 OpenAI 的自定义 provider 发送 `compaction_trigger`。0.154.0 的远程压缩收集器要求恰好一个 compaction 条目；0.160.0 已实际完成条目回灌及续轮。

## 思考与字段语义

官方查证日期 2026-11-08：

- [Anthropic effort](https://platform.claude.com/docs/en/build-with-claude/effort)：按型号区分 low/medium/high/xhigh/max；Opus 4.6、Sonnet 4.6、Mythos Preview 有 max、无 xhigh。
- [Thinking 支持矩阵](https://platform.claude.com/docs/en/build-with-claude/thinking-troubleshooting)：Opus 5.5、Fable、Mythos 思考始终开启；Sonnet 5.5 以 between_tools 控制；其余支持表见 `internal/relay/bridge/effort.go`。

始终开启思考的型号明确拒绝 `reasoning.effort=none`，提示选择 low 或更高。早期渠道 23 曾接受 disabled，与官方矩阵不同；最终实现遵循已批准的严格参数语义，正式验收使用 low/high。未知型号仅允许显式 disabled 尝试，非 none 的强度返回具体错误。

thinking/redacted thinking 使用带版本、来源厂商和型号的自包含编码，保留原始文本、签名或 redacted 内容。编码用于客户端回传，并非加密存储。外部密文、来源和型号不兼容、损坏内容均报错。

暂未提供的结构化 `text.format`、background、store:true、context_management 明确拒绝；无等价物的辅助元数据写入脱敏诊断。HTTP 转接不保存服务端历史，非空 previous_response_id 明确拒绝；WS 在连接内展开后再进入同一 HTTP 链路。

## 实际验收

真实调用仅使用本机隔离数据库中的渠道 23 配置，生产渠道、价格、路由和数据库均未修改。生产密钥只进入隔离数据库，客户端使用本地测试 key。

| 场景 | 客户端结果 | 观察内容 |
|---|---|---|
| HTTP 工具与两轮修改 | 通过 | exec_command 读文件、custom apply_patch 改动、读回、第二次修改 |
| WS 工具与两轮修改 | 通过 | 预热无模型调用；连续 previous_response_id 增量续接；真实文件与回复一致 |
| HTTP / WS 手动摘要 | 通过 | 摘要后不读文件，正确恢复随机标记、stage-two 和待验证动作 |
| HTTP / WS 低阈值自动摘要 | 通过 | 工具之间多次 contextCompaction，继续完成原任务，最终正确恢复标记和文件状态 |
| 显式 compaction_trigger | 通过 | OpenAI 名称分支产生条目，后续请求原样回灌后继续任务 |
| POST /responses/compact | 通过 | 普通 JSON response.compaction，恰好一个 compaction 条目，回灌恢复原标记 |
| 签名思考回灌 | 通过 | HTTP 原样回放 reasoning 编码，WS 缓存包含思考后的续轮正常 |
| WS 断开重连 | 通过 | 完整输入重发继续工具链；关闭连接后旧引用明确 not_found |
| WS 引用失效 | 通过 | 注入 previous_response_not_found，stream_max_retries=1 时客户端重发完整输入，完成剩余任务 |
| WS 显式取消 | 通过 | response.cancel 后返回 cancelled 终态，原 HTTP 上游取消；仅已生成部分按现行规则结算 |

针对 legacy compact 的短合成对话，渠道 23 两次返回上游内容/使用政策拒绝。换为前述已完成的真实工具历史后得到有效摘要，回灌成功；未将上游拒绝改为成功。早期故障注入使用 stream_max_retries=0，客户端直接回落 HTTP，纯 WS 测试代理不能接收 POST；修正实验重试配置后完成全量恢复。

离线回归覆盖：消息/图片和工具返回图片、namespace/additional_tools、custom 字符串转义、并行分片和调用顺序、工具配对错误、原始思考回灌、外来内容拒绝、用量最后字段覆盖、输出上限、空/截断摘要、首事件错误重试、流中断不重试、取消和一次结算。多行摘要 SSE 的 JSON 字符串保持转义，事件分隔符独立编码。

内部结算读取 Messages 四桶，外部 Responses input 为 input + cache_read + cache_write。模拟账单用量 100/200/300/50、价格 1/0.1/3/2 时 cost=$0.001120；原生 Responses、Chat、Messages、System One 全套数据库回归通过。

## 交付边界

代码、迁移 0034 和测试经 PR 交付；合并及生产发布单独执行。WS 历史只属于当前连接，最后两个响应，共享节点缓存预算 256 MiB（数据及簿记估算）；淘汰/重启/断开后重发完整历史。HTTP 原生接口仍保留厂商原始字段，原生 /compact 发到原生上游端点。
