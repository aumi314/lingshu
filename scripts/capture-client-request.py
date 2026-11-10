"""抓客户端默认配置下发出的真实请求体，给协议转接的固定样本用（docs/research/responses-messages.md「默认客户端样本」）。

用法：python3 -I scripts/capture-client-request.py 端口 输出文件
起一个只记录请求的本地服务（所有 POST 都回 400，客户端随即退出），再让客户端用独立的临时配置目录连上来，例如：

  CODEX_HOME=$(mktemp -d) CAP_KEY=dummy codex exec --skip-git-repo-check -m claude-opus-5-5 \
    -c model_provider=cap -c 'model_providers.cap={name="cap",base_url="http://127.0.0.1:端口/v1",env_key="CAP_KEY",wire_api="responses"}' \
    "say hi" </dev/null
  CLAUDE_CONFIG_DIR=$(mktemp -d) ANTHROPIC_BASE_URL=http://127.0.0.1:端口 ANTHROPIC_AUTH_TOKEN=dummy claude -p "say hi" </dev/null

抓到的请求含本机系统提示、设备标识等，不要直接提交；只把结构（顶层字段、工具类型、消息角色）照抄进 testdata 的样本。
"""
import http.server
import json
import sys


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        with open(sys.argv[2], "ab") as f:
            f.write(self.path.encode() + b"\n" + body + b"\n")
        try:
            data = json.loads(body)
            tools = [(t.get("type") or "custom") + ":" + str(t.get("name", "")) for t in data.get("tools", [])]
            roles = [m.get("role") or m.get("type") for m in data.get("messages", data.get("input", [])) if isinstance(m, dict)]
            print(self.path, "顶层字段", sorted(data), "\n  工具", tools, "\n  消息", roles, flush=True)
        except ValueError:
            print(self.path, "不是 JSON", flush=True)
        self.send_response(400)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"error":{"type":"invalid_request_error","message":"capture only"}}')

    def do_GET(self):
        self.send_response(404)
        self.end_headers()

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
