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

## 现役备份与恢复

两端每日备份保留最近 14 份。DMIT 脚本在 `deploy/backup/lingshu-dmit-backup`，PostgreSQL 数据卷和 PiQode 原有 PostgreSQL 15 完全独立。

- UM 手动备份：以 root 执行 `systemctl start lingshu-backup.service`。
- DMIT 手动备份：以 root 执行 `/usr/local/bin/lingshu-backup`。
- UM 恢复：先停 Lingshu，再用 `postgres` 用户向确认过的目标库执行 `pg_restore --no-owner --exit-on-error`；覆盖现有库须另行确认。
- DMIT 恢复演练：在 `lingshu-postgres` 内创建独立临时数据库，再通过 `docker exec -i lingshu-postgres pg_restore -U lingshu -d <临时库> --no-owner --exit-on-error` 输入备份。验证逐表行数。不要操作 `newapi-postgres`。

本轮已验证 DMIT 日常备份可恢复，15 张表行数一致。当前仅保证各主机本地备份；新增副本的持续异地备份另行规划。UM 迁移记录见 [um580d-migration-20261022.md](um580d-migration-20261022.md)，DMIT 快照与验收见 [dmit-copy-20261023.md](dmit-copy-20261023.md)。

## 现役代理与渠道

UM 的 OpenRouter 等渠道如需出境，可用 `lingshu-proxy.service` 提供的 `socks5://127.0.0.1:10808`。DMIT 直连，不复制这条本机代理依赖。本轮只清空 DMIT 渠道 id=12 的旧代理地址，保持其停用状态；其他渠道开关和上游密钥保持复制时状态。

## 现役回滚

两端还保留较早版本 `/opt/lingshu/lingshu.prev-20261023`。最近一次发布前二进制为 `/opt/lingshu/lingshu.prev-102f002`。程序回滚采用临时文件加原子替换，再重启服务；schema 11 改变了用户分组表结构，回滚到旧二进制前必须恢复该次发布前数据库备份。

DMIT 整体停用副本的步骤和隔离边界见 [dmit-copy-20261023.md](dmit-copy-20261023.md)。UM 原入口可独立使用。

腾讯云旧部署步骤保留在 Git 历史中；现役操作以上述双机流程为准。
