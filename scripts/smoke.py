"""冒烟测试：通过灵枢调用每个模型（流式 + 非流式），核对客户端收到的 usage 和日志记录是否一致。

用法（在项目根目录，灵枢已启动）：
    LINGSHU_KEY=sk-ls-... python3 -I scripts/smoke.py
可选：LINGSHU_URL（默认 http://127.0.0.1:8300）
日志从本地开发库读取（docker 容器 lingshu-postgres）。
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

URL = os.environ.get("LINGSHU_URL", "http://127.0.0.1:8300")
KEY = os.environ["LINGSHU_KEY"]
PROMPT = "Count 1 to 3."

CASES = [
    ("deepseek-v4.1-flash", "chat"),
    ("deepseek-v4.1-flash", "responses"),
    ("GLM-5.3", "chat"),
    ("mimo-v2.6-pro", "chat"),
    ("kimi-k3", "chat"),
    ("step-5-preview", "chat"),
    ("qwen3.8-max", "messages"),
    ("qwen3.8-flash", "messages"),
]
PATHS = {"chat": "/v1/chat/completions", "messages": "/v1/messages", "responses": "/v1/responses"}


def body_for(proto, model, stream, include_usage):
    if proto == "chat":
        b = {"model": model, "messages": [{"role": "user", "content": PROMPT}], "max_tokens": 400}
        if include_usage:
            b["stream_options"] = {"include_usage": True}
    elif proto == "messages":
        b = {"model": model, "max_tokens": 400, "messages": [{"role": "user", "content": PROMPT}]}
    else:
        b = {"model": model, "input": PROMPT}
    b["stream"] = stream
    return b


def normalize(proto, u):
    """和 internal/relay/usage 同一套口径，独立实现一遍用来交叉核对。"""
    if proto == "chat":
        pd = u.get("prompt_tokens_details") or {}
        cr, cw = pd.get("cached_tokens") or 0, pd.get("cache_write_tokens") or 0
        return {"input": max(u["prompt_tokens"] - cr - cw, 0), "cache_read": cr, "output": u["completion_tokens"]}
    if proto == "messages":
        return {"input": u.get("input_tokens", 0), "cache_read": u.get("cache_read_input_tokens") or 0,
                "output": u.get("output_tokens", 0)}
    cr = (u.get("input_tokens_details") or {}).get("cached_tokens") or 0
    return {"input": u["input_tokens"] - cr, "cache_read": cr, "output": u["output_tokens"]}


def call(proto, model, stream, include_usage=False):
    body = json.dumps(body_for(proto, model, stream, include_usage)).encode()
    req = urllib.request.Request(URL + PATHS[proto], data=body, headers={
        "Authorization": "Bearer " + KEY, "Content-Type": "application/json", "User-Agent": "lingshu-smoke/1"})
    t0 = time.time()
    with urllib.request.urlopen(req, timeout=180) as r:
        rid = r.headers["X-Lingshu-Request-Id"]
        client_usage, usage_only_chunks, text = None, 0, ""
        if not stream:
            d = json.loads(r.read())
            client_usage = d.get("usage")
        else:
            for line in r:
                line = line.decode().strip()
                if not line.startswith("data:"):
                    continue
                p = line[5:].strip()
                if p == "[DONE]":
                    continue
                e = json.loads(p)
                u = e.get("usage") or (e.get("message") or {}).get("usage") or (e.get("response") or {}).get("usage")
                if u:
                    client_usage = {**(client_usage or {}), **u}  # 各字段取最后出现的值
                if proto == "chat" and e.get("choices") == [] and e.get("usage"):
                    usage_only_chunks += 1
    return rid, client_usage, usage_only_chunks, time.time() - t0


def log_of(rid):
    for _ in range(50):
        out = subprocess.run(["docker", "exec", "lingshu-postgres", "psql", "-U", "lingshu", "-tAF", "|", "-c",
                              f"SELECT status, input_tokens, cache_read_tokens, output_tokens, upstream_model, first_byte_ms "
                              f"FROM request_logs WHERE request_id = '{rid}'"], capture_output=True, text=True).stdout.strip()
        if out:
            s, i, c, o, um, fb = out.split("|")
            return {"status": s, "input": int(i), "cache_read": int(c), "output": int(o), "upstream_model": um, "first_byte_ms": int(fb)}
        time.sleep(0.1)
    return None


failed = 0
for model, proto in CASES:
    variants = [("非流式", False, False), ("流式", True, False)]
    if proto == "chat":
        variants.append(("流式+客户端要usage", True, True))
    for label, stream, inc in variants:
        name = f"{model:<20} {proto:<9} {label}"
        try:
            rid, cu, uo, secs = call(proto, model, stream, inc)
        except urllib.error.HTTPError as e:
            print(f"FAIL {name}: HTTP {e.code} {e.read()[:200]}")
            failed += 1
            continue
        log = log_of(rid)
        problems = []
        if not log or log["status"] != "success":
            problems.append(f"日志状态 {log and log['status']}")
        if cu is not None:
            want = normalize(proto, cu)
            got = {k: log[k] for k in want} if log else None
            if got != want:
                problems.append(f"usage 不一致：客户端 {want}，日志 {got}")
        elif proto == "chat" and stream and not inc:
            if uo:
                problems.append("客户端没要 usage，却收到了 usage 块")
            if log and log["input"] == 0:
                problems.append("网关没拿到 usage")
        else:
            problems.append("客户端没收到 usage")
        status = "FAIL" if problems else "ok  "
        failed += bool(problems)
        detail = f"in={log['input']} out={log['output']} 首字节={log['first_byte_ms']}ms 总={secs:.1f}s" if log else ""
        print(f"{status} {name} {detail} {'; '.join(problems)}")

print(f"\n{'全部通过' if not failed else f'{failed} 项失败'}")
sys.exit(1 if failed else 0)
