-- 渠道级代理：空表示直连。例如 OpenRouter 在中国被按地区拒绝，经本机出境代理 socks5://127.0.0.1:10808（DMIT 洛杉矶）出去。
ALTER TABLE channels ADD COLUMN proxy_url TEXT NOT NULL DEFAULT '';
