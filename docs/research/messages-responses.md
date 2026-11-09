# Messages 客户端 → Responses 上游

2026-11-09。实现分支 `codex/messages-responses`，交付 PR，尚未发布。

## 场景与路径

客户端使用 `/v1/messages`，渠道只有 Responses 地址时，网关直接转换到 `/responses`。同渠道有 Messages 地址则原生透传，现有优先级、分组、key 选择与重试继续生效。与既有 Responses → Messages 独立，两者均不经过 Chat。

## 官方协议依据

核对日期 2026-11-09：

- OpenAI Responses create： https://developers.openai.com/api/reference/resources/responses/methods/create
- OpenAI streaming events： https://developers.openai.com/api/reference/resources/responses/streaming-events
- OpenAI reasoning items： https://developers.openai.com/cookbook/examples/responses_api/reasoning_items
- Anthropic Messages streaming： https://platform.claude.com/docs/en/build-with-claude/streaming

Responses 使用 input 中的 message / function_call / function_call_output，工具参数以 JSON 字符串返回；工具结果支持文本、图片和文件内容。完整 reasoning 加密状态应取 `response.output_item.done`，`output_item.added` 的状态可能尚未完整。

Messages 依次发送 message_start、每个 content block 的 start/delta/stop、message_delta、message_stop。工具参数 delta 是 JSON 片段，最终 input 必须是对象；thinking 签名通过 signature_delta 发出。错误使用 error 事件，不补成功终态。

## 参考仓库（只读，不继承代码）

| 本地快照 | 读过的入口 | 采用的设计结论 |
|---|---|---|
| sub2api `6b4b37db4` | `backend/internal/pkg/apicompat/anthropic_to_responses.go`、`responses_to_anthropic.go` | 有直接转换路径；工具选择可映射，reasoning 状态必须回传。灵枢保留请求的输出上限，不套用它的最低输出额度，也不把裸密文直接当作 Claude 签名。 |
| CLIProxyAPI `681ce41b` | `internal/translator/claude/openai/responses/init.go`、`claude_openai-responses_request.go` | 读到的注册为 Responses 客户端 → Claude 上游，主要用于检查现有正向转换的对称点，不能将它描述为本次反向实现证据。 |
| new-api `8faf1d4` | `relay/chat_completions_via_responses_test.go` | 存在 Claude 入口到 Responses 上游的转换链回归；路由和协议链应分别记录。 |
| magpie `b1e8eb15a` | `internal/gateway/reasoning_state_anthropic_test.go`、`anthropic_tool_pairing_test.go` | 思考状态和工具配对需独立回归。灵枢对缺失结果明确报错，不生成虚构工具结果。 |

## 实现合同

- 请求：system、user/assistant 文本、图片、文档、客户端工具及结果按顺序转换；工具名映射可逆，call_id 保持。未知工具、孤立结果和非对象参数在上游调用前报错。
- 上游使用 stream:true、store:false、include reasoning.encrypted_content；没有新增会话表；思考封装附带原响应 ID，通过既有日志将回放固定到原渠道和 key，原来源失效明确报错。JSON 上游与流式上游共用状态机。
- 文本及时输出；并行工具的分片按 Messages block 顺序输出；JSON 字符串里的换行不作为 SSE 分隔符。
- reasoning 完整条目用 `lingshu.responses.v1:` 封装，包含来源、版本和原始 JSON；这是编码，不是网关加密。只恢复可识别的 reasoning 条目，原生 Claude 签名明确拒绝。
- 结算读取 Responses 原始 usage，input = input_tokens − cached_tokens，cache_read = cached_tokens，cache_write = 0，output 包含 reasoning token。面向 Messages 客户端单独输出该协议的 usage。
- 先收到有效内容才提交流响应头；首事件错误仍能重试；已输出后错误只发错误终态。请求取消沿原有 context 取消上游。
- 请求带不支持的 stop_sequences、top_k、服务端工具或其他专用语义字段时明确报错；原生 Messages 请求继续原样透传。

## 验收记录

验收使用隔离数据库与模拟 Responses 上游，不消耗真实模型额度。最终测试、CI 与 PR 状态写入 docs/history.md；真实 Claude Code 及付费上游端到端验收仍须单独执行，不将模拟测试写成真实客户端验收。