# OpenCode Zen 原生渠道

本分支将 OpenCode Zen 接入灵枢 Go 进程；由灵枢直接连接官方推理服务，运行不需要 Bun、Magpie、NewAPI 或额外的转发容器。本轮只交付 PR，线上渠道仍维持原配置。

## 配置

新建渠道选择 **OpenCode Zen** 模板，填写自己的上游 API key，拉取模型并映射到已定价的站内模型。模板的 Chat Base URL 是 `https://opencode.ai/inference/openai/v1`，实际请求路径为 `/chat/completions`，模型列表为 `/models`。本期只验证 Chat，不做 Messages / Responses 的协议转换。

`deepseek-v4.1-flash-free` 使用独立站内模型名，可在定价页把四项单价设为零；创建模板不自动调整任何价格或现有渠道。其他模型需先冒烟测试，模型列表中的存在不保证该模型支持 Chat 协议。

2026-11-07 当前状态：下文保留历史有效批次的成功结果；后续原生请求返回 429，免费层接入保持待验收。`iceman-free` 只有官方 OpenCode 2.2.6 CLI 的单次成功，尚未取得灵枢原生通路的成功结果。模板说明同步展示待验收状态；本次只修复响应兼容性并交付 PR。

## 请求与响应

只有显式选择 `opencode-zen` 模板且上游模型名以 `-free` 结尾时，灵枢才补充编码客户端的上下文、核心工具声明与会话信息。其他模板保持原有转发行为，OpenCode 的非免费模型保留请求体。

- 保留调用者的系统提示词、消息、工具定义、推理强度、输出上限和其他模型参数。新增的通用编码上下文使用逻辑目录 `/workspace`，不读取宿主目录、环境文件或用户资料。
- 缺少的 `bash`、`edit`、`glob`、`grep`、`read` 以明确标注不可用的工具声明补齐。已有同名工具保持原定义；如果模型实际调用补充的工具，返回错误，避免执行不存在的工具或把丢失的动作伪装成成功。
- 工具名允许跨 SSE 分片；在当前 choice 结束或 `[DONE]` 时检查完整名称，保留 `readmore` 等真实工具。流式客户端可能先收到部分工具调用，再收到错误事件；收到完整成功结束前应等待最终状态。非流式调用在组装阶段整体报错，失败请求不扣费。
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


## 第二轮实验：减小改动（2026-11-07）

### 方法与有效性

总计 63 次调用，低于 70 次上限。默认 `max_tokens=128`；失效 E 批的补充诱导样本改为 512。实验发生于北京时间 2026-11-07；本轮固定沿用第一轮 UA 2.2.5，以保持 D03 条件一致，没有刷新 UA。

本轮以第一轮 D03 作为基准：个人 key、PR 的 Bun 1.3.14 ClientHello、`opencode/latest/2.2.5/cli`、一个真实格式的 `x-opencode-session`、完整系统提示词、五个占位工具、`tool_choice: "auto"`、上游 SSE 和 `include_usage=true`。模型固定 `deepseek-v4.1-flash-free`，默认问题为 `Reply with exactly: OK`，每次新建连接，调用间隔至少 4 秒。有效批次 A-D 共 39 次，每批开始和结束 D03 均 HTTP 200、`billing_source=free`；批次 E 的结束对照和批次 F 的开始对照均 HTTP 429，因此 E/F 全部作废。E/F 的结果只记录作废原因，不用于结论。

程序记录 HTTP 状态、错误前 300 字、响应体 `usage.opencode.billing_source`、输入/输出 token、`finish_reason`、结构化 `tool_calls`、实际协议和 ALPN。第一轮已知服务器没有返回 billing source 响应头，本轮继续以响应体 usage 为准。全部有效成功样本均有 `[DONE]` 和 usage，cost 为 0；输入 token 含缓存 token，输出含 reasoning token。错误 F 复用第一轮表前所列的完整 `FreeUsageLimitError` 响应，长度少于 300 字。Bun ClientHello 只提供 `http/1.1`；`ForceAttemptHTTP2` 的两次请求实际也协商为 `HTTP/1.1`，所以本轮没有获得 Go HTTP/2 成功样本。

### 结果表

表中条件列依次是系统提示词、工具集合、`tool_choice`、问题；`full` 是完整注入，`first` 只保留第一句，`emptyenv` 为空环境块，`unrelated` 为普通助手提示词，`userbefore/userafter` 分别表示用户 system 在注入前/后；注入内容均完整。用户 system 固定为 `Answer concisely in English. Preserve any requested tool use.`。`files` 问题为 `List the files in the current directory.`，`weather` 为 `What's the weather in Paris?`；`get_weather` 声明必填字符串参数 `city`。`five` 为五个占位工具，`bash`/`read`/`two` 为对应最小集合，`empty-schema` 为五个同名工具但参数 schema 为空对象，`renamed`/`noop` 使用非 OpenCode 名称。工具集合列中的 `none` 表示不发送工具，choice 列中的 `none` 表示声明工具但禁止结构化调用；`user` 是五个占位工具加 `get_weather`。SSE 内容中出现 DSML 文本不计为结构化 `tool_calls`。

| 组别 | 条件（system/tools/choice/question） | HTTP | 错误 | billing | in/out | tool_calls | finish | 协议 | 内容摘要 |
|---|---|---:|---|---|---:|---|---|---|---|
| R2A01-D03-start | full/five/auto/ok | 200 | — | free | 542/21 | — | stop | HTTP/1.1 | OK |
| R2A02-none-ok | full/five/none/ok | 200 | — | free | 95/3 | — | stop | HTTP/1.1 | OK |
| R2A03-none-files | full/five/none/files | 200 | — | free | 98/77 | — | stop | HTTP/1.1 |   <｜DSML｜ calls> <｜DSML｜ invoke name="bash"> <｜DSML｜ parameter name="command" string="true">ls -la</ |
| R2A04-auto-files | full/five/auto/files | 200 | — | free | 545/128 | — | length | HTTP/1.1 | I'm not able to list the directory contents. Although bash/glob/read-style tools are listed in my en |
| R2A05-auto-files | full/five/auto/files | 200 | — | free | 545/128 | — | length | HTTP/1.1 | I’m unable to list the |
| R2A06-auto-files | full/five/auto/files | 200 | — | free | 545/128 | — | length | HTTP/1.1 | I can't list the files for you — in this session none of the tools that would let me inspect the fil |
| R2A07-user-weather | full/user/auto/weather | 200 | — | free | 592/55 | get_weather | tool_calls | HTTP/1.1 | — |
| R2A08-none-ok-repeat | full/five/none/ok | 200 | — | free | 95/3 | — | stop | HTTP/1.1 | OK |
| R2A09-none-files-repeat | full/five/none/files | 200 | — | free | 98/128 | — | length | HTTP/1.1 | — |
| R2A10-user-weather-repeat | full/user/auto/weather | 200 | — | free | 592/58 | get_weather | tool_calls | HTTP/1.1 | — |
| R2A11-D03-end | full/five/auto/ok | 200 | — | free | 542/16 | — | stop | HTTP/1.1 | OK |
| R2B01-D03-start | full/five/auto/ok | 200 | — | free | 542/3 | — | stop | HTTP/1.1 | OK |
| R2B02-first-sentence | first/five/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2B03-empty-env | emptyenv/five/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2B04-unrelated | unrelated/five/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2B05-injection-before | userafter/five/auto/ok | 200 | — | free | 557/3 | — | stop | HTTP/1.1 | OK |
| R2B06-injection-after | userbefore/five/auto/ok | 200 | — | free | 557/3 | — | stop | HTTP/1.1 | OK |
| R2B07-first-sentence-repeat | first/five/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2B08-empty-env-repeat | emptyenv/five/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2B09-D03-end | full/five/auto/ok | 200 | — | free | 542/12 | — | stop | HTTP/1.1 | OK |
| R2C01-D03-start | full/five/auto/ok | 200 | — | free | 542/14 | — | stop | HTTP/1.1 | OK |
| R2C02-bash | full/bash/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2C03-read | full/read/auto/ok | 200 | — | free | 353/3 | — | stop | HTTP/1.1 | OK |
| R2C04-two | full/two/auto/ok | 200 | — | free | 400/17 | — | stop | HTTP/1.1 | OK |
| R2C05-empty-schema | full/empty-schema/auto/ok | 200 | — | free | 467/3 | — | stop | HTTP/1.1 | OK |
| R2C06-renamed-five | full/renamed/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2C07-noop | full/noop/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2C08-D03-end | full/five/auto/ok | 200 | — | free | 542/3 | — | stop | HTTP/1.1 | OK |
| R2D01-D03-start | full/five/auto/ok | 200 | — | free | 542/15 | — | stop | HTTP/1.1 | OK |
| R2D02-go-h2 | full/five/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2D03-go-h2-repeat | full/five/auto/ok | 429 | F (429) | — | — | — | — | HTTP/1.1 | — |
| R2D04-read-repeat | full/read/auto/ok | 200 | — | free | 353/3 | — | stop | HTTP/1.1 | OK |
| R2D05-two-repeat | full/two/auto/ok | 200 | — | free | 400/15 | — | stop | HTTP/1.1 | OK |
| R2D06-schema-repeat | full/empty-schema/auto/ok | 200 | — | free | 467/3 | — | stop | HTTP/1.1 | OK |
| R2D07-before-repeat | userafter/five/auto/ok | 200 | — | free | 557/3 | — | stop | HTTP/1.1 | OK |
| R2D08-after-repeat | userbefore/five/auto/ok | 200 | — | free | 557/17 | — | stop | HTTP/1.1 | OK |
| R2D09-read-none | full/read/none/ok | 200 | — | free | 95/3 | — | stop | HTTP/1.1 | OK |
| R2D10-read-none-repeat | full/read/none/ok | 200 | — | free | 95/3 | — | stop | HTTP/1.1 | OK |
| R2D11-D03-end | full/five/auto/ok | 200 | — | free | 542/3 | — | stop | HTTP/1.1 | OK |

### 失效批次

| 批次 | 请求数 | 对照 | 处理 |
|---|---:|---|---|
| E | 15 | 首次 D03 为 200，结束 D03 为 429 | 整批作废；不使用环境字段、用户工具、长输出和 `tool_choice:none` 的重复结果 |
| F | 6 | 首次 D03 为 429，结束 D03 为 429 | 整批作废；不使用会话复用和一天前 session 的结果 |

两批失败请求均返回同一 `FreeUsageLimitError`。F 批采用预排批量执行，起始对照失败后仍发出了剩余五次请求，共计入 60 次预算；这些请求全为无效结果。有效批次的对照成功与失效批次的对照失败发生在同一账户和出口上，说明服务状态在实验期间发生了变化。

### 结论

- `tool_choice: "none"` 在简单问题上两次成功，输入/output 为 95/3，模型返回 OK。诱导文件问题两次也返回 HTTP 200，均没有结构化 `tool_calls`；其中一次正文出现 DSML 风格的 `invoke name="bash"`，另一次用完 128 token 思考预算后正文为空（`finish_reason=length`）。本程序没有执行任何工具，客户端可能看到模拟工具调用的文本。
- `auto` 下的三次诱导文件请求都返回 HTTP 200，均没有结构化工具调用，且 `finish_reason=length`，输出被 128 token 上限截断；这只能记录为 0/3 结构化占位工具调用，不能当作低概率估计。长输出复测所在 E 批作废，未形成有效分母。
- 用户自带工具的有效样本在 A 批含五个占位工具时两次都调用了结构化 `get_weather`，说明用户工具可以正常保留。只带用户工具的关键测试落在失效 E 批，尚未形成可用结论。
- 系统提示词的有效 B 批中，完整提示词改成只保留第一句、空环境块或无关提示词均为 429；完整提示词放在用户 system 前或后均为 200。第一轮提示词消融与本轮 B 批共同支持保留完整 OpenCode 提示词；E 批对更细的环境字段拆分作废。
- 工具集合的有效 C/D 批中，单个 `read`、`bash+read`、五个同名工具的空 schema 均成功，其中 `read` 和 `bash+read` 有重复成功；只留 `bash`、把五个工具改成无关名称或只用 `noop` 均 429。当前重复成功的最小工具集合是单个 `read`（保留 PR 原 schema）。五工具的空 schema 是另一项成功结果；“read+空 schema”未组合验证。无关名称的失败与名称相关，但名称、工具数和 schema 的完整服务端规则仍待细分。
- Go 标准 Transport 开启 `ForceAttemptHTTP2` 的两次请求实际 ALPN 为 `http/1.1` 且 HTTP 429；这既没有得到 HTTP/2 成功，也没有证明 Go TLS 本身可以移除。需要以后用带 h2 ALPN 的同一 Bun spec 或真正 HTTP/2 客户端再拆分。
- 会话复用和过期 session 批次无有效对照，缓存 TTL 暂不调整。

### 对 PR #21 的精简建议

1. 工具补全的精简候选是只补同名 `read`（已有同名真实工具时保留调用方定义），依据 R2C03/R2D04；`bash+read` 是另一已重复成功的集合（R2C04/R2D05）。正式采用前需补测单 read 与用户真实工具组合以及诱导工具调用；本轮真实工具成功样本仍携带五个占位工具。五工具使用空参数 schema 的结果也可用（R2C05/R2D06）；如要同时改成单 read 和空 schema，应另测组合后再采用。
2. `tool_choice: "none"` 可作为调用方没有自带工具时的候选策略：简单问题的重复依据是 R2A02/R2A08/R2D09/R2D10。诱导问题暴露 DSML 文本和空输出，因此暂不建议作为默认通用策略；保留调用方已有 tool_choice，继续允许其真实工具调用。
3. 注入提示词放在用户 system 前后都能通过，依据 R2B05/R2B06/R2D07/R2D08；具体删减环境字段需重跑有效对照，E 批已作废。
4. 暂不删除 uTLS、强制上游 SSE 或完整提示词；Go HTTP/2 本轮没有成功样本。工具自动调用路径仍保留占位工具调用检测，单 read 成功不代表该风险已消失。
5. 暂不修改会话缓存 TTL；复用/过期 session 未形成有效批次。

本节只记录实验和建议，未修改 PR #21 实现、未部署、未修改线上数据。

### 传输细节与作废记录

- `R2A01-D03-start`：ALPN `http/1.1`，Accept-Encoding `gzip`，Connection `close`；实际写入头顺序 `Host → User-Agent → Content-Length → Authorization → Content-Type → X-Opencode-Session → Accept-Encoding → Connection`。顺序只含字段名，未记录鉴权值。
- `R2D02-go-h2`：ALPN `http/1.1`，Accept-Encoding `gzip`，Connection `close`；实际写入头顺序 `Host → User-Agent → Content-Length → Authorization → Content-Type → X-Opencode-Session → Accept-Encoding → Connection`。顺序只含字段名，未记录鉴权值。

Bun 捕获的 ALPN 仅提供 `http/1.1`；Go 允许 HTTP/2 协商但服务端实际选择 `http/1.1`。两者实际压缩头均为 gzip，应用请求头顺序相同；本轮没有得出真正 HTTP/2 的判定结果。

以下记录全部标为 **INVALID**，只供复核批次失败及用量，不参与上面的结论。条件中的额外 system 值表示分别删除某一环境字段、只保留环境块或只保留工作目录；session=reuse 三次使用同一进程内编号，old 为一天前生成规则的编号。

| 组别（全作废） | system/tools/choice/question/session/max_tokens | HTTP | 错误 | billing | in/out | tool_calls | finish | 协议 |
|---|---|---:|---|---|---|---|---|---|
| R2E01-D03-start INVALID | full/five/auto/ok/new/128 | 200 | — | free | 542/13 | — | stop | HTTP/1.1 |
| R2E02-envonly INVALID | envonly/five/auto/ok/new/128 | 200 | — | free | 520/31 | — | stop | HTTP/1.1 |
| R2E03-minenv INVALID | minenv/five/auto/ok/new/128 | 200 | — | free | 494/16 | — | stop | HTTP/1.1 |
| R2E04-nodir INVALID | nodir/five/auto/ok/new/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2E05-noroot INVALID | noroot/five/auto/ok/new/128 | 200 | — | free | 532/3 | — | stop | HTTP/1.1 |
| R2E06-nogit INVALID | nogit/five/auto/ok/new/128 | 200 | — | free | 533/3 | — | stop | HTTP/1.1 |
| R2E07-noplatform INVALID | noplatform/five/auto/ok/new/128 | 200 | — | free | 537/3 | — | stop | HTTP/1.1 |
| R2E08-user-tool-only INVALID | full/user-only/auto/weather/new/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2E09-omit-choice INVALID | full/five/omit/ok/new/128 | 200 | — | free | 542/3 | — | stop | HTTP/1.1 |
| R2E10-auto-files-512 INVALID | full/five/auto/files/new/512 | 200 | — | free | 545/93 | — | stop | HTTP/1.1 |
| R2E11-auto-files-512 INVALID | full/five/auto/files/new/512 | 200 | — | free | 545/245 | — | stop | HTTP/1.1 |
| R2E12-auto-files-512 INVALID | full/five/auto/files/new/512 | 200 | — | free | 545/245 | — | stop | HTTP/1.1 |
| R2E13-none-files-512 INVALID | full/five/none/files/new/512 | 200 | — | free | 98/51 | — | stop | HTTP/1.1 |
| R2E14-none-files-512-repeat INVALID | full/five/none/files/new/512 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2E15-D03-end INVALID | full/five/auto/ok/new/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2F01-D03-start INVALID | full/five/auto/ok/new/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2F02-reuse-1 INVALID | full/five/auto/ok/reuse/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2F03-reuse-2 INVALID | full/five/auto/ok/reuse/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2F04-reuse-3 INVALID | full/five/auto/ok/reuse/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2F05-old-session INVALID | full/five/auto/ok/old/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |
| R2F06-D03-end INVALID | full/five/auto/ok/new/128 | 429 | F (429) | — | — | — | — | HTTP/1.1 |


### 第二把 key 的复核（2026-11-07）

用户提供第二把 OpenCode key 后，使用同一临时 runner 先发 D03 起始对照：个人 key、Bun ClientHello、`opencode/latest/2.2.5/cli`、真实格式 `x-opencode-session`、完整提示词、五个工具、`tool_choice: "auto"`、流式。该请求返回 HTTP 429 `FreeUsageLimitError`，没有 usage 或 `billing_source`；随后按用户要求重试一次，仍为 429。按批次规则未继续发送精简组合，因此第二把 key 没有产生有效的提示词、工具或协议结论，也没有将该结果解释为 key 类型差异。

第三把 key 的 D03 起始对照也返回 HTTP 429 `FreeUsageLimitError`，没有 usage 或 `billing_source`，按规则立即停止，未发送后续精简组合。第二把重试和第三把起始对照共增加 2 次请求，均只作为无效对照记录；本轮累计 63 次调用。临时程序和远端文件已清理。
