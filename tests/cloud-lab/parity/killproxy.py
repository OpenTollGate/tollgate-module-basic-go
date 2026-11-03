#!/usr/bin/env python3
"""killproxy — armable deterministic crash-window injection for the
wallet parity suite.

Sits between cdk-walletd and the mint. Passthrough until <arm-file>
appears; after arming, the FIRST watched response (POST whose path
matches, status 200 — i.e. the mint HAS processed and signed) writes
the marker and is never delivered; every subsequent watched response
is also swallowed (the wallet library retries; a one-shot swallow
would let the retry complete and defeat the injection) until
<marker>.release appears next to the marker. After release: full
passthrough — exactly what post-crash recovery needs.

Modelled on killerproxy.py (the #497 harness); the arm file is the
parity addition: funding traffic passes through unwatched... no —
funding swaps also pass, but only AFTER arming does swallowing start,
so tests arm immediately before the operation they want to kill.

Usage: killproxy.py <listen-port> <target-base> <watch-paths-csv> <marker-file> <arm-file>
"""
import http.server
import os
import socket
import sys
import threading
import time
import urllib.error
import urllib.request

listen_port = int(sys.argv[1])
target = sys.argv[2].rstrip("/")
watch_paths = [p.strip() for p in sys.argv[3].split(",") if p.strip()]
marker = sys.argv[4]
arm_file = sys.argv[5]


def watched(path):
    p = path.split("?")[0].rstrip("/")
    return any(p == w for w in watch_paths)

armed_flag = {"v": False}    # set lazily at request time (no polling race)
released = threading.Event()   # marker.release seen


def armed_now() -> bool:
    """Arm check at REQUEST time — a background watcher polled at 100ms
    granularity raced the melt POST (localhost latency < poll period)
    and let the first watched response slip through unarmed."""
    if armed_flag["v"]:
        return True
    if os.path.exists(arm_file):
        armed_flag["v"] = True
        print(f"killproxy: ARMED ({arm_file})", flush=True)
        return True
    return False


def watch_files():
    """Release watcher AND cycle resetter: when the marker (and its
    .release) are removed by the test fixture, the proxy rearms for the
    next case — one proxy process serves the whole suite."""
    while True:
        if not released.is_set() and os.path.exists(marker + ".release"):
            released.set()
            print("killproxy: RELEASED", flush=True)
        if (
            released.is_set()
            and armed_flag.get("triggered", False)
            and not os.path.exists(marker)
            and not os.path.exists(marker + ".release")
        ):
            armed_flag["v"] = False
            armed_flag["triggered"] = False
            released.clear()
            print("killproxy: RESET (cycle complete, rearmed)", flush=True)
        time.sleep(0.1)


def forward(method, path, headers, body):
    req = urllib.request.Request(
        target + path, data=body if body else None, method=method
    )
    for h in ("content-type",):
        if h in headers:
            req.add_header(h, headers[h])
    try:
        resp = urllib.request.urlopen(req, timeout=60)
        return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()


class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _relay(self):
        n = int(self.headers.get("content-length", 0) or 0)
        body = self.rfile.read(n) if n else None
        print(f"killproxy: {self.command} {self.path}", flush=True)
        code, rb = forward(self.command, self.path, self.headers, body)

        if (
            self.command == "POST"
            and watched(self.path)
            and code == 200
            and armed_now()
            and not released.is_set()
        ):
            first = not armed_flag.get("triggered", False)
            armed_flag["triggered"] = True
            if first:
                with open(marker, "w") as f:
                    f.write(f"{self.path}\n")
                print(f"killproxy: SWALLOWED first watched response "
                      f"({self.path}) — marker written", flush=True)
            # Swallow: the mint HAS answered; the caller must never see
            # it. Hang the connection.
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            self.close_connection = True
            return

        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(rb)))
        self.end_headers()
        self.wfile.write(rb)

    do_POST = _relay
    do_GET = _relay

    def log_message(self, format, *args):  # noqa: A002 - base-class signature
        pass


if __name__ == "__main__":
    threading.Thread(target=watch_files, daemon=True).start()
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", listen_port), H)
    print(f"killproxy: listening on 127.0.0.1:{listen_port} -> {target} "
          f"(watch {watch_paths})", flush=True)
    srv.serve_forever()
