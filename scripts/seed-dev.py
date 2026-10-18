"""把 .env 里的上游 key 写进本地开发库：一个 default 分组、6 个渠道、7 个模型。

用法（在项目根目录，先 make db-up）：
    set -a; source <(sed 's/\r$//' .env); set +a
    python3 -I scripts/seed-dev.py | docker exec -i lingshu-postgres psql -U lingshu -q -v ON_ERROR_STOP=1

可以重复执行。前端做好之前，渠道和模型靠它配置。价格在阶段 2 再填。
"""
import os

E = os.environ

# (渠道名, 模板, key 变量, chat 地址, messages 地址, responses 地址, [(标准名, 上游名)])
CHANNELS = [
    ("deepseek", "deepseek", "DEEPSEEK_API_KEY", E["DEEPSEEK_BASE_URL"], "", E["DEEPSEEK_BASE_URL"],
     [("deepseek-v4.1-flash", "deepseek-flash")]),
    ("zhipu", "zhipu", "ZHIPU_API_KEY", E["ZHIPU_BASE_URL"], "", "", [("GLM-5.3", "")]),
    ("mimo", "mimo", "MIMO_API_KEY", E["MIMO_BASE_URL"], "", "", [("mimo-v2.6-pro", "")]),
    ("moonshot", "moonshot", "MOONSHOT_API_KEY", E["MOONSHOT_BASE_URL"], "", "", [("kimi-k3", "")]),
    ("stepfun", "stepfun", "STEPFUN_API_KEY", E["STEPFUN_BASE_URL"], "", "", [("step-5-preview", "")]),
    ("dashscope", "dashscope", "DASHSCOPE_API_KEY", "", E["DASHSCOPE_BASE_URL"], "",
     [("qwen3.8-max", ""), ("qwen3.8-flash", "")]),
]


def q(s):
    return "'" + s.replace("'", "''") + "'"


print("BEGIN;")
print("INSERT INTO groups (name) VALUES ('default') ON CONFLICT (name) DO NOTHING;")
for name, template, key_env, chat, messages, responses, models in CHANNELS:
    print(f"""INSERT INTO channels (name, template, api_key, base_url_chat, base_url_messages, base_url_responses)
VALUES ({q(name)}, {q(template)}, {q(E[key_env])}, {q(chat)}, {q(messages)}, {q(responses)})
ON CONFLICT (name) DO UPDATE SET api_key = EXCLUDED.api_key, base_url_chat = EXCLUDED.base_url_chat,
  base_url_messages = EXCLUDED.base_url_messages, base_url_responses = EXCLUDED.base_url_responses;""")
    print(f"""INSERT INTO channel_groups SELECT c.id, g.id FROM channels c, groups g
WHERE c.name = {q(name)} AND g.name = 'default' ON CONFLICT DO NOTHING;""")
    for std, upstream in models:
        print(f"INSERT INTO models (name) VALUES ({q(std)}) ON CONFLICT (name) DO NOTHING;")
        print(f"""INSERT INTO channel_models SELECT c.id, m.id, {q(upstream)} FROM channels c, models m
WHERE c.name = {q(name)} AND m.name = {q(std)}
ON CONFLICT (channel_id, model_id) DO UPDATE SET upstream_name = EXCLUDED.upstream_name;""")
print("COMMIT;")
