#!/usr/bin/env python3
"""killerproxy-reqswallow — scenario (a) crash-injection proxy: pre-POST kill.

SCRATCH-ADDITION rc1-771 (disclosed): unlike the stock killer (which
forwards to the mint, swallows the FIRST watched 200 RESPONSE — the mint
HAS signed — and marks), this variant swallows the watched REQUEST from
one specific source IP (the tollgate) WITHOUT ever forwarding it to the
mint. The intent is journaled by the tollgate before the POST, the POST
arrives here, the marker is written, the request dies — the mint never
saw the swap. That lands the process death exactly between "intent
journaled" and "request sent" — the scenario-(a) clean-resume boundary.

Requests from any other source (the client container funding wallets,
cdk-cli, keyset polls) pass through untouched, so the funding phase is
unaffected. After <marker-file>.release appears next to the marker,
everything — including the tollgate's watched POSTs — passes through,
which is what the boot-time resume replay needs: the replay's re-POST
reaches the mint for the first time (fresh outputs, first exposure).

Usage: killerproxy-reqswallow.py <listen-port> <mint-target> <watch-path> <marker-file> <tollgate-ip>
"""
import hashlib
import http.server
import socket
import sys
import threading
import urllib.request

listen_port = int(sys.argv[1])
target = sys.argv[2].rstrip("/")
watch_path = sys.argv[3]
marker = sys.argv[4]
tollgate_ip = sys.argv[5] if len(sys.argv) > 5 else ""

armed = threading.Event()    # set by the first swallowed watched request
released = threading.Event()  # set when <marker>.release appears


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
        src = self.client_address[0]
        n = int(self.headers.get("content-length", 0) or 0)
        body = self.rfile.read(n) if n else None
        watched = (
            self.command == "POST"
            and watch_path in self.path
            and src == tollgate_ip
        )

        if watched:
            h = hashlib.sha256(body or b"").hexdigest()
            print(f"hash: src={src} {self.command} {self.path} body_sha256={h} len={len(body or b'')}", flush=True)

        if watched and not released.is_set():
            first = not armed.is_set()
            armed.set()
            if first:
                with open(marker, "w") as f:
                    f.write(self.path)
                threading.Thread(target=watch_release, daemon=True).start()
            # swallow the REQUEST: never forward, never respond. The mint
            # never sees this swap; the tollgate's connection resets.
            print(f"decision: src={src} {self.path} REQ-SWALLOWED{' first(marker)' if first else ''}", flush=True)
            self.close_connection = True
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            return

        if watched:
            print(f"decision: src={src} {self.path} PASSED-THROUGH released={released.is_set()}", flush=True)

        code, rb = forward(self.command, self.path, self.headers, body)
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(rb)))
        self.end_headers()
        self.wfile.write(rb)

    do_GET = do_POST = do_PUT = _relay

    def log_message(self, *a):
        pass


http.server.ThreadingHTTPServer(("0.0.0.0", listen_port), H).serve_forever()
