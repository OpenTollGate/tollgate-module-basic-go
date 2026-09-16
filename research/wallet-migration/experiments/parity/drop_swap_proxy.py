#!/usr/bin/env python3
"""Transparent HTTP proxy that can drop the *response* to one /v1/swap.

Why this exists
---------------
T15's ``swap_proof_loss`` case (fork commit ``7dc430b``) needs the wallet to be
interrupted in the one window that actually risks funds: **the mint has already
accepted the swap (spent the inputs, issued the outputs) but the wallet never
learns the new proofs.** A local fakewallet mint is far too fast to land a kill
inside that window by timing alone, so we widen it deterministically at the
network layer instead.

The local ``cdk-mintd`` serves **plain HTTP**, so no TLS interception is
needed: this proxy forwards everything, and when an *armed* marker file exists
it delivers the next POST whose path is ``/v1/swap`` to the mint but throws the
response away and closes the client connection. The armed marker is one-shot,
so later swaps pass through normally.

Usage
-----
    drop_swap_proxy.py --listen-port 8086 --target-port 8085 \
        --armed-file /path/to/arm

Arm by ``touch``ing the armed file; the proxy removes it when it fires.
Every request is logged to stderr as ``[proxy] METHOD PATH -> STATUS`` and the
one dropped response as ``[proxy] DROPPED ...``.
"""

import argparse
import http.client
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

_HOP_BY_HOP = {
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailers",
    "transfer-encoding",
    "upgrade",
    "content-length",
    "host",
}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    target_host = "127.0.0.1"
    target_port = 8085
    armed_file = None
    lock = threading.Lock()

    def log_message(self, *args):  # silence the default access log
        pass

    def _take_armed(self):
        if not self.armed_file or not os.path.exists(self.armed_file):
            return False
        with self.lock:
            if os.path.exists(self.armed_file):
                os.remove(self.armed_file)
                return True
        return False

    def _forward(self, method):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else None
        headers = {
            k: v for k, v in self.headers.items() if k.lower() not in _HOP_BY_HOP
        }

        conn = http.client.HTTPConnection(
            self.target_host, self.target_port, timeout=30
        )
        try:
            conn.request(method, self.path, body=body, headers=headers)
            resp = conn.getresponse()
            data = resp.read()
        except Exception as exc:  # upstream unreachable / timed out
            sys.stderr.write(
                f"[proxy] upstream error {method} {self.path}: {exc}\n"
            )
            sys.stderr.flush()
            self.send_error(502)
            return
        finally:
            conn.close()

        path = self.path.split("?", 1)[0]
        if path.endswith("/swap") and self._take_armed():
            sys.stderr.write(
                f"[proxy] DROPPED {resp.status} for {method} {self.path} "
                f"({len(data)}B) -- mint processed it, client will not see it\n"
            )
            sys.stderr.flush()
            # Close with no response: the client sees a broken connection, so it
            # cannot learn the freshly-issued proofs.
            self.close_connection = True
            try:
                self.connection.close()
            except OSError:
                pass
            return

        self.send_response(resp.status)
        for k, v in resp.getheaders():
            if k.lower() in _HOP_BY_HOP:
                continue
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if method != "HEAD":
            self.wfile.write(data)
        sys.stderr.write(f"[proxy] {method} {self.path} -> {resp.status}\n")
        sys.stderr.flush()

    def do_GET(self):
        self._forward("GET")

    def do_POST(self):
        self._forward("POST")

    def do_HEAD(self):
        self._forward("HEAD")

    def do_DELETE(self):
        self._forward("DELETE")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--listen-port", type=int, default=8086)
    ap.add_argument("--target-host", default="127.0.0.1")
    ap.add_argument("--target-port", type=int, default=8085)
    ap.add_argument("--armed-file", required=True)
    a = ap.parse_args()

    Handler.target_host = a.target_host
    Handler.target_port = a.target_port
    Handler.armed_file = a.armed_file

    srv = ThreadingHTTPServer(("127.0.0.1", a.listen_port), Handler)
    sys.stderr.write(
        f"[proxy] listening 127.0.0.1:{a.listen_port} -> "
        f"{a.target_host}:{a.target_port} (armed-file={a.armed_file})\n"
    )
    sys.stderr.flush()
    srv.serve_forever()


if __name__ == "__main__":
    main()
