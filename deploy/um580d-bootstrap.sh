#!/usr/bin/env bash
# Prepare an isolated Lingshu installation on the UM580D host.
# Run as root after copying lingshu, its unit, and the Nginx config to the stage directory.
set -euo pipefail

stage_dir=/home/aumif/lingshu-stage
service_user=lingshu
app_dir=/opt/lingshu
env_dir=/etc/lingshu
nginx_conf=/etc/nginx/conf.d/lingshu-um580d.conf
marker=/var/lib/lingshu-um580d/bootstrap.done

if [ "$(id -u)" -ne 0 ]; then
	echo "Run this script with sudo." >&2
	exit 1
fi

if [ -e "$marker" ] || id "$service_user" >/dev/null 2>&1 || [ -e "$app_dir" ] || [ -e "$env_dir" ] || [ -e "$nginx_conf" ]; then
	echo "A Lingshu UM580D installation already exists; refusing to overwrite it." >&2
	exit 1
fi

if [ ! -s "$stage_dir/lingshu" ] || [ ! -s "$stage_dir/lingshu.service" ] || [ ! -s "$stage_dir/nginx-lingshu-um580d.conf" ]; then
	echo "Missing staged Lingshu files in $stage_dir." >&2
	exit 1
fi

if getent passwd postgres >/dev/null 2>&1 || command -v psql >/dev/null 2>&1 || command -v nginx >/dev/null 2>&1; then
	echo "PostgreSQL or Nginx already exists; inspect the host before continuing." >&2
	exit 1
fi

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y nginx postgresql

systemctl stop nginx || true
if [ -L /etc/nginx/sites-enabled/default ]; then
	unlink /etc/nginx/sites-enabled/default
fi

useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin "$service_user"
install -d -o root -g root -m 0755 "$app_dir"
install -d -o root -g "$service_user" -m 0750 "$env_dir"
install -o root -g root -m 0755 "$stage_dir/lingshu" "$app_dir/lingshu"

systemctl enable --now postgresql

db_password=$(openssl rand -hex 32)
runuser -u postgres -- psql -v ON_ERROR_STOP=1 <<SQL
CREATE ROLE lingshu LOGIN PASSWORD '$db_password';
CREATE DATABASE lingshu OWNER lingshu;
SQL

install -o root -g "$service_user" -m 0640 /dev/null "$env_dir/env"
printf 'LINGSHU_LISTEN=127.0.0.1:8300\nLINGSHU_DATABASE_URL=postgres://lingshu:%s@127.0.0.1:5432/lingshu?sslmode=disable\n' \
	"$db_password" >"$env_dir/env"
unset db_password

install -o root -g root -m 0644 "$stage_dir/lingshu.service" /etc/systemd/system/lingshu.service
install -o root -g root -m 0644 "$stage_dir/nginx-lingshu-um580d.conf" "$nginx_conf"
nginx -t

systemctl enable --now nginx
systemctl daemon-reload
install -d -o root -g root -m 0700 "$(dirname "$marker")"
date -Is >"$marker"

echo "UM580D prerequisites installed. Restore the production database before starting lingshu.service."
