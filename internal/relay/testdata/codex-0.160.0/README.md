# Codex 0.160.0 请求结构回归

由隔离 app-server 抓包整理，CLI 二进制 SHA-256 为 `12eb3e81114588aca3b7998f4f19e8997b056aca08e57a7ca7c8a3ec8c652aad`。保留请求字段、控制条目与增量续接结构；系统指令、环境路径、工具描述和会话信息替换为合成测试内容，鉴权头全部移除。胶囊占位符仅用于描述字段形状，实际回灌测试使用转换器生成的胶囊。

- `http-local-compaction.json`：普通自定义 provider 的生成、普通摘要、摘要后续轮。
- `http-auto-compaction.json`：低阈值自动摘要与续轮。
- `http-remote-compaction.json`：OpenAI 名称分支的 compaction_trigger 和回灌。
- `ws-continuation.json`：generate:false、完整输入、previous_response_id 与增量输入。
