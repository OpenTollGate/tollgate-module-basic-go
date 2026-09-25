#!/usr/bin/env python3
"""Offline TollGate stub used by the harness self-test.

NOT a router simulator. It answers the exact HTTP surfaces the harness asserts,
so the harness can be run with no hardware at all -- which is what lets us prove
that individual checks actually go RED. A check that has never been seen red is
decoration, and a hardware-only suite can never be seen red on demand.

Scenario mutations (--scenario FILE, JSON) let the self-test break one thing at a
time and assert the matching check id flips to FAIL:

  portal_asset_byte   mutate one byte of a served portal asset
  portal_entry_missing drop the entry chunk from the served docroot
  stub_no_redirect    :STUB_PORT serves a 200 page with no location.replace
  stub_wrong_port     the stub's expression points at the wrong port
  stub_no_noscript    the stub loses its no-JS fallback
  captive_200         the captive port answers 200 (no enforcement)
  captive_no_redir    the captive 307 drops the redir=<original> parameter
  spa_on_stub_port     the SPA entry is ALSO served on the stub port
  root_kind           override kind:10021 (e.g. 9999)
  root_degraded       drop the price_per_step tags (degraded mode)
  whoami_sentinel     /whoami answers 00:00:00:00:00:00
  balance_active      /balance reports session_active=true
  balance_malformed   /balance loses its keys
  usage_bad           /usage answers something that is not used/allotment
  session_state       /session-state answers a real session-state shape
  luci_307_no_target  :LUCI_PORT 307s to a dead https URL
  ln_200              /ln-invoice answers 200 instead of the 400 poll
  ln_wrong_error      /ln-invoice 400s with a different error string
  empty_token_ok      POST / with an empty body returns 200 kind:1022 (bypass)
  post_reject_token   POST / with a NON-empty body is refused (400 kind:21023):
                      the box will not take a payment that carries a proof
  no_cors_preflight   OPTIONS loses Access-Control-Allow-Methods
  portal_no_root_el   splash.html loses id="root"
  portal_no_hash      the entry chunk name loses its content hash
  renew               the SECOND-purchase lane's session model:
                        "ok"     -> the re-purchase opens the gate: the probe path
                                    answers 204 (online) once a purchase is made
                        "stuck"  -> the reported hardware defect: the balance is
                                    restored (session_active true, allotment set)
                                    while the probe path keeps 307-ing to the
                                    splash, i.e. the gate stayed shut
                      plus active_first: the box already reports an ACTIVE session
                      before the lane buys (drives paid2:first-allotment-spent red)

stdlib only.
"""

import argparse
import json
import os
import re
import socket
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCENARIO = {}
PORTS = {}
RATE_STATE = {}
# Live state of the stub's own session model, so a case can drive a SEQUENCE
# (buy -> spend -> buy again) instead of a fixed answer. Only the renew lane
# reads it; every other scenario leaves it at zero and is unaffected.
SESSION = {"purchases": 0}


def mut(name, default=False):
    return SCENARIO.get(name, default)


def renew_mode():
    """-> "" (off) | "ok" | "stuck"  -- the second-purchase model."""
    value = mut("renew")
    return value if value in ("ok", "stuck") else ""


def renew_session_active():
    """What /balance and /session-state answer in the renew model.

    active_first models the precondition the lane must refuse: a session that is
    still live, so a "second purchase" would be a renewal of an open gate.
    Otherwise the session becomes active exactly when the lane's POST lands --
    which is the reported symptom (the balance IS restored)."""
    if not renew_mode():
        return False
    if mut("active_first"):
        return True
    return SESSION["purchases"] >= 1


def gate_open():
    """Whether the customer's traffic is past the gate in the stub's model.

    "ok"    -> the re-purchase really opened it (probe path answers 204)
    "stuck" -> the hardware defect: the gate never re-opened (probe path keeps
               307-ing to the splash), while renew_session_active() says the
               balance came back. The two must be able to disagree -- that
               disagreement IS the bug this lane catches."""
    return renew_mode() == "ok" and SESSION["purchases"] >= 1


# The paths an OS uses to decide "am I behind a captive portal?".
PROBE_PATHS = ("generate_204", "hotspot-detect.html", "connecttest.txt",
               "success.txt", "ncsi.txt")


def is_probe_path(path):
    return any(fragment in path for fragment in PROBE_PATHS)


class Base(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_name = "rhp-stub"

    def log_message(self, format, *args):  # noqa: A002 - matches BaseHTTPRequestHandler
        pass

    def _send(self, code, body, ctype="text/html; charset=utf-8", extra=None):
        if isinstance(body, str):
            body = body.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def _static(self, docroot, path, default="index.html"):
        rel = path.lstrip("/") or default
        full = os.path.join(docroot, rel)
        if not os.path.isfile(full):
            return self._send(404, "<h1>Error 404 - Not Found</h1>")
        data = open(full, "rb").read()
        if os.path.basename(full) == "splash.html" and not mut("portal_asset_byte"):
            if mut("portal_no_root_el"):
                data = data.replace(b'id="root"', b'id="app"')
            if mut("portal_no_hash"):
                # strip the content hash off EVERY asset ref: cache-busting gone
                data = re.sub(rb"/assets/([A-Za-z0-9_.]+?)-[A-Za-z0-9_-]{8}\.(js|css)",
                              rb"/assets/\1.\2", data)
        if mut("portal_asset_byte") and os.path.basename(full) not in ("splash.html",):
            data = data[:-1] + bytes([(data[-1] + 1) % 256])
        ctype = "text/html; charset=utf-8"
        if full.endswith(".js"):
            ctype = "application/javascript"
        elif full.endswith(".css"):
            ctype = "text/css"
        elif full.endswith(".json"):
            ctype = "application/json"
        elif full.endswith(".png") or full.endswith(".ico"):
            ctype = "image/png"
        self._send(200, data, ctype)


class PortalHandler(Base):
    """:PORTAL_PORT -- the SPA docroot."""

    def do_GET(self):
        if mut("portal_entry_missing") and "-" in self.path.split("/")[-1] and self.path.endswith(".js"):
            return self._send(404, "<h1>Error 404 - Not Found</h1>")
        if self.path in ("/", ""):
            # real box: 403 (no index.html in this docroot)
            return self._send(403, "<h1>Forbidden</h1>You don't have permission to access / on this server.")
        html = self._static(PORTS["portal_docroot"], self.path.split("?")[0], default="splash.html")
        return html


class StubHandler(Base):
    """:STUB_PORT -- the cache-bust stub."""

    def do_GET(self):
        if self.path.startswith("/assets/"):
            # The stub docroot holds ONLY the stub page on the real box.
            if mut("spa_on_stub_port"):
                return self._static(PORTS["portal_docroot"], self.path.split("?")[0], default="splash.html")
            return self._send(404, "<h1>Error 404 - Not Found</h1>")
        noscript = ""
        if not mut("stub_no_noscript"):
            noscript = ('<noscript>\n<p><a href="http://%s:%d/splash.html?_cb=1">Continue to the '
                        'TollGate portal</a></p>\n</noscript>' % (PORTS["host"], PORTS["portal"]))
        if mut("stub_no_redirect"):
            return self._send(200, "<!DOCTYPE html><html><body><p>Portal temporarily unavailable.</p>%s</body></html>" % noscript)
        port = 9999 if mut("stub_wrong_port") else PORTS["portal"]
        body = (
            '<!DOCTYPE html>\n<html>\n<head>\n<meta charset="utf-8">\n'
            '<title>TollGate Portal</title>\n</head>\n<body>\n'
            '<p>Redirecting to the TollGate portal...</p>\n<script>\n'
            "location.replace('http://' + location.hostname + ':%d/splash.html?_cb=' + "
            "Date.now() + location.search.replace('?', '&'));\n"
            '</script>\n%s\n</body>\n</html>\n' % (port, noscript))
        return self._send(200, body)


class AdminHandler(Base):
    """:ADMIN_PORT -- the admin SPA docroot."""

    def do_GET(self):
        if mut("admin_entry_missing") and self.path.split("?")[0] in ("/", "/index.html"):
            return self._send(404, "<h1>Error 404 - Not Found</h1>")
        return self._static(PORTS["admin_docroot"], self.path.split("?")[0])


class ApiHandler(Base):
    """:API_PORT -- the module API."""

    def _json(self, code, obj, ctype="application/json"):
        return self._send(code, json.dumps(obj), ctype)

    def _root_doc(self):
        """GET / -- also what /session-state falls through to on a build that
        does not ship the endpoint (byte-identical is the whole point)."""
        kind = mut("root_kind") or 10021
        tags = [["metric", "bytes"], ["step_size", "22020096"], ["tips", "1", "2"]]
        if not mut("root_degraded"):
            tags.append(["price_per_step", "cashu", "1", "sat", "https://mint.stub.invalid", "0"])
        return self._json(200, {"kind": kind, "id": "stub-id", "pubkey": "stub-pubkey",
                                "created_at": 0, "tags": tags})

    def do_OPTIONS(self):
        extra = {}
        if not mut("no_cors_preflight"):
            extra["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS"
            extra["Access-Control-Allow-Headers"] = "Content-Type, Authorization"
        return self._send(200, "", "text/plain", extra)

    def _throttle(self):
        """The module rate-limits its ROOT handler per client IP -- and
        /session-state falls through to that handler on a build that does not
        ship the endpoint, so it shares the same budget.

        Scenarios:
          {"api_429_first": N}       429 the first N root hits, then serve
          {"api_429_always": true}  429 every root hit
          {"api_429_retry_after": S}  Retry-After to advertise (default 1s)
        """
        first = SCENARIO.get("api_429_first")
        if not mut("api_429_always") and not first:
            return False
        RATE_STATE["root_hits"] = RATE_STATE.get("root_hits", 0) + 1
        if not mut("api_429_always") and RATE_STATE["root_hits"] > int(first or 0):
            return False
        self._send(429, '{"error":"rate limit exceeded"}', "application/json",
                   {"Retry-After": str(SCENARIO.get("api_429_retry_after", 1))})
        return True

    def do_GET(self):
        path = self.path.split("?")[0]
        if path in ("/", "/session-state") and self._throttle():
            return
        if path == "/":
            return self._root_doc()
        if path == "/whoami":
            mac = "00:00:00:00:00:00" if mut("whoami_sentinel") else "aa:bb:cc:dd:ee:ff"
            return self._send(200, "mac=%s\n" % mac, "text/plain; charset=utf-8")
        if path == "/balance":
            if mut("balance_malformed"):
                return self._json(200, {"status": 1})
            if renew_mode():
                active = renew_session_active()
                allotment = 22020096 if active else 0
                return self._json(200, {"status": 1, "session_active": active, "metric": "bytes",
                                        "usage": 0, "allotment": allotment,
                                        "remaining": allotment, "start_time": 0})
            return self._json(200, {"status": 1, "session_active": bool(mut("balance_active")),
                                    "usage": 0, "allotment": 0, "remaining": 0})
        if path == "/usage":
            return self._send(200, "nonsense" if mut("usage_bad") else "-1/-1",
                              "text/plain; charset=utf-8")
        if path == "/session-state":
            if renew_mode():
                # In the renew model the endpoint that exists to tell a
                # first-time visitor from an exhausted customer answers the
                # stateful value, so the lane reads its precondition here.
                active = renew_session_active()
                allotment = 22020096 if active else 0
                return self._json(200, {"status": 1, "session_active": active,
                                        "state": "active" if active else "expired",
                                        "remaining": allotment, "allotment": allotment})
            if mut("session_state"):
                return self._json(200, {"session_active": False, "remaining": 0, "allotment": 0})
            # pre-#541 behaviour: the mux falls through to the root handler
            # -> BYTE-IDENTICAL to GET /, which is what the harness detects
            return self._root_doc()
        if path == "/identity":
            return self._json(200, {"npub": "npub1stubstubstub", "ipv4": "100.64.0.1",
                                    "macs": {"br-lan": "aa:bb:cc:dd:ee:ff"}})
        if path == "/ln-invoice":
            if mut("ln_200"):
                return self._json(200, {"status": 1, "quote": "", "state": "", "access_granted": False})
            err = "quote is missing" if mut("ln_wrong_error") else "quote is required"
            return self._json(400, {"status": 0, "quote": "", "mint_url": "", "amount": 0,
                                    "state": "", "access_granted": False, "error": err})
        return self._send(404, "<h1>Error 404 - Not Found</h1>")

    def do_POST(self):
        if self._throttle():
            return
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""
        if mut("empty_token_ok"):
            return self._json(200, {"kind": 1022, "id": "stub-session", "content": "session granted"})
        if not body:
            return self._json(400, {"kind": 21023, "id": "stub-notice",
                                    "content": "No payment tag found in event"})
        if mut("post_reject_token"):
            return self._json(400, {"kind": 21023, "id": "stub-notice",
                                    "content": "token could not be redeemed"})
        # a real token would be redeemed by the module; the stub only reports what
        # the paid lane expects from a successful redemption. The purchase counter
        # backs the renew model (buy -> spend -> buy again).
        SESSION["purchases"] += 1
        return self._json(200, {"kind": 1022, "id": "stub-session", "content": "session granted"})


class CaptiveHandler(Base):
    """:CAPTIVE_PORT -- nodogsplash pre-auth interception."""

    def do_GET(self):
        # The OS captive-portal probes, arrived at through the customer's data
        # path. On hardware this URL is a real public endpoint and NoDogSplash
        # either intercepts it (307 to the splash) or lets it through; the stub
        # plays both roles on this one port, which is what lets the self-test hold
        # the two client-visible outcomes apart. Only the renew model's "ok"
        # branch opens the gate, and only after a purchase.
        if is_probe_path(self.path) and gate_open():
            return self._send(204, "")
        if mut("captive_200"):
            return self._send(200, "<html><body>no enforcement here</body></html>")
        # NOTE: build these with concatenation, never %-formatting: the encoded
        # URL contains literal '%3a' etc. and Python's % operator chokes on them
        # (the handler then dies mid-response and the client sees a bare 000).
        loc = "http://" + PORTS["host"] + ":" + str(PORTS["stub"]) + "/splash.html"
        if not mut("captive_no_redir"):
            host_bits = PORTS["host"] + (":" + str(PORTS["captive"]) if PORTS["captive"] != 80 else "")
            original = ("http%3a%2f%2f" + host_bits
                        + self.path.replace("/", "%2f").replace("?", "%3f").replace("=", "%3d"))
            loc += "?redir=" + original
        return self._send(307, "<html><body><a href='" + loc + "'>continue</a></body></html>",
                          "text/html; charset=utf-8", {"Location": loc})


class LuciHandler(Base):
    """:LUCI_PORT -- LuCI http, 307 to https."""

    def do_GET(self):
        port = 1 if mut("luci_307_no_target") else PORTS["tls"]
        loc = "https://%s:%d/" % (PORTS["host"], port)
        return self._send(307, "", "text/plain", {"Location": loc})


class TlsHandler(Base):
    """:TLS_PORT -- the target of the LuCI redirect (plain HTTP here; the
    harness reaches it with curl -k, so a self-signed TLS hop is what a real
    router offers)."""

    def do_GET(self):
        return self._send(200, "<html><body>LuCI</body></html>")


class BannerHandler(Base):
    """:SSH_PORT -- a liveness-only TCP listener (no protocol)."""

    def do_GET(self):
        self._send(200, "")


def serve(handler, port, tls=False):
    httpd = ThreadingHTTPServer(("0.0.0.0", port), handler)
    if tls:
        import ssl
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(PORTS["cert"], PORTS["key"])
        httpd.socket = ctx.wrap_socket(httpd.socket, server_side=True)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd


def ssh_banner(port):
    """A bare TCP listener: proves net:tcp-<port> without pretending to be SSH."""
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("0.0.0.0", port))
    srv.listen(16)

    def loop():
        while True:
            try:
                conn, _addr = srv.accept()
                conn.sendall(b"SSH-2.0-rhp-stub\r\n")
                conn.close()
            except OSError:
                return

    threading.Thread(target=loop, daemon=True).start()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--scenario", default="")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--portal-docroot", required=True)
    ap.add_argument("--admin-docroot", required=True)
    ap.add_argument("--portal-port", type=int, required=True)
    ap.add_argument("--stub-port", type=int, required=True)
    ap.add_argument("--api-port", type=int, required=True)
    ap.add_argument("--admin-port", type=int, required=True)
    ap.add_argument("--luci-port", type=int, required=True)
    ap.add_argument("--captive-port", type=int, required=True)
    ap.add_argument("--tls-port", type=int, required=True)
    ap.add_argument("--ssh-port", type=int, required=True)
    ap.add_argument("--cert", default="")
    ap.add_argument("--key", default="")
    args = ap.parse_args()

    global SCENARIO
    if args.scenario:
        SCENARIO = json.load(open(args.scenario))

    PORTS.update({"host": args.host, "portal": args.portal_port, "stub": args.stub_port,
                  "api": args.api_port, "admin": args.admin_port, "luci": args.luci_port,
                  "captive": args.captive_port, "tls": args.tls_port,
                  "portal_docroot": args.portal_docroot, "admin_docroot": args.admin_docroot,
                  "cert": args.cert, "key": args.key})

    serve(PortalHandler, args.portal_port)
    serve(StubHandler, args.stub_port)
    serve(ApiHandler, args.api_port)
    serve(AdminHandler, args.admin_port)
    serve(CaptiveHandler, args.captive_port)
    serve(LuciHandler, args.luci_port)
    serve(TlsHandler, args.tls_port, tls=bool(args.cert))
    ssh_banner(args.ssh_port)

    print("stub-ready", flush=True)
    try:
        threading.Event().wait()
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
