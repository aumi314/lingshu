# 完整 Codex 伪装下的官方基准（docs/deploy.md「模型检测伪装配置」），经本机 Magpie 的 codex/*，复现 internal/relay/probe_responses.go 的请求体。
# 用法：python3 scripts/probe/calib_codex.py references/probe-sites/probe-disguise.json gpt-6.2-sol gpt-6.2-astra；结果写进 internal/probe/run.go 的 templateBases。
import json, sys, time, uuid, urllib.request
D = json.load(open(sys.argv[1]))["codex"]
def anchor(model):
    items = json.loads(json.dumps(D["input"]).replace("{{DATE}}", time.strftime("%Y-%m-%d")))
    items.append({"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Reply with exactly: ok"}]})
    s = str(uuid.uuid4())
    body = {"model": "codex/" + model, "stream": True, "store": False, "include": ["reasoning.encrypted_content"], "prompt_cache_key": s, **D.get("body", {}), "input": items}
    if D.get("reasoning_context"): body["reasoning"] = {"context": D["reasoning_context"]}
    req = urllib.request.Request("http://127.0.0.1:3425/v1/responses", data=json.dumps(body).encode(), headers={"Authorization": "Bearer magpie", "Content-Type": "application/json"})
    text, usage = "", None
    with urllib.request.urlopen(req, timeout=300) as r:
        for raw in r:
            line = raw.decode("utf-8", "replace").strip()
            if not line.startswith("data:") or line == "data: [DONE]": continue
            ev = json.loads(line[5:])
            if ev.get("type") == "response.output_text.delta": text += ev["delta"]
            if ev.get("type") == "response.completed": usage = ev["response"].get("usage")
    return text, (usage or {}).get("input_tokens")
for m in sys.argv[2:]:
    print(m, [anchor(m) for _ in range(2)], flush=True)
