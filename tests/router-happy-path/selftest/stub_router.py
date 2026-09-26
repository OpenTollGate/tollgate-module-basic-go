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
  luci_307_to_alt     :LUCI_PORT 307s to a LIVE https port that is NOT :TLS_PORT
                      (:ALT_PORT, a port the harness never sweeps). This is the
                      decoy for the credit rule: the "LuCI target answered 200"
                      check really does PASS, but against a different port, so a
                      credit rule that asks "did some check PASS for this id"
                      demotes a dead :TLS_PORT to a WARNING while the rule that
                      credits the port the request LANDED on keeps it FAIL.
  ln_200              /ln-invoice answers 200 instead of the 400 poll
  ln_wrong_error      /ln-invoice 400s with a different error string
  empty_token_ok      POST / with an empty body returns 200 kind:1022 (bypass)
  no_cors_preflight   OPTIONS loses Access-Control-Allow-Methods
  identity_silent     the API stops reporting the ?mac= claim it ignored (the
                      module goes back to ignoring the parameter SILENTLY, which
                      is how a rig reads 'the gate never opened')
  portal_no_root_el   splash.html loses id="root"
  portal_no_hash      the entry chunk name loses its content hash

Liveness/timing scenarios (the section-0 TCP burst):
  unbound_ports       a list of surfaces whose LISTENER IS NEVER BOUND, i.e. the
                      port is genuinely dead for the whole run ("admin", "tls",
                      "ssh", "portal", "stub", "api", "luci", "captive").
                      Real case: :8090 as a br-lan client sees it (the #566 guard
                      blocks it) -- a dead port, not a slow one.
  ssh_late_bind_s     bind the :SSH_PORT listener this many seconds after start.
                      Real case: the first connect burst races the box's own
                      convergence, so the first attempt is refused and a LATER
                      attempt answers. Deterministic: the harness's first attempt
                      happens ~0.2 s after stub-ready, so 1.0 s with a 3 s backoff
                      means attempt 1 is refused and attempt 2 answers.
  admin_bind_on_first_http
                      bind the :ADMIN_PORT listener on the first HTTP request the
                      stub handles. Deterministic version of "dead at preflight
                      (TCP-only), alive for every check after it": the burst runs
                      before any HTTP, so :ADMIN is refused there and answers for
                      every later phase.

stdlib only.
"""

import argparse
import json
import os
import re
import socket
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCENARIO = {}
PORTS = {}
RATE_STATE = {}
LATE_BIND_LOCK = threading.Lock()


def mut(name, default=False):
    return SCENARIO.get(name, default)


def unbound(name):
    """True when this surface's listener must never be bound (a genuinely dead
    port for the whole run -- e.g. :8090 as a br-lan client sees it)."""
    return name in (SCENARIO.get("unbound_ports") or [])


def late_bind_admin():
    """--scenario {"admin_bind_on_first_http": true}: bind the admin listener the
    first time any HTTP request is handled.

    The harness's section-0 liveness burst is TCP-only and runs before any HTTP,
    so this is a DETERMINISTIC stand-in for the real bench behaviour that matters:
    the port is refused during the burst and answers for every phase after it.
    The bind happens synchronously, before the triggering request is answered, so
    no later check can race the listener into existence.
    """
    if not SCENARIO.get("admin_bind_on_first_http") or unbound("admin"):
        return
    with LATE_BIND_LOCK:
        if PORTS.get("admin_bound"):
            return
        PORTS["admin_bound"] = True
    serve(AdminHandler, PORTS["admin"])


class Base(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_name = "rhp-stub"

    def log_message(self, format, *args):  # noqa: A002 - matches BaseHTTPRequestHandler
        pass

    def parse_request(self):  # every method, every handler: see late_bind_admin()
        late_bind_admin()
        return super().parse_request()

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

    def _json(self, code, obj, ctype="application/json", extra=None):
        return self._send(code, json.dumps(obj), ctype, extra)

    # --- the socket-identity contract (see docs/operator-guide.md) --------
    # Every client-scoped route answers for the client at the other end of the
    # SOCKET, and says which client that was: X-TollGate-Client-MAC. A `?mac=`
    # the caller sent is a claim the module accepts for wire compatibility with
    # the shipped portal and never honours, so when it differs from the socket
    # address the answer also names the claim it did NOT honour
    # (X-TollGate-Mac-Claim-Ignored). `identity_silent` removes that second
    # header: the harness must then go red, because a silently-ignored claim is
    # exactly what misled the bench rig on 2026-09-26.
    def _claimed_mac(self):
        """The `?mac=` the caller asserted, canonicalised, or ""."""
        m = re.search(r"[?&]mac=([0-9A-Fa-f:]{17})", self.path)
        return m.group(1).lower() if m else ""

    def _identity_headers(self):
        """What the module reports about the client it answered for."""
        resolved = "00:00:00:00:00:00" if mut("whoami_sentinel") else "aa:bb:cc:dd:ee:ff"
        hdrs = {"X-TollGate-Client-MAC": resolved}
        claimed = self._claimed_mac()
        if claimed and claimed != resolved and not mut("identity_silent"):
            hdrs["X-TollGate-Mac-Claim-Ignored"] = claimed
        return hdrs

    def _root_doc(self, extra=None):
        """GET / -- also what /session-state falls through to on a build that
        does not ship the endpoint (byte-identical is the whole point)."""
        kind = mut("root_kind") or 10021
        tags = [["metric", "bytes"], ["step_size", "22020096"], ["tips", "1", "2"]]
        if not mut("root_degraded"):
            tags.append(["price_per_step", "cashu", "1", "sat", "https://mint.stub.invalid", "0"])
        return self._json(200, {"kind": kind, "id": "stub-id", "pubkey": "stub-pubkey",
                                "created_at": 0, "tags": tags},
                          extra=self._identity_headers())

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
            return self._send(200, "mac=%s\n" % mac, "text/plain; charset=utf-8",
                              self._identity_headers())
        if path == "/balance":
            if mut("balance_malformed"):
                return self._json(200, {"status": 1}, extra=self._identity_headers())
            return self._json(200, {"status": 1, "session_active": bool(mut("balance_active")),
                                    "usage": 0, "allotment": 0, "remaining": 0},
                              extra=self._identity_headers())
        if path == "/usage":
            return self._send(200, "nonsense" if mut("usage_bad") else "-1/-1",
                              "text/plain; charset=utf-8", self._identity_headers())
        if path == "/session-state":
            if mut("session_state"):
                return self._json(200, {"session_active": False, "remaining": 0, "allotment": 0},
                                  extra=self._identity_headers())
            # pre-#541 behaviour: the mux falls through to the root handler
            # -> BYTE-IDENTICAL to GET /, which is what the harness detects
            return self._root_doc(extra=self._identity_headers())
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
            return self._json(200, {"kind": 1022, "id": "stub-session", "content": "session granted"},
                              extra=self._identity_headers())
        if not body:
            return self._json(400, {"kind": 21023, "id": "stub-notice",
                                    "content": "No payment tag found in event"},
                              extra=self._identity_headers())
        # a real token would be redeemed by the module; the stub only reports what
        # the paid lane expects from a successful redemption -- including the
        # signed device-identifier tag naming the client the grant went to.
        return self._json(200, {"kind": 1022, "id": "stub-session", "content": "session granted",
                                "tags": [["device-identifier", "mac", "aa:bb:cc:dd:ee:ff"]]},
                          extra=self._identity_headers())


class CaptiveHandler(Base):
    """:CAPTIVE_PORT -- nodogsplash pre-auth interception."""

    def do_GET(self):
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
        if mut("luci_307_to_alt"):
            # A live https target that is NOT the port the sweep calls :TLS_PORT.
            port = PORTS["alt"]
        else:
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


def ssh_banner(port, delay=0.0):
    """A bare TCP listener: proves net:tcp-<port> without pretending to be SSH.

    delay > 0 (--scenario {"ssh_late_bind_s": N}) does not even BIND until then,
    so the harness's first connect is refused and a later attempt answers -- the
    bench's real "the first burst raced the box" case, made deterministic. The
    bind stays synchronous for the default delay=0, so the listener is up before
    "stub-ready" and no case depends on a startup race.
    """
    def bind_and_listen():
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

    if delay > 0:
        def delayed():
            time.sleep(delay)
            bind_and_listen()
        threading.Thread(target=delayed, daemon=True).start()
    else:
        bind_and_listen()


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
    ap.add_argument("--alt-port", type=int, default=0,
                    help="a second, LIVE https listener the harness does not know about; "
                         "bound only under the luci_307_to_alt scenario")
    ap.add_argument("--ssh-port", type=int, required=True)
    ap.add_argument("--cert", default="")
    ap.add_argument("--key", default="")
    args = ap.parse_args()

    global SCENARIO
    if args.scenario:
        SCENARIO = json.load(open(args.scenario))

    PORTS.update({"host": args.host, "portal": args.portal_port, "stub": args.stub_port,
                  "api": args.api_port, "admin": args.admin_port, "luci": args.luci_port,
                  "captive": args.captive_port, "tls": args.tls_port, "alt": args.alt_port,
                  "portal_docroot": args.portal_docroot, "admin_docroot": args.admin_docroot,
                  "cert": args.cert, "key": args.key})

    # A surface listed in "unbound_ports" is never bound at all -- the honest
    # stand-in for a port that is genuinely dead for the whole run (as :8090 is
    # for a br-lan client). The admin one may still be bound later, on demand:
    # see late_bind_admin().
    if not unbound("portal"):
        serve(PortalHandler, args.portal_port)
    if not unbound("stub"):
        serve(StubHandler, args.stub_port)
    if not unbound("api"):
        serve(ApiHandler, args.api_port)
    if not unbound("admin") and not SCENARIO.get("admin_bind_on_first_http"):
        serve(AdminHandler, args.admin_port)
    if not unbound("captive"):
        serve(CaptiveHandler, args.captive_port)
    if not unbound("luci"):
        serve(LuciHandler, args.luci_port)
    if not unbound("tls"):
        serve(TlsHandler, args.tls_port, tls=bool(args.cert))
    # The decoy listener: live https, on a port the harness never probes, so the
    # only thing that can reach it is the :LUCI 307 Location. It exists so the
    # rig can tell "a check reached the port in question" apart from "a check
    # that mentions the port PASSed".
    if args.alt_port and SCENARIO.get("luci_307_to_alt"):
        serve(TlsHandler, args.alt_port, tls=bool(args.cert))
    if not unbound("ssh"):
        ssh_banner(args.ssh_port, delay=float(SCENARIO.get("ssh_late_bind_s", 0)))

    print("stub-ready", flush=True)
    try:
        threading.Event().wait()
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
