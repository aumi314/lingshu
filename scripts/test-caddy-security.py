#!/usr/bin/env python3
"""在本机 Docker 中验证 Caddy 入口防护；仅用随机本地端口和模拟上游。"""
import base64
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import socket
import ssl
import subprocess
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parents[1]
IMAGE = os.environ.get('CADDY_TEST_IMAGE', 'caddy:2.11.9-alpine')
HOST = 'gateway.piqode.test'


def docker(*args, **kwargs):
    return subprocess.run(['docker', *args], check=True, capture_output=True, text=True, **kwargs)


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


class Upstream(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path == '/ws':
            accept = base64.b64encode(hashlib.sha1((self.headers['Sec-WebSocket-Key'] +
                '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
            self.send_response(101)
            self.send_header('Upgrade', 'websocket')
            self.send_header('Connection', 'Upgrade')
            self.send_header('Sec-WebSocket-Accept', accept)
            self.end_headers()
            self.wfile.write(bytes([0x81, 2]) + b'ok')
            self.wfile.flush()
            self.connection.settimeout(3)
            try:
                self.rfile.read(1)
            except OSError:
                pass
            self.close_connection = True
            return
        if self.path == '/events':
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Connection', 'close')
            self.end_headers()
            self.wfile.write(b'data: first\n\n')
            self.wfile.flush()
            time.sleep(1.5)
            self.wfile.write(b'data: last\n\n')
            self.wfile.flush()
            self.close_connection = True
            return
        body = json.dumps({'ip': self.headers.get('X-Real-IP')}).encode()
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    template = (ROOT / 'deploy/caddy-lingshu-dmit.conf').read_text()
    assert '@not_cloudflare not remote_ip ' in template
    assert 'respond @not_cloudflare 403' in template
    assert 'flush_interval -1' in template
    upstream = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Upstream)
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    https_port, http_port = free_port(), free_port()
    name = 'lingshu-security-caddy-' + str(os.getpid())
    tls = ssl._create_unverified_context()  # 仅测试容器自签证书
    with tempfile.TemporaryDirectory(prefix='lingshu-caddy-security-') as tmp:
        config = Path(tmp) / 'Caddyfile'
        config.write_text(template)
        docker('run', '--rm', '-v', f'{config}:/etc/caddy/Caddyfile:ro', IMAGE,
               'caddy', 'validate', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile')
        # 用本地回环来源模拟 CF。127.0.0.1 不受信任，127.0.0.2 才受信任。
        test = re.sub(r'(@not_cloudflare not remote_ip )[^\n]+', r'\g<1>127.0.0.2/32', template)
        test = test.replace('gateway.piqode.com {', f'https://{HOST}:{https_port} {{\n  tls internal')
        test = test.replace('172.18.0.1:8300', f'127.0.0.1:{upstream.server_port}')
        test = test.replace('/var/log/caddy/lingshu-access.log', '/tmp/lingshu-access.log')
        test = f'{{\n admin off\n http_port {http_port}\n}}\n' + test
        config.write_text(test)
        docker('run', '-d', '--name', name, '--network', 'host',
               '-v', f'{config}:/etc/caddy/Caddyfile:ro', IMAGE)

        class LocalTLS(http.client.HTTPSConnection):
            def connect(self):
                raw = socket.create_connection(('127.0.0.1', https_port), self.timeout, self.source_address)
                self.sock = tls.wrap_socket(raw, server_hostname=HOST)

        def connection(source='127.0.0.2'):
            return LocalTLS(HOST, https_port, timeout=5, context=tls, source_address=(source, 0))

        def request(path, source='127.0.0.2', headers=None):
            conn = connection(source)
            conn.request('GET', path, headers={'Host': f'{HOST}:{https_port}', **(headers or {})})
            response = conn.getresponse()
            result = response.status, dict(response.getheaders()), response.read()
            conn.close()
            return result

        try:
            for _ in range(100):
                try:
                    if request('/healthz')[0] == 200:
                        break
                except (OSError, http.client.HTTPException):
                    time.sleep(0.1)
            else:
                print(docker('logs', name).stderr)
                raise AssertionError('Caddy startup timeout')
            status, headers, body = request('/healthz')
            assert status == 200
            expected = {'Strict-Transport-Security': 'max-age=31536000',
                        'X-Content-Type-Options': 'nosniff', 'X-Frame-Options': 'DENY',
                        'Content-Security-Policy': "frame-ancestors 'none'",
                        'Referrer-Policy': 'strict-origin-when-cross-origin'}
            assert all(headers.get(k) == v for k, v in expected.items()), headers
            assert request('/healthz', '127.0.0.1')[0] == 403
            spoof = {'X-Forwarded-For': '127.0.0.2', 'X-Real-IP': '127.0.0.2',
                     'CF-Connecting-IP': '127.0.0.2'}
            assert request('/healthz', '127.0.0.1', spoof)[0] == 403
            assert json.loads(request('/healthz', headers={'X-Real-IP': '203.0.113.99'})[2])['ip'] == '127.0.0.2'
            for path in ['/.env', '/.git/config', '/wp-login.php', '/actuator/health']:
                assert request(path)[0] == 404, path
            conn = http.client.HTTPConnection('127.0.0.1', http_port, timeout=5)
            conn.request('GET', '/healthz', headers={'Host': HOST})
            response = conn.getresponse()
            assert response.status == 308
            assert response.getheader('Location') == f'https://{HOST}:{https_port}/healthz'
            response.read(); conn.close()
            conn = connection()
            start = time.monotonic()
            conn.request('GET', '/events', headers={'Host': f'{HOST}:{https_port}'})
            response = conn.getresponse()
            assert response.status == 200 and response.readline() == b'data: first\n'
            assert time.monotonic() - start < 1.2, 'SSE buffered until complete'
            assert b'data: last' in response.read()
            conn.close()
            conn = connection()
            conn.request('GET', '/ws', headers={'Host': f'{HOST}:{https_port}',
                'Connection': 'Upgrade', 'Upgrade': 'websocket', 'Sec-WebSocket-Version': '13',
                'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ=='})
            response = conn.getresponse()
            assert response.status == 101
            assert response.getheader('Sec-WebSocket-Accept') == 's3pPLMBiTxaQ9kYGzzhZRbK+xOo='
            assert response.fp.read(4) == bytes([0x81, 2]) + b'ok'
            response.close(); conn.close()
            print('PASS: Caddy validate, peer allow/deny, spoofed headers, security headers, scanner paths, HTTP redirect, immediate SSE, WebSocket 101 and frame')
        finally:
            docker('rm', '-f', name)
            upstream.shutdown()


if __name__ == '__main__':
    main()
