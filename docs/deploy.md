# 部署（腾讯云上海）

服务器资产和全局约束见 `/home/aumif/myproject/01_main/infra/`（腾讯云 = `tencent-server`，OpenCloudOS 9.4，管理用户 `ubuntu`）。

## 布局

| 项 | 位置 |
|---|---|
| 程序 | `/opt/lingshu/lingshu`（上一版 `lingshu.prev`，用于回滚） |
| 配置 | `/etc/lingshu/env`（600，root:lingshu 可读），含数据库密码 |
| 服务 | `lingshu.service`，系统用户 `lingshu`，只监听 `127.0.0.1:8300` |
| 数据库 | 服务器已有的 PostgreSQL，库 `lingshu`、角色 `lingshu` |
| 站点 | `/etc/nginx/conf.d/lingshu.conf` + 共用部分 `/etc/nginx/lingshu-common.inc`（模板在 `deploy/`） |
| 域名 | `lingshu.focuskeeper.cn`（境内外都能访问，日常用这个）；`lingshu.piqode.com`（只有境内能访问，境外 TLS 握手会被按域名断开） |
| 证书 | certbot webroot，两个域名各一张：`/etc/letsencrypt/live/<域名>/` |

## 日常发布

```bash
bash deploy/deploy.sh
```

本地编译、上传、保留上一版、重启、健康检查；健康检查不过自动回滚。

## 首次部署（一次性）

1. 系统用户和目录：`useradd --system --no-create-home --shell /sbin/nologin lingshu`；建 `/opt/lingshu`、`/etc/lingshu`。
2. 数据库：用 `postgres` 超级用户建角色 `lingshu`（随机密码）和库 `lingshu`（owner lingshu）。
3. `/etc/lingshu/env`：
   ```text
   LINGSHU_LISTEN=127.0.0.1:8300
   LINGSHU_DATABASE_URL=postgres://lingshu:<密码>@127.0.0.1:5432/lingshu?sslmode=disable
   ```
4. 安装 `deploy/lingshu.service`，`systemctl enable --now lingshu`（启动时自动建表）。
5. 先备份 `/etc/nginx` 到 `/var/backups/nginx/`；装一个只有 80 端口的临时站点（ACME 路径 + 跳转），`nginx -t` 后 reload。
6. 证书（和其他站点一样走 webroot），每个域名单独一张：`certbot certonly --webroot -w /var/www/letsencrypt -d <域名>`；再装 `deploy/nginx-lingshu-common.inc` 和 `deploy/nginx-lingshu.conf`，`nginx -t` 通过后 reload。
7. 建管理员：`sudo -u lingshu env $(cat /etc/lingshu/env | xargs) /opt/lingshu/lingshu admin create -username <名字>`。
8. 渠道、模型、价格：本地 `scripts/seed-dev.py` 生成 SQL，经 ssh 灌进线上库。

## 数据库备份

- `lingshu-backup.timer` 每天 04:00（随机延后 10 分钟内）执行 `/usr/local/bin/lingshu-backup`：`pg_dump -Fc` 到 `/var/backups/lingshu/`，保留最近 14 份。脚本在 `deploy/backup/`。
- `deploy/deploy.sh` 每次发布前也会先备份一次（新版本启动时会执行数据库迁移）。
- 2026-10-20 恢复演练：最新备份恢复到临时库，9 张表行数和余额总额都与线上一致。
- 恢复：`sudo -u postgres pg_restore -d lingshu --clean --no-owner /var/backups/lingshu/<文件>`（先停 `lingshu.service`）。
- 已知不足：备份和数据库在同一台服务器上，防不了整机故障；异地备份待接入。

## 服务日志

```bash
ssh tencent-server 'sudo journalctl -u lingshu --since today | grep -E "登录失败|登录被锁定"'
```

登录失败、被锁定会记录用户名和访问者 IP（Nginx 转来的 `X-Real-IP`）。

## 回滚

```bash
ssh tencent-server 'cd /opt/lingshu && sudo mv lingshu.prev lingshu && sudo systemctl restart lingshu'
```

站点配置回滚：删除 `/etc/nginx/conf.d/lingshu.conf`，`nginx -t && systemctl reload nginx`；部署前的 Nginx 备份在 `/var/backups/nginx/`。
