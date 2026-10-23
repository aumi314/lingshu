# UM580D 迁移记录（2026-10-22）

## 结果

`lingshu.piqode.com` 已切到 UM580D，并通过 Cloudflare Tunnel 的 `um580d-home` 转发到 UM 本机 Nginx。Cloudflare DNS 是指向 tunnel 的代理 CNAME；Tunnel 只新增 Lingshu HTTP 路由，原 SSH 路由保留。临时 staging 主机名和 tunnel 路由已删除。

腾讯云 Lingshu 服务已停止，数据库保留为切换前回滚基线。UM 是当前唯一接受请求的生产端。回滚前先把 UM 最新数据库同步到腾讯云，不能让两端同时写入。

## UM 服务与数据

- Ubuntu 26.04.1；Lingshu、Nginx、PostgreSQL、cloudflared 均运行中。Lingshu 和每日备份 timer 均已设为开机启动。
- Lingshu 监听 `127.0.0.1:8300`，Nginx 监听 `127.0.0.1:8080`，PostgreSQL 监听 `127.0.0.1:5432`，均未暴露公网监听端口。
- 从腾讯云停写后制作最终加密数据副本并恢复至 UM；此前核对的 15 张公开表行数一致。UM 最新备份位于 `/var/backups/lingshu`，权限为 `0600`。
- 备份归档已用独立临时数据库完成恢复演练，并核对逐表行数。敏感环境文件权限为 `0640 root:lingshu`。

## 验收

- UM 本机 `/healthz` 直连和经过 Nginx 均返回 `ok`。
- 经 UM SOCKS 出口访问 OpenRouter Models API 返回 HTTP 200。
- 生产域名经 Cloudflare HTTPS `/healthz` 返回 HTTP 200。
- 生产域名真实 Chat Completions SSE 请求返回 HTTP 200，模型 `deepseek-v4.1-flash`，收到 10 个事件并正常收到 `[DONE]`；首事件约 2.23 秒，事件最大间隔 339 毫秒。临时 API key 与测试日志已由脚本清理。
- Cloudflare Tunnel 路由现在仅保留 `ssh-um580d.piqode.com` 和 `lingshu.piqode.com`；DNS 中已删除 staging 记录。

## 部署文件

- `deploy/um580d-bootstrap.sh`：UM 初始化与部署脚本。
- `deploy/nginx-lingshu-um580d.conf`：UM Nginx 配置。

迁移相关脚本与本记录纳入项目 Git 管理。2026-10-23 新增 DMIT 独立副本，UM 仍是团队原入口；两端统一应用版本，详见 `docs/deploy.md`。原有 `docs/design/C1.svg` 至 `C6.svg` 保持未改动。