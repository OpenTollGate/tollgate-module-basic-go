#!/usr/bin/env python3
"""Controllable-latency HTTP reverse proxy in front of the local mint.

Adds DELAY_MS to every response so fault-injection tests (kill mid-swap) can
reliably land inside the swap window. Listens on PORT, forwards to BACKEND.
"""
import http.server
import os
import socketserver
import time
import urllib.error
import urllib.request

BACKEND = os.environ.get("BACKEND", "http://127.0.0.1:8085").rstrip("/")
DELAY = float(os.environ.get("DELAY_MS", "600")) / 1000.0
PORT = int(os.environ.get("PORT", "8086"))

HOP = {"host", "content-length", "connection", "transfer-encoding", "keep-alive"}


class Proxy(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _forward(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else None
        req = urllib.request.Request(BACKEND + self.path, data=body, method=self.command)
        for k, v in self.headers.items():
            if k.lower() not in HOP:
                req.add_header(k, v)
        try:
            resp = urllib.request.urlopen(req, timeout=30)
            data, code, hdrs = resp.read(), resp.status, list(resp.headers.items())
        except urllib.error.HTTPError as e:
            data, code, hdrs = e.read(), e.code, list(e.headers.items())
        except Exception as e:  # noqa: BLE001
            data, code, hdrs = str(e).encode(), 502, []
        time.sleep(DELAY)  # widen the window
        self.send_response(code)
        for k, v in hdrs:
            if k.lower() not in HOP:
                self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = _forward
    do_POST = _forward

    def log_message(self, *a):
        pass


socketserver.ThreadingTCPServer.allow_reuse_address = True
with socketserver.ThreadingTCPServer(("127.0.0.1", PORT), Proxy) as httpd:
    print(f"latency proxy on :{PORT} -> {BACKEND} (+{int(DELAY*1000)}ms)", flush=True)
    httpd.serve_forever()
