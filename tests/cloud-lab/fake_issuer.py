#!/usr/bin/env python3
"""A fake LNURLcash issuer for the cloud-lab lane.

This speaks the ISSUER side of LUD-03 and nothing else. It exists so the gate's
redemption path can be exercised end to end without a real wallet host, and so
each failure mode can be planted deliberately instead of waited for.

It is not a mint and holds no sats. "Paying" the invoice means: record the quote
id, and report success to the gate. Whether the gate then grants access is
decided by the gate's OWN settlement mint, which this process cannot influence —
that separation is the property the lane is here to test.

Modes (mutated at runtime via /_control):
  ok          callback returns {"status":"OK"} for the first presentation only
  spent       410 Gone with {"status":"ERROR","reason":"note already spent"}
  offline     HTTP 503, no body
  slow        sleeps SLOW_SECONDS then answers OK
  wrongpr     answers OK but never tells the mint anything (the melt did not
              happen) — the gate must NOT grant
  forever     answers OK on every presentation (a broken issuer that double-pays)

Endpoints:
  GET  /withdraw?k1=...        LUD-03 withdrawRequest  -> {callback, k1, maxWithdrawable, ...}
  GET  /callback?k1=..&pr=..   LUD-03 callback         -> the outcome above
  POST /_control               {"mode": "...", "max_msat": N} to plant a mode
  GET  /_state                 what happened, for assertions
"""

import hashlib
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

SLOW_SECONDS = float(os.environ.get("FAKE_ISSUER_SLOW_SECONDS", "3"))

LOCK = threading.Lock()
STATE = {
    "mode": "ok",
    "max_msat": int(os.environ.get("FAKE_ISSUER_MAX_MSAT", "21000")),
    # k1 -> list of quote ids it was presented with
    "presentations": {},
    # every callback we saw, for assertions about ordering and retries
    "callbacks": [],
}


def k1_of(note_secret: str) -> str:
    """Stable fake k1 for a note secret, so tests can address one note."""
    return hashlib.sha256(note_secret.encode()).hexdigest()[:32]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, format, *args):  # keep the lane's output readable
        pass

    def _send(self, code: int, payload=None, raw: bytes = b"", ctype="application/json"):
        body = raw if raw else json.dumps(payload or {}).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        parsed = urlparse(self.path)
        q = parse_qs(parsed.query)

        if parsed.path == "/withdraw":
            k1 = (q.get("k1") or [""])[0]
            if not k1:
                return self._send(400, {"status": "ERROR", "reason": "no k1"})
            with LOCK:
                mode = STATE["mode"]
                max_msat = STATE["max_msat"]
            if mode == "offline":
                return self._send(503, {"status": "ERROR", "reason": "issuer offline"})
            if mode == "spent":
                return self._send(410, {"status": "ERROR", "reason": "note already spent"})
            # The callback URL must be the SAME HOST the gate dialled, or the gate
            # is right to refuse it. The test driver sets FAKE_ISSUER_PUBLIC_HOST.
            host = os.environ.get("FAKE_ISSUER_PUBLIC_HOST", self.headers.get("Host", "issuer"))
            return self._send(200, {
                "tag": "withdrawRequest",
                "callback": f"http://{host}/callback",
                "k1": k1,
                "maxWithdrawable": max_msat,
                "minWithdrawable": 1000,
                "defaultDescription": "fake issuer note",
            })

        if parsed.path == "/callback":
            k1 = (q.get("k1") or [""])[0]
            pr = (q.get("pr") or [""])[0]
            with LOCK:
                mode = STATE["mode"]
                STATE["callbacks"].append({"k1": k1, "pr": pr, "mode": mode, "t": time.time()})
                seen = STATE["presentations"].setdefault(k1, [])
                first = len(seen) == 0
                seen.append(pr)
            if mode == "offline":
                return self._send(503, {"status": "ERROR", "reason": "issuer offline"})
            if mode == "spent":
                return self._send(410, {"status": "ERROR", "reason": "note already spent"})
            if mode == "slow":
                time.sleep(SLOW_SECONDS)
                return self._send(200, {"status": "OK"})
            if not pr:
                return self._send(400, {"status": "ERROR", "reason": "no invoice"})
            if mode == "forever":
                # A broken issuer: pays on every presentation, including replays.
                return self._send(200, {"status": "OK"})
            # default "ok" and "wrongpr": single-use. wrongpr is distinguishable
            # only at the mint, which is the point — the gate must not grant on it.
            if not first:
                return self._send(410, {"status": "ERROR", "reason": "note already spent"})
            return self._send(200, {"status": "OK"})

        if parsed.path == "/_state":
            with LOCK:
                return self._send(200, STATE)

        return self._send(404, {"status": "ERROR", "reason": "not found"})

    def do_POST(self):
        parsed = urlparse(self.path)
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b"{}"
        if parsed.path == "/_control":
            try:
                patch = json.loads(raw or b"{}")
            except json.JSONDecodeError:
                return self._send(400, {"status": "ERROR", "reason": "bad json"})
            with LOCK:
                if "mode" in patch:
                    STATE["mode"] = patch["mode"]
                if "max_msat" in patch:
                    STATE["max_msat"] = int(patch["max_msat"])
                if patch.get("reset"):
                    STATE["presentations"] = {}
                    STATE["callbacks"] = []
                return self._send(200, STATE)
        return self._send(404, {"status": "ERROR", "reason": "not found"})


def main():
    port = int(os.environ.get("FAKE_ISSUER_PORT", "8099"))
    srv = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    print(f"fake issuer listening on :{port}", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
