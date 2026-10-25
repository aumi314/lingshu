-- 渠道级代理：空表示直连；填写地址后，该渠道请求统一经过所配置的代理。
ALTER TABLE channels ADD COLUMN proxy_url TEXT NOT NULL DEFAULT '';
