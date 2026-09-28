#!/usr/bin/env python3
"""API-shape, captive-quote and (opt-in) paid-purchase checks against a live router.

Everything in the DEFAULT run is a read, except exactly one POST that carries an
empty body -- which cannot touch anyone's ecash, because there is no proof in it.
The only code path that can move value is the paid lane, and it is gated on
RHP_CASHU_TOKEN + RHP_SPEND_MAX_SATS (see paid_lane below). If you are reading
this because the harness spent something you did not expect, that gate failed and
that is a bug worth reporting.

Emits: RHPCHECK <id> <PASS|FAIL|SKIP> <detail>  /  RHPNOTE <text>
stdlib only.
"""

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import cashtoken  # noqa: E402

UA = "router-happy-path-harness/1"
MAC_RE = re.compile(r"^mac=((?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2})$")
USAGE_RE = re.compile(r"^-?\d+/-?\d+$")
SENTINEL = "00:00:00:00:00:00"

# The client-identity contract of the API on :2121 (see docs/operator-guide.md,
# "every client-scoped endpoint is socket-scoped"). Every client-scoped route
# answers for the client at the other end of the SOCKET; a `?mac=` a caller sends
# is a claim the module accepts for wire compatibility with the shipped portal
# and never honours. It must not be ignored SILENTLY, though: measured on the
# bench (MT3000, 2026-09-26) a rig that posted a token "for" a MAC it was not
# using bought a session for its own socket, and nothing on the wire said so --
# the rig read "no session on the MAC I named" as "the gate never opened".
IDENTITY_HEADER = "X-TollGate-Client-MAC"       # the client the module answered for
CLAIM_HEADER = "X-TollGate-Mac-Claim-Ignored"   # a claim it did NOT honour
# A claim no run of this harness can be making: the checks below assert that this
# address is never named as an identity, and that the module says it ignored it.
CLAIM_MAC = "02:11:22:33:44:55"


def chk(cid, status, detail=""):
    print("RHPCHECK %s %s %s" % (cid, status, detail), flush=True)


def note(text):
    print("RHPNOTE %s" % text, flush=True)


# --------------------------------------------------------------------------
# HTTP transport -- and the module's rate limiter.
#
# The module wraps its ROOT handler (GET/POST "/", and /session-state, which
# falls through to it) in a per-client-IP limiter: 10 requests/minute by default,
# TOLLGATE_RATE_LIMIT_RPM overrides it on the box. One hardware run makes a
# handful of root requests and consecutive runs make more, so a 429 on this box
# is a THROTTLE, not a regression -- and reporting one as the other is exactly
# the failure mode this harness exists to prevent. So: honour the server's own
# Retry-After, retry, then pace every later request, so a burst that tripped the
# limiter cannot cascade into a page of red lines.
# --------------------------------------------------------------------------
RETRY_ATTEMPTS = max(1, int(os.environ.get("RHP_429_ATTEMPTS", "5")))
RETRY_MAX_WAIT = float(os.environ.get("RHP_429_MAX_WAIT", "30"))
PACE_AFTER_429 = float(os.environ.get("RHP_429_PACE", "6.2"))
THROTTLE = {"recovered": 0, "gaveup": [], "interval": 0.0, "next": 0.0}


def _pace():
    """Keep at least THROTTLE['interval'] between requests, once we know the box
    is limiting us. No-op until the first 429 (a clean run is never slowed)."""
    if THROTTLE["interval"] <= 0:
        return
    delta = THROTTLE["next"] - time.time()
    if delta > 0:
        time.sleep(delta)
    THROTTLE["next"] = time.time() + THROTTLE["interval"]


def _retry_after(hdrs, default=6.0):
    for k, v in (hdrs or {}).items():
        if str(k).lower() == "retry-after":
            try:
                return max(0.0, float(str(v).strip()))
            except (TypeError, ValueError):
                return default
    return default


def _once(url, method="GET", body=None, content_type="", timeout=12):
    data = body
    headers = {"User-Agent": UA}
    if data is not None:
        headers["Content-Type"] = content_type or "text/plain"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read(), dict(resp.headers)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read(), dict(exc.headers or {})
    except Exception as exc:
        return None, ("%r" % (exc,)).encode(), {}


def request(url, method="GET", body=None, content_type="", timeout=12):
    """-> (status, body_bytes, headers_dict). status None on transport failure.

    A 429 is retried with the server's own Retry-After; after the first throttle
    the rest of the run is paced. Only a 429 that survives every attempt is
    handed back to the caller, and the note says out loud that it is a throttle.
    """
    waited = 0.0
    st, raw, hdrs = None, b"", {}
    for attempt in range(1, RETRY_ATTEMPTS + 1):
        _pace()
        st, raw, hdrs = _once(url, method=method, body=body,
                              content_type=content_type, timeout=timeout)
        if st != 429:
            return st, raw, hdrs
        nap = min(_retry_after(hdrs), RETRY_MAX_WAIT)
        if attempt >= RETRY_ATTEMPTS or waited + nap > RETRY_MAX_WAIT * 2:
            THROTTLE["gaveup"].append(url)
            note("http: HTTP 429 on %s survived %d attempt(s) -- the module rate-limits its root "
                 "handler per client IP, so this is a throttle and not a regression; wait a minute "
                 "and re-run before reading anything below as a defect" % (url, attempt))
            return st, raw, hdrs
        note("http: HTTP 429 from the module rate limiter on %s (Retry-After %.0fs) -- retrying; "
             "a throttle is not a regression" % (url, nap))
        time.sleep(nap)
        waited += nap
        THROTTLE["recovered"] += 1
        THROTTLE["interval"] = max(THROTTLE["interval"], PACE_AFTER_429)
    return st, raw, hdrs


def jload(raw):
    try:
        return json.loads(raw.decode("utf-8", "replace"))
    except Exception:
        return None


def header(hdrs, name):
    """Case-insensitive response-header lookup, or "" -- HTTP names are not case-sensitive."""
    for k, v in (hdrs or {}).items():
        if str(k).lower() == name.lower():
            return str(v).strip()
    return ""


def granted_identity(obj, hdrs):
    """Which client the module says it granted a purchase to, or "" if it says nothing.

    Read from the module's OWN answer -- the signed ``device-identifier`` tag of the
    session event (kind 1022), else the identity header on the same response -- and
    never from the ``?mac=`` this harness sent, which does not decide it. See
    resolve_mac().
    """
    tags = obj.get("tags") if isinstance(obj, dict) else None
    for tag in tags or []:
        if isinstance(tag, list) and len(tag) >= 3 and tag[0] == "device-identifier":
            return str(tag[2]).strip().lower()
    return header(hdrs, IDENTITY_HEADER).lower()


def claim_failures(hdrs, label, socket_mac):
    """Check one response against the socket-identity contract -> [reason, ...].

    ``socket_mac`` is this harness's own address as /whoami reported it, or "" when
    the router could not place us. The contract: the claim we sent is reported as
    ignored BY NAME, the identity named is the socket's, and the claim is never
    named as the identity.
    """
    bad = []
    ignored = header(hdrs, CLAIM_HEADER).lower()
    named = header(hdrs, IDENTITY_HEADER).lower()
    if ignored != CLAIM_MAC:
        bad.append("%s did not report the ?mac=%s it carried as ignored (%s=%r): a claim that is "
                   "dropped silently is how a rig reads 'the gate never opened'"
                   % (label, CLAIM_MAC, CLAIM_HEADER, ignored))
    if named == CLAIM_MAC:
        bad.append("%s named the caller's claim %s as the identity it acted for" % (label, CLAIM_MAC))
    elif socket_mac and named != socket_mac:
        bad.append("%s named %r as the client, want the socket-resolved %s" % (label, named, socket_mac))
    return bad


def api_base(args):
    return "http://%s:%d" % (args.router_ip, args.api_port)


def resolve_mac(args):
    """This client's MAC as the router sees it (socket-derived), or "" if unknown.

    Read from ``GET /whoami``, which answers from the request's source IP: the
    module takes an identity from the socket and from nothing the caller sends.
    The value is what THIS harness compares against — its preconditions, and the
    identity the module reports back on a purchase — and it is also passed along
    as ``?mac=``, which the module accepts for wire compatibility with the
    shipped portal and IGNORES.

    Do not read that ``?mac=`` as a binding. The grant goes to the socket the
    request came from, never to the address in the query string, so a run that
    posts a token "for" a MAC this harness is not itself using buys a session for
    the harness's own address (measured on the bench, MT3000, 2026-09-26 — the
    rig then read "no session on the MAC I named" as "the gate never opened").
    The paid lane below therefore checks the identity in the module's own answer
    against THIS value instead of trusting the claim.

    The all-zero sentinel is never accepted as an identity: redeeming a token
    against it would grant access to nothing (or, worse, to somebody else).
    """
    if args.mac:
        return args.mac
    st, raw, _ = request(api_base(args) + "/whoami")
    if st == 200:
        m = MAC_RE.match(raw.decode("utf-8", "replace").strip())
        if m and m.group(1).lower() != SENTINEL:
            return m.group(1)
    return ""


# --------------------------------------------------------------------------
# Precondition -- runs FIRST, before anything else touches the router.
# --------------------------------------------------------------------------
def check_preconditions(args, quiet=False):
    """The bench must be idle before any result is attributable.

    Fail closed: an unreadable /balance counts as NOT idle. Never assume.

    quiet=True is used when this is an internal gate (the paid lane) rather than
    its own reporting pass, so a check id is never emitted twice per run.
    """
    base = api_base(args)
    st, raw, _ = request(base + "/balance")
    bal = jload(raw)
    if st != 200 or not isinstance(bal, dict) or "session_active" not in bal:
        if not quiet:
            chk("pre:balance-readable", "FAIL",
                "GET /balance -> HTTP %s body=%r (an unreadable balance is treated as NOT idle)"
                % (st, raw[:120]))
            chk("pre:idle", "FAIL", "cannot prove the box is idle (fail closed)")
        return False
    if not quiet:
        chk("pre:balance-readable", "PASS",
            "status=%s usage=%s/%s remaining=%s" % (bal.get("status"), bal.get("usage"),
                                                    bal.get("allotment"), bal.get("remaining")))
    if bal["session_active"] is True:
        if not quiet:
            chk("pre:idle", "FAIL",
                "session_active=true: a session is already open, so nothing below is attributable "
                "to this run (and the paid lane must not spend)")
        return False
    if not quiet:
        chk("pre:idle", "PASS", "session_active=false")
    return True


# --------------------------------------------------------------------------
# API shapes
# --------------------------------------------------------------------------
def check_api(args):
    base = api_base(args)
    root_status, root_raw, _ = request(base + "/")
    root = jload(root_raw)
    if root_status != 200:
        chk("api:root-kind10021", "FAIL", "GET / -> HTTP %s (%s)" % (root_status, root_raw[:120]))
        root = None
    elif not isinstance(root, dict) or root.get("kind") != 10021:
        chk("api:root-kind10021", "FAIL",
            "GET / -> HTTP 200 but kind=%r (want 10021)" % (root.get("kind") if isinstance(root, dict) else root_raw[:60]))
        root = None
    else:
        chk("api:root-kind10021", "PASS",
            "kind=10021 id=%s pubkey=%s" % (str(root.get("id", ""))[:12], str(root.get("pubkey", ""))[:12]))

    if root is None:
        for cid in ("api:root-tags", "api:root-full-mode"):
            chk(cid, "FAIL", "no parseable advertisement from GET /")
    else:
        tags = root.get("tags") or []
        names = [t[0] for t in tags if isinstance(t, list) and t]
        required = ["metric", "step_size", "tips"]
        missing = [r for r in required if r not in names]
        chk("api:root-tags", "PASS" if not missing else "FAIL",
            "tags=%s%s" % (sorted(set(names)),
                           "" if not missing else " MISSING %s" % missing))
        mints = [t for t in tags if isinstance(t, list) and t and t[0] == "price_per_step"]
        if not mints:
            chk("api:root-full-mode", "FAIL",
                "no price_per_step tag: the router is in DEGRADED mode (no reachable mints) "
                "-- fix upstream before trusting anything below")
        else:
            mints_ok = all(len(t) >= 5 and str(t[1]) == "cashu" for t in mints)
            chk("api:root-full-mode", "PASS" if mints_ok else "FAIL",
                "%d cashu price_per_step mints: %s" % (len(mints), ", ".join(str(t[4]) for t in mints[:4])))

    # The claim rides on the requests this lane already makes: the parameter is
    # documented as ignored, so asking WITH it must not change the answer -- and
    # asking with it is what proves the module says so out loud (id_fail below).
    st, raw, whoami_hdrs = request(base + "/whoami?mac=" + CLAIM_MAC)
    mac = ""
    if st != 200:
        chk("api:whoami-shape", "FAIL", "GET /whoami -> HTTP %s" % st)
    else:
        m = MAC_RE.match(raw.decode("utf-8", "replace").strip())
        if not m:
            chk("api:whoami-shape", "FAIL", "/whoami -> %r (want mac=<aa:bb:cc:dd:ee:ff>)" % raw[:80])
        else:
            mac = m.group(1)
            chk("api:whoami-shape", "PASS", "/whoami -> mac=%s" % mac)
    if mac:
        chk("api:whoami-not-sentinel", "PASS" if mac.lower() != SENTINEL else "FAIL",
            "mac=%s%s" % (mac, "" if mac.lower() != SENTINEL else
                          " -- the ALL-ZERO sentinel means the client could not be resolved "
                          "from the socket (identity hardening regression)"))
    else:
        chk("api:whoami-not-sentinel", "FAIL", "no MAC resolved, cannot rule out the sentinel")

    st, raw, balance_hdrs = request(base + "/balance?mac=" + CLAIM_MAC)
    balance = jload(raw)
    need = ["status", "session_active", "usage", "allotment", "remaining"]
    if st != 200:
        chk("api:balance-shape", "FAIL", "GET /balance -> HTTP %s" % st)
        balance = None
    elif not isinstance(balance, dict):
        chk("api:balance-shape", "FAIL", "/balance -> not JSON: %r" % raw[:120])
        balance = None
    else:
        missing = [k for k in need if k not in balance]
        bad = [k for k in need if k in balance and k not in ("metric", "error", "start_time")
               and not isinstance(balance[k], (int, bool))]
        if missing:
            chk("api:balance-shape", "FAIL", "/balance missing keys %s (got %s)" % (missing, sorted(balance)))
        elif not isinstance(balance.get("session_active"), bool):
            chk("api:balance-shape", "FAIL", "session_active is %r, want a JSON bool" % balance.get("session_active"))
        else:
            chk("api:balance-shape", "PASS",
                "status=%s session_active=%s usage=%s/%s remaining=%s"
                % (balance["status"], balance["session_active"], balance["usage"],
                   balance["allotment"], balance["remaining"]))
    if balance is not None:
        note("api:box-idle session_active=%s (the paid lane requires false)" % balance.get("session_active"))

    st, raw, usage_hdrs = request(base + "/usage?mac=" + CLAIM_MAC)
    text = raw.decode("utf-8", "replace").strip()
    if st != 200:
        chk("api:usage-shape", "FAIL", "GET /usage -> HTTP %s" % st)
    elif not USAGE_RE.match(text):
        chk("api:usage-shape", "FAIL", "/usage -> %r (want used/allotment, e.g. -1/-1)" % text[:60])
    else:
        chk("api:usage-shape", "PASS", "/usage -> %s" % text)

    # /session-state: shipped by newer artifacts. Absent => the Go default mux
    # falls through to "/" and answers with the advertisement document. Report
    # that as an honest SKIP, never as a PASS, and never as a FAIL unless the
    # caller says this build must ship it (--strict).
    st, raw, state_hdrs = request(base + "/session-state?mac=" + CLAIM_MAC)
    if st is None:
        chk("api:session-state", "FAIL", "GET /session-state -> transport failure %s" % raw[:120])
    elif st != 200:
        chk("api:session-state", "FAIL", "GET /session-state -> HTTP %s" % st)
    elif root_raw and raw == root_raw:
        reason = ("not shipped in this build: byte-identical to GET / (the mux falls "
                  "through to the root handler)")
        chk("api:session-state", "FAIL" if args.strict else "SKIP",
            reason + ("; --strict makes this fatal" if not args.strict else ""))
    else:
        obj = jload(raw)
        shaped = isinstance(obj, dict) and ("session_active" in obj or "remaining" in obj or "allotment" in obj)
        chk("api:session-state", "PASS" if shaped else "FAIL",
            ("keys=%s" % sorted(obj)) if shaped else
            "HTTP 200 but neither a session-state shape nor the root doc: %r" % raw[:120])

    st, raw, _ = request(base + "/identity")
    if st == 404:
        chk("api:identity-shape", "FAIL" if args.strict else "SKIP",
            "GET /identity -> 404 (identity routes not registered in this build; "
            "they need a merchant key in identities.json)")
    elif st != 200:
        chk("api:identity-shape", "FAIL", "GET /identity -> HTTP %s" % st)
    else:
        obj = jload(raw)
        if isinstance(obj, dict):
            macs = obj.get("macs")
            if (str(obj.get("npub", "")).startswith("npub1")
                    and isinstance(macs, dict) and obj.get("ipv4")):
                chk("api:identity-shape", "PASS",
                    "npub=%s ipv4=%s macs=%s" % (str(obj.get("npub"))[:16], obj.get("ipv4"),
                                                 sorted(macs)))
            else:
                chk("api:identity-shape", "FAIL", "/identity -> unexpected shape: %r" % raw[:120])
        else:
            chk("api:identity-shape", "FAIL", "/identity -> not JSON: %r" % raw[:120])

    # The socket-identity contract, collected from the responses above (the money
    # route is covered by the paid lane, which reads the identity out of the
    # module's own kind:1022 answer). A build that predates the contract cannot
    # report a claim as ignored, so a silent ignore is a FAIL and never a SKIP:
    # this harness runs against OUR artifacts, and a rig that cannot tell which
    # client was served is exactly the instrument failure this check prevents.
    id_fail = []
    for label, hdrs in (("/whoami", whoami_hdrs), ("/balance", balance_hdrs),
                        ("/usage", usage_hdrs), ("/session-state", state_hdrs)):
        id_fail += claim_failures(hdrs, label, mac)
    if id_fail:
        chk("api:identity-contract", "FAIL",
            "?mac=%s was not reported as an ignored claim on every client-scoped route: %s"
            % (CLAIM_MAC, "; ".join(id_fail)))
    else:
        chk("api:identity-contract", "PASS",
            "?mac=%s is reported as ignored (%s) and the identity named is the socket-resolved "
            "%s (%s), on /whoami, /balance, /usage and /session-state"
            % (CLAIM_MAC, CLAIM_HEADER, mac or "unresolved", IDENTITY_HEADER))

    st, raw, hdrs = request(base + "/ln-invoice", method="OPTIONS")
    allow = hdrs.get("Access-Control-Allow-Methods", "")
    if st != 200 or "OPTIONS" not in allow.upper():
        chk("api:cors-preflight", "FAIL",
            "OPTIONS /ln-invoice -> HTTP %s allow=%r (the portal on :2051 is cross-origin to "
            "the API on :2121, so a rejected preflight breaks the Lightning lane)" % (st, allow))
    else:
        chk("api:cors-preflight", "PASS", "OPTIONS -> 200, allow=%s" % allow)

    return mac, balance


# --------------------------------------------------------------------------
# Lightning quote contract
# --------------------------------------------------------------------------
def check_ln_quote(args):
    base = api_base(args)
    st, raw, _ = request(base + "/ln-invoice")
    obj = jload(raw)
    if st is None:
        chk("ln:no-quote-status-poll", "FAIL", "GET /ln-invoice -> transport failure %s" % raw[:120])
        return
    if st != 400:
        chk("ln:no-quote-status-poll", "FAIL",
            "GET /ln-invoice with no quote -> HTTP %s (want 400; a 200 here means the "
            "no-quote contract changed)" % st)
        return
    if not isinstance(obj, dict):
        chk("ln:no-quote-status-poll", "FAIL", "GET /ln-invoice -> 400 but body is not JSON: %r" % raw[:120])
        return
    err = obj.get("error", "")
    if err == "quote is required":
        chk("ln:no-quote-status-poll", "PASS",
            '400 {"error":"quote is required"} -- this is a status poll, not a fault')
    else:
        chk("ln:no-quote-status-poll", "FAIL",
            '400 but error=%r (want "quote is required")' % err)
    if obj.get("access_granted") is False:
        chk("ln:no-quote-not-granted", "PASS", "access_granted=false")
    else:
        chk("ln:no-quote-not-granted", "FAIL",
            "a quote-less request answered access_granted=%r" % obj.get("access_granted"))


# --------------------------------------------------------------------------
# The money path -- default run: one empty POST, no value, no ecash
# --------------------------------------------------------------------------
def check_empty_token(args):
    if args.skip_money_path:
        chk("money:empty-token-rejected", "SKIP", "--skip-money-path")
        return
    base = api_base(args)
    st, raw, _ = request(base + "/?mac=%s" % (resolve_mac(args) or SENTINEL),
                         method="POST", body=b"")
    obj = jload(raw)
    if st is None:
        chk("money:empty-token-rejected", "FAIL", "POST / with an empty body -> transport failure %s" % raw[:120])
        return
    kind = obj.get("kind") if isinstance(obj, dict) else None
    if st == 400 and kind == 21023:
        chk("money:empty-token-rejected", "PASS",
            "400 kind:21023 notice (an empty body carries no proof: nothing was redeemed)")
    elif st == 200:
        chk("money:empty-token-rejected", "FAIL",
            "an EMPTY token was accepted with HTTP 200 (kind=%r) -- that is a payment-bypass "
            "defect, not a harness problem" % kind)
    else:
        chk("money:empty-token-rejected", "FAIL",
            "POST / with an empty body -> HTTP %s kind=%r body=%r" % (st, kind, raw[:120]))


# --------------------------------------------------------------------------
# The paid lane -- OPT-IN. Absent either env var, nothing is sent.
# --------------------------------------------------------------------------
def paid_lane(args):
    token = os.environ.get("RHP_CASHU_TOKEN", "").strip()
    declared = os.environ.get("RHP_SPEND_MAX_SATS", "").strip()
    if not token:
        chk("paid:token-supplied", "SKIP",
            "RHP_CASHU_TOKEN not set -- the default run spends nothing (this is the safe default)")
        for cid in ("paid:spend-declaration", "paid:token-inspected",
                    "paid:purchase-accepted", "paid:session-flip"):
            chk(cid, "SKIP", "no token supplied")
        return
    # Never spend onto a box that is already serving somebody's session.
    if not check_preconditions(args, quiet=True):
        chk("paid:token-supplied", "SKIP", "refusing to spend: the box is not provably idle")
        for cid in ("paid:spend-declaration", "paid:token-inspected",
                    "paid:purchase-accepted", "paid:session-flip"):
            chk(cid, "SKIP", "box not idle")
        return
    if not declared:
        chk("paid:spend-declaration", "FAIL",
            "RHP_CASHU_TOKEN is set but RHP_SPEND_MAX_SATS is not: refusing to spend "
            "without an explicit declaration of how much value you are willing to lose")
        return
    try:
        declared_sats = int(declared)
    except ValueError:
        chk("paid:spend-declaration", "FAIL", "RHP_SPEND_MAX_SATS=%r is not an integer" % declared)
        return
    chk("paid:spend-declaration", "PASS",
        "operator declares <= %d sat is spendable; harness will abort above that" % declared_sats)

    info = cashtoken.inspect(token)
    if not info["parseable"]:
        chk("paid:token-inspected", "FAIL", "cannot inspect the token: %s" % info["note"])
        return
    if info["total_sats"] is not None and info["total_sats"] > declared_sats:
        chk("paid:token-inspected", "FAIL",
            "token carries %d sat > declared RHP_SPEND_MAX_SATS=%d: refusing to redeem it"
            % (info["total_sats"], declared_sats))
        return
    chk("paid:token-inspected", "PASS",
        "v%s mint(s)=%s total=%s%s"
        % (info["version"], info["mint_urls"], info["total_sats"],
           "" if not info["note"] else " (%s)" % info["note"]))

    base = api_base(args)
    mac = resolve_mac(args)
    if not mac:
        chk("paid:purchase-accepted", "FAIL",
            "cannot resolve this client's MAC from the socket; refusing to redeem a token "
            "against the all-zero sentinel")
        chk("paid:session-flip", "SKIP", "no purchase attempted")
        return
    st, raw, hdrs = request(base + "/?mac=%s" % mac, method="POST", body=token.encode("utf-8"))
    obj = jload(raw)
    kind = obj.get("kind") if isinstance(obj, dict) else None
    if st == 200 and kind == 1022:
        chk("paid:purchase-accepted", "PASS",
            "POST / -> 200 kind:1022 session event (the token has now been redeemed)")
    else:
        chk("paid:purchase-accepted", "FAIL",
            "POST / -> HTTP %s kind=%r body=%r" % (st, kind, raw[:200]))
        return

    # Which client did the module actually grant this to? The `?mac=` above did
    # not decide it — the socket did — so assert against the module's OWN answer:
    # the session event's signed `device-identifier` tag, or the identity header
    # on the same response. A mismatch means this run is paying for a device that
    # is not the one being measured (the harness resolves its MAC from /whoami),
    # and every conclusion drawn from the session it opened would be about the
    # wrong client.
    granted = granted_identity(obj, hdrs)
    if granted == mac:
        chk("paid:grant-identity", "PASS",
            "POST / granted the session to %s — the socket this harness requested from, as the module reports it" % granted)
    elif not granted:
        chk("paid:grant-identity", "FAIL",
            "POST / -> 200 kind:1022 with no identity to check: neither a device-identifier tag nor "
            "an %s header on the response (body=%r)" % (IDENTITY_HEADER, raw[:200]))
    else:
        chk("paid:grant-identity", "FAIL",
            "POST / was granted to %s, not to this harness's socket identity %s: the purchase is real, "
            "but it belongs to another device — do not read this run's session checks as this client's"
            % (granted, mac))

    st2, raw2, _ = request(base + "/balance")
    bal = jload(raw2)
    if isinstance(bal, dict) and bal.get("session_active") is True:
        chk("paid:session-flip", "PASS",
            "session_active false->true, allotment=%s remaining=%s" % (bal.get("allotment"), bal.get("remaining")))
    else:
        chk("paid:session-flip", "FAIL",
            "after an accepted payment /balance still reports %r" % (bal if bal is not None else raw2[:120]))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--router-ip", required=True)
    ap.add_argument("--api-port", type=int, default=2121)
    ap.add_argument("--strict", action="store_true")
    ap.add_argument("--skip-money-path", action="store_true")
    ap.add_argument("--only", default="")
    ap.add_argument("--mac", default="")
    args = ap.parse_args()

    if not args.only or args.only == "pre":
        check_preconditions(args)
    if not args.only or args.only == "api":
        mac, balance = check_api(args)
        args.mac = mac or args.mac
        if balance is not None and balance.get("session_active") is not False:
            note("api:box-idle WARNING: session_active=%r -- the paid lane will refuse to run "
                 "because a session is already open and nothing below is attributable"
                 % balance.get("session_active"))
    if not args.only or args.only == "ln":
        check_ln_quote(args)
    if not args.only or args.only == "money":
        check_empty_token(args)
    if not args.only or args.only == "paid":
        paid_lane(args)
    if THROTTLE["recovered"]:
        note("http: recovered from %d throttle response(s) (HTTP 429, the module's root-handler "
             "rate limit) by honouring Retry-After -- no check above is red because of the limiter"
             % THROTTLE["recovered"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
