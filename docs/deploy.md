# 现役部署

2026-10-23 核实。源码仓库为 `https://github.com/aumi314/lingshu`。WSL 管理源码和构建；服务器运行成品，通过 `/opt/lingshu/release.json` 记录对应提交和 SHA-256。两端应用统一版本，数据库与余额各自独立，不自动同步。

## 环境

| 项 | UM580D：团队原入口 | DMIT：独立副本 |
|---|---|---|
| 域名 | `https://lingshu.piqode.com` | `https://gateway.piqode.com` |
| SSH | Windows `ssh minisforum-um580d` | WSL `ssh -i ~/.ssh/dmit_lax_ed25519 -o IdentitiesOnly=yes root@136.175.178.126` |
| 程序 / 配置 | `/opt/lingshu/lingshu`、`/etc/lingshu/env` | 同左，凭据独立 |
| 应用服务 | `lingshu.service`，用户 `lingshu` | 同左；依赖 `lingshu-db.service` |
| 应用监听 | `127.0.0.1:8300` | `172.18.0.1:8300`，只供 Docker 网桥访问 |
| 数据库 | 系统 PostgreSQL 18，`127.0.0.1:5432` | 独立 PostgreSQL 18.6 容器，`127.0.0.1:55433` |
| HTTPS | Cloudflare Tunnel → Nginx `127.0.0.1:8080` | Cloudflare → 现有 Caddy 容器 |
| 备份 | `/var/backups/lingshu/`，`lingshu-backup.timer` | 同左，独立备份脚本 |

WSL 访问 UM 时调用 `/mnt/c/Windows/System32/OpenSSH/ssh.exe minisforum-um580d`，复用 Windows 配置和密钥。DMIT 资产与连接说明见基础设施项目的 `servers/dmit-lax.md` 和 `ssh/README.md`。

`lingshu.focuskeeper.cn` 和腾讯云部署属于历史环境，本轮没有迁移该旧域名。`deploy/deploy.sh` 是旧腾讯云流程，已取消默认目标；现役发布使用下面的步骤。

## 发布：同一提交、同一成品

1. 明确已推送的 Git 提交，在干净的 detached worktree 构建。WSL 默认 Node 18 不适合当前 Vite，使用 `/home/aumif/.nvm/versions/node/v22.22.3/bin`。
2. 执行 `npm --prefix web ci --prefer-offline --no-audit --no-fund`、`npm --prefix web run build` 和 `touch web/dist/.gitkeep`，再执行 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=true -trimpath -ldflags='-s -w' -o bin/lingshu ./cmd/lingshu`。
3. 全量 Go 测试使用独立测试库：设置 `LINGSHU_TEST_DATABASE_URL` 后执行 `go test -p 1 ./...`。测试会重建指定库的 public schema，只能指向可丢弃的测试库。
4. 记录提交号、构建工具版本和 `sha256sum bin/lingshu`。Go 构建不一定嵌入 VCS 信息，发布凭证以干净 worktree 和 `release.json` 为准。
5. 将同一成品传到两端，先执行各自主机的数据库备份；保留现有程序后，在同目录用临时文件加原子替换安装。先发布 DMIT 并验收，再更新 UM。
6. 重启 `lingshu.service`，检查数据库迁移、主机内部 `/healthz`、公网 HTTPS、真实三协议调用、日志计费。更新两端 `release.json`，确认提交与程序哈希相同。

2026-10-23 的应用功能提交为 `4bef2a13d8bbd7ef654bb788aa2a26643ec74859`（System One）。成品 SHA-256 为 `f94184bf62c270543405752757025f91931b3c6d306d96d5fe026f05a66bdbfd`。2026-10-23 后续发布移除了法定节假日定价设置：schema 10 将旧 `days: non_workday` 迁移为 `weekend`、把 DeepSeek 低谷规则名改为“低谷（周末）”，并删除 `settings.cn_holidays`。完整发布提交和成品 SHA-256 以两端 `release.json` 为准。

2026-10-23 用户分组权限发布：应用提交 `102f002180bd818c3f1f809bfccf3d2bbc47bc9c`，成品 SHA-256 `0e164f21f3880887713e2d9fc599fc8e6af00eb506f8bd1195e3f0c0db563b6e`。schema 11 将旧逐用户特殊分组授权迁移为用户分组成员资格和服务分组允许列表；两个数据库均确认有唯一 `default` 用户分组、没有用户缺少分组。DMIT 发布前备份为 `/var/backups/lingshu/lingshu-20261023T114127Z.dump`，UM580D 发布前备份为 `/var/backups/lingshu/lingshu-20261023-194707.dump`。两端 schema 均为 `0011_user_groups_and_group_scope`，服务健康检查和公网页面均返回 200，前端资源包含用户分组设置。发布前二进制保留为 `/opt/lingshu/lingshu.prev-102f002`。回滚此版本时要先从对应备份恢复数据库，再启动旧二进制；旧程序不兼容 schema 11 的用户分组表结构。

2026-10-23 渠道默认分组发布：新建渠道默认只选启用的 `default` 服务分组。应用提交 `4024abed7cf3a0b629a1eff4bd9f328538688487`，成品 SHA-256 `437e1fc19f958d5c28f78fe7a0b98566f22af9c3a2d76490111683de6fd47da1`。发布前 DMIT 备份为 `/var/backups/lingshu/lingshu-20261023T123958Z.dump`，UM580D 备份为 `/var/backups/lingshu/lingshu-20261023-204016.dump`。schema 未变化；两端二进制哈希一致，内部健康检查及两个公网入口均返回 200。发布前二进制保留为 `/opt/lingshu/lingshu.prev-4024abe`。

2026-10-23 OpenAI Platform 渠道模板发布：提交 `08f53b6441aafe3cec17d8ec49fc9e977494ca5f`，两端成品 SHA-256 `62f3f9c4ede0632e4db46f2201d60ee50150a1972a298543d487819750dc8f8f`。模板使用官方 `https://api.openai.com/v1`，只启用 Chat Completions 和 Responses，Messages 留空。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261023T160252Z.dump`，UM580D 备份 `/var/backups/lingshu/lingshu-20261024-000611.dump`；两端本机健康检查、公网入口均返回 200。DMIT 新建渠道 id=16，挂 `default`，只映射 `gpt-6-luna -> gpt-5.6-luna`；API key 只存 DMIT 数据库。创建前没有同名渠道；模型 id=18 的原价保持不变（输入 `$0.10`、输出 `$0.50`、缓存读 `$0.01`、缓存写 `$0.125`/百万 token）。OpenAI 官方实测：Chat Completions 和 Responses 均 HTTP 200，Messages 返回 404。

2026-10-23 OpenAI Platform 图标发布：提交 `5270ebd68ee44ba2f6f308a39fc9c074f4ce4e8c`，图标使用仓库已有的 `openai.svg`，接入管理端渠道列表和模板选择。两端成品 SHA-256 `678a2ccf81169174432da3c09b70fd767067e98a8d01d58cdd9af85318130a46` 一致；发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261023T230402Z.dump`，UM580D 备份 `/var/backups/lingshu/lingshu-20261024-070406.dump`。两端本机和公网健康检查均返回 200；两个公网首页加载的 JS bundle 均含 OpenAI 模板图标映射。

2026-10-23 DMIT 上线 Kimi K3.1 Pro：数据库变更前备份 `/var/backups/lingshu/lingshu-20261023T233119Z.dump`。新增模型 id=26 `kimi-k3.1-pro`，按官方价格设置输入 `$2`、输出 `$10`、缓存命中 `$0.20`、缓存写入 `$2`/百万 token，并映射到已启用的 Moonshot 渠道 id=4。渠道原有服务分组 `Official` 不变；不含 highspeed，不改 UM。使用该渠道现有 key 向 Moonshot 发真实 Chat Completions 请求，HTTP 200，返回内容非空，usage 为输入 90、输出 58 tokens。

2026-10-23 移除模型启用开关：提交 `a5c056cce8d7512615a18e2c1b89563f6eb525de`，schema 12 删除 `models.enabled`；存在定价记录即视为已配置，零价有效，路由只按渠道、映射、分组和协议判断。DMIT 发布前数据库备份 `/var/backups/lingshu/lingshu-20261024T004621Z-pre-a5c056c.dump`，UM580D 备份 `/var/backups/lingshu/lingshu-20261024T004624Z-pre-a5c056c.dump`；旧程序分别保存在 `/opt/lingshu/lingshu.prev-a5c056c`。两端 schema 12、模型数 26 / 23、无 `models.enabled` 列，服务和公网健康检查均返回 200；同一二进制 SHA-256 为 `224685ee25900d5d2bc824eecfe5cfffb3bec95d01f36eb9bc087f7913797978`。用 aumif 现有账户的临时 key 分别经两端本机监听调用 DeepSeek Chat，均 HTTP 200，输入 / 输出 38 / 13 tokens，日志状态 success，费用 `$0.0000135`；临时 key 已撤销。WSL 经 DMIT 公网域名发送模型请求时收到 403，网关没有对应请求日志；本机监听实测成功，公网 API 请求来源尚未确认。回滚需先恢复对应发布前备份，再换回旧二进制。

2026-10-23 限制 System One 展示：提交 `9198bf9`，仅 `typesafe` / `nace` 模板显示 System One 地址项、协议标签，并在渠道测试时将其作为可测协议。没有 schema 变化。发布前 DMIT 数据库备份 `/var/backups/lingshu/lingshu-20261024T010342Z-pre-9198bf9.dump`，UM580D 备份 `/var/backups/lingshu/lingshu-20261024T010345Z-pre-9198bf9.dump`；旧程序保留在 `/opt/lingshu/lingshu.prev-9198bf9`。两端程序 SHA-256 同为 `eb2eab6c41fa0d2cbf09f087711003a1bac6330153888407aebb87b16a2112e6`，本机与公网健康检查均返回 200，两个公网首页均加载新前端资源 `index-BndXhI9w.js`。

2026-10-23 渠道预设模板默认值：提交 `2d44247`，第一项改名为“预设模板”，新建渠道默认选中“标准模板”。没有 schema 变化。发布前 DMIT 数据库备份 `/var/backups/lingshu/lingshu-20261024T020212Z-pre-2d44247.dump`，UM580D 备份 `/var/backups/lingshu/lingshu-20261024T020215Z-pre-2d44247.dump`；旧程序保留在 `/opt/lingshu/lingshu.prev-2d44247`。两端 SHA-256 同为 `df1983cdf1c0c203d224a791d3d7ef69bb287825202a03c7ec9d5639d9ebf8d1`，本机和公网健康检查均返回 200，两个公网首页均加载新前端资源 `index-Bp6yl5ns.js`。

2026-10-23 标准渠道模板发布：提交 `ebfd2e97b950ed425cedd1ed4377bbf837387abd`，从干净提交构建一次，两端二进制 SHA-256 均为 `bdccb8c47b934f6dbb62b702c9b31e922dcd4ec56d736b4894cac5031b73ade5`；schema 无变化。DMIT 发布前备份 `/var/backups/lingshu/lingshu-20261024T014302Z.dump`，UM580D 发布前备份 `/var/backups/lingshu/lingshu-20261024-095030.dump`；两端旧程序均保留为 `/opt/lingshu/lingshu.prev-ebfd2e9`。两端 `release.json` 提交号和 SHA-256 一致，systemd、主机内健康检查及两个公网健康检查均通过；两个公网首页均加载 `index-CWQk-9NI.js`。DMIT 使用渠道 15 已保存的 key 直连测试标准模板生成的 `https://api.kinght.top/v1/chat/completions`、`/v1/messages`、`/v1/responses`，均 HTTP 200。没有改动两端渠道数据。

## 现役备份与恢复

### DeepSeek 工作日峰谷规则补齐（2026-10-23）

现场核对发现两端模型 id=1 `deepseek-v4.1-flash` 只有周末谷价规则，缺少文档中约定的夜间及午间规则，导致工作日全天按高峰。备份后通过现有 aumif 的临时会话调用 `PUT /api/admin/models/1`，保留基础价及周末规则，增加北京时间 18:00–09:00 与 12:00–14:00 两条谷价规则；接口与数据库读回均为三条。基础价不变：输入/输出/缓存读/缓存写为 0.30/1.20/0.006/0，谷价为 0.15/0.60/0.003/0（USD/百万 tokens），最终乘服务分组倍率。`TestDeepSeekOffPeak` 覆盖高峰边界、周末及自然星期处理，通过。按请求开始时间匹配，已有账单不追溯变更。

修改前 DMIT 数据库备份 `/var/backups/lingshu/lingshu-20261024T051740Z-pre-deepseek-time-rules.dump`（SHA-256 `1f9800dcf05cbb34570a69ed3df4dbee579eaa0c0e57607b1d282454211cf7e6`），UM `/var/backups/lingshu/lingshu-20261024T051751Z-pre-deepseek-time-rules.dump`（SHA-256 `4ccd70b97ae53d16227942d505a8ade650e96433b2159eda9aa250cb56ae990a`），均通过 `pg_restore --list`；同前缀 `-model-before.json` / `-model-after.json` 保留改前改后值。临时会话已删除。无代码或 schema 变更，实时生效，不重启服务。

### 系统设置标签页（2026-10-23）

提交 `5e5d897`：系统设置改为与定价页一致的顶部“运营设置 / 支付设置”标签，默认运营设置，支付页可用 `?tab=payments` 直达并保留刷新状态，内容宽度与定价页一致。前端类型检查/构建及 Playwright 中英文、切换/刷新、按需加载和手机布局验证通过。没有后端或 schema 变更。

同一干净提交构建一次发布两端，程序 SHA-256 均为 `089d6fc9896188c1d435e016bd0ccd2fbca00a23df2c51a2c2a09dfa4e58b632`，新资源 `index-_HyFulUn.js`。发布前 DMIT 数据库备份 `/var/backups/lingshu/lingshu-20261024T051424Z-pre-5e5d897.dump`（SHA-256 `d6eb3ed5a9a4c2778a9c42c360be3c60850576be1a6277a269ae0d340d131c64`），UM 备份 `/var/backups/lingshu/lingshu-20261024T051518Z-pre-5e5d897.dump`（SHA-256 `3aaa00dfc5a5e76deb056787953e101fcb1bea6f52fe91926793077f3976ac97`）；均通过 `pg_restore --list`，同前缀 `.env` 备份配置。旧程序及标记分别保留在 `/opt/lingshu/lingshu.prev-5e5d897`、`/opt/lingshu/release.prev-5e5d897.json`。

两端服务 active，主机内与公网健康检查、新前端资源均通过，schema 保持 13。DMIT 充值开启、EPay 配置版本 2、余额 `$10.8493093969`、已付订单恰好一条流水；UM 充值关闭、商户配置为空、余额 `$9.8492565429`，均未改变。

### 多支付发布（2026-10-23；备份时间为 UTC 2026-10-24）

应用提交 `ba139c4b7b5df55fd8c81d39581b380bfd3bdbe2` 已推送 GitHub；从干净 detached worktree 构建一次，先发布 DMIT，再发布 UM580D。两端程序 SHA-256 均为 `20b7b92fba2d2ee25322d772a3b61ce9593081b2c7dfae0081ed698edcf13a0c`，`release.json` 记录同一提交，schema 均为 `0013_payments`。新增五种支付适配、配置版本、订单及原子余额入账；迁移时用户、模型和原有余额调整流水完整保留（DMIT 1/26/1，UM 1/23/1）；后续 DMIT 真实充值另新增一条流水。

发布前各自备份数据库、环境文件、程序及发布标记：

| 主机 | 数据库备份 | dump SHA-256 |
|---|---|---|
| DMIT | `/var/backups/lingshu/lingshu-20261024T045936Z-pre-ba139c4.dump` | `80ff538eb4c01182b7bdff4919ce442b96398a9054536ccd3369bac193f87afc` |
| UM580D | `/var/backups/lingshu/lingshu-20261024T050136Z-pre-ba139c4.dump` | `2f7e94d9954c0ca7456ec020bd1c27cec32a9e04c801cc05379070197601de57` |

对应环境备份为相同前缀 `.env`；两端旧程序 `/opt/lingshu/lingshu.prev-ba139c4`，旧标记 `/opt/lingshu/release.prev-ba139c4.json`。两份 dump 均通过 `pg_restore --list`。只增加各自的 `LINGSHU_PUBLIC_URL`，保留监听、数据库和渠道差异。

验证：独立库 `make test`、相关包 `go test -race -p 1`、`go vet ./...`、前端类型检查/构建、程序构建通过。Playwright 模拟账户验证中文/英文钱包、付款方式首次加载、固定报价确认、付款后余额刷新、375px 手机布局、五服务配置及 Pancake 管理入口。两端服务 active，本机与公网 `/healthz`、钱包页面和资源 `index-BypGXaz3.js` 均通过；DMIT 公网通知 GET/POST 可达应用，未签名请求按规范返回 HTTP 400 `fail`。

DMIT 从 PiQode 数据库仅在内存读取现有 EPay 商户凭据，通过 Lingshu 管理接口保存，未修改 PiQode/LMM。ZPAY 网关验证通过；使用现有 aumif（id=1）建立管理员验收订单 `149dd1a4f9e707e1610e21d2317e3ca5`，报价 CNY 6.70，到账 USD 1，通知地址 `https://gateway.piqode.com/api/payments/epay/1/prod/notify`。用户实际付款后，2026-10-24T05:04:08Z 收到有效成功通知，订单自动入账；独立向 ZPAY 查单确认成功、订单一致、实付 CNY 6.70。余额从 `$9.8493093969` 增加至 `$10.8493093969`，只有一条 USD 1 的余额流水，operator 为空；连续两次核对没有重复入账。**EPay 真实付款验收通过，已通过管理接口打开 DMIT EPay 服务及充值总开关**。当前配置版本为 2，通知版本仍为 1，地址不变，钱包仅显示支付宝。开放前另备份已付款数据库 `/var/backups/lingshu/lingshu-20261024T050524Z-post-epay-paid.dump`（SHA-256 `6e06cbae717fa3d0d02e323cc4c1778e1accb7f9f2881951205123178cd8ec1f`），通过 `pg_restore --list`。

UM580D 五服务均未配置商户、均关闭，充值总开关关闭；检查余额 `$9.8492565429`，订单为 0。两端验证均复用 aumif 的短期会话，用完已删除。Stripe、Creem、Waffo、Pancake 只有协议与 SDK 模拟测试，没有真实付款验收。

接入与故障处理见 [支付接入](payments.md)。发生支付故障先关闭新充值并保留通知。产生真实付款后不能用本次发布前数据库备份覆盖新订单和余额。

两端每日备份保留最近 14 份。DMIT 脚本在 `deploy/backup/lingshu-dmit-backup`，PostgreSQL 数据卷和 PiQode 原有 PostgreSQL 15 完全独立。

- UM 手动备份：以 root 执行 `systemctl start lingshu-backup.service`。
- DMIT 手动备份：以 root 执行 `/usr/local/bin/lingshu-backup`。
- UM 恢复：先停 Lingshu，再用 `postgres` 用户向确认过的目标库执行 `pg_restore --no-owner --exit-on-error`；覆盖现有库须另行确认。
- DMIT 恢复演练：在 `lingshu-postgres` 内创建独立临时数据库，再通过 `docker exec -i lingshu-postgres pg_restore -U lingshu -d <临时库> --no-owner --exit-on-error` 输入备份。验证逐表行数。不要操作 `newapi-postgres`。

本轮已验证 DMIT 日常备份可恢复，15 张表行数一致。当前仅保证各主机本地备份；新增副本的持续异地备份另行规划。UM 迁移记录见 [um580d-migration-20261022.md](um580d-migration-20261022.md)，DMIT 快照与验收见 [dmit-copy-20261023.md](dmit-copy-20261023.md)。

## 现役代理与渠道

UM 的 OpenRouter 等渠道如需出境，可用 `lingshu-proxy.service` 提供的 `socks5://127.0.0.1:10808`。DMIT 直连，不复制这条本机代理依赖。本轮只清空 DMIT 渠道 id=12 的旧代理地址，保持其停用状态；其他渠道开关和上游密钥保持复制时状态。

## 现役回滚

两端还保留较早版本 `/opt/lingshu/lingshu.prev-20261023`。发布前二进制按版本保留为 `/opt/lingshu/lingshu.prev-<commit>`。程序回滚采用临时文件加原子替换，再重启服务；回滚到依赖旧结构的程序前必须恢复对应发布前数据库备份。schema 11 的用户分组变更及 schema 12 的模型开关删除都要求先恢复备份。

DMIT 整体停用副本的步骤和隔离边界见 [dmit-copy-20261023.md](dmit-copy-20261023.md)。UM 原入口可独立使用。

腾讯云旧部署步骤保留在 Git 历史中；现役操作以上述双机流程为准。

2026-10-23 TypeSafe / Nace 模板说明简化：提交 `717d2ad210644538e3352362856032aa33c5d20b`，两项模板说明均显示 `System One`。无数据库迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T020742Z-pre-717d2ad.dump`，UM580D 备份 `/var/backups/lingshu/lingshu-20261024T020838Z-pre-717d2ad.dump`；旧程序均保留为 `/opt/lingshu/lingshu.prev-717d2ad`。同一成品 SHA-256 `0679f90942d9dd9481be38696c8e7a6e726713c3f706186d6aa461796a9937bd`。两端服务 active，主机内及公网健康检查均返回 200。

2026-10-23 System One 模板提示修正：提交 `01d72c46870ecc0c01a4f2441b89fe577713b9a5`，TypeSafe / Nace 编辑渠道时隐藏通用的模板切换提示，使模板下方只显示 `System One`；其他模板提示不变。无数据库迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T021509Z-pre-01d72c4.dump`（SHA-256 `a833f402a3fc16d87acad1e08df48cf03cdf81d06461fca0b9c2e46bed0b3d50`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024T021631Z-pre-01d72c4.dump`（SHA-256 `ad05c34ac20548ec53a85b4267def74c4d2cebea72db3d5f671a8394abb28d32`）；两份均通过 `pg_restore --list` 检查。两端旧程序保留为 `/opt/lingshu/lingshu.prev-01d72c4`。同一成品 SHA-256 `fe624a3545a8de76515ff1f8dda3ce63a24d8038006ee1da76b179a7d0d439c9`；两端服务 active，主机内、公网健康检查均返回 200，公网页面加载资源 `index-Bq4GHhS9.js`。

2026-10-23 TypeSafe / Nace 协议地址显示修正：提交 `d44b85e22d1b89511e8855df70931a29dc24485f`，编辑这两个模板时只显示 System One 地址行；Chat、Messages、Responses 空行隐藏。其他模板不变，无数据库迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T022416Z-pre-d44b85e.dump`（SHA-256 `a1535d55dfa276daebe074b1a581281bdb1e27de756fde3aa2345831f53581d9`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024T022416Z-pre-d44b85e.dump`（SHA-256 `3ec30cb1d0fd933551c8b689701d8f8f53c55e2e4554c54edfaaa8184b7cc2d5`）；两份均通过 `pg_restore --list` 检查。两端旧程序保留为 `/opt/lingshu/lingshu.prev-d44b85e`。同一成品 SHA-256 `ed9941b1968dd964e3d39470dfb9c24810e34e789fbbe73a57cab71a2061f42f`；两端服务 active，主机内、公网健康检查均返回 200，公网页面加载资源 `index-wxi_N9PQ.js`。

2026-10-23 渠道冒烟测试交互：提交 `2b34e63b27c43bb98ce0422c62bacc37a4e3e5b5`，点击测试先选择协议和模型；可单测一个模型，或批量测试所选协议下全部映射模型。没有数据库迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T031759Z-pre-2b34e63.dump`（SHA-256 `c54ad54728a7e48fc6b112931e9f5dbe8156f99ea786b5267e4b9fdc4fb11d26`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024T031851Z-pre-2b34e63.dump`（SHA-256 `6cff93ecd32e9585fabcd4aa498d6e368cdbc78c3768f5543a42a7640819d78e`）；两份均通过 `pg_restore --list` 检查。两端旧程序保留为 `/opt/lingshu/lingshu.prev-2b34e63`。同一成品 SHA-256 `39ae3f18626eed85d3456bc0bf46ff04ea67e8eb2d4535effcaacef2791f2e67`；两端服务 active，主机内、公网健康检查均返回 200，公网页面加载资源 `index-CbEeEJHn.js`。

2026-10-23 系统设置板块：应用提交 `dce849e5b3c151580775c8e51be3167e00d1b907`，运营设置包含显示汇率，支付设置预留默认关闭的用户充值开关，复用 settings 表，无数据库迁移。独立测试库 `make test`、前端类型检查及构建、程序构建均通过；从干净提交构建一次，两端二进制 SHA-256 均为 `71efd2f75847bb468d25250cab25b6db3bb9e59ccefbde5440ca239610e4f2ad`。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T034609Z-pre-dce849e.dump`（SHA-256 `b35b128168243ca5abef7d9baa21afe7db9e288feed0dcd61ac3b9cef0e2784e`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024T034946Z-pre-dce849e.dump`（SHA-256 `4926e7146ac3add5149a3d5a67d1953ba206a793c1d76097f658fa45e3146b1f`）；两份均通过 `pg_restore --list` 检查。两端旧程序与发布标记分别保留为 `/opt/lingshu/lingshu.prev-dce849e`、`/opt/lingshu/release.prev-dce849e.json`。两端服务 active，主机内及公网健康检查均返回 200，公网页面加载资源 `index-DCjy-Epl.js`。用现有 aumif 账户的临时会话读取管理设置，两端均返回原有汇率 `6.7`、`payment_enabled: false`；会话用完删除，没有新建用户或改动余额、渠道配置。实际支付渠道后续接入。

2026-10-23 用户钱包入口：应用提交 `9b6f196f8b4e2ea2c75f52dec156f6dd3adb979a`，用户区第五项新增“钱包充值”，页面显示当前余额，沿用货币偏好和显示汇率；进入时刷新账户数据，充值暂由管理员处理。无后端或数据库结构变更。前端类型检查、前端和程序构建通过；Playwright 使用模拟账户响应验证普通用户 / 管理员入口、第五项顺序、USD / CNY、中英文与 375px 手机布局。干净提交只构建一次，两端二进制 SHA-256 均为 `99fa59d1f9c94d53aeaea2c03277ab1ab27ca518d90fc7c1b8373e7e703e8502`。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T040548Z-pre-9b6f196.dump`（SHA-256 `e937eaac7ca7a40ff5f5dd3488d3a89c37b87d9209c55c8d7ada75b24e4e6ad0`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024T040638Z-pre-9b6f196.dump`（SHA-256 `f736be9fb59bfdb6774d83a444f3d93a82c870bf4add25bb6600a8bef922367a`），两份均通过 `pg_restore --list`。两端旧程序与标记保留为 `/opt/lingshu/lingshu.prev-9b6f196`、`/opt/lingshu/release.prev-9b6f196.json`。两端服务 active，内部与公网健康检查、`/console/wallet` 及新资源 `index-B3jr5bjn.js` 均通过；未改动用户、余额或支付开关。

2026-10-23 用户编辑与改名：提交 `99ac3e7ef466cbd50ed5ba1e0fbcd7c0bff8b59d`。同一干净 worktree 构建后发布到两端，程序 SHA-256 均为 `f8732b531c41859f89a44caba052fdfcbbd46d1c630b615f59fbe4c1b8447e87`。前端类型检查和构建、Linux amd64 程序构建通过；按本轮执行要求未运行测试套件。无数据库迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T063255Z.dump`，UM580D 备份 `/var/backups/lingshu/lingshu-20261024-143309.dump`；发布前程序和 `release.json` 分别保留为 `/opt/lingshu/lingshu.prev-99ac3e7`、`/opt/lingshu/release.prev-99ac3e7.json`。两端 `release.json` 指向相同提交和哈希，systemd active，主机内健康检查返回 `ok`，两个公网 `/healthz` 均返回 `ok`。未修改用户、余额或服务配置。

2026-10-24 注册管理：提交 `59d8759e87155ff453301db82572d3de67c5b592`。干净 worktree 构建一次，两端同一程序 SHA-256 为 `ae887869b25441efc64d9266867ad87b33a5ccc3c84feb28eef7a2080066506d`；迁移 `0014_registration_codes` 随启动完成。全量 `go test -count=1 -p 1 ./...`、前端类型检查/构建、Linux amd64 程序构建通过。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T094052Z.dump`（SHA-256 `9c57620331c300caccb16cc9b9f07d6f3bc996f48fa800bfc7f62412f0460648`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024-174314.dump`（SHA-256 `82c8b82128a9e5e38a59c4f15a181eb4f21624ecae7b07e2381f30ae46d9de7e`），两份均通过 `pg_restore --list`；发布前程序和标记保留为 `/opt/lingshu/lingshu.prev-59d8759`、`/opt/lingshu/release.prev-59d8759.json`。两端服务 active，主机内和公网健康检查均返回 `ok`，公网注册状态均为 `{"enabled":false}`。注册方式未开启，未生成注册码或新建账户。

2026-10-24 EPay 环境选项移除：提交 `0f58bda7c716e6b68f5d8076fce602c7e23fe63a`。EPay 固定为生产环境，其他支付服务的测试环境和管理员测试付款入口保留；后端拒绝 EPay 测试配置。无数据库迁移或支付配置变更。独立测试库 `make test`、前端构建通过。同一程序发布两端，SHA-256 为 `2eb1ccb6f5266fe8dc1685fe30dd92a39f44020f3620edde1004e876d27fdffa`，新资源为 `index-DhDNqoRW.js`。

2026-10-24 实例级 API Base URL：提交 `079da61`。系统设置允许管理员编辑 Base URL；未保存自定义值时由本实例 `LINGSHU_PUBLIC_URL` 加 `/v1` 默认生成。API keys 与概览页共用该设置；支付回调地址不受影响。两端程序 SHA-256 均为 `2fb7371390b0babaff4b0e972fc26d9b2fe17ff1ae237418e3d83424c3abed8b`，前端资源均为 `index-B_yF9u6X.js`，无数据库迁移。发布前 DMIT 数据库备份 `/var/backups/lingshu/lingshu-20261024T105149Z.dump`（SHA-256 `65c84af91b9b603d87391c89bb11329abfae180b0c222a65f49830eacf19642b`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024-185256.dump`（SHA-256 `9a18990ccc10276ae0c1abb39232d1837835285e0788e5c136e65d951ed55c05`）；两份均通过 `pg_restore --list`。发布前配置、程序和标记分别保留为 `/etc/lingshu/env.pre-079da61`、`/opt/lingshu/lingshu.prev-079da61`、`/opt/lingshu/release.prev-079da61.json`。DMIT 默认显示 `https://gateway.piqode.com/v1`，UM580D 默认显示 `https://lingshu.piqode.com/v1`。两端服务 active，主机内与公网健康检查均返回 `ok`，首页加载新 JS 资源。数据库无需迁移。

2026-10-24 API keys 地址栏视觉调整：提交 `a46ae70`，将占据整行的大型蓝色提示框改为紧凑的地址栏，地址仍可直接复制。全量 Go 测试、前端类型检查与构建通过。同一程序发布两端，SHA-256 均为 `e07b3b0df6545b54b866b0900b3cf45f394170a3aff302bc9a6f3c2da409a916`，前端资源均为 `index-DiCFmXb1.js`，无数据库迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T105900Z.dump`（SHA-256 `146bd2c298d36e21a59948c051192f361c107b54da4a73d1fbc8529a0a08f30c`），UM580D 备份 `/var/backups/lingshu/lingshu-20261024-185916.dump`（SHA-256 `ea63be980cf4f46e4f3d513aff3b6496d59d1970bd19afbb629c8f493a45fab0`）；两份均通过 `pg_restore --list`。发布前程序、配置和标记分别保留为 `/opt/lingshu/lingshu.prev-a46ae70`、`/etc/lingshu/env.pre-a46ae70`、`/opt/lingshu/release.prev-a46ae70.json`。两端服务 active，主机内及公网健康检查均返回 `ok`，首页加载新资源。

发布前 DMIT 数据库备份 `/var/backups/lingshu/lingshu-20261024T100432Z.dump`（SHA-256 `2daa4525297a915fb3c5b85bed063d3e9f7bd7a3455aaafecabcd77951b13c48`）；配置未修改，留存副本 `/var/backups/lingshu/lingshu-20261024T100432Z-pre-0f58bda.env`，与服务器当前配置一致。UM580D 发布前数据库备份 `/var/backups/lingshu/lingshu-20261024-180942.dump`（SHA-256 `a0915d11a54cef7520d7d37259a05c557c27b9f53f70937dd2cfa64d21a61642`）及配置副本 `/var/backups/lingshu/lingshu-20261024T100942Z-pre-0f58bda.env`。两份数据库备份均通过 `pg_restore --list`。两端旧程序及发布标记保留为 `/opt/lingshu/lingshu.prev-0f58bda`、`/opt/lingshu/release.prev-0f58bda.json`。服务 active，主机内和公网健康检查均通过，首页均加载新资源；部署临时上传文件已清理。
