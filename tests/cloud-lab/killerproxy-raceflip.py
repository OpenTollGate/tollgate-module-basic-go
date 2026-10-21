#!/usr/bin/env python3
"""killerproxy-raceflip — intervention proxy for the rc1-793 (a)-finding.

SCRATCH-ADDITION rc1-793 (disclosed). Crash window is byte-for-byte
killerproxy-reqswallow's: watched POST requests from the tollgate IP are
swallowed WITHOUT forwarding (the mint never sees the swap), the first
writes the marker, until <marker-file>.release appears.

The intervention is post-release: the FIRST POST /v1/checkstate from the
tollgate IP is held <hold-seconds> before being forwarded — everything
else, including the boot-resume replay's /v1/swap, passes immediately.
At boot both the merchant's NUT-07 reconcile (checkstate) and
TollWallet.New's background replay fire unsequenced; holding checkstate
guarantees the replay reaches the mint first, so the reconcile must find
the proofs SPENT. If the crashed purchase then resolves `owed` (instead
of the `abandoned` observed when checkstate wins), the ordering race is
proven to be the only variable — the fix direction validated
empirically.

Usage: killerproxy-raceflip.py <listen-port> <mint-target> <watch-path> <marker-file> <tollgate-ip>[ <hold-seconds>]
  hold-seconds defaults to 6.
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
extra = (sys.argv[5] + " " + (sys.argv[6] if len(sys.argv) > 6 else "")).split()
tollgate_ip = extra[0] if extra else ""
hold_seconds = float(extra[1]) if len(extra) > 1 else 6.0

armed = threading.Event()    # set by the first swallowed watched request
released = threading.Event()  # set when <marker>.release appears
checkstate_held = threading.Event()  # first post-release checkstate


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
        watched = (
            self.command == "POST"
            and watch_path in self.path
            and src == tollgate_ip
        )
        from_tollgate = src == tollgate_ip

        if watched:
            h = hashlib.sha256(body or b"").hexdigest()
            print(f"{ts()} hash: src={src} {self.command} {self.path} body_sha256={h} len={len(body or b'')}", flush=True)

        if watched and not released.is_set():
            first = not armed.is_set()
            armed.set()
            if first:
                with open(marker, "w") as f:
                    f.write(self.path)
                threading.Thread(target=watch_release, daemon=True).start()
            print(f"{ts()} decision: src={src} {self.path} REQ-SWALLOWED{' first(marker)' if first else ''}", flush=True)
            self.close_connection = True
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            return

        if (
            from_tollgate
            and self.command == "POST"
            and "/v1/checkstate" in self.path
            and released.is_set()
            and not checkstate_held.is_set()
        ):
            checkstate_held.set()
            print(f"{ts()} decision: src={src} {self.path} CHECKSTATE-HELD {hold_seconds:g}s (replay must reach the mint first)", flush=True)
            time.sleep(hold_seconds)
            code, rb = forward(self.command, self.path, self.headers, body)
            print(f"{ts()} decision: src={src} {self.path} CHECKSTATE-FORWARDED code={code}", flush=True)
            self._respond(code, rb)
            return

        if watched:
            print(f"{ts()} decision: src={src} {self.path} PASSED-THROUGH released={released.is_set()}", flush=True)

        code, rb = forward(self.command, self.path, self.headers, body)
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
