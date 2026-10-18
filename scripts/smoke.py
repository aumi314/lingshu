"""冒烟测试：通过灵枢调用每个模型（流式 + 非流式），核对：
- 客户端收到的 usage 和日志记录一致；
- 扣费 = 用量 × 日志里的价格快照 × 倍率；
- 用户余额正好减少了全部扣费之和。

用法（在项目根目录，灵枢已启动）：
    LINGSHU_KEY=sk-ls-... python3 -I scripts/smoke.py
可选：LINGSHU_URL（默认 http://127.0.0.1:8300）
可选：LINGSHU_PSQL，读日志用的 psql 命令（默认本地开发库容器），
      比如线上：LINGSHU_PSQL="ssh tencent-server sudo -u postgres psql -d lingshu"
"""
import json
import os
import shlex
from decimal import Decimal
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


PSQL = shlex.split(os.environ.get("LINGSHU_PSQL", "docker exec lingshu-postgres psql -U lingshu"))


def sql(query):
    if PSQL[0] == "ssh":  # 经过 ssh 时，远端 shell 会再解析一次参数
        return subprocess.run(PSQL + ["-tAF", "'|'", "-c", shlex.quote(query)], capture_output=True, text=True).stdout.strip()
    return subprocess.run(PSQL + ["-tAF", "|", "-c", query], capture_output=True, text=True).stdout.strip()


def log_of(rid):
    for _ in range(50):
        out = sql(f"SELECT status, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, first_byte_ms, "
                  f"input_price, output_price, cache_read_price, cache_write_price, group_ratio, cost "
                  f"FROM request_logs WHERE request_id = '{rid}'")
        if out:
            f = out.split("|")
            return {"status": f[0], "input": int(f[1]), "cache_read": int(f[2]), "cache_write": int(f[3]),
                    "output": int(f[4]), "first_byte_ms": int(f[5]), "prices": [Decimal(x) for x in f[6:10]],
                    "ratio": Decimal(f[10]), "cost": Decimal(f[11])}
        time.sleep(0.1)
    return None


def expected_cost(log):
    ip, op, crp, cwp = log["prices"]
    raw = log["input"] * ip + log["output"] * op + log["cache_read"] * crp + log["cache_write"] * cwp
    return (raw / Decimal(1_000_000) * log["ratio"]).quantize(Decimal("1e-10"))


def balance():
    return Decimal(sql(f"SELECT u.balance FROM users u JOIN api_keys k ON k.user_id = u.id "
                       f"WHERE k.key_hash = encode(sha256('{KEY}'::bytea), 'hex')"))


failed = 0
total_cost = Decimal(0)
start_balance = balance()
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
        if log:
            if log["cost"] != expected_cost(log) or log["cost"] <= 0:
                problems.append(f"扣费 {log['cost']} ≠ 用量×单价×倍率 {expected_cost(log)}")
            total_cost += log["cost"]
        status = "FAIL" if problems else "ok  "
        failed += bool(problems)
        detail = (f"in={log['input']} cache={log['cache_read']} out={log['output']} ${log['cost']:.6f} "
                  f"首字节={log['first_byte_ms']}ms 总={secs:.1f}s") if log else ""
        print(f"{status} {name} {detail} {'; '.join(problems)}")

spent = start_balance - balance()
print(f"\n余额减少 ${spent}，日志扣费合计 ${total_cost}：{'一致' if spent == total_cost else '不一致！'}")
failed += spent != total_cost
print(f"{'全部通过' if not failed else f'{failed} 项失败'}")
sys.exit(1 if failed else 0)
