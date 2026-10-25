# 部署与运维

项目在本机 WSL 管理源码和构建，部署到个人 DMIT 洛杉矶服务器。仓库为 `https://github.com/aumi314/lingshu`，线上入口为 `https://gateway.piqode.com`，默认 API Base URL 为 `https://gateway.piqode.com/v1`。

## 当前运行基线

2026-10-25 复核：DMIT 应用、数据库和备份 timer 正常，公网 `/healthz` 返回 HTTP 200。应用版本为 `9be6d37b368618a18da15e110b5fb8ae6851ca7c`，程序 SHA-256 为 `138378388c429ec3ce59c53df32f72d9e368b771bb0051e3ddb563bc6478db09`，与 `/opt/lingshu/release.json` 一致。数据库为 schema 14（`0014_registration_codes`）。

该版本为 Grok / Muse 补齐模型图标。发布前 DMIT 备份为 `/var/backups/lingshu/lingshu-20261025T044938Z.dump`，SHA-256 为 `bb0a71a2472beb7d3c0561e06bfab93e5b7b23b2c16c36e46435072d97d1396d`，已通过 `pg_restore --list`；原程序、配置及发布标记以 `prev-9be6d37` 后缀留存。前端资源为 `index-CnxsVh8s.js`。

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
