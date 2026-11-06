# OpenCode Zen 原生渠道

本分支将 OpenCode Zen 接入灵枢 Go 进程；由灵枢直接连接官方推理服务，运行不需要 Bun、Magpie、NewAPI 或额外的转发容器。本轮只交付 PR，线上渠道仍维持原配置。

## 配置

新建渠道选择 **OpenCode Zen** 模板，填写自己的上游 API key，拉取模型并映射到已定价的站内模型。模板的 Chat Base URL 是 `https://opencode.ai/inference/openai/v1`，实际请求路径为 `/chat/completions`，模型列表为 `/models`。本期只验证 Chat，不做 Messages / Responses 的协议转换。

`deepseek-v4.1-flash-free` 使用独立站内模型名，可在定价页把四项单价设为零；创建模板不自动调整任何价格或现有渠道。其他模型需先冒烟测试，模型列表中的存在不保证该模型支持 Chat 协议。

## 请求与响应

只有显式选择 `opencode-zen` 模板且上游模型名以 `-free` 结尾时，灵枢才补充编码客户端的上下文、核心工具声明与会话信息。其他模板保持原有转发行为，OpenCode 的非免费模型保留请求体。

- 保留调用者的系统提示词、消息、工具定义、推理强度、输出上限和其他模型参数。新增的通用编码上下文使用逻辑目录 `/workspace`，不读取宿主目录、环境文件或用户资料。
- 缺少的 `bash`、`edit`、`glob`、`grep`、`read` 以明确标注不可用的工具声明补齐。已有同名工具保持原定义；如果模型实际调用补充的工具，返回错误，避免执行不存在的工具或把丢失的动作伪装成成功。
- 上游始终请求 SSE 与完整 usage。流式调用沿用现有用量解析及首字前重试规则；非流式调用收完整 SSE 后返回 Chat completion JSON，保留文本、推理、工具调用、结束原因和完整 usage。截断或错误事件不算成功。
- 会话 ID 按灵枢调用 key、渠道及客户端会话隔离并在内存中有限缓存；无显式会话时用首条用户消息归属。重启后会话 ID 可变化。项目归属用 `global`，不把服务器路径当作用户项目。
- 上游鉴权使用该渠道自己的 key；普通用户的灵枢 key 不会发给厂商。计费、冻结、分组权限、路由重试及调用日志继续走原流程。

## 验证依据

2026-11-06 同机对照中，Go 默认 TLS 和 Chrome ClientHello 的请求返回 `429 FreeUsageLimitError`；使用本地捕获的 Bun ClientHello 构造 Go uTLS 请求，直接从 DMIT 调用得到 HTTP 200、`OK`、`opencode.billing_source: free` 和零费用。因此该模板使用单独的原生 Go TLS 传输，普通渠道不受影响。该次对照没有逐项排除请求头与请求体的影响；后续单变量实验见下节。

公开合同与模型可用性会变化。官方说明免费层面向 OpenCode 内调用，服务端限制仍可能返回 403 / 429；灵枢沿用现有错误和重试规则，不将免费模型改成收费模型兜底。当前验证范围为 `deepseek-v4.1-flash-free`。

参考来源：[官方推理 API](https://opencode.ai/v2/docs/console/api/inference/)、[模型目录](https://opencode.ai/v2/docs/console/models/)、[uTLS](https://github.com/refraction-networking/utls)。


## 判定条件实验（2026-11-07）

在 DMIT 直连官方 `/inference/openai/v1/chat/completions`，固定模型 `deepseek-v4.1-flash-free`，用户消息为 `Reply with exactly: OK`，`max_tokens=128`。共 44 次，间隔至少 4 秒；各批前后以 PR #21 当前组合验证，8 次对照全部 HTTP 200、输出 OK、`billing_source=free`、cost 0。四批各自的开始和结束对照均成功，个人 key 的比较有效。服务器没有返回包含 billing source 的响应头；表中的来源来自响应体 SSE 的 `usage.opencode.billing_source`。所有成功请求返回完整 SSE 和 `[DONE]`；429 无 usage。成功的 input/output tokens 分别见表，包含缓存 token，output 包含可能的 reasoning token。

固定版本：更新服务 `https://opencode.ai/update/api/latest/cli/npm` 当时返回 `2.2.5`。`latest` UA 是 `opencode/latest/2.2.5/cli`；`old` 是 PR 原有的 `opencode/1.19.4 ai-sdk/provider-utils/4.0.23 runtime/bun/1.4.4`；`none` 为不发送 UA。TLS `go` 是 Go 标准 `http.Transport`、HTTP/1.1；`go-auto` 使用默认协商（实测仍为 HTTP/1.1）；`bun` 是 PR 的 uTLS Bun 1.3.14 ClientHello，全部采用 HTTP/1.1。每次新建连接和新的真实格式会话，真实会话采用 `ses_` + 倒序毫秒 + 随机后缀，任意值固定 `abc123`。

表中条件依次为 TLS、UA、会话头数量/编号、请求体、key、上游流式、其他头。`four` 为四个同值会话头，`one` 只保留 `x-opencode-session`，`none` 全不带；请求体 `none` 为原始消息，`system` 加 PR 系统提示词，`tools` 加五个 PR 占位工具及 `tool_choice=auto`，`both` 两者均加。S 为 `stream=true` 且 `stream_options.include_usage=true`，N 为 `stream=false`、不发送 stream_options。`magpie` 带 `x-opencode-client: cli`、固定 SHA-1 项目标识、`b3`、`traceparent`、`Accept: */*`；`pr` 使用 `x-opencode-project: global`、`traceparent`、`x-opencode-client`、`Accept`，不带 b3；`bare` 去掉上述所有头。所有请求保留 Authorization 和 Content-Type。个人 key 从现有渠道读入内存；`public` 是公开匿名 key。

失败结果 F 为相同的完整响应（少于 300 字）：`{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached. Free models work in OpenCode (https://opencode.ai); subscribe to OpenCode Go to keep going: https://opencode.ai/go"}}`。相同个人 key 的起止对照成功，表明该报错也会出现在本批测试的请求条件不满足时。匿名 key 的完整 PR 对照同样返回 429，可能是匿名额度限制；它没有成功对照，无法区分额度和识别条件。

| 组别 | 条件（TLS, UA, 会话/格式, 请求体, key, S/N, 其他头） | HTTP | 错误 | billing_source | input/output tokens |
|---|---|---:|---|---|---|
| A01-control-start | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/2 |
| A02-magpie-stream | go, latest, four/real, none, own, S, magpie | 429 | F (429) | — | — |
| A03-magpie-nonstream | go, latest, four/real, none, own, N, magpie | 429 | F (429) | — | — |
| A04-control-end | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/13 |
| B01-control-start | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/41 |
| B02-magpie-public | go, latest, four/real, none, public, S, magpie | 429 | F (429) | — | — |
| B03-magpie-go-auto | go-auto, latest, four/real, none, own, S, magpie | 429 | F (429) | — | — |
| B04-magpie-bun | bun, latest, four/real, none, own, S, magpie | 429 | F (429) | — | — |
| B05-go-system | go, latest, four/real, system, own, S, magpie | 429 | F (429) | — | — |
| B06-go-tools | go, latest, four/real, tools, own, S, magpie | 429 | F (429) | — | — |
| B07-go-both | go, latest, four/real, both, own, S, magpie | 429 | F (429) | — | — |
| B08-bun-system | bun, latest, four/real, system, own, S, magpie | 429 | F (429) | — | — |
| B09-bun-tools | bun, latest, four/real, tools, own, S, magpie | 429 | F (429) | — | — |
| B10-bun-both | bun, latest, four/real, both, own, S, magpie | 200 | — | free | 542/2 |
| B11-control-end | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/13 |
| C01-control-start | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/3 |
| C02-no-session | bun, latest, none/real, both, own, S, magpie | 429 | F (429) | — | — |
| C03-one-session | bun, latest, one/real, both, own, S, magpie | 200 | — | free | 542/3 |
| C04-four-arbitrary | bun, latest, four/arbitrary, both, own, S, magpie | 429 | F (429) | — | — |
| C05-one-arbitrary | bun, latest, one/arbitrary, both, own, S, magpie | 429 | F (429) | — | — |
| C06-no-ua | bun, none, four/real, both, own, S, magpie | 429 | F (429) | — | — |
| C07-old-ua | bun, old, four/real, both, own, S, magpie | 200 | — | free | 542/19 |
| C08-go-tls | go, latest, four/real, both, own, S, magpie | 429 | F (429) | — | — |
| C09-public-key | bun, latest, four/real, both, public, S, magpie | 429 | F (429) | — | — |
| C10-nonstream | bun, latest, four/real, both, own, N, magpie | 429 | F (429) | — | — |
| C11-no-system | bun, latest, four/real, tools, own, S, magpie | 429 | F (429) | — | — |
| C12-no-tools | bun, latest, four/real, system, own, S, magpie | 429 | F (429) | — | — |
| C13-control-headers | bun, latest, four/real, both, own, S, pr | 200 | — | free | 542/2 |
| C14-bare-headers | bun, latest, four/real, both, own, S, bare | 200 | — | free | 542/2 |
| C15-control-end | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/16 |
| D01-control-start | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/2 |
| D02-public-pr | bun, old, four/real, both, public, S, pr | 429 | F (429) | — | — |
| D03-one-minimal-headers | bun, latest, one/real, both, own, S, bare | 200 | — | free | 542/3 |
| D04-one-minimal-headers-repeat | bun, latest, one/real, both, own, S, bare | 200 | — | free | 542/13 |
| D05-one-minimal-go | go, latest, one/real, both, own, S, bare | 429 | F (429) | — | — |
| D06-one-minimal-old-ua | bun, old, one/real, both, own, S, bare | 200 | — | free | 542/16 |
| D07-one-minimal-no-ua | bun, none, one/real, both, own, S, bare | 429 | F (429) | — | — |
| D08-one-minimal-arbitrary | bun, latest, one/arbitrary, both, own, S, bare | 429 | F (429) | — | — |
| D09-one-minimal-no-session | bun, latest, none/real, both, own, S, bare | 429 | F (429) | — | — |
| D10-one-minimal-nonstream | bun, latest, one/real, both, own, N, bare | 429 | F (429) | — | — |
| D11-one-minimal-tools | bun, latest, one/real, tools, own, S, bare | 429 | F (429) | — | — |
| D12-one-minimal-system | bun, latest, one/real, system, own, S, bare | 429 | F (429) | — | — |
| D13-one-minimal-public | bun, latest, one/real, both, public, S, bare | 429 | F (429) | — | — |
| D14-control-end | bun, old, four/real, both, own, S, pr | 200 | — | free | 542/13 |

**Magpie 当前源码核对**

本轮只读核对 [Magpie `8cbaeec`](https://github.com/yetone/magpie/tree/8cbaeecae976d2a8561293c1abc5daf6aa89d77f)。它的免费模型并非只改请求头：[`zenfree.go`](https://github.com/yetone/magpie/blob/8cbaeecae976d2a8561293c1abc5daf6aa89d77f/internal/gateway/zenfree.go) 补齐核心工具和 `<env>`；[`gateway.go`](https://github.com/yetone/magpie/blob/8cbaeecae976d2a8561293c1abc5daf6aa89d77f/internal/gateway/gateway.go) 强制上游流式并重建请求头。其工具名称和 schema、环境内容与本实验 PR 请求体不同。Magpie 使用 Go 默认传输，但其真实线路可能协商 HTTP/2；本实验全部实际协商为 HTTP/1.1，也未对不同 `Accept-Encoding` 做消融。因而 B10/C08 表现是这两种完整传输配置的差异，仍不能单独归因到 TLS ClientHello 的某个字段。Magpie 源码中的会话格式检查是客户端实现规则，服务端具体校验规则仍以黑盒实验为准。

**观察与建议**

- 当前测试中最精简且重复成功的组合是：个人 key、Bun ClientHello、OpenCode UA、一个真实格式 `x-opencode-session`、系统提示词、五个占位工具、上游流式。D03/D04 各成功一次；逐项去掉会话、改成 `abc123`、去掉 UA、换 Go TLS、去掉系统提示词、去掉工具或关闭上游流式均返回 429。这里的“最精简”指本实验覆盖的消融范围：工具个数、具体提示词、UA 内容及会话格式的内部规则仍未进一步缩减。
- 会话头数量可以从四个缩为一个：C03 与 B10 都成功；在其他辅助头全部去掉后，D03/D04 仍成功。`abc123` 失败，说明值并非任意字符串即可，实际校验字段和时间窗口仍未知。
- 新版和旧版 UA 都可用（B10、C07、D06）；缺 UA 失败（C06、D07）。因此本批不支持“1.19 一律 429”。没有测试任意非 OpenCode UA。
- 其他条件相同，B10 与 C08、D03 与 D05 分别显示 Bun 成功、Go 默认 TLS 失败；传输组合对本组请求有影响；HTTP 版本、压缩请求头等仍需控制，不能将差异精确归因到 ClientHello。仅换 Bun 而保持原始请求体仍失败（B04），系统提示词和工具共同加入后才通过（B10）。是否分别存在更短的可用内容需另测。
- 同一成功组合上游 `stream=false` 返回 429（C10、D10），因此非流式客户端仍需由灵枢消费上游 SSE 并组装响应。
- 可供确认后的 PR 精简方向：只保留 `x-opencode-session`，移除其余三个冗余会话头；测试组里项目、链路、`x-opencode-client` 和 `Accept` 均可去掉。是否保留这些头以兼容厂商未来规则属于实现决策。当前证据支持暂时保留 Bun TLS、提示词、工具和流式转换；匿名 key 需在其对照成功时重测。以上为建议，本次未修改实现。

这些结果是该时间、模型、出口及账户上的黑盒观察；服务端可能调整规则，429 也可能由额度触发。
