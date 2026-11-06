-- GPT 的 Fast（OpenAI service_tier = priority / fast）：按模型开放，扣费 = 价格规则选出的单价（含长上下文）× fast_multiplier。
-- NULL 表示不开放：网关转发前去掉请求里的高速档位，按标准处理和计费。只对 chat / responses 协议生效。
ALTER TABLE models ADD COLUMN fast_multiplier NUMERIC(10,4);

-- service_tier：这次请求的档位结果。'' 标准；fast 按 Fast 计费；fast_downgraded 要了 Fast、上游按标准处理（按标准价）；
-- unavailable 要了 Fast / Ultrafast 但模型未开放，网关已去掉，按标准处理。
ALTER TABLE request_logs ADD COLUMN service_tier TEXT NOT NULL DEFAULT '';
