#!/usr/bin/env python3
"""模型检测（试验版）：按 suite.json 的检测题检测一个 OpenAI 兼容地址上的模型，结果写成 JSON，和官方样本对比。

  运行：python3 scripts/probe/probe.py run --base http://127.0.0.1:3425/v1 --key magpie --model codex/gpt-6.1-sol \\
          --round 1 --out scripts/probe/baselines [--only tokens,fingerprint,math,needle,pelican] \\
          [--pelican-dir 目录 --edge msedge.exe 路径 --judge-model codex/gpt-6.1-sol]
  对比：python3 scripts/probe/probe.py compare 结果1.json 结果2.json ...

检测项和口径见 docs/decisions.md「模型检测」。只用标准库；密钥只从命令行传入，不写进结果。
所有请求都用流式：经 Cloudflare 的非流式请求超过 100 秒会被 524 断开。
"""
import argparse
import base64
import concurrent.futures as cf
import json
import os
import random
import re
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request
from itertools import combinations

HERE = os.path.dirname(os.path.abspath(__file__))
SUITE = json.load(open(os.path.join(HERE, "suite.json"), encoding="utf-8"))


# ---- 请求 ----

def call(args, content, effort=None, model=None, base=None, retries=3):
    """流式调用 chat completions，返回文本、用量和耗时。content 可以是字符串或多段内容（带图片）。"""
    body = {"model": model or args.model, "messages": [{"role": "user", "content": content}],
            "stream": True, "stream_options": {"include_usage": True}}
    if effort:
        body["reasoning_effort"] = effort
    url = (base or args.base).rstrip("/") + "/chat/completions"
    err = ""
    for attempt in range(retries):
        req = urllib.request.Request(url, data=json.dumps(body).encode(),
                                     headers={"Authorization": "Bearer " + args.key, "Content-Type": "application/json"})
        start, first, parts, usage, meta, finish = time.time(), None, [], {}, {}, None
        try:
            with urllib.request.urlopen(req, timeout=1800) as r:
                for raw in r:
                    line = raw.decode("utf-8", "replace").strip()
                    if not line.startswith("data:") or line == "data: [DONE]":
                        continue
                    ev = json.loads(line[5:])
                    for k in ("id", "model", "service_tier", "system_fingerprint"):
                        if ev.get(k) is not None:
                            meta[k] = ev[k]
                    for ch in ev.get("choices") or []:
                        finish = ch.get("finish_reason") or finish
                        piece = (ch.get("delta") or {}).get("content") or ""
                        if piece and first is None:
                            first = time.time()
                        parts.append(piece)
                    if ev.get("usage"):
                        usage = ev["usage"]
            if finish is None and not usage:
                raise ConnectionError("流在正常结束前断开")  # 半截的回答不能用，重试
            return {
                "text": "".join(parts).strip(),
                "finish_reason": finish,
                "prompt_tokens": usage.get("prompt_tokens"),
                "cached_tokens": (usage.get("prompt_tokens_details") or {}).get("cached_tokens"),
                "completion_tokens": usage.get("completion_tokens"),
                "reasoning_tokens": (usage.get("completion_tokens_details") or {}).get("reasoning_tokens"),
                "first_token_s": round(first - start, 2) if first else None,
                "seconds": round(time.time() - start, 2),
                **meta,
            }
        except urllib.error.HTTPError as e:
            err = f"HTTP {e.code}: {e.read()[:300]!r}"
            if e.code in (400, 401, 403, 404):
                break
        except Exception as e:  # 网络中断、超时、半截的流
            err = f"{type(e).__name__}: {e}"[:300]
        time.sleep(5 * (attempt + 1))
    return {"error": err}


def parallel(args, fn, jobs):
    with cf.ThreadPoolExecutor(args.concurrency) as ex:
        return list(ex.map(fn, jobs))


# ---- 分词指纹：差值只取决于分词器，不受各家注入的系统提示词影响 ----

def probe_tokens(args):
    t = SUITE["tokens"]
    base = [call(args, t["suffix"].strip()).get("prompt_tokens") for _ in range(2)]
    anchor = call(args, t["suffix"]).get("prompt_tokens")
    deltas = {}
    for name, text in t["probes"].items():
        r = call(args, text + t["suffix"])
        deltas[name] = None if r.get("prompt_tokens") is None or anchor is None else r["prompt_tokens"] - anchor
    return {"base": base, "anchor": anchor, "deltas": deltas}


# ---- 单词指纹：几十个“随机说一个”的问题，每题多次，答案分布就是指纹 ----

def normalize(text):
    """去掉空白和标点、转小写；保留各种文字（希腊字母、汉字等）。清洗后为空（比如只回了表情符号）就保留原样。"""
    cleaned = re.sub(r"[\W_]", "", text.lower())
    return (cleaned or text.strip())[:40]


def has_cjk(text):
    return re.search(r"[一-鿿]", text) is not None


def probe_fingerprint(args):
    """答案按题目文本记录（第 2 版起；第 1 版按序号记，对比时按第 1 版题目顺序换回文本）。
    连续 10 次出错（多半是额度用完）就不再发剩下的请求。"""
    f = SUITE["fingerprint"]
    prompts = f["prompts"][args.fingerprint_from:]
    if args.fingerprint_limit:
        prompts = prompts[: args.fingerprint_limit]
    jobs = [p for p in prompts for _ in range(f["samples"])]
    state = {"streak": 0, "stopped": False}

    def one(p):
        if state["stopped"]:
            return p, None, "已停止"
        suffix = (" " + f["suffix_zh"]) if has_cjk(p) and f.get("suffix_zh") else f["suffix"]
        r = call(args, p + suffix, effort=f["effort"])
        if r.get("error"):
            state["streak"] += 1
            if state["streak"] >= 10:
                state["stopped"] = True
                print(f"连续出错，停止：{r['error'][:120]}", file=sys.stderr, flush=True)
            return p, None, r["error"]
        state["streak"] = 0
        return p, normalize(r.get("text", "")), None
    answers = {p: [] for p in prompts}
    errors = 0
    for p, a, e in parallel(args, one, jobs):
        if e:
            errors += 1
        else:
            answers[p].append(a)
    return {"samples": f["samples"], "errors": errors, "stopped": state["stopped"], "answers": answers}


# ---- 数学题：按种子生成，本地算标准答案；记正确率和推理 token（思考深度、稳定性） ----

def lattice(rng, w_range, blocks):
    w, h = rng.randint(*w_range), rng.randint(*w_range)
    blocked = set()
    while len(blocked) < blocks:
        blocked.add((rng.randint(1, w - 1), rng.randint(1, h - 1)))
    ways = [[0] * (h + 1) for _ in range(w + 1)]
    for i in range(w + 1):
        for j in range(h + 1):
            if (i, j) not in blocked:
                ways[i][j] = 1 if i == j == 0 else (ways[i - 1][j] if i else 0) + (ways[i][j - 1] if j else 0)
    pts = ", ".join(f"({x},{y})" for x, y in sorted(blocked))
    q = (f"On a grid, count the monotone lattice paths from (0,0) to ({w},{h}) using unit steps right (+1,0) or up (0,+1) "
         f"that never visit any of the points {pts}. Answer with the number only.")
    return q, ways[w][h]


def recurrence(rng, mods, n_range, linear):
    mod, n = rng.choice(mods), rng.randint(*n_range)
    a, b, c, x = rng.randint(2, 30), rng.randint(1, 30) if linear else 0, rng.randint(1, 30), rng.randint(2, 50)
    x1 = x
    for _ in range(n - 1):
        x = (a * x * x + b * x + c) % mod
    expr = f"{a}*x(k)^2 + {b}*x(k) + {c}" if linear else f"{a}*x(k)^2 + {c}"
    return f"Let x1 = {x1} and x(k+1) = ({expr}) mod {mod}. What is x{n}? Answer with the number only.", x


def make_problem(kind, rng):
    if kind == "recurrence":
        return recurrence(rng, [97, 101, 103, 107, 109], (60, 120), False)
    if kind == "recurrence_hard":
        return recurrence(rng, [997, 1009, 1013], (150, 300), True)
    if kind == "lattice":
        return lattice(rng, (6, 9), 3)
    if kind == "lattice_hard":
        return lattice(rng, (10, 12), 6)
    if kind == "subset_mod":
        n, k, m = rng.randint(18, 24), rng.randint(4, 5), rng.randint(7, 13)
        from itertools import combinations as comb
        count = sum(1 for s in comb(range(1, n + 1), k) if sum(s) % m == 0)
        return f"How many {k}-element subsets of {{1, 2, ..., {n}}} have a sum divisible by {m}? Answer with the number only.", count
    if kind == "digit_sum":
        t = rng.randint(20, 40)
        dp = [1] + [0] * t  # 7 位数字（含前导零）数字和为 t 的个数，再去掉 0
        for _ in range(7):
            dp = [sum(dp[s - d] for d in range(10) if s - d >= 0) for s in range(t + 1)]
        return f"How many integers n with 1 <= n < 10000000 have a digit sum equal to {t}? Answer with the number only.", dp[t]
    raise ValueError(kind)


def last_int(text):
    nums = re.findall(r"-?\d+", text.replace(",", ""))
    return int(nums[-1]) if nums else None


def probe_math(args):
    m = SUITE["math"]
    rng = random.Random(m["seed"])
    problems = [(p["kind"], p["tier"], *make_problem(p["kind"], rng)) for p in m["problems"]]
    jobs = [(i, q, a) for i, (_, _, q, a) in enumerate(problems) for _ in range(m["repeats"])]

    def one(job):
        i, q, a = job
        r = call(args, q, effort=m["effort"])
        got = last_int(r.get("text", ""))
        return {"problem": i, "correct": got == a, "got": r.get("text", "")[-40:], "reasoning_tokens": r.get("reasoning_tokens"),
                "seconds": r.get("seconds"), "error": r.get("error")}
    return {"problems": [{"kind": k, "tier": t, "question": q, "answer": a} for k, t, q, a in problems],
            "runs": parallel(args, one, jobs)}


# ---- 大海捞针：长上下文里藏三个编号，看有没有被截断 ----

WORDS = ("river mountain ledger copper violet engine harbor lantern meadow quartz signal timber orbit canvas falcon "
         "garden pillar rocket saddle thunder velvet window yellow zenith anchor bridge candle desert ember forest").split()


def probe_needle(args):
    n = SUITE["needle"]
    rng = random.Random(n["seed"])
    codes = ["".join(rng.choice("ABCDEFGHJKLMNPQRSTUVWXYZ23456789") for _ in range(8)) for _ in range(3)]
    sentences = [" ".join(rng.choice(WORDS) for _ in range(9)).capitalize() + "." for _ in range(n["tokens"] // 12)]
    for frac, code, k in zip((0.1, 0.5, 0.9), codes, (1, 2, 3)):
        sentences.insert(int(len(sentences) * frac), f"Important: secret code number {k} is {code}.")
    q = " ".join(sentences) + "\n\nThe text above contains three secret codes (numbers 1, 2 and 3). List them in order, one per line, nothing else."
    r = call(args, q, effort=n["effort"])
    return {"prompt_tokens": r.get("prompt_tokens"), "found": sum(c in r.get("text", "") for c in codes),
            "seconds": r.get("seconds"), "error": r.get("error")}


# ---- 鹈鹕：生成 HTML，截图后交给可信的看图模型按检查表判定结构 ----

def screenshot(args, html_path, png_path):
    if not args.edge:
        return False
    win = lambda p: subprocess.run(["wslpath", "-w", p], capture_output=True, text=True).stdout.strip()
    profile = os.path.join(os.path.dirname(png_path), ".edge-profile")
    os.makedirs(profile, exist_ok=True)
    subprocess.run([args.edge, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--window-size=1200,800",
                    "--virtual-time-budget=2500", f"--user-data-dir={win(profile)}", f"--screenshot={win(png_path)}",
                    "file:///" + win(html_path).replace("\\", "/")], capture_output=True, timeout=90)
    return os.path.exists(png_path)


def judge(args, png_path):
    img = base64.b64encode(open(png_path, "rb").read()).decode()
    r = call(args, [{"type": "text", "text": SUITE["pelican"]["judge_prompt"]},
                    {"type": "image_url", "image_url": {"url": "data:image/png;base64," + img}}],
             effort="low", model=args.judge_model, base=args.judge_base or args.base)
    m = re.search(r"\{.*\}", r.get("text", ""), re.S)
    try:
        return json.loads(m.group()) if m else {"error": r.get("error") or r.get("text", "")[:200]}
    except json.JSONDecodeError:
        return {"error": r.get("text", "")[:200]}


def measure_legs(args, html_path):
    """在无头浏览器里让动画跑起来，测量腿的长度变化和脚是否跟着踏板走（docs/decisions.md「模型检测」）。

    分类：真实蹬踏（腿长基本不变、脚绕圈且贴着踏板）、整腿摆动（腿长不变但脚不跟踏板）、
    腿在伸缩（腿长明显变化）、腿不动、没找到腿（代码里没有可识别的腿）。
    """
    if not args.edge:
        return None
    import tempfile
    html = open(html_path, encoding="utf-8", errors="replace").read()
    js = open(os.path.join(HERE, "legs.js"), encoding="utf-8").read()
    # 放在最前面，先于页面自己的脚本执行（替换 requestAnimationFrame）
    m = re.search(r"<head[^>]*>", html, re.I)
    tag = "<script>" + js + "</script>"
    html = html[:m.end()] + tag + html[m.end():] if m else tag + html
    work = tempfile.mkdtemp(prefix="legprobe-", dir=os.path.dirname(html_path))
    copy = os.path.join(work, "page.html")
    open(copy, "w", encoding="utf-8").write(html)
    win = lambda q: subprocess.run(["wslpath", "-w", q], capture_output=True, text=True).stdout.strip()
    out = subprocess.run([args.edge, "--headless=new", "--disable-gpu", "--window-size=1200,800", "--virtual-time-budget=5000",
                          f"--user-data-dir={win(os.path.join(work, 'profile'))}", "--dump-dom",
                          "file:///" + win(copy).replace("\\", "/")], capture_output=True, timeout=120).stdout.decode("utf-8", "replace")
    import shutil
    shutil.rmtree(work, ignore_errors=True)  # 只删本次 mkdtemp 建的临时目录
    m = re.search(r'<pre id="__legprobe__">(\[.*?)</pre>', out, re.S)
    if not m:
        return {"verdict": "测量失败"}
    import html as htmllib
    frames = json.loads(htmllib.unescape(m.group(1)))
    return classify_legs(frames)


def classify_legs(frames):
    """按 legs.js 采到的逐帧数据判定腿部动画。

    三个独立的问题标记：
      伸缩：同一条腿任一节（相邻两个关节点之间）的长度变化超过 12%；
      没踩板：脚和最近的踏板中心之间的距离随时间变化超过 12 像素（脚没跟着踏板走）；
      反着踏：脚绕圈的方向和车轮转向相反（鹈鹕往前骑时，曲柄和车轮应同向转）。
    都没有才算“真实蹬踏”；脚基本不动算“腿不动”；没识别出腿算“没找到腿”。
    """
    def dist(a, b):
        return ((a[0] - b[0]) ** 2 + (a[1] - b[1]) ** 2) ** 0.5

    if not frames:
        return {"verdict": "测量失败"}
    n = min(len(f["legs"]) for f in frames)
    legs = []
    for i in range(n):
        track = [(f, f["legs"][i]) for f in frames if f["legs"][i] and len(f["legs"][i]) >= 2]
        if len(track) < len(frames) // 2 or len({len(v) for _, v in track}) != 1:
            continue
        segs = len(track[0][1]) - 1
        seg_lens = [[dist(v[j], v[j + 1]) for _, v in track] for j in range(segs)]
        if sum(statistics.mean(x) for x in seg_lens) < 15:  # 脚趾、阴影这类小笔画
            continue
        stretch = max((max(x) - min(x)) / statistics.mean(x) for x in seg_lens if statistics.mean(x) > 3)

        def foot(f, v):
            ends = [v[0], v[-1]]
            if f["pedals"]:
                return min(ends, key=lambda e: min(dist(e, q) for q in f["pedals"]))
            return max(ends, key=lambda e: e[1])  # 没找到踏板就取更靠下的一端
        feet = [foot(f, v) for f, v in track]
        span = max(dist(a, b) for a in feet for b in feet)
        gaps = [min(dist(ft, q) for q in f["pedals"]) for ft, (f, _) in zip(feet, track) if f["pedals"]]
        # 鞋带公式求脚轨迹的有向面积：屏幕坐标 y 向下，正值表示顺时针
        area = sum(a[0] * b[1] - b[0] * a[1] for a, b in zip(feet, feet[1:] + feet[:1])) / 2
        legs.append({"segments": segs, "stretch": round(stretch, 3), "foot_span": round(span, 1),
                     "pedal_gap_change": round(max(gaps) - min(gaps), 1) if gaps else None,
                     "foot_turn": "顺时针" if area > 50 else "逆时针" if area < -50 else "不绕圈"})
    # 车轮转向：各车轮元素旋转角随时间的变化（展开 ±180° 跳变）取多数
    spins = []
    w = min(len(f["wheels"]) for f in frames)
    for i in range(w):
        angles = [f["wheels"][i] for f in frames if f["wheels"][i] is not None]
        total = 0.0
        for a, b in zip(angles, angles[1:]):
            d = (b - a + 180) % 360 - 180
            total += d
        if abs(total) > 30:
            spins.append(1 if total > 0 else -1)  # 角度增大 = 屏幕上顺时针
    wheel_turn = None if not spins else "顺时针" if sum(spins) > 0 else "逆时针"

    moving = [l for l in legs if l["foot_span"] > 5]
    flags = []
    if moving:
        if max(l["stretch"] for l in moving) > 0.12:
            flags.append("伸缩")
        if any(l["pedal_gap_change"] is None or l["pedal_gap_change"] > 12 for l in moving):
            flags.append("没踩板")
        turns = {l["foot_turn"] for l in moving if l["foot_turn"] != "不绕圈"}
        if wheel_turn and turns and wheel_turn not in turns:
            flags.append("反着踏")
        if not turns:
            flags.append("脚不绕圈")
    verdict = "没找到腿" if not legs else "腿不动" if not moving else ("真实蹬踏" if not flags else "、".join(flags))
    return {"verdict": verdict, "wheel_turn": wheel_turn, "legs": moving or legs}


def probe_pelican(args):
    p = SUITE["pelican"]
    os.makedirs(args.pelican_dir, exist_ok=True)
    tag = re.sub(r"[^\w.-]", "_", args.model) + f"-r{args.round}"

    def one(i):
        for _ in range(3):  # 生成不完整（没有收尾标签）就重新生成
            r = call(args, p["prompt"], effort=p["effort"])
            if r.get("error"):
                return {"error": r["error"]}
            # 代码块前常有一段说明文字；取第一个代码块，没有代码块就从 <!DOCTYPE / <html / <svg 开始取
            m = re.search(r"```(?:html)?\s*\n(.*?)(?:```|$)", r["text"], re.S)
            html = m.group(1) if m else r["text"][max(0, min([i for i in (r["text"].find("<!DOCTYPE"), r["text"].find("<html"), r["text"].find("<svg")) if i >= 0], default=0)):]
            if re.search(r"</html>|</svg>", html, re.I):
                break
        else:
            return {"error": "三次生成都不完整"}
        html_path = os.path.join(args.pelican_dir, f"{tag}-{i + 1}.html")
        open(html_path, "w", encoding="utf-8").write(html)
        out = {"file": os.path.basename(html_path), "output_tokens": r.get("completion_tokens"), "reasoning_tokens": r.get("reasoning_tokens"),
               "seconds": r.get("seconds"), "html_chars": len(html),
               "shapes": len(re.findall(r"<(?:path|circle|ellipse|rect|line|polygon|polyline)\b", html))}
        png = html_path[:-5] + ".png"
        if screenshot(args, html_path, png):
            out["judge"] = judge(args, png)
        out["legs"] = measure_legs(args, html_path)
        return out
    return {"samples": parallel(args, one, range(p["samples"]))}


PROBES = {"tokens": probe_tokens, "fingerprint": probe_fingerprint, "math": probe_math, "needle": probe_needle, "pelican": probe_pelican}


def run(args):
    os.makedirs(args.out, exist_ok=True)
    path = os.path.join(args.out, re.sub(r"[^\w.-]", "_", args.model) + f"-r{args.round}.json")
    out = json.load(open(path)) if os.path.exists(path) else {}
    out.update({"suite_version": SUITE["version"], "model": args.model, "round": args.round})
    for name in args.only.split(","):
        start = time.time()
        print(f"[{args.model} 第 {args.round} 轮] {name} …", file=sys.stderr, flush=True)
        result = PROBES[name](args)
        # 只补跑部分单词指纹题时，合并进这一轮已有的答案（新跑的题替换旧答案），不覆盖整份
        if name == "fingerprint" and "fingerprint" in out and (args.fingerprint_from or args.fingerprint_limit):
            merged = fingerprint_answers(out["fingerprint"])
            merged.update(result["answers"])
            result["answers"] = merged
            result["errors"] += out["fingerprint"].get("errors", 0)
        out[name] = result
        out[name]["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
        out[name]["elapsed_seconds"] = round(time.time() - start, 1)
        json.dump(out, open(path, "w"), ensure_ascii=False, indent=1)
    print(path)


# ---- 对比 ----

def tvd(a, b):
    """两组答案的总变差距离：0 表示分布相同，1 表示完全不重叠。"""
    if not a or not b:
        return None
    keys = set(a) | set(b)
    return 0.5 * sum(abs(a.count(k) / len(a) - b.count(k) / len(b)) for k in keys)


V1_PROMPTS = SUITE["fingerprint"]["prompts"][:24]  # 第 1 版按序号记录答案，题目就是现在题库的前 24 题


def fingerprint_answers(fp):
    """统一成 {题目文本: [答案…]}。"""
    return {(V1_PROMPTS[int(k)] if k.isdigit() else k): v for k, v in fp["answers"].items()}


def fingerprint_distance(x, y):
    """两份结果在共同题目上的平均总变差和众数一致比例；只算两边都至少有 3 个答案的题。"""
    xa, ya = fingerprint_answers(x), fingerprint_answers(y)
    ds, top = [], []
    for i in xa:
        a, b = xa[i], ya.get(i, [])
        if len(a) < 3 or len(b) < 3:
            continue
        d = tvd(a, b)
        if d is not None:
            ds.append(d)
            top.append(max(set(a), key=a.count) == max(set(b), key=b.count))
    return statistics.mean(ds), sum(top) / len(top)


def distributions(files, out):
    """按题目汇总各型号（多轮合并）的答案分布，并标出每题在型号之间的区分度。

    区分度 = 各型号两两之间总变差的平均值：越大，这道题越能分辨型号；接近 0 的题（全家族都答 mango）没有区分作用。
    """
    from collections import Counter, defaultdict
    merged = defaultdict(lambda: defaultdict(list))
    for f in files:
        r = json.load(open(f))
        if "fingerprint" not in r:
            continue
        for q, ans in fingerprint_answers(r["fingerprint"]).items():
            merged[q][r["model"]].extend(ans)
    result = []
    for q in SUITE["fingerprint"]["prompts"]:
        models = merged.get(q, {})
        pairs = [tvd(a, b) for (_, a), (_, b) in combinations(models.items(), 2) if a and b]
        # 自身波动：每个型号的答案一分为二，两半之间的总变差；型号之间也按同样的样本量（各取一半）比，才公平
        halves = {m: (a[: len(a) // 2], a[len(a) // 2:]) for m, a in models.items() if len(a) >= 6}
        self_noise = [tvd(h[0], h[1]) for h in halves.values()]
        between = [tvd(x[0], y[1]) for (_, x), (_, y) in combinations(halves.items(), 2)]
        score = statistics.mean(between) - statistics.mean(self_noise) if self_noise and between else None
        result.append({
            "prompt": q,
            "separation": round(statistics.mean(pairs), 3) if pairs else None,
            "self_noise": round(statistics.mean(self_noise), 3) if self_noise else None,
            "score": round(score, 3) if score is not None else None,
            "models": {m: {"samples": len(a), "counts": dict(Counter(a).most_common())} for m, a in sorted(models.items())},
        })
    doc = {"suite_version": SUITE["version"], "generated_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "sources": sorted(os.path.basename(f) for f in files),
           "note": ("单词指纹答案分布：每道题各型号（多轮合并）的答案计数。separation：型号两两总变差的平均值；self_noise：同一型号答案一分为二后两半的总变差（自身波动）；"
                    "score：同样本量下型号之间的差距减去自身波动，越大越能分辨型号。高随机的题（随机颜色代码）separation 高但自身波动也高，score 不高。"),
           "prompts": result}
    json.dump(doc, open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    ranked = sorted((x for x in result if x["score"] is not None), key=lambda x: -x["score"])
    print(f"已写入 {out}：{len(result)} 题，能算区分度的 {len(ranked)} 题")
    for x in ranked[:15]:
        tops = "；".join(f"{m.split('/')[-1]}: " + "/".join(list(v['counts'])[:2]) for m, v in x["models"].items())
        print(f"  区分 {x['score']:.2f}（自身波动 {x['self_noise']:.2f}） {x['prompt'][:40]:<42} {tops}")


def compare(files):
    rs = [json.load(open(f)) for f in files]
    names = [f"{r['model']}#{r['round']}" for r in rs]
    print("== 分词指纹（差值是否一致 / 注入提示词基准）")
    for n, r in zip(names, rs):
        if "tokens" in r:
            print(f"  {n:<32} 基准 {r['tokens']['base'][0]}  差值 {list(r['tokens']['deltas'].values())}")
    fp = [(n, r["fingerprint"]) for n, r in zip(names, rs) if "fingerprint" in r]
    if len(fp) > 1:
        print("== 单词指纹距离（平均总变差，越小越像；括号里是众数一致的比例）")
        for (n1, a), (n2, b) in combinations(fp, 2):
            d, t = fingerprint_distance(a, b)
            print(f"  {n1:<32} ↔ {n2:<32} {d:.3f}（{t:.0%}）")
    print("== 数学题（每题：对几次 / 推理 token 中位数）")
    for n, r in zip(names, rs):
        if "math" not in r:
            continue
        cells = []
        for i, p in enumerate(r["math"]["problems"]):
            runs = [x for x in r["math"]["runs"] if x["problem"] == i]
            rt = [x["reasoning_tokens"] for x in runs if x["reasoning_tokens"] is not None]
            cells.append(f"{p['kind'][:10]} {sum(x['correct'] for x in runs)}/{len(runs)} {int(statistics.median(rt)) if rt else '-'}")
        print(f"  {n:<32} " + " | ".join(cells))
    print("== 大海捞针 / 鹈鹕")
    for n, r in zip(names, rs):
        cells = []
        if "needle" in r:
            cells.append(f"捞针 {r['needle']['found']}/3（输入 {r['needle']['prompt_tokens']}）")
        if "pelican" in r:
            for s in r["pelican"]["samples"]:
                j = s.get("judge") or {}
                checks = [v for k, v in j.items() if isinstance(v, bool)]
                cells.append(f"鹈鹕 {s.get('file', s.get('error', ''))[-16:]} 结构 {sum(checks)}/{len(checks)} 输出 {s.get('output_tokens')}")
        if cells:
            print(f"  {n:<32} " + "；".join(cells))


def main():
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--base", required=True)
    r.add_argument("--key", required=True)
    r.add_argument("--model", required=True)
    r.add_argument("--round", type=int, default=1)
    r.add_argument("--out", required=True)
    r.add_argument("--only", default="tokens,fingerprint,math,needle,pelican")
    r.add_argument("--concurrency", type=int, default=6)
    r.add_argument("--pelican-dir", default="/tmp/lingshu-probe-pelican")
    r.add_argument("--edge", default="")
    r.add_argument("--judge-model", default="codex/gpt-6.1-sol")
    r.add_argument("--judge-base", default="")
    c = sub.add_parser("compare")
    c.add_argument("files", nargs="+")
    ds = sub.add_parser("dist", help="把各型号的单词指纹答案汇总成分布文件")
    ds.add_argument("--out", required=True)
    ds.add_argument("files", nargs="+")
    r.add_argument("--fingerprint-from", type=int, default=0, help="单词指纹从第 N 题开始（0 起算），用于只补跑新加的题")
    r.add_argument("--fingerprint-limit", type=int, default=0, help="最多跑 N 道单词指纹题（0 表示不限）")
    lg = sub.add_parser("legs", help="单独测量鹈鹕 HTML 的腿部动画")
    lg.add_argument("--edge", required=True)
    lg.add_argument("files", nargs="+")
    args = p.parse_args()
    if args.cmd == "run":
        run(args)
    elif args.cmd == "dist":
        distributions(args.files, args.out)
    elif args.cmd == "legs":
        for f in args.files:
            r = measure_legs(args, f)
            print(f"{os.path.basename(f):<34} {r['verdict']:<6} " + json.dumps(r.get("legs", []), ensure_ascii=False)[:300])
    else:
        compare(args.files)


if __name__ == "__main__":
    main()
