# 灵枢两个域名共用的站点配置。安装位置 /etc/nginx/lingshu-common.inc（不在 conf.d 里，避免被单独加载）

include /etc/letsencrypt/options-ssl-nginx.conf;
ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

add_header Strict-Transport-Security "max-age=31536000" always;
add_header X-Content-Type-Options "nosniff" always;
add_header X-Frame-Options "DENY" always;

# 带图片的长上下文请求可能很大，和网关的上限一致
client_max_body_size 32m;

gzip on;
gzip_types text/css application/javascript application/json image/svg+xml;
gzip_min_length 1024;

location / {
    proxy_pass http://127.0.0.1:8300;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # 流式输出：收到一块转一块，不攒包；推理模型可能很久才出第一个字
    proxy_buffering off;
    proxy_cache off;
    proxy_request_buffering off;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;
}
