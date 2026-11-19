# 多轮长上下文保真测试（完整 Codex 伪装，同一会话每轮带全部历史，最后约 19 万 token）：第 1 轮埋 4 个细节、第 7 轮埋 2 个，最后一轮要求原样复述。
# 用来查中转有没有压缩历史。请求实现复用同目录的 gpt_channel_check.py；每轮落盘到 ./out/<tag>.json，重跑从断点续。
# 用法（DMIT）：python3 gpt_multiturn.py <channel_key_id> <responses_base_url> <tag>；最好同时对一个原生渠道跑同一套当对照。
import os, sys, json, random, uuid
KID, BASE, TAG = sys.argv[1], sys.argv[2], sys.argv[3]
sys.argv = ["gpt_channel_check.py", KID, BASE, TAG]
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "gpt_channel_check.py")).read().split("rng = random.Random")[0])
TEMPLATE = D["input"]
rng = random.Random(20261120)  # 两家同一套内容
def chunk(n, rng):
    return "\n".join(f"[{i:04d}] " + " ".join(rng.choice(P.WORDS) for _ in range(9)) + "." for i in range(n))
facts = {
    "codename": "Kestrel-%04d" % rng.randrange(10000),
    "db_port": str(rng.randrange(20000, 60000)),
    "budget_code": "QX-%04d-%s" % (rng.randrange(10000), rng.choice("ABCDEFGH")),
    "policy": "Every rollback must be approved by two reviewers from different time zones before %02d:%02d UTC." % (rng.randrange(24), rng.randrange(60)),
    "deploy_token": "".join(rng.choice("ABCDEFGHJKLMNPQRSTUVWXYZ23456789") for _ in range(10)),
    "oncall_ext": str(rng.randrange(1000, 9999)),
}
c1 = chunk(950, rng).split("\n")
for pos, line in zip((80, 400, 700, 900), (f"NOTE: The project codename is {facts['codename']}.", f"NOTE: The staging database listens on port {facts['db_port']}.",
                                           f"NOTE: The budget code is {facts['budget_code']}.", f"POLICY (quote verbatim when asked): {facts['policy']}")):
    c1.insert(pos, line)
turns = [("Here are my project notes. Read them carefully; I will ask about details later. For now reply only: noted.\n\n" + "\n".join(c1))]
for t in range(2, 14):
    lines = chunk(950, rng).split("\n"); word = rng.choice(P.WORDS)
    if t == 7:
        lines.insert(300, f"NOTE: The deploy token for the payments service is {facts['deploy_token']}.")
        lines.insert(650, f"NOTE: The on-call phone extension is {facts['oncall_ext']}.")
    turns.append(f"Log chunk {t}. How many lines in this chunk contain the word '{word}'? Reply with the number only.\n\n" + "\n".join(lines))
turns.append("Without re-reading anything new, answer from our conversation. Reply in exactly six lines, nothing else:\n"
             "1. The project codename\n2. The staging database port\n3. The budget code\n4. The POLICY sentence, quoted verbatim\n"
             "5. The deploy token for the payments service\n6. The on-call phone extension")

session = R.get("session") or str(uuid.uuid4()); R["session"] = session; R["facts"] = facts; R.setdefault("turns", [])
history = []
for i, prompt in enumerate(turns):
    if i < len(R["turns"]) and R["turns"][i].get("status") == 200 and R["turns"][i].get("text"):
        r = R["turns"][i]
    else:
        D["input"] = TEMPLATE + history
        r = send(prompt, "medium", session); D["input"] = TEMPLATE
        r["turn"] = i + 1; r["prompt_chars"] = len(prompt)
        R["turns"] = R["turns"][:i] + [r]; save()
        print(TAG, "第", i + 1, "轮", r.get("status"), "输入", r.get("input"), "缓存", r.get("cached"), "推理", r.get("reasoning"), "用时", r["secs"], "回答", repr(r.get("text", ""))[:40], flush=True)
        est = TPL_BASE + (sum(len(json.dumps(h)) for h in history) + len(prompt)) // 3
        if (r.get("input") or 0) > 2 * est:
            print(TAG, "计费异常，停止：输入", r.get("input"), "预期约", est, flush=True); sys.exit(1)
        if r.get("status") != 200 or not r.get("text"):
            print(TAG, "本轮失败，停止，可重跑续接", str(r.get("err"))[:200], flush=True); sys.exit(1)
    history += [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": prompt + NT}]},
                {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": r["text"]}]}]
ans = R["turns"][-1]["text"]
R["score"] = {k: (v in ans) for k, v in facts.items()}; save()
print(TAG, "结果", R["score"], flush=True); print(TAG, "完成", flush=True)
