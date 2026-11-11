# 部署与运维

项目在本机 WSL 管理源码和构建，部署到个人 DMIT 洛杉矶服务器。仓库为 `https://github.com/aumi314/lingshu`，线上入口为 `https://gateway.piqode.com`，默认 API Base URL 为 `https://gateway.piqode.com/v1`。

## 当前运行基线

2026-11-05 起由自动发布部署，当前版本以 DMIT `/opt/lingshu/release.json` 为准（`source` 为 `github-actions main-latest`），每次发布记录在 `journalctl -u lingshu-deploy.service`，不再逐次在此手写。改为自动发布前的手动发布记录、初次上线和真实支付等验收证据见 `docs/history.md`。

## 运维速查

日常要用的几条，细节见后面各节。

- **确认上线**：PR 合并到 main 后，DMIT 的 `lingshu-deploy.timer` 约一分钟内拉取并发布（发布前自动备份）；看 `/opt/lingshu/release.json` 里的 commit。只改 `docs/`、`*.md`、`.github/`、`deploy/`、`scripts/`、`LICENSE` 的提交不发布。
- **SSH 复用连接**：DMIT 对短时间内重复建连会直接断开，先建一条主连接，后续 `ssh -o ControlPath=/tmp/dmit-ctl root@136.175.178.126 …` 复用；连接过期时删掉 `/tmp/dmit-ctl` 再建。

  ```bash
  ssh -i ~/.ssh/dmit_lax_ed25519 -o IdentitiesOnly=yes -o ControlMaster=yes -o ControlPath=/tmp/dmit-ctl -o ControlPersist=60m -fN root@136.175.178.126
  ```

- **线上数据库**：`docker exec lingshu-postgres psql -U lingshu -d lingshu`（改数据前遵守 `AGENTS.md`「运维约定」）。
- **临时调管理接口**：在 `sessions` 插一条记录（`token_hash` 为随机 token 的 SHA-256，`user_id = 1`，过期时间设短），请求带 Cookie `lingshu_session=<token>`，用完立即删除并确认删掉。
- **本地测试库**：`LINGSHU_TEST_DATABASE_URL='postgres://lingshu:lingshu_dev@127.0.0.1:55432/lingshu_test?sslmode=disable' go test -p 1 ./...`（`make db-up` 起容器；测试会重建该库的 public schema）。
- **打真实上游的实测**：`go test` 加 `-count=1`，否则命中缓存、重放旧结果。
- **官方对照组**：本机 Magpie 网关 `http://127.0.0.1:3425/v1`，key `magpie`；`codex/*` 走用户自己的官方账号，用于模型检测的官方样本。Claude 暂无免费对照：渠道 18（Anthropic 官方 API）余额不足；渠道 12（OpenRouter）锁定 `provider.only: ["anthropic"]` 可对照模型行为，但贵，用前先报预计花费。

## 模型检测伪装配置

`/etc/lingshu/probe-disguise.json`（root:lingshu，0640），`/etc/lingshu/env` 里设 `LINGSHU_PROBE_DISGUISE_FILE=/etc/lingshu/probe-disguise.json`。内容是真实客户端的请求头、系统提示和工具（客户端原文，不进仓库），由 WSL 上 `references/probe-sites/build_disguise.py` 从抓包生成。更新：抓新版客户端的请求 → 重新生成 → 传到服务器覆盖 → `systemctl restart lingshu`（日志里有“模型检测伪装配置已加载”）。文件缺失或格式不对时服务照常启动，检测退回简版伪装并打一条警告。

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

## Lingshu 站点防护（2026-11-09，已发布）

2026-11-09 已分别发布应用 `eba864f`（北京时间 02:02）和 Caddy 站点防护（02:13），版本、备份、哈希与验收证据见 `docs/history.md`「三个 PR 合并发布与转接产物复验」。模板仅限制 gateway.piqode.com，按 remote_ip 连接来源放行 Cloudflare，其他来源 HTTPS 返回 403；旧站与全局防火墙不变。现役 Caddy 2.11.4 的即时 SSE、WS 与客户端断开取消已在隔离模拟上游验证。

- 官方网段来自 https://www.cloudflare.com/ips-v4 和 https://www.cloudflare.com/ips-v6，2026-11-09 核对为 IPv4 15 条、IPv6 7 条。更新时验证两份列表每行 CIDR，将同一列表同时用于站点 @not_cloudflare 和现有全局 servers.trusted_proxies static；保留其他全局选项。列表与现役相同则无需改全局块。
- remote_ip 决定准入，client_ip 解析可信代理传来的真实用户地址并覆盖 X-Real-IP。生产名单不加入回环或整个内网；运维健康检查访问 http://172.18.0.1:8300/healthz。
- HSTS 仅作用本域名，不含 includeSubDomains；CSP 仅设 frame-ancestors 'none'，保留现有脚本、样式与支付跳转。
- HTTP 自动重定向保留。活动 HTTP-01 挑战在用户路由之前处理，见 [Caddy v2.11.9 server.go](https://github.com/caddyserver/caddy/blob/v2.11.9/modules/caddyhttp/server.go#L708) 和 [tls.go](https://github.com/caddyserver/caddy/blob/v2.11.9/modules/caddytls/tls.go#L806)；TLS-ALPN 在握手层处理。无需放开任意伪造 challenge 路径。隔离测试验证跳转，真实证书续期在上线时检查日志。

获准发布后的 Caddy 操作：

1. 记录 docker exec newapi-caddy caddy version，备份 /opt/newapi/Caddyfile 为同目录带 UTC 时间戳副本。候选文件只替换 gateway 站点块，按上述步骤核对全局 trusted_proxies；逐段 diff，旧站块保持原样。
2. 候选配置复制到容器临时路径，执行 caddy validate --config <容器候选路径> --adapter caddyfile。校验通过后才替换现役配置。
3. 将候选内容原位写入宿主 /opt/newapi/Caddyfile，保持 bind mount 的原 inode；核对容器挂载路径后执行 docker exec newapi-caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile，采用平滑 reload。
4. 低频验证公开 HTTPS、HTTP 308、内部 healthz、匿名管理 401、安全头；外部 curl --resolve gateway.piqode.com:443:<源站IP> https://gateway.piqode.com/healthz 应为 403，伪造转发头仍为 403。公开域名扫描路径为 404；核对旧站、SSH 正常及证书日志。
5. 异常时将备份内容原位写回并 reload，再确认健康；应用二进制按既有发布流程回退。不改数据库、不重启其他服务。

隔离回归：在 WSL/Linux、Docker 环境运行 python3 scripts/test-caddy-security.py，默认镜像 caddy:2.11.9-alpine，可用 CADDY_TEST_IMAGE 指定与生产相同镜像。只使用随机本地端口、临时 Caddy 容器与模拟上游，结束移除该容器；覆盖来源允许/拒绝、伪造头、安全头、扫描路径、HTTP 跳转、SSE 和 WS 帧数据。

应用兼容约定：登录和 challenge 各限 30 次/分钟、突发 10，429 以 Retry-After 为准。带请求体的写接口要求 application/json；无来源头 CLI 和无体 DELETE 可用。生产 LINGSHU_PUBLIC_URL 设置公开 HTTPS 地址；本地 Vite 开发可不设置，若显式设置须和前端 origin 一致。模型 API 与支付通知不套浏览器来源检查。

## 发布

### 自动发布（默认，2026-11-05 起）

规则见 `docs/decisions.md`「CI 与自动发布」。流程：分支开发 → PR → CI 全绿 → 合并到 main → GitHub 构建发布到滚动预发布 `main-latest` → DMIT 每分钟拉取并部署 → 核实。

| 环节 | 文件 | 位置 |
|---|---|---|
| CI 检查 | `.github/workflows/ci.yml` | GitHub Actions（push 到 main、PR） |
| 构建发布 | `.github/workflows/release.yml` | main 上 CI 成功后触发，产物在 Release `main-latest`：`lingshu`、`manifest.json` |
| 拉取部署 | `deploy/release/lingshu-dmit-deploy` | DMIT `/usr/local/bin/lingshu-deploy` |
| 定时触发 | `deploy/release/lingshu-dmit-deploy.service`、`.timer` | `/etc/systemd/system/lingshu-deploy.service`、`lingshu-deploy.timer` |

部署脚本每次：读清单 → 提交与 `/opt/lingshu/release.json` 相同就退出 → 待执行迁移含破坏性迁移（删表、删列、`TRUNCATE`、`DELETE FROM`；删约束、索引等不算，规则在 `release.yml`）则暂停 → 下载成品并核对清单哈希 → 单独备份 `/var/backups/lingshu/lingshu-deploy-<时间>-<提交>.dump`（保留 10 份，不占每日备份名额）并检查可读 → 保存 `prev-<提交>` → 原子替换、重启 → 30 秒内健康检查 → 写 `release.json`（`source` 为 `github-actions main-latest`）→ 清掉这个提交的暂停 / 放行标记，旧版本只留最近 5 套。健康检查失败自动换回旧版本，并记 `/var/lib/lingshu-deploy/failed-<提交>`，同一提交不再重试。

在 DMIT 上查看与干预：

```bash
journalctl -u lingshu-deploy.service -n 50 --no-pager   # 每次发布的输出
systemctl list-timers lingshu-deploy.timer
cat /opt/lingshu/release.json
ls /var/lib/lingshu-deploy/                              # hold-* 暂停中 / failed-* 失败过
touch /var/lib/lingshu-deploy/approve-<完整提交号>        # 确认破坏性迁移后放行
systemctl stop lingshu-deploy.timer                      # 临时停止自动发布
```

等待一次发布结束时注意：发布服务是 oneshot，运行中状态为 `activating`，不是 `active`；用 `systemctl show -p ActiveState --value lingshu-deploy.service` 判断。重启时旧进程会等进行中的流式请求结束（最长 35 秒），这期间公网可能短暂返回 502。

首次自动发布（2026-11-05）：`f2d9211` 由 GitHub 构建，DMIT 08:27:00（PST）发现、08:27:21 完成，含单独备份与健康检查；`release.json` 来源为 `github-actions main-latest`，程序哈希与清单一致。

核实：GitHub Actions 两个工作流都成功；DMIT 日志出现“已发布 <提交>”；`release.json` 提交与哈希和清单一致；公网 `/healthz` 和页面资源正常；涉及协议或计费时做真实调用核对。

### 手动发布（备用）

GitHub 或自动发布不可用时使用。

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

## 邮箱注册与 Outlook 发信

邮箱注册版本合并并完成自动发布后，先保持邮箱注册关闭，导入和测试发信账号，再开放邮箱注册。操作使用超级管理员权限，入口为“系统设置 → 注册管理 → 发信账号”。

1. 从项目外的私有配置导入五个 Outlook 账号：原始文件第 3 个为主发，第 4～7 个依次备用。每个账号填写邮箱、Client ID 和独立 Refresh Token；Client ID 可共用，新增窗口会预填已有账号的 Client ID。发件名称固定为 `Lingshu`。保存后只显示授权已配置，编辑时 Refresh Token 留空保留原值。
2. 逐个点击“检查授权”。需要 Microsoft Graph `Mail.Send` 与 `offline_access` 授权。修改邮箱、Client ID 或 Refresh Token 后重新检查。按上移、下移调整主备顺序；停用账号不参与发送。
3. 逐个点击“测试邮件”，收件地址默认 `support@piqode.com`。此操作会实际发送邮件。五个账号分别确认提交结果并核对收件箱；Graph `202` 和后台“已提交发送”表示微软已接受请求，实际收件由收件端验收，发件账号的“已发送”记录可辅助核对。
4. 授权和收件验证完成后，打开“开放注册”，勾选“邮箱认证”并保存。至少有一个启用且授权检查通过的账号时才允许保存。用受控收件箱验证收码、注册、默认分组、自动登录，再退出并用用户名和密码登录。需要邀请码限制时同时勾选“注册码验证”；两项都选时注册必须同时验证。关闭开放注册仅影响后续注册，已有账号正常使用。

批量导入也可使用同一管理接口：先 `GET /api/admin/mail-accounts` 核对现有账号，逐个 `POST /api/admin/mail-accounts`，请求字段为 `email`、`client_id`、`refresh_token`、`enabled`、`sort_order`（主发为 0，备用依次为 1～4）。私有配置在本地进程内读取并作为 HTTPS 请求体发送，不将凭据放入命令行参数、终端输出或日志。后续按返回的 id 操作：

| 操作 | 接口 |
|---|---|
| 编辑、启停 | `PUT /api/admin/mail-accounts/{id}`，字段同添加；空 Refresh Token 保留原值 |
| 调整全部账号顺序 | `PUT /api/admin/mail-accounts/order`，请求 `{"ids":[...]}` |
| 检查授权 | `POST /api/admin/mail-accounts/{id}/check` |
| 实际发送测试邮件 | `POST /api/admin/mail-accounts/{id}/test`，请求 `{"recipient":"support@piqode.com"}` |
| 要求注册码验证 | `PUT /api/admin/settings`，请求 `{"registration_enabled":true,"registration_code_required":true}` |
| 要求邮箱认证 | `PUT /api/admin/settings`，请求 `{"registration_enabled":true,"registration_email_required":true}` |

日常发送从首个启用账号开始，仅账号授权失效或微软明确返回账号故障时按顺序切备用。收到 `429` 时按 `Retry-After` 等待；超时、连接中断或服务端结果不明时先检查收件箱，再决定重试。需要暂停公开注册时，将 `registration_enabled` 设为 `false`；用户登录保持原样。

Refresh Token 保存在数据库中，微软轮换后会自动写回。数据库备份也包含这些凭据，沿用下节的 root 私有目录和恢复流程；导出或传输备份时按凭据文件处理。恢复旧备份可能恢复旧授权，恢复后逐个检查授权并发送测试邮件，通过后再开放邮箱注册。

## 用户协议与隐私政策

在“系统设置 → 主设置”的公告下方分别编辑用户协议、隐私政策，支持纯文本多行内容。旧版 PiQode 原文来源为旧站数据库 `options` 中的 `legal.user_agreement` 和 `legal.privacy_policy`；读取后通过 Lingshu `PUT /api/admin/settings` 的 `user_agreement`、`privacy_policy` 字段导入，随后逐字核对返回内容。原文不放入公开仓库。

公开阅读地址为 `/terms`、`/privacy`，无需登录。配置至少一份文档后，所有自助注册均要求勾选同意；后端校验文档版本，用户记录保存同意版本及时间。文档更新后，停留在旧注册页的用户需刷新阅读并重新同意。管理员手动创建和现有账号不补录注册同意。

## 备份、恢复与回滚

`lingshu-backup.timer` 每天服务器时间 04:00 触发，随机延后最多十分钟。脚本在 `/var/backups/lingshu/` 生成 PostgreSQL custom-format dump 及 SHA-256 文件，目录为 root 私有。

保留策略（2026-11-06 调整）：

| 类别 | 文件 | 保留 | 清理者 |
|---|---|---|---|
| 每日备份 | `lingshu-<UTC 时间>.dump` | 分层：最近 14 天每天一份，再往前 8 周每周一份，再往前 6 个月每月一份（约 27 份）；同一周期多次备份留最新的；刚生成的一份不删 | `lingshu-backup` |
| 发布前备份 | `lingshu-deploy-<时间>-<提交>.dump` | 最近 10 份 | `lingshu-deploy` |
| 旧版本 | `/opt/lingshu/lingshu.prev-<提交>`、`env.prev-<提交>`、`release.prev-<提交>.json` | 最近 5 套（按程序文件时间），发布成功后清理 | `lingshu-deploy` |
| 安装基线 | `/var/backups/lingshu-dmit-copy-20261023/` | 长期保留（整体停用时恢复原 Caddyfile、UFW 用） | 手动 |

改为分层前是“最近 14 份”，手动发布一天跑好几次备份时，实际只能覆盖一周左右。手动做的临时备份不要用 `lingshu-<时间>.dump` 这个名字，否则会被每日轮换按日期处理；用带说明的后缀，例如 `lingshu-<时间>-pre-<用途>.dump`，用完手动删除。

当前备份都在 DMIT 本机，机器整体故障时会和数据库一起丢失；异地备份待安排。

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
