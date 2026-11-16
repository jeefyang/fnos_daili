#!/usr/bin/env python3
"""Minimal gateway probe: listens on a Unix socket, echoes headers and paths.

This is a disposable verification fixture, not product code. It writes raw
request metadata (including cookies) to TRIM_PKGVAR/gateway-probe.log.
"""
import html
import json
import os
import socketserver
import sys
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler

PREFIX_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "prefix")
try:
    with open(PREFIX_FILE, "r", encoding="utf-8") as f:
        PREFIX = f.read().strip().rstrip("/")
except OSError:
    PREFIX = "/app/gwprobe"
if not PREFIX:
    PREFIX = "/app/gwprobe"

SOCK = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.environ.get("TRIM_APPDEST", "."), "app.sock")
LOG_DIR = os.environ.get("TRIM_PKGVAR", os.path.dirname(SOCK))
os.makedirs(LOG_DIR, exist_ok=True)
LOG_PATH = os.path.join(LOG_DIR, "gateway-probe.log")
LOG_FD = os.open(LOG_PATH, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
LOG = os.fdopen(LOG_FD, "a", encoding="utf-8", buffering=1)


def route_path(raw_path):
    path = urllib.parse.urlsplit(raw_path).path
    if path == PREFIX:
        return "/"
    if path.startswith(PREFIX + "/"):
        return path[len(PREFIX):]
    return path


def record(handler):
    client = handler.client_address
    if isinstance(client, (tuple, list)):
        client = client[0] if client else ""
    item = {
        "time": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "method": handler.command,
        "raw_path": handler.path,
        "route": route_path(handler.path),
        "client": str(client),
        "headers": [[k, v] for k, v in handler.headers.items()],
    }
    LOG.write(json.dumps(item, ensure_ascii=False) + "\n")
    return item


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        pass

    def _send(self, status, body=b"", content_type="text/plain; charset=utf-8", extra=None):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        if extra:
            for k, v in extra:
                self.send_header(k, v)
        self.end_headers()
        if body:
            self.wfile.write(body)

    def _json(self, obj, status=200):
        body = json.dumps(obj, ensure_ascii=False, indent=2).encode("utf-8")
        self._send(status, body, "application/json; charset=utf-8")

    def _headers_obj(self):
        return {
            "method": self.command,
            "raw_path": self.path,
            "route": route_path(self.path),
            "headers": {k: v for k, v in self.headers.items()},
            "header_list": [[k, v] for k, v in self.headers.items()],
        }

    def do_GET(self):
        item = record(self)
        route = route_path(self.path)

        if route in ("/", "/index.html"):
            page = """<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>Gateway Probe</title>
<style>body{font:14px/1.5 system-ui,sans-serif;margin:2rem;max-width:60rem}code{background:#eee;padding:.1rem .3rem}pre{background:#111;color:#eee;padding:1rem;overflow:auto;max-height:28rem}button{margin:.2rem}</style>
</head><body>
<h1>Gateway Probe</h1>
<p>raw path: <code id="raw"></code></p>
<p>prefix: <code>__PREFIX__</code>；route: <code id="route"></code></p>
<ul>
<li><a href="__PREFIX__/static.txt">绝对路径静态资源</a></li>
<li><a href="static.txt">相对路径静态资源</a></li>
<li><a href="__PREFIX__/redirect">302 到 __PREFIX__/headers</a></li>
<li><a href="__PREFIX__/cookie">设置 Path=__PREFIX__ 的 cookie 后跳转</a></li>
<li><a href="__PREFIX__/headers">查看当前请求头</a></li>
</ul>
<p>cookie: <code id="cookie"></code></p>
<p>
<button id="btn-headers">GET /headers</button>
<button id="btn-spoof">带伪造 X-Trim-* 头 GET /headers</button>
<button id="btn-post">POST /post（表单同源）</button>
</p>
<pre id="out">点上面的按钮。</pre>
<script>
const PREFIX = "__PREFIX__";
document.getElementById("raw").textContent = location.pathname;
document.getElementById("cookie").textContent = document.cookie || "(none)";
fetch(PREFIX + "/headers").then(r => r.json()).then(j => {
  document.getElementById("route").textContent = j.route;
}).catch(e => { document.getElementById("out").textContent = String(e); });
async function show(url, opts) {
  try {
    const r = await fetch(url, opts);
    const t = await r.text();
    document.getElementById("out").textContent = r.status + " " + r.statusText + "\n" + t;
  } catch (e) {
    document.getElementById("out").textContent = "fetch error: " + e;
  }
}
document.getElementById("btn-headers").onclick = () => show(PREFIX + "/headers");
document.getElementById("btn-spoof").onclick = () => show(PREFIX + "/headers", {
  headers: {"X-Trim-Isadmin": "true", "X-Trim-Userid": "99999", "X-Trim-Username": "mallory"}
});
document.getElementById("btn-post").onclick = () => show(PREFIX + "/post", {
  method: "POST",
  headers: {"Content-Type": "application/x-www-form-urlencoded", "X-Probe": "form"},
  body: "hello=1&from=page"
});
</script>
</body></html>"""
            page = page.replace("__PREFIX__", html.escape(PREFIX, quote=True))
            self._send(200, page.encode("utf-8"), "text/html; charset=utf-8")
            return
        if route == "/headers":
            self._json(self._headers_obj())
            return
        if route == "/static.txt":
            self._send(200, ("static ok; prefix=" + PREFIX + "\n").encode("utf-8"))
            return
        if route == "/redirect":
            self._send(302, b"", extra=[("Location", PREFIX + "/headers?from=redirect")])
            return
        if route == "/cookie":
            self._send(302, b"", extra=[
                ("Location", PREFIX + "/headers?from=cookie"),
                ("Set-Cookie", "gwprobe=1; Path=" + PREFIX + "; HttpOnly; SameSite=Strict"),
            ])
            return
        if route == "/health":
            self._send(200, b"ok\n")
            return
        if route == "/favicon.ico":
            self._send(204, b"")
            return
        self._json({"error": "not found", "raw_path": self.path, "route": route}, status=404)

    def do_POST(self):
        record(self)
        route = route_path(self.path)
        length = int(self.headers.get("Content-Length", "0") or 0)
        body = self.rfile.read(length) if length else b""
        if route in ("/post", "/echo", "/headers"):
            obj = self._headers_obj()
            obj["body"] = body.decode("utf-8", "replace")
            obj["origin"] = self.headers.get("Origin", "")
            obj["referer"] = self.headers.get("Referer", "")
            self._json(obj)
            return
        self._json({"error": "not found", "raw_path": self.path, "route": route}, status=404)

    def do_OPTIONS(self):
        self._send(204, b"", extra=[("Allow", "GET, POST, OPTIONS")])


UnixStreamServer = getattr(socketserver, "UnixStreamServer", socketserver.TCPServer)


class UnixHTTPServer(UnixStreamServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, address, handler):
        super().__init__(address, handler)
        self.server_name = "gateway-probe"
        self.server_port = 0


def main():
    try:
        os.unlink(SOCK)
    except FileNotFoundError:
        pass
    if os.path.exists(SOCK):
        raise SystemExit("socket already exists: " + SOCK)
    os.umask(0o077)
    server = UnixHTTPServer(SOCK, Handler)
    try:
        os.chmod(SOCK, 0o660)
    except OSError:
        pass
    print("listening on " + SOCK + " prefix=" + PREFIX, flush=True)
    try:
        server.serve_forever()
    finally:
        server.server_close()
        try:
            os.unlink(SOCK)
        except OSError:
            pass


if __name__ == "__main__":
    main()
