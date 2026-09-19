#!/usr/bin/env python3
"""lib/mock-mint.py — a deliberately controllable Cashu mint endpoint.

Speaks just enough of the mint HTTP surface for the wallet's receive path
(/v1/info, /v1/keysets, /v1/keys, /v1/keys/<id>, /v1/swap) and can be switched
between failure modes at runtime, so that fault injection is hermetic and
re-runnable without a real mint.

Modes (POST /_mode/<mode> to switch, GET /_mode to read):
  ok            well-formed but EMPTY keyset list (enough to reach the parse
                path; a full happy-path swap needs real mint keys and is
                therefore out of scope here)
  http-500      every request answers 500
  malformed-json  every request answers 200 with unparseable bytes
  truncated     every request answers 200 with a body cut mid-JSON
  slow          accepts the connection and never answers (no response)
  blackhole     closes the connection immediately without a response
  bad-state     /v1/keysets is fine, /v1/swap answers 200 with a NUT-07-ish
                body that contradicts the local record

Every request is logged to stdout as:  <epoch> <method> <path> mode=<mode>
so the caller can capture exactly what the wallet asked for.

Usage:
  python3 mock-mint.py [--port 18080] [--host 0.0.0.0]
Control:  curl -s -XPOST http://127.0.0.1:18080/_mode/http-500
Stop:     curl -s -XPOST http://127.0.0.1:18080/_quit
"""
import argparse
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODE = "ok"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # keep stdout clean for the request log
        pass

    def _log(self, verb):
        sys.stdout.write("%.3f %s %s mode=%s\n" % (time.time(), verb, self.path, MODE))
        sys.stdout.flush()

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def _send(self, code, payload, ctype="application/json"):
        if isinstance(payload, (dict, list)):
            payload = json.dumps(payload).encode()
        elif isinstance(payload, str):
            payload = payload.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def _answer(self, ok_payload):
        """Apply the current mode to a would-be 200 response."""
        global MODE
        if MODE == "http-500":
            self._send(500, {"detail": "injected server error"})
        elif MODE == "malformed-json":
            self._send(200, b"this is not json at all {{{")
        elif MODE == "truncated":
            raw = json.dumps(ok_payload).encode()
            self._send(200, raw[: max(1, len(raw) // 2)])
        elif MODE == "slow":
            time.sleep(120)  # never answers within any sane client timeout
        elif MODE == "blackhole":
            self.close_connection = True  # drop without a response
        else:  # ok / bad-state
            self._send(200, ok_payload)

    def do_GET(self):
        global MODE
        self._log("GET")
        if self.path == "/_mode":
            self._send(200, {"mode": MODE})
        elif self.path.startswith("/_mode/"):
            MODE = self.path.rsplit("/", 1)[-1]
            self._send(200, {"mode": MODE})
        elif self.path == "/v1/info":
            self._answer({"name": "baseline mock mint", "version": "mock-1",
                          "nuts": {"4": {"methods": [{"method": "bolt11", "unit": "sat"}]},
                                   "5": {"methods": [{"method": "bolt11", "unit": "sat"}]}}})
        elif self.path == "/v1/keysets":
            self._answer({"keysets": []})
        elif self.path.startswith("/v1/keys"):
            self._answer({"keysets": []})
        else:
            self._answer({})

    def do_POST(self):
        global MODE
        self._log("POST")
        self._body()
        if self.path == "/_quit":
            self._send(200, {"bye": True})
            self.server.shutdown_requested = True
        elif self.path.startswith("/_mode/"):
            MODE = self.path.rsplit("/", 1)[-1]
            self._send(200, {"mode": MODE})
        elif self.path.startswith("/v1/swap"):
            if MODE in ("ok", "bad-state") and MODE == "bad-state":
                # 200, but a NUT-05-style body claiming a state the client did
                # not ask for: exercises "success-looking body that is a lie".
                self._send(200, {"signatures": [], "state": "SPENT"})
            else:
                self._answer({"signatures": []})
        else:
            self._answer({})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18080)
    ap.add_argument("--host", default="0.0.0.0")
    a = ap.parse_args()
    srv = ThreadingHTTPServer((a.host, a.port), Handler)
    srv.daemon_threads = True
    sys.stdout.write("mock-mint listening on %s:%d mode=%s\n" % (a.host, a.port, MODE))
    sys.stdout.flush()
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
