# System One：TypeSafe 与 Nace

2026-10-22，本地实现和真实上游验证完成，**未发布线上**。范围与决策见 [decisions.md](../decisions.md)。

## 渠道配置与调用

在管理端「渠道」选择 TypeSafe 或 Nace 模板，填对应上游 key，拉取并选择模型，映射到已定价的站内模型。模板只预填协议地址，不写入密钥或自动创建价格。

| 模板 | System One 地址（填入后台） | 实测模型 | 输入单价（USD / 百万 token） | 输出单价 |
|---|---|---|---:|---:|
| TypeSafe | `https://api.typesafe.ai/v1` | `jev-latest` → `jev-1.14.0` | 0.042 | 0 |
| Nace | `https://console.nace.ai/v1` | `drex-v2.0` | 0.06 | 0 |

地址后由灵枢追加 `/systemone` 或 `/models`。其他三个协议地址在这两个模板中留空。两家都用 `Authorization: Bearer`，每个渠道的代理设置同样生效。

用户向灵枢的 `/v1/systemone` 发送 JSON，鉴权使用灵枢 key：

```bash
curl "$LINGSHU_BASE_URL/v1/systemone" \
  -H "Authorization: Bearer $LINGSHU_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "drex-v2.0",
    "state": "Help! My payouts have been failing for 3 days.",
    "questions": {
      "is_urgent": {"type": "noul", "instructions": "Does this convey urgency?"}
    }
  }'
```

`model` 使用站内名称；其余字段原样交给上游，响应也原样返回。已实测 `noul`、`choice`、`score`。TypeSafe 的 score `legend` 是对象，Nace 是数组，灵枢保留各自原始结构。仅接受非流式请求；`stream: true` 在请求上游前返回 400，并提示移除该参数。

上游模型发现读取 `{ "models": [{ "name": "…" }] }`，兼容原有渠道的 `{ "data": [{ "id": "…" }] }`。这是后台的上游拉取功能；灵枢对外 `GET /v1/models` 仍使用已有的 OpenAI 格式，TypeSafe SDK 的 `models.list()` 返回格式尚未适配。调用 `/v1/systemone` 不依赖该发现接口。

## 用量和计费

- 读取 `usage.input_tokens` / `output_tokens`，分别作为输入 / 输出；输出 token 仍记日志，单价由管理员填 0。
- 输入和输出字段都存在（允许显式 0）才标为真实用量。缺失或不完整时按内容估算，并标记 `usage_estimated`。
- 成功响应必须为合法 JSON 且含非空 `answers`。上游返回 200 却没有有效结果时视为失败，可在返回客户端前换渠道；不向用户收费。
- 沿用现有按分组选路、模型名映射、鉴权、额度、重试和健康状态。529 可以换渠道，422 不重试；最终只对成功的一次结算。
- TypeSafe `jev-latest`、`jev-preview` 当前输入价均为 0.042；Nace `drex-v1.5` 为 0.05，`drex-v2.0`、`drex-max` 为 0.06。别名、可用模型和价格以厂商当前接口与文档为准；不根据这次请求的耗时比较厂商性能。

## 验收证据

真实调用均通过本地最终构建，使用独立验证库，分组倍率为 1。相同状态下批量询问 `noul` / `choice` / `score`，每次三个问题，两家均 HTTP 200：

| 请求模型 | 输入 token | 输出 token | 日志扣费（USD） | 用量估算 |
|---|---:|---:|---:|---|
| `jev-latest` | 395 | 72 | 0.00001659 | 否 |
| `drex-v2.0` | 82 | 181 | 0.00000492 | 否 |

两笔金额都等于输入 token × 官方输入单价 / 1,000,000。更早的单问题渠道测试也通过：Jev 为 283 / 23 token，Drex 为 26 / 24 token。脱敏样本保存在 `internal/relay/usage/testdata/*_systemone.json`。

自动化验证包括：全量 Go 测试（数据库测试串行运行）、前端类型检查和 `make build`。新测试覆盖模板返回、渠道地址保存 / 修改 / 读取、保留 key 与不回传 key、模型列表解析、非流式渠道测试、请求模型映射及未知字段保留、精确计费与上游成本、鉴权和余额拒绝、错误入口提示、529 重试、422 不重试、流式拒绝、缺失用量估算及无效成功响应不扣费。原有三种聊天协议的测试通过。

Playwright 检查：TypeSafe / Nace 模板可选，切换时只预填 System One 地址；渠道列表显示协议，真实渠道测试显示「通过」及输入 / 输出用量；用户模型页显示 `/v1/systemone`。相关页面无浏览器控制台错误。页面截图和快照在本次 `/tmp/lingshu-systemone-browser-*` 目录保留，供复核。

验证库为 `lingshu_systemone_test_20261022`（自动化测试）和 `lingshu_systemone_verify_20261022`（真实接口及页面验证）。真实上游验证结束后，验证渠道已停用，上游密钥已替换为无效占位符，测试灵枢 key 已停用；库和页面产物保留。正式库、现有渠道、线上服务均未改动。

本机 WSL 默认 Node 18 不满足当前 Vite 的要求。复验构建时使用已安装的 Node 22，例如：

```bash
PATH="$HOME/.nvm/versions/node/v22.22.3/bin:$PATH" make build
LINGSHU_TEST_DATABASE_URL='postgres://lingshu:lingshu_dev@127.0.0.1:55432/lingshu_systemone_test_20261022?sslmode=disable' \
  go test -p 1 ./...
```

测试命令会重建**专用测试库**的 public schema，只能指向可丢弃的测试库。

## 官方来源

- [TypeSafe System One API](https://docs.typesafe.ai/api.md)
- [TypeSafe 模型与价格](https://docs.typesafe.ai/models.md)
- [TypeSafe 模型列表契约](https://docs.typesafe.ai/api/list-models.md)
- [Nace 从 TypeSafe 迁移](https://console.nace.ai/docs/drex/guides/migrate-from-typesafe)
- [Nace 模型](https://console.nace.ai/docs/drex/reference/models)
- [Nace 计费](https://console.nace.ai/docs/drex/reference/pricing)
