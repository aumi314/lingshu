# 钱包充值与支付接入

Lingshu 各实例独立管理商户、充值订单和 USD 余额。支付服务分别适配，共用订单与入账事务。部署者使用自己的域名和商户；仓库不包含真实凭据。

## 部署配置

在应用环境文件设置，重启后生效：

```dotenv
LINGSHU_PUBLIC_URL=https://gateway.piqode.com
```

开发可用 `http://localhost:8300`。该地址生成通知及返回地址，不读取请求 Host。反向代理应原样转发 `/api/payments/` 的方法、查询参数、原始请求体和签名头，公开通知入口无需用户登录。

“系统设置 → 支付设置”有充值总开关和五个独立服务。总开关默认关闭。各服务配置收款环境、收款单价、USD 到账额度范围及商户信息；EPay 使用真实网关，只提供生产环境，其他服务保留测试环境。未配置、停用或处于测试环境的服务不会出现在用户支付方式中。保存只保存本地配置，Pancake 创建远端资源须显式点击。

密钥接口只返回配置标记，输入留空保留已存值，点“清除”并保存才会清空。数据库版本保存旧凭据，备份也包含密钥，应按生产配置保护。切换商户、商品或环境后更新服务商通知地址，保留旧端点直到旧订单结清。

## 金额与订单

用户输入希望到账的 USD 额度（默认 $1–$1000，最多两位小数）。后端按对应服务单价计算报价，按付款币种保留两位小数。例如到账 $2、EPay 单价 6.7，则支付 CNY 13.40。人民币默认单价取首次配置时的显示汇率，此后独立；改变显示汇率不改变报价或历史订单。

国际服务如加税，记录商品金额、税额及实付合计，到账额度仍为原下单值。金额使用 decimal/NUMERIC。Creem 实付范围 $1–$999999.99；Stripe 最低 $0.50、最高 $999999.99。商户账户、国家或支付方式另有约束时，以服务商实际返回为准。

确认窗口展示固定报价，确认后才调用外部收银台。重试使用同一本地订单和已保存支付会话；外部请求超时保留待核对，避免创建另一笔未知支付。返回钱包仅查询状态，不凭浏览器返回入账。历史待付款订单可“继续付款”或“核对付款”。

通过验签、订单、配置版本、环境、金额和币种核对后，锁订单、标记成功、增加余额、写唯一余额流水在同一事务完成。税额不会计入 USD 到账额。测试环境记录支付结果但不增加真实余额。失败或过期之后迟到的有效成功通知仍可入账，成功之后的失败通知不会回退余额。

关闭充值阻止新下单；已有订单、旧配置通知和查单仍正常处理。管理员验收入口可以在总开关关闭时为当前管理员创建真实订单，不创建测试用户。

## 各服务配置

| 服务 | 需要填写 | 使用方式 |
|---|---|---|
| EPay（含 ZPAY） | 网关、商户 ID、商户密钥、支付方式 | CNY，`go-epay v0.0.5` 生成签名表单并验签；支持支付宝、微信及商户其他已开通标识 |
| Stripe | API Key、Webhook Secret | USD，`stripe-go/v81 v81.4.0` Checkout 一次性付款，动态行项目金额，商户订单作为幂等标识 |
| Creem | API Key、Webhook Secret、USD 一次性商品 ID | 官方 HTTP API，`custom_price` 按美分传动态金额，`units=1`；订阅商品不可用 |
| Waffo | 商户 ID、API Key、PKCS8 私钥、X509 平台公钥、环境 | USD，`waffo-go v1.3.2` 托管收银台；密钥填 Base64 DER，通知及通知应答均由 SDK 验签/签名 |
| Waffo Pancake | 商户 ID、PEM/Base64 私钥、环境、店铺 ID、一次性商品 ID | USD，官方 Go SDK `v0.16.0`，下单固定 price snapshot、外部订单号和用户身份，托管收银台 |

以下是配置字段示例，全部是占位值，须替换为部署者自己的商户。分别提交到 `PUT /api/admin/payments/{provider}` 或在后台填入对应字段。首次保存保持 `enabled: false`，先验证连接和测试支付，再开放。其余金额范围默认 $1–$1000，可在后台调整。

```json
{
  "epay": {"enabled": false, "environment": "prod", "currency": "CNY", "unit_price": "6.7", "values": {"gateway": "https://pay.example.com", "merchant_id": "YOUR_MERCHANT_ID"}, "secrets": {"key": "YOUR_MERCHANT_KEY"}, "methods": [{"id": "alipay", "name": "支付宝"}]},
  "stripe": {"enabled": false, "environment": "test", "values": {}, "secrets": {"api_key": "YOUR_TEST_API_KEY", "webhook_secret": "YOUR_WEBHOOK_SECRET"}},
  "creem": {"enabled": false, "environment": "test", "values": {"product_id": "YOUR_ONE_TIME_PRODUCT_ID"}, "secrets": {"api_key": "YOUR_TEST_API_KEY", "webhook_secret": "YOUR_WEBHOOK_SECRET"}},
  "waffo": {"enabled": false, "environment": "test", "values": {"merchant_id": "YOUR_MERCHANT_ID", "public_key": "BASE64_PLATFORM_PUBLIC_KEY"}, "secrets": {"api_key": "YOUR_API_KEY", "private_key": "BASE64_PKCS8_PRIVATE_KEY"}},
  "waffo_pancake": {"enabled": false, "environment": "test", "values": {"merchant_id": "YOUR_MERCHANT_ID", "store_id": "YOUR_STORE_ID", "product_id": "YOUR_ONE_TIME_PRODUCT_ID"}, "secrets": {"private_key": "YOUR_PEM_PRIVATE_KEY"}}
}
```

Stripe webhook 选择 `checkout.session.completed`、`checkout.session.async_payment_succeeded`、`checkout.session.async_payment_failed`、`checkout.session.expired`；completed 必须 payment_status=paid 才入账。Creem webhook 选择 `checkout.completed`，checkout completed 且 order paid 才入账。Waffo 处理 `PAYMENT_NOTIFICATION` 中 `PAY_SUCCESS`。Pancake 在对应店铺添加 `order.completed` HTTP 通知，测试和生产分别配置对应地址；密钥和 storeId、mode、外部订单及买家身份均需匹配。

Pancake 可先“验证连接”或“拉取店铺和商品”，选择已有店铺和一次性商品；也可显式创建 Lingshu 专用商品，店铺 ID 为空时先创建店铺，再创建并发布商品。途中失败会保留已得到的 ID，避免误以为远端什么都没创建。保存配置不会创建店铺、商品或 webhook；通知地址显示后由部署者在支付后台登记。

EPay、Stripe、Creem、Waffo 可主动查单。Stripe/Creem 需要本地已经保存外部支付会话 ID；若创建响应在网络中丢失，则等待通知。Pancake 当前 SDK 没有用于该流程的可靠订单查询，页面明确提示等待通知。不能把“不支持查单”或网络超时当作未付款。

## 接口与验收

用户：`GET /api/me/payment-methods`，`POST/GET /api/me/topups`，`GET /api/me/topups/{id}`，`POST /api/me/topups/{id}/checkout`、`refresh`。订单查询仅限本人。

管理员：`GET /api/admin/payments`，`PUT /api/admin/payments/{provider}`，`POST /api/admin/payments/{provider}/probe`、`test-order`，Pancake 专有 `catalog` / `create-product`，`GET /api/admin/topups`。管理配置及跨用户列表仅管理员访问。

通知：`/api/payments/{provider}/{callback_version}/{environment}/notify`，不依赖支付总开关。改变价格或额度范围沿用通知版本；改变凭据、环境或商户信息产生新版本。每笔订单绑定下单配置，避免改配置后旧订单验签失败。

使用独立可丢弃数据库运行 `make test`，并执行支付相关包的 `go test -race -p 1`。测试覆盖五家协议和 SDK 模拟、入账事务、配置快照、并发与权限；模拟测试通过不代表真实付款已验收。DMIT 的 EPay 已完成 CNY 6.70 → USD 1 的真实付款验收：通知自动入账、余额与唯一流水一致，重复核对未重复加钱，充值已开放。其余四种服务尚未完成真实付款验收。发布和验收记录见 [现役部署](deploy.md)。

数据库迁移 `0013_payments` 增加配置版本、订单及余额流水关联，保留现有余额与管理员调整流程。发布前备份数据库、程序和环境配置。上线出故障先关闭新充值并保留通知；已有真实付款后应按订单修复、前滚处理，不能直接恢复旧数据库覆盖新订单与余额。

接口依据：[Creem 动态价格](https://docs.creem.io/api-reference/endpoint/create-checkout)、[Stripe Checkout](https://docs.stripe.com/api/checkout/sessions/create)、[Pancake Go SDK v0.16.0](https://github.com/waffo-com/waffo-pancake-sdk-go/tree/v0.16.0)、[Waffo Go SDK](https://github.com/waffo-com/waffo-go)。参考 NewAPI 已有的独立支付服务与统一充值思路，未引用 `references/` 中的代码。
