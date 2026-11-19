# GPT 渠道手动检测（完整 Codex 伪装，复现 internal/relay/probe_responses.go；2026-11-19 测三家 GPT 渠道用，结论见 docs/research/model-probe.md）。
# 在 DMIT 上跑：key 从数据库读进内存，不落盘、不打印；伪装模板读 /etc/lingshu/probe-disguise.json。
# 每个请求完成即落盘到 ./out/<tag>.json，重跑只补缺。用法：python3 gpt_channel_check.py <channel_key_id> <responses_base_url> <tag>
# 测：注入（3 个最短请求）、推理（递推 / 网格难题 medium 8 + high 4）、缓存（同会话重复发一段新内容）、约 3.5 万 token 大海捞针。
# 注意：aresai 这类中转会拦 "Reply with exactly: ok"，注入项会 403。
import json, os, random, sys, time, uuid, threading, subprocess, urllib.request, urllib.error, importlib.util
import concurrent.futures as cf
KID, BASE, TAG = sys.argv[1], sys.argv[2].rstrip("/"), sys.argv[3]
MODEL = "gpt-6.1-sol"; TPL_BASE = 9845
K = subprocess.run(["docker", "exec", "-i", "lingshu-postgres", "psql", "-U", "lingshu", "-d", "lingshu", "-tAc",
                    f"SELECT api_key FROM channel_keys WHERE id={int(KID)}"], capture_output=True, text=True).stdout.strip()
assert K, "key 为空"
D = json.load(open("/etc/lingshu/probe-disguise.json"))["codex"]
spec = importlib.util.spec_from_file_location("probe", os.path.join(os.path.dirname(os.path.abspath(__file__)), "probe.py")); P = importlib.util.module_from_spec(spec); spec.loader.exec_module(P)
INSTALL = str(uuid.uuid4()); NT = "\n\nAnswer directly; do not use any tools."
OUT = f"out/{TAG}.json"; os.makedirs(os.path.dirname(OUT), exist_ok=True)
R = json.load(open(OUT)) if os.path.exists(OUT) else {}
lock = threading.Lock(); stop = threading.Event()
def save():
    with lock:
        json.dump(R, open(OUT + ".tmp", "w"), ensure_ascii=False, indent=1); os.replace(OUT + ".tmp", OUT)

def send(prompt, effort=None, session=None):
    s = session or str(uuid.uuid4()); now = time.time(); turn = str(uuid.uuid4())
    items = json.loads(json.dumps(D["input"]).replace("{{DATE}}", time.strftime("%Y-%m-%d")))
    items.append({"type": "message", "role": "user", "content": [{"type": "input_text", "text": prompt + NT}]})
    meta = {"installation_id": INSTALL, "session_id": s, "thread_id": s, "agent_name": "/root", "turn_id": turn, "window_id": s + ":0", "window_number": 0,
            "context_window_id": str(uuid.uuid4()), "request_kind": "turn", "root_turn_id": turn, "thread_source": "user", "turn_trigger": "exec",
            "sandbox": "seccomp", "sandbox_mode": "read-only", "auto_review_enabled": False, "node_repl_auto_review_required": True,
            "node_repl_disabled": False, "turn_started_at_unix_ms": int(now * 1000), "analytics_enabled": True, "model": MODEL}
    if effort: meta["reasoning_effort"] = effort
    tm = json.dumps(meta, separators=(",", ":"))
    body = {"model": MODEL, "stream": True, "store": False, "include": ["reasoning.encrypted_content"], "prompt_cache_key": s, **D.get("body", {}), "input": items,
            "client_metadata": {"x-codex-window-id": s + ":0", "turn_id": turn, "x-codex-turn-metadata": tm, "thread_id": s, "x-codex-installation-id": INSTALL, "root_turn_id": turn, "session_id": s}}
    rs = {}
    if effort: rs["effort"] = effort
    if D.get("reasoning_context"): rs["context"] = D["reasoning_context"]
    if rs: body["reasoning"] = rs
    hdr = {**D["headers"], "Content-Type": "application/json", "Accept": "text/event-stream", "X-Codex-Turn-Metadata": tm, "X-Codex-Window-Id": s + ":0",
           "Session-Id": s, "Thread-Id": s, "X-Client-Request-Id": s, "Authorization": "Bearer " + K}
    for attempt in range(3):
        t0 = time.time(); out = {"text": "", "ttft": None, "attempt": attempt}
        try:
            with urllib.request.urlopen(urllib.request.Request(BASE + "/responses", data=json.dumps(body).encode(), headers=hdr), timeout=600) as r:
                out["status"] = r.status
                for raw in r:
                    line = raw.decode("utf-8", "replace").strip()
                    if not line.startswith("data:") or line == "data: [DONE]": continue
                    ev = json.loads(line[5:]); typ = ev.get("type")
                    if out["ttft"] is None and typ and "delta" in typ: out["ttft"] = round(time.time() - t0, 2)
                    if typ == "response.output_text.delta": out["text"] += ev["delta"]
                    elif typ in ("response.completed", "response.incomplete", "response.failed"):
                        resp = ev["response"]; u = resp.get("usage") or {}
                        out.update(final=typ, model=resp.get("model"), tier=resp.get("service_tier"), input=u.get("input_tokens"),
                                   cached=(u.get("input_tokens_details") or {}).get("cached_tokens"), output=u.get("output_tokens"),
                                   reasoning=(u.get("output_tokens_details") or {}).get("reasoning_tokens"), err=resp.get("error"))
                    elif typ == "error": out["err"] = ev
        except urllib.error.HTTPError as e:
            out["status"] = e.code; out["err"] = e.read()[:300].decode("utf-8", "replace").replace(K, "sk-***")
        except Exception as e:
            out["status"] = 0; out["err"] = str(e)[:200]
        out["secs"] = round(time.time() - t0, 2)
        if out["status"] in (429, 502, 503, 504, 524, 529, 0): time.sleep(5 * (attempt + 1)); continue
        return out
    return out

def job(name, prompt, expected_in, effort=None, session=None, extra=None):
    if name in R and R[name].get("status") == 200 or stop.is_set(): return
    r = send(prompt, effort, session); r.update(extra or {}); r["effort"] = effort
    if "answer" in r: r["got"] = P.last_int(r["text"].replace(",", "")); r["correct"] = r["got"] == r["answer"]
    with lock: R[name] = r
    save()
    inp = r.get("input") or 0
    print(TAG, name, r.get("status"), "输入", inp, "缓存", r.get("cached"), "推理", r.get("reasoning"), "用时", r["secs"], "对" if r.get("correct") else ("错" if "answer" in r else ""), flush=True)
    if inp > 3 * expected_in and inp > expected_in + 10000:
        print(TAG, "计费异常，停止：", name, inp, "预期约", expected_in, flush=True); stop.set()

rng = random.Random(int(KID) * 7919)  # 每个渠道固定种子：重跑时题目不变
# 1 注入
for i in range(3): job(f"inject{i}", "Reply with exactly: ok", TPL_BASE + 30)
# 2 推理
jobs = []
for i in range(4):
    for kind in ("recurrence_hard", "lattice_hard"):
        q, a = P.make_problem(kind, rng); jobs.append((f"reason-medium-{kind}-{i}", q, "medium", a, kind))
for i in range(2):
    for kind in ("recurrence_hard", "lattice_hard"):
        q, a = P.make_problem(kind, rng); jobs.append((f"reason-high-{kind}-{i}", q, "high", a, kind))
with cf.ThreadPoolExecutor(2) as ex:
    list(ex.map(lambda j: job(j[0], j[1], TPL_BASE + 200, j[2], extra={"answer": j[3], "kind": j[4], "question": j[1]}), jobs))
# 3 缓存：没见过的约 2 万 token 前缀，同会话连发两次
filler = " ".join(" ".join(rng.choice(P.WORDS) for _ in range(9)).capitalize() + "." for _ in range(20000 // 24))
sess = str(uuid.uuid4())
for i in range(2): job(f"cache{i}", filler + "\n\nReply with exactly: ok", TPL_BASE + 25000, session=sess)
# 4 大海捞针（最后发，约 8 万 token）
codes = ["".join(rng.choice("ABCDEFGHJKLMNPQRSTUVWXYZ23456789") for _ in range(8)) for _ in range(3)]
sents = [" ".join(rng.choice(P.WORDS) for _ in range(9)).capitalize() + "." for _ in range(80000 // 24)]
for frac, c, k in zip((0.1, 0.5, 0.9), codes, (1, 2, 3)): sents.insert(int(len(sents) * frac), f"Important: secret code number {k} is {c}.")
job("needle", " ".join(sents) + "\n\nThe text above contains three secret codes (numbers 1, 2 and 3). List them in order, one per line, nothing else.",
    TPL_BASE + 100000, extra={"codes": codes})
if "needle" in R: R["needle"]["found"] = [c in R["needle"]["text"] for c in codes]; save()
print(TAG, "完成", flush=True)
