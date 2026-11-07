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
- **官方对照组**：本机 Magpie 网关 `http://127.0.0.1:3425/v1`，key `magpie`；`codex/*` 走用户自己的官方账号，用于模型检测的官方样本。

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
