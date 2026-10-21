#!/usr/bin/env python3
"""killerproxy-hold — SCRATCH-ADDITION rc1-793 (disclosed).

Receive-during-resume observatory for the #793 lane. Crash-window
behavior is byte-for-byte the stock killer's (swallow the FIRST watched
POST-200 response — the mint HAS signed — then every watched response,
until <marker-file>.release appears). The ONLY new behavior is
post-release: the FIRST watched POST after release (the boot-resume
replay) is forwarded to the mint immediately, a <marker-file>.hold
marker is written the moment it arrives, and its RESPONSE is held for
<hold-seconds> before delivery — a deterministic slow-mint window
inside which the lane fires a concurrent fresh purchase. Every later
watched POST passes straight through.

All logging carries a UTC wall-clock timestamp with milliseconds so the
driver can prove request ordering (replay held while the fresh
purchase's swap passes).

Usage: killerproxy-hold.py <listen-port> <mint-target> <watch-path> <marker-file> <hold-seconds>
  e.g. killerproxy-hold.py 8085 http://172.31.84.2:8085 /v1/swap /marker/kill.marker 14
"""
import hashlib
import http.server
import socket
import sys
import threading
import time
import urllib.request

listen_port = int(sys.argv[1])
target = sys.argv[2].rstrip("/")
watch_path = sys.argv[3]
marker = sys.argv[4]
hold_seconds = float(sys.argv[5])

armed = threading.Event()        # set by the first watched 200 (crash window)
released = threading.Event()     # set when <marker>.release appears
replay_arrived = threading.Event()  # first watched POST after release


def ts():
    now = time.time()
    return time.strftime("%H:%M:%S", time.gmtime(now)) + ".%03d" % (int(now * 1000) % 1000)


def watch_release():
    import os
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
        src = self.client_address[0]
        n = int(self.headers.get("content-length", 0) or 0)
        body = self.rfile.read(n) if n else None
        watched = self.command == "POST" and watch_path in self.path

        if watched:
            h = hashlib.sha256(body or b"").hexdigest()
            print(
                f"{ts()} hash: src={src} {self.command} {self.path} "
                f"body_sha256={h} len={len(body or b'')}",
                flush=True,
            )

        # --- crash window: stock killerproxy behavior, untouched ---
        if watched and not released.is_set():
            code, rb = forward(self.command, self.path, self.headers, body)
            if code == 200:
                first = not armed.is_set()
                armed.set()
                if first:
                    with open(marker, "w") as f:
                        f.write(self.path)
                    threading.Thread(target=watch_release, daemon=True).start()
                print(
                    f"{ts()} decision: src={src} SWALLOWED"
                    f"{' first(marker)' if first else ''}",
                    flush=True,
                )
                self.close_connection = True
                try:
                    self.connection.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                return
            # non-200 pre-release: stock proxy passes errors through
            print(f"{ts()} decision: src={src} PASSED code={code} (pre-release, non-200)", flush=True)
            self._respond(code, rb)
            return

        # --- post-release: hold the FIRST watched POST (the resume replay) ---
        if watched and not replay_arrived.is_set():
            replay_arrived.set()
            code, rb = forward(self.command, self.path, self.headers, body)
            with open(marker + ".hold", "w") as f:
                f.write(
                    f"{ts()} code={code} body_sha256="
                    f"{hashlib.sha256(body or b'').hexdigest()}\n"
                )
            print(
                f"{ts()} decision: src={src} HELD code={code} "
                f"hold={hold_seconds:g}s (mint answered; delivery delayed)",
                flush=True,
            )
            time.sleep(hold_seconds)
            print(f"{ts()} decision: src={src} HOLD-RELEASED code={code}", flush=True)
            self._respond(code, rb)
            return

        code, rb = forward(self.command, self.path, self.headers, body)
        if watched:
            print(
                f"{ts()} decision: src={src} PASSED code={code} released=True",
                flush=True,
            )
        self._respond(code, rb)

    def _respond(self, code, rb):
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(rb)))
        self.end_headers()
        self.wfile.write(rb)

    do_GET = do_POST = do_PUT = _relay

    def log_message(self, *a):
        pass


http.server.ThreadingHTTPServer(("0.0.0.0", listen_port), H).serve_forever()
