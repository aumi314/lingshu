#!/usr/bin/env bash
# 发布新版本到腾讯云：本地编译 → 上传 → 备份数据库 → 保留上一版 → 重启 → 健康检查，失败自动回滚程序。
# 用法（项目根目录）：bash deploy/deploy.sh
# 首次部署的一次性准备见 docs/deploy.md。
set -euo pipefail

HOST="${LINGSHU_HOST:-tencent-server}"

cd "$(dirname "$0")/.."
make build
scp -q bin/lingshu "$HOST:/tmp/lingshu.new"

ssh "$HOST" 'bash -s' <<'REMOTE'
set -euo pipefail
# 新版本启动时会自动执行数据库迁移，先备份一次，出问题能退回发布前的数据
sudo /usr/local/bin/lingshu-backup
cd /opt/lingshu
sudo install -o root -g root -m 755 /tmp/lingshu.new lingshu.new
rm -f /tmp/lingshu.new
[ -f lingshu ] && sudo cp -p lingshu lingshu.prev
sudo mv lingshu.new lingshu
sudo systemctl restart lingshu

for i in $(seq 1 20); do
  if curl -fsS -m 2 http://127.0.0.1:8300/healthz >/dev/null 2>&1; then
    echo "发布成功：$(sudo systemctl show -p ActiveEnterTimestamp --value lingshu)"
    exit 0
  fi
  sleep 1
done

echo "健康检查失败，回滚到上一版" >&2
sudo journalctl -u lingshu -n 30 --no-pager >&2 || true
if [ -f lingshu.prev ]; then
  sudo mv lingshu.prev lingshu
  sudo systemctl restart lingshu
fi
exit 1
REMOTE
