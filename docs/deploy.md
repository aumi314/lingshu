# 部署与运维

项目在本机 WSL 管理源码和构建，部署到个人 DMIT 洛杉矶服务器。仓库为 `https://github.com/aumi314/lingshu`，线上入口为 `https://gateway.piqode.com`，默认 API Base URL 为 `https://gateway.piqode.com/v1`。

## 当前运行基线

2026-11-05 发布并复核（概览页改版）：DMIT 已发布提交 `b5090dcb1881e60bdaa1f28b8e368ea7192a0505`，运行中程序 SHA-256 为 `995c0f0635ac55fb8830db57aa06dd3b8cf036f2e012109afc7da837200e8f29`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T160124Z.dump`，SHA-256 为 `ab5105544efe475be997872e7e65a9e12e143fc319029ba90cb2e23b62080194`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-b5090dc`。只改前端，无迁移，schema 仍为 `0020_commercial_relay_mode`；回滚换回 `prev-b5090dc` 程序即可。内网与公网健康检查通过，公网入口资源 `index-DRFHDpk-.js` 与干净构建产物 SHA-256 `54ea3d0fd1851a35d5c296af7f8fc9f935b3e7b5abbec83227362855b7c184c0` 一致，服务日志无 ERROR。压缩成品上传约 9 分钟；部署方式拟改为 GitHub Actions 构建、DMIT 下载成品（仓库为公开仓库，下载无需 token），待确认。

2026-11-05 发布并复核（商业中转定位、用户筛选）：DMIT 已发布提交 `17e48474ff30321d33fe3d8661d424f53c85d149`（含 `5b70ebf` 用户启用状态筛选），运行中程序 SHA-256 为 `697774a77a95b8a17208223c80bc3d105e15686b2699f5ec1ac32b2f0c84d9ba`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T152552Z.dump`，SHA-256 为 `50be89fb5dcaabf842802ccc4cca7c86ea1ee047b57e3ae3928a90929a845355`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-17e4847`。

迁移 `0020_commercial_relay_mode` 后 schema 为 `0020_commercial_relay_mode`；线上 `deployment_mode` 原为未设置（按默认显示公司团队网关），现为 `commercial_relay`。回滚换回 `prev-17e4847` 程序即可，旧程序不认识该值时页面仍显示公司团队网关，不影响功能。内网与公网健康检查通过，公网入口资源 `index-CFF1fsTx.js` 与干净构建产物 SHA-256 `e45edd97b13e3e6af9008379b4b33b6cd758d7e0de872b8a7557d28351937ae2` 一致，服务日志无 ERROR。`5b70ebf` 单独上传的成品未安装，已删除，合并到本次一起发布，只重启一次。

2026-11-05 发布并复核（用户页与用户分组入口）：DMIT 已发布提交 `4b2736262197116eb4afb521b690888741b6d89e`，运行中程序 SHA-256 为 `a2afd6aaf36ffa6330facac0b55c88763cbe7d2fa751d6075b0209cd62ded542`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T145456Z.dump`，SHA-256 为 `e337e4a69775880f3cc991a5d8bfb05db26228a861aa1f9091cdd39525d7428d`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-4b27362`。无数据库迁移，schema 仍为 `0019_log_request_details`；回滚换回 `prev-4b27362` 程序即可。内网与公网健康检查通过，公网入口资源 `index-DkalBhoK.js` 与干净构建产物 SHA-256 `8b1556d50caca9e07ce5e9ffb46c46506dcab53f4dd30c9ffb1ed8d20d3baabf` 一致，服务日志无 ERROR。压缩成品约 5.3MB，上传约 7.5 分钟。

2026-11-05 发布并复核（请求 IP、请求列、速度口径）：DMIT 已发布提交 `88d8823f8fedf9a353e60acd693904664acb285e`（含 `6106589` 速度口径修正），运行中程序 SHA-256 为 `32388c364777dbd88580bf6ec62e8d01ed4e58a95d3b520b9474de6e1b974322`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T141959Z.dump`，SHA-256 为 `b01ab8a8427efb98a25a69a95b6361224c543ed5bca1a5f88f9a84d473f6b145`，`pg_restore --list` 可读（20 张表数据）。旧程序、配置与发布标记保留为 `prev-88d8823`。

迁移 `0019_log_request_details` 给 `request_logs` 加 `client_ip`、`reasoning_effort`，线上 schema 升到 `0019_log_request_details`。发布后第一条新请求记下公网 IP 与推理强度 `high`，确认 Caddy 经 Docker 网桥转发的 `X-Real-IP` 已被采信（此前全部记为 `172.18.0.4`，登录防护也按此计数）。公网入口资源 `index-CoclI6Bs.js` 与干净构建产物 SHA-256 `6b2bdabb4967d2da1322caff342c27b42b8c6bc584d2c19591087196e15d2d90` 一致，服务日志无 ERROR。0019 是纯增列迁移，回滚换回 `prev-88d8823` 程序即可。

传输备注：本次 WSL → DMIT 上行很慢，scp 13MB 成品中途断开过一次；改为本地 `gzip -9`（约 5.3MB）后上传，耗时约 16 分钟，服务器端解压后核对哈希一致。

2026-11-05 发布并复核（操作日志）：DMIT 已发布提交 `4ac5a7a56f887be66100e8b6c86262d2512166bc`，运行中程序 SHA-256 为 `e1541a729f2ac256267e7a44bbe838474008c4066f73e815c32e347d5c43ed86`，属主 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T104022Z.dump`，SHA-256 为 `b24c51029a7f7410cde04ed9e15d4e2f426cbf93ef5a9d00b7e9afaa583d006b`，`pg_restore --list` 可读（19 张表数据）。旧程序、配置与发布标记保留为 `prev-4ac5a7a`。

迁移 `0018_operation_logs` 新建操作日志表并回填历史余额流水，线上 schema 由 `0017_log_group_name` 升到 `0018_operation_logs`；10 条 `balance_adjustments` 全部回填，逐条核对无遗漏。服务启动、内网与公网健康检查通过，服务日志无 ERROR。公网入口资源 `index-YxoBAqJh.js` 与干净构建产物 SHA-256 `856a68a982db7fea5e48bf66d48c9cfd57148109849c34ce09c91df467d3a3db` 一致。上一版发布后的 72 条新请求均写入了分组名快照。

0018 只新建表并回填，旧程序不读该表：回滚只需换回 `prev-4ac5a7a` 程序。回滚后新产生的操作不会被记录，再次升级时表和已有记录保留。

2026-11-05 发布并复核：DMIT 已发布提交 `186bcc883cee3e4d073c9900deacf41ad65e8ae6`，运行中程序 SHA-256 为 `2c43284acb99cc22d694bef72fc8e451ce80a14cb81f80f7c57d33c6a939f81e`，属主保持 `lingshu:lingshu` 0755。发布前备份 `/var/backups/lingshu/lingshu-20261105T092701Z.dump`，SHA-256 为 `813a62c93e41922a54deddafbefc79176e8e431435732153d81ab48bdb7d6410`，`pg_restore --list` 可读（19 张表数据）。旧程序、配置与发布标记保留为 `prev-186bcc8`。

本次日志分为调用日志 / 操作日志两个标签：迁移 `0017_log_group_name` 给 `request_logs` 加服务分组名快照并按现有分组回填，线上 schema 由 `0016_user_note` 升到 `0017_log_group_name`。回填时 3185 条请求日志中 1190 条补上分组名；1995 条的 `group_id` 早已因分组删除置空（倍率快照 0.2、0.1、1 各有一批，2026-10-28 至 11-05），新迁移无法补回，可按 request_id 从删除前的每日备份恢复。旧进程等待进行中请求结束后退出，新进程启动、内网健康检查通过，服务日志无 ERROR。公网首页引用的 `index-B0Hsk727.js` 与干净构建产物 SHA-256 `4b0617cd224271183e28d25205035751f71f9894cc534714e8292f4c4d36a4fa` 一致。

0017 是纯增列迁移，旧程序不读该列：回滚只需换回 `prev-186bcc8` 程序，不需要数据库恢复。

运维备注：DMIT 的 SSH 对短时间内重复建连会直接断开（`Connection closed`），发布时用 `ControlMaster` 开一条主连接复用。本次从 WSL 直连 `gateway.piqode.com:443` 出现 TLS 握手失败（`SSL_ERROR_SYSCALL`），从 DMIT 经 Cloudflare 访问正常，公网核对改在 DMIT 上执行。

2026-11-01 发布并复核：DMIT 已发布提交 `a716f27161e93c0fca6c99fb2cce890ad43c0131`，运行中程序 SHA-256 为 `51becc46e164ec1614d46c6e72acb05b69d0593f1ffe3b627e1c780441b83f2e`，服务及内外网健康检查通过。发布前备份 `/var/backups/lingshu/lingshu-20261101T151309Z.dump`，SHA-256 为 `da3a5cfeda473da641ce9abe6a3bbf7bae21ed8f8b73cbb687d86b2b63ed17c7`，已通过 `pg_restore --list`。旧程序、配置与发布标记保留为 `prev-a716f27`。

本次新增管理员用户备注：迁移 `0016_user_note` 给 `users` 加 `note TEXT NOT NULL DEFAULT ''`，线上 schema 由 `0015_channel_balance` 升到 `0016_user_note`；该字段只在管理端用户列表返回，`/api/me` 不含。用户编辑弹窗同时把额度区的「备注」改名「额度备注」，并去掉「最近调整」列表（流水仍写库，可在账户日志与全站日志查看）。发布后用临时管理员会话只读核对：`/api/admin/users` 返回 `note` 字段、`/api/me` 不含该字段，会话已删除。公网首页与入口资源 `index-HpP6EULs.js` 的 SHA-256 为 `367755f967692605d101c7470865b5045b9210dec3a3001dcaace4e0ee230f74`，与干净构建产物逐字节一致。复核公网时注意：用 Python 默认 `Python-urllib` UA 访问首页会得到 403（Cloudflare），需换浏览器 UA。

0016 是纯增列迁移，旧程序不读该列：回滚只需换回 `prev-a716f27` 程序，不需要数据库恢复。

构建备注：本次 `npm ci` 因 registry 单包下载耗时过长（个别包 10–30 分钟）中断，改为复用已安装的 `web/node_modules`，并在复制前后校验 `package-lock.json` 与 `node_modules/.package-lock.json` 的 SHA-256 完全一致；构建仍取自干净的 detached worktree（提交 `a716f27`），worktree 除 `node_modules`、`web/dist` 外无改动。

2026-10-27 发布并复核：DMIT 已发布提交 `030f94751af51f2509d936f2ae51b0c250319698`，运行中程序 SHA-256 为 `493daa23191fcafd438c48493a1754c971898dd3d12b90f72b15be683e1efbe7`，服务及内外网健康检查通过。发布前备份 `/var/backups/lingshu/lingshu-20261027T155013Z.dump`，SHA-256 为 `f07af5f6a9aea19f561e994adf03ec529aa4ff87a59e7829fc82f8e3b46e26df`，已通过 `pg_restore --list`。旧程序、配置与发布标记保留为 `prev-030f947`。

本次取消上游余额估算，改为渠道「额度」按需查询：迁移 0015 删除 `upstream_accounts`、`upstream_account_entries` 两表及 `channels.upstream_account_id`、`request_logs.upstream_account_id`、`request_logs.upstream_cost`，渠道新增 `balance_detail` / `balance_checked_at` / `balance_error`。线上 schema 由 14 升到 `0015_channel_balance`，19 个渠道保留。用临时管理员在生产实测 DeepSeek、Moonshot、OpenRouter 三个渠道查询成功（¥8.93、¥71.345474、$9.97802656），无余额接口的智谱返回 400；验证账号与会话已删除。公网资源 `index-DhYQCzPW.js` 的 SHA-256 为 `cb18849d99b4cf4e728d0f97a2c324373d6790dd42d76251280970793950bf12`，与干净构建产物一致。

0015 是删表删字段的不可逆变更：旧程序依赖已删除的表和列，回滚不能只换程序，必须恢复发布前备份并接受备份之后的请求日志丢失；保留的 `prev-030f947` 只用于同一 schema 下的紧急替换。

2026-10-26 发布并复核：DMIT 已发布提交 `5f0ee4f475debb41d4e120f2e92cec32ea1dca81`，运行中程序 SHA-256 为 `d3e759a08f960cd6959f83187d868786a22dbe75c84225c92142cf2e8fc928d7`，服务及内外网健康检查通过。发布前备份 `/var/backups/lingshu/lingshu-20261026T234615Z.dump`，SHA-256 为 `dd22f00caa17cd4e5d94555528f5d75f5ba2ed7e68f80284055aa76515a98e92`，已通过 `pg_restore --list`。旧程序、配置与发布标记保留为 `prev-5f0ee4f`。前端资源 `index-Br285SK3.js` 的 SHA-256 为 `3cbe34a584e4dff178591701323cc0b22a691ad54bf7462edc77599c8d23fd9d`，与已推送提交的干净构建产物一致。

本次在用户和服务分组列表新增 ID 列，并将渠道列表表头统一为 `ID`。前端类型检查、前端及程序构建通过；登录后的三处页面已只读核对，用户及服务分组的显示 ID 与接口返回值一致，无接口或数据库变更。系统使用模式仍默认且仅开放“公司团队网关”。

2026-10-25 发布并复核：DMIT 应用正常，公网 `/healthz`、账户日志页与前端资源均返回 HTTP 200。应用版本为 `a08594cd18bbf66d6623d825952a0f6fab4d6e6a`，程序 SHA-256 为 `603c3d888ba62659bd4cd673c75d9a3ba0c68e30b5fcaf9ede4dcb0b2e2da696`，与 `/opt/lingshu/release.json` 一致。本次无数据库迁移。

该版本将模型调用、在线充值和额度调整统一纳入全站日志及用户账户日志，分页总数和各类金额独立统计。发布前备份为 `/var/backups/lingshu/lingshu-20261026T040328Z.dump`（UTC 时间），SHA-256 为 `8357fd0f9ec49cdf72cc98619ee8ad76dd2340511688fdc276f9fe7a25ddc311`，已通过 `pg_restore --list`；旧程序、配置及发布标记以 `prev-a08594c` 后缀留存。前端资源为 `index-D5w4AcIX.js`，公网内容 SHA-256 为 `08656f8a28bb9c1922f1c0fb3d516e342fd67d37acb21b49abc1d8b2d1b47d31`，与干净检出构建产物一致。

使用已有账号的短期验证会话，只读验证管理员全站日志与用户账户日志页面，以及全部、充值、额度调整和调用筛选，响应与页面均通过；验证会话已删除。未新增充值、未调整真实用户余额。

## 连接、安装和隔离

从 WSL 连接 DMIT：

```bash
ssh -i ~/.ssh/dmit_lax_ed25519 -o IdentitiesOnly=yes root@136.175.178.126
```

| 用途 | 仓库文件 | DMIT 安装位置 |
|---|---|---|
| 应用成品 | `bin/lingshu` | `/opt/lingshu/lingshu` |
| 数据库 Compose | `deploy/dmit-postgres.compose.yml` | `/opt/lingshu/postgres.compose.yml` |
| 数据库服务 | `deploy/lingshu-dmit-db.service` | `/etc/systemd/system/lingshu-db.service` |
| 应用服务 | `deploy/lingshu-dmit.service` | `/etc/systemd/system/lingshu.service` |
| HTTPS 站点 | `deploy/caddy-lingshu-dmit.conf` | 追加到现有 `/opt/newapi/Caddyfile` |
| 备份脚本 | `deploy/backup/lingshu-dmit-backup` | `/usr/local/bin/lingshu-backup` |
| 备份服务 / timer | `deploy/backup/lingshu-dmit-backup.service`、`deploy/backup/lingshu-dmit-backup.timer` | `/etc/systemd/system/lingshu-backup.service`、`/etc/systemd/system/lingshu-backup.timer` |

应用以 `lingshu` 用户运行。安装目录、服务账户和凭据文件先准备好，再安装 unit、执行 `systemctl daemon-reload`，启用 `lingshu-db.service`、`lingshu.service` 和 `lingshu-backup.timer`。备份脚本需要可执行权限。现有安装已完成这些步骤。

- `/etc/lingshu/postgres.env` 为 root 私有的 0600 文件，包含 `POSTGRES_USER=lingshu`、`POSTGRES_DB=lingshu` 和独立密码。
- `/etc/lingshu/env` 权限为 0640、属主为 root:lingshu；设置 `LINGSHU_LISTEN=172.18.0.1:8300`、`LINGSHU_PUBLIC_URL=https://gateway.piqode.com`，以及连接回环端口 `55433` 的 `LINGSHU_DATABASE_URL`。数据库密码需与数据库凭据一致；凭据不进入 Git、终端输出或日志。
- `lingshu-postgres` 是独立的 PostgreSQL 18.6 容器，镜像固定 digest，数据卷挂载 `/var/lib/postgresql`；宿主仅监听 `127.0.0.1:55433`。
- 应用监听 `172.18.0.1:8300`，供 `newapi_default` Docker 网桥中的 Caddy 使用。现有 UFW 只允许网桥 `br-9ec5e8fedd4a` 上 `172.18.0.0/16` 来源访问 8300；重新创建网络时检查实际桥名及地址后再配置。
- HTTPS 使用 Cloudflare → 现有 Caddy 容器 → 应用。站点保留流式刷新和真实客户端 IP 转发，访问日志为 Caddy 容器内 `/var/log/caddy/lingshu-access.log`。修改 Caddyfile 前保存原文件，校验通过后平滑 reload。
- PiQode 原有 PostgreSQL 15、Redis 及其他容器继续使用各自数据和配置。Lingshu 的数据库操作只针对 `lingshu-postgres`，不操作 `newapi-postgres`。上游渠道使用 DMIT 直连。OpenRouter 预设仍预填 `socks5://127.0.0.1:10808`；新建渠道采用直连时在表单中清空代理地址。

## 发布

1. 在 WSL 明确已推送的 Git 提交，使用干净的 detached worktree 构建。当前 Vite 使用 Node 22；已验证的路径为 `/home/aumif/.nvm/versions/node/v22.22.3/bin`。先完成本次变更所需测试，前端执行类型检查和构建。
2. 构建嵌入前端资源的 Linux amd64 成品：

   ```bash
   npm --prefix web ci --prefer-offline --no-audit --no-fund
   npm --prefix web run build
   touch web/dist/.gitkeep
   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=true -trimpath -ldflags='-s -w' -o bin/lingshu ./cmd/lingshu
   sha256sum bin/lingshu
   ```

3. 全量 Go 测试设置 `LINGSHU_TEST_DATABASE_URL` 后执行 `go test -p 1 ./...`。测试会重建指定库的 public schema，只使用可丢弃的独立测试库。
4. 记录完整提交号、构建工具版本和 SHA-256。Go 成品不一定嵌入 VCS 信息，发布证据以干净 worktree 和 `release.json` 为准。
5. 上传成品到 DMIT 临时位置；发布前执行 `/usr/local/bin/lingshu-backup`，检查备份可读，并保存当前程序、`/etc/lingshu/env` 和 `/opt/lingshu/release.json`。按本次提交命名保留文件，配置副本保持原有严格权限。
6. 将成品安装为 `/opt/lingshu/` 中的临时文件，核对哈希和可执行权限后原子替换正式程序。重启 `lingshu.service`，检查启动迁移和服务日志；在 `/opt/lingshu/release.json` 记录发布提交与哈希。
7. 检查主机内 `/healthz`、公网 HTTPS、页面及新前端资源。协议或计费变更需进行对应真实调用，核对请求日志、usage、扣费及 key 额度。最终确认运行程序哈希与发布记录一致。

## 备份、恢复与回滚

`lingshu-backup.timer` 每天服务器时间 04:00 触发，随机延后最多十分钟。脚本在 `/var/backups/lingshu/` 生成 PostgreSQL custom-format dump 及 SHA-256 文件，目录为 root 私有；仅轮换脚本生成的常规备份，保留最近 14 份。当前备份保存在 DMIT 本机，持续异地备份待安排。

在 DMIT 上手动备份并检查：

```bash
/usr/local/bin/lingshu-backup
systemctl status lingshu-backup.service lingshu-backup.timer
```

恢复演练使用容器内新建的独立临时数据库。将下列变量替换为选定备份与本次演练专用的新库名，先校验哈希，再检查归档及逐表行数：

```bash
backup_file='/var/backups/lingshu/<selected-backup>.dump'
restore_database='lingshu_restore_verify_<timestamp>'
sha256sum -c "$backup_file.sha256"
docker exec -i lingshu-postgres pg_restore --list < "$backup_file"
docker exec lingshu-postgres createdb -U lingshu "$restore_database"
docker exec -i lingshu-postgres pg_restore -U lingshu -d "$restore_database" --no-owner --exit-on-error < "$backup_file"
```

部分历史发布备份的哈希记录在本文或对应发布证据中，未必附带 `.sha256` 文件；这类备份使用 `sha256sum` 与已记录值核对。当前 schema 的恢复验收按当前表清单进行。

生产恢复前先确认目标库、恢复时间和受影响订单，保存当前数据库及配置，再停 `lingshu.service`。准备确认过的空目标库后，在 `lingshu-postgres` 内执行 `pg_restore -U lingshu -d <target_database> --no-owner --exit-on-error`，核对数据后恢复应用。覆盖现有数据库单独制定操作步骤。

程序回滚采用临时文件加原子替换，配套恢复环境文件和发布标记，再启动服务。schema 11 的用户分组、schema 12 的模型结构等变更与旧程序不兼容；跨越不兼容迁移时需要数据库恢复方案。产生真实付款后，发布前备份不能直接覆盖新增订单、通知和余额；支付故障处理见 [支付接入](payments.md)。

整体停用 Lingshu 时，停用应用、备份 timer 和数据库 service，恢复预先保存的 Caddyfile 并校验、平滑 reload，移除对应的 8300 网桥放行规则。保留数据库卷与备份。

## 检查与排障

在 DMIT 上检查运行状态：

```bash
systemctl is-active lingshu.service lingshu-db.service lingshu-backup.timer
journalctl -u lingshu.service -n 100 --no-pager
journalctl -u lingshu-backup.service -n 50 --no-pager
docker exec lingshu-postgres pg_isready -U lingshu -d lingshu
curl --fail --silent --show-error http://172.18.0.1:8300/healthz
curl --fail --silent --show-error https://gateway.piqode.com/healthz
sha256sum /opt/lingshu/lingshu
cat /opt/lingshu/release.json
```

登录故障先检查公网页面、浏览器请求响应及 Secure / HttpOnly 会话 cookie，再结合应用 journald 和 Caddy 访问日志定位。核对 `LINGSHU_PUBLIC_URL`、HTTPS 和站点转发配置；检查日志时不输出会话值或上游密钥。

Cloudflare 曾对 Python 标准库默认 `Python-urllib` User-Agent 返回 1010；部署检查标识以及 requests / OpenAI / Anthropic SDK 标识验证可达。公网调用若返回 403 且应用无对应日志，先查 Cloudflare 与 Caddy。历史上主机内调用成功不能单独证明同一次公网调用通过。

## DMIT 历史验收证据

### 初次上线与恢复演练（2026-10-23）

初始数据库快照时间为 `2026-10-23T08:45:17.337778Z`，使用同一事务快照核对 15 张表：1 个用户、2 把 API key、11 个渠道、23 个模型、24 条渠道模型映射、43 条请求日志。随后从干净 worktree 发布 System One 提交 `4bef2a13d8bbd7ef654bb788aa2a26643ec74859`，程序 SHA-256 为 `f94184bf62c270543405752757025f91931b3c6d306d96d5fe026f05a66bdbfd`，当时为 schema 9。

- 公网 HTTPS、管理员登录、Secure / HttpOnly 会话 cookie、首页和构建资源通过。TypeSafe / Nace 模板可用，当轮没有自动创建对应生产渠道。
- 9 个启用渠道、12 个模型映射，三种聊天协议共 36/36 个真实流式测试通过，全部上报完整用量。公网 Chat 流式、Messages 和 Responses 非流式均 HTTP 200；Chat 样本含 259 个 SSE 事件及 `[DONE]`，首事件 0.415 秒、事件最大间隔 0.371 秒。
- 三笔调用总扣费 `0.0003729000 USD`，与 token × 价格 × 分组倍率、测试余额减少和 key 已用额度精确一致。验收账号已停用，会话撤销，测试 key 已停用；测试日志保留。
- 日常备份完成恢复演练，schema 9 的 15 张表逐表行数一致；验证库 `lingshu_restore_verify_20261023` 和 `lingshu_restore_release_4bef2a1` 保留供复核。这是历史版本的恢复证据。
- 既有容器 ID、启动时间及重启计数保持不变，只对 Caddy 平滑 reload。数据库回环监听、应用网桥监听和三个服务 / timer 的开机启动验证通过。

DMIT 初始安装前备份和验收 JSON 保存在 root 私有目录 `/var/backups/lingshu-dmit-copy-20261023/`，包括原 Caddyfile、UFW 规则、既有数据库备份、快照 manifest、容器基线、协议计费与恢复演练结果。归档及数据库含密钥，保留在主机私有目录，不进入 Git。

### 真实支付（2026-10-24 UTC）

多支付版本 `ba139c4b7b5df55fd8c81d39581b380bfd3bdbe2` 在 DMIT 完成 schema 13 迁移，原有用户、模型及余额调整流水完整保留。发布前备份为 `/var/backups/lingshu/lingshu-20261024T045936Z-pre-ba139c4.dump`，SHA-256 `80ff538eb4c01182b7bdff4919ce442b96398a9054536ccd3369bac193f87afc`；旧程序和标记为 `/opt/lingshu/lingshu.prev-ba139c4`、`/opt/lingshu/release.prev-ba139c4.json`。

使用现有管理员验收订单 `149dd1a4f9e707e1610e21d2317e3ca5`，报价 CNY 6.70、到账 USD 1，通知地址为 `https://gateway.piqode.com/api/payments/epay/1/prod/notify`。用户实际付款后，`2026-10-24T05:04:08Z` 收到有效成功通知并自动入账；独立向 ZPAY 查单确认成功、订单一致、实付 CNY 6.70。余额从 `$9.8493093969` 增至 `$10.8493093969`，仅一条 USD 1 流水，operator 为空；连续两次核对无重复入账。

付款后备份 `/var/backups/lingshu/lingshu-20261024T050524Z-post-epay-paid.dump`，SHA-256 `6e06cbae717fa3d0d02e323cc4c1778e1accb7f9f2881951205123178cd8ec1f`，通过 `pg_restore --list`。验收后通过管理接口开启 EPay 及充值总开关，配置版本为 2、通知版本为 1，钱包显示支付宝；这些是验收时的配置与余额，当前值以管理接口为准。Stripe、Creem、Waffo、Pancake 只有协议和 SDK 模拟测试记录，真实付款尚待验收。

### DeepSeek 峰谷规则配置（2026-10-23）

DMIT 模型 ID 1 `deepseek-v4.1-flash` 在保留周末谷价的基础上补齐 UTC+8 18:00–09:00 和 12:00–14:00 两条谷价。通过管理接口保存，接口与数据库回读均为三条，`TestDeepSeekOffPeak` 边界测试通过。基础输入/输出/缓存读/缓存写价格为 0.30/1.20/0.006/0，谷价为 0.15/0.60/0.003/0（USD / 百万 token），按请求开始时间匹配，历史账单不追溯。

改前备份 `/var/backups/lingshu/lingshu-20261024T051740Z-pre-deepseek-time-rules.dump`，SHA-256 `1f9800dcf05cbb34570a69ed3df4dbee579eaa0c0e57607b1d282454211cf7e6`，通过 `pg_restore --list`；同前缀 `-model-before.json` / `-model-after.json` 保留修改记录。

### 注册管理（2026-10-24）

`59d8759e87155ff453301db82572d3de67c5b592` 发布时完成 schema 14 迁移。发布前 DMIT 备份 `/var/backups/lingshu/lingshu-20261024T094052Z.dump`，SHA-256 `9c57620331c300caccb16cc9b9f07d6f3bc996f48fa800bfc7f62412f0460648`，通过 `pg_restore --list`；旧程序与标记为 `/opt/lingshu/lingshu.prev-59d8759`、`/opt/lingshu/release.prev-59d8759.json`。当次服务、内部及公网健康检查通过，公网注册状态为 `{"enabled":false}`，未生成注册码或新增账户。
