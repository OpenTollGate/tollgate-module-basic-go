#!/usr/bin/env python3
"""killerproxy-hash — lane-local observability variant of killerproxy.py.

SCRATCH-ADDITION rc1-771 (disclosed): control flow is byte-for-byte the
stock killer's (swallow the FIRST watched POST-200 response, then every
watched response, until <marker>.release); the ONLY change is logging:
each watched POST gets one line with the client IP and sha256 of the
request body, plus the decision taken. This lets the rel-verify lane
prove the boot-resume replay re-POSTs identical bytes (same hash before
the kill and after the restart) without altering lane semantics.

Usage: killerproxy-hash.py <listen-port> <mint-target> <watch-path> <marker-file>
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

armed = threading.Event()    # set by the first watched response
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
        watched = self.command == "POST" and watch_path in self.path

        if watched:
            h = hashlib.sha256(body or b"").hexdigest()
            print(f"hash: src={src} {self.command} {self.path} body_sha256={h} len={len(body or b'')}", flush=True)

        code, rb = forward(self.command, self.path, self.headers, body)

        if watched and code == 200 and not released.is_set():
            first = not armed.is_set()
            armed.set()
            if first:
                with open(marker, "w") as f:
                    f.write(self.path)
                threading.Thread(target=watch_release, daemon=True).start()
            print(f"decision: src={src} {self.path} SWALLOWED{' first(marker)' if first else ''}", flush=True)
            self.close_connection = True
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            return

        if watched:
            print(f"decision: src={src} {self.path} PASSED code={code} released={released.is_set()}", flush=True)
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(rb)))
        self.end_headers()
        self.wfile.write(rb)

    do_GET = do_POST = do_PUT = _relay

    def log_message(self, *a):
        pass


http.server.ThreadingHTTPServer(("0.0.0.0", listen_port), H).serve_forever()
