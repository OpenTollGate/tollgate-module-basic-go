#!/usr/bin/env python3
"""killerproxy — deterministic crash-window injection for the #497 harness.

Sits between the tollgate and the mint. All traffic passes through
unchanged except the FIRST response to a POST matching the watch path:
the mint's answer is received (the mint HAS signed), a marker file is
written, and the response is never delivered — the connection hangs
until the caller (the driver) SIGKILLs the tollgate. That lands the
process death exactly between "mint accepted and signed" and "wallet
processed the response" — the #497 acceptance boundary.

Usage: killerproxy.py <listen-port> <mint-target> <watch-path> <marker-file>
  e.g. killerproxy.py 18085 http://172.31.77.2:8085 /v1/swap /tmp/run/kill.marker

Protocol: the FIRST watched response writes the marker and begins
swallow mode — EVERY subsequent watched response is also swallowed
(the wallet library retries connections; a one-shot swallow lets the
retry complete the operation and defeats the injection) — until a
<marker-file>.release appears next to the marker. The driver touches
the release file right after it has SIGKILLed the tollgate; from then
on everything passes through, which is exactly what the boot-time
resume replay needs.
"""
import http.server
import socket
import sys
import threading
import urllib.request

listen_port = int(sys.argv[1])
target = sys.argv[2].rstrip("/")
watch_path = sys.argv[3]
marker = sys.argv[4]

armed = threading.Event()   # set by the first watched response
released = threading.Event() # set when <marker>.release appears


def watch_release():
    import os, time
    release = marker + ".release"
    while not released.is_set():
        if os.path.exists(release):
            released.set()
            return
        time.sleep(0.2)


def forward(method, path, headers, body):
    req = urllib.request.Request(
        target + path, data=body if body else None, method=method
    )
    for h in ("content-type",):
        if h in headers:
            req.add_header(h, headers[h])
    try:
        resp = urllib.request.urlopen(req, timeout=30)
        return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()


class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _relay(self):
        print(f"killer: {self.command} {self.path}", flush=True)
        n = int(self.headers.get("content-length", 0) or 0)
        body = self.rfile.read(n) if n else None
        code, rb = forward(self.command, self.path, self.headers, body)

        if (
            self.command == "POST"
            and watch_path in self.path
            and code == 200
            and not released.is_set()
        ):
            first = not armed.is_set()
            armed.set()
            if first:
                with open(marker, "w") as f:
                    f.write(self.path)
                threading.Thread(target=watch_release, daemon=True).start()
            # swallow: never write a response. The wallet's connection
            # resets, its retry ladder re-POSTs, and every watched
            # response is swallowed again until the driver releases —
            # the operation cannot complete behind the kill.
            self.close_connection = True
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            return

        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(rb)))
        self.end_headers()
        self.wfile.write(rb)

    do_GET = do_POST = do_PUT = _relay

    def log_message(self, *a):
        pass


http.server.ThreadingHTTPServer(("0.0.0.0", listen_port), H).serve_forever()
