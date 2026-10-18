"""从真实上游抓取响应原文，存为 usage 解析的测试样本。

用法（在项目根目录）：
    set -a; source <(sed 's/\r$//' .env); set +a
    python3 -I scripts/capture-fixtures.py

只保存响应内容，不保存请求头和 key。
"""
import json
import os
import pathlib
import urllib.request

OUT = pathlib.Path("internal/relay/usage/testdata")
E = os.environ
PROMPT = "Count 1 to 3."

CASES = [
    # (文件名前缀, 协议, base 环境变量, key 环境变量, 上游模型)
    ("deepseek_chat", "chat", "DEEPSEEK_BASE_URL", "DEEPSEEK_API_KEY", "deepseek-flash"),
    ("deepseek_responses", "responses", "DEEPSEEK_BASE_URL", "DEEPSEEK_API_KEY", "deepseek-flash"),
    ("zhipu_chat", "chat", "ZHIPU_BASE_URL", "ZHIPU_API_KEY", "glm-5.3"),
    ("mimo_chat", "chat", "MIMO_BASE_URL", "MIMO_API_KEY", "mimo-v2.6-pro"),
    ("moonshot_chat", "chat", "MOONSHOT_BASE_URL", "MOONSHOT_API_KEY", "kimi-k3"),
    ("stepfun_chat", "chat", "STEPFUN_BASE_URL", "STEPFUN_API_KEY", "step-5-preview"),
    ("dashscope_messages", "messages", "DASHSCOPE_BASE_URL", "DASHSCOPE_API_KEY", "qwen3.8-max"),
]


def request(proto, base, key, model, stream):
    if proto == "chat":
        url = base + "/chat/completions"
        body = {"model": model, "messages": [{"role": "user", "content": PROMPT}], "max_tokens": 400}
        if stream:
            body["stream_options"] = {"include_usage": True}
        headers = {"Authorization": "Bearer " + key}
    elif proto == "messages":
        url = base + "/v1/messages"
        body = {"model": model, "max_tokens": 400, "messages": [{"role": "user", "content": PROMPT}]}
        headers = {"x-api-key": key, "anthropic-version": "2023-06-01"}
    else:
        url = base + "/responses"
        body = {"model": model, "input": PROMPT}
        headers = {"Authorization": "Bearer " + key}
    body["stream"] = stream
    req = urllib.request.Request(url, data=json.dumps(body).encode(), headers={**headers, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return r.read()


for name, proto, base_env, key_env, model in CASES:
    for stream in (False, True):
        path = OUT / f"{name}.{'sse' if stream else 'json'}"
        data = request(proto, E[base_env], E[key_env], model, stream)
        path.write_bytes(data)
        print(f"{path} {len(data)} bytes")
