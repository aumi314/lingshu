#!/usr/bin/env bash
# 灵枢数据库每日备份：pg_dump 自定义格式（自带压缩，可单表恢复），保留最近 14 份。
# 安装位置 /usr/local/bin/lingshu-backup，由 lingshu-backup.timer 每天触发。
set -euo pipefail
DIR=/var/backups/lingshu
KEEP=14
install -d -m 700 -o postgres -g postgres "$DIR"
OUT="$DIR/lingshu-$(date +%Y%m%d-%H%M%S).dump"
sudo -u postgres pg_dump -Fc -d lingshu -f "$OUT.part"
mv "$OUT.part" "$OUT"
chmod 600 "$OUT"
# 只留最新的 KEEP 份
ls -1t "$DIR"/lingshu-*.dump | tail -n +$((KEEP + 1)) | xargs -r rm -f
echo "备份完成: $OUT ($(du -h "$OUT" | cut -f1))，现有 $(ls -1 "$DIR"/lingshu-*.dump | wc -l) 份"
