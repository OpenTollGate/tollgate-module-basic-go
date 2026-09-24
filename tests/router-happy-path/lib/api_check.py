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
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import cashtoken  # noqa: E402

UA = "router-happy-path-harness/1"
MAC_RE = re.compile(r"^mac=((?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2})$")
USAGE_RE = re.compile(r"^-?\d+/-?\d+$")
SENTINEL = "00:00:00:00:00:00"


def chk(cid, status, detail=""):
    print("RHPCHECK %s %s %s" % (cid, status, detail), flush=True)


def note(text):
    print("RHPNOTE %s" % text, flush=True)


def request(url, method="GET", body=None, content_type="", timeout=12):
    """-> (status, body_bytes, headers_dict). status None on transport failure."""
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


def jload(raw):
    try:
        return json.loads(raw.decode("utf-8", "replace"))
    except Exception:
        return None


def api_base(args):
    return "http://%s:%d" % (args.router_ip, args.api_port)


def resolve_mac(args):
    """This client's MAC as the router sees it (socket-derived), or "" if unknown.

    Purchase requests must be bound to a real MAC: redeeming a token against the
    all-zero sentinel would grant access to nothing (or, worse, to somebody else).
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

    st, raw, _ = request(base + "/whoami")
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

    st, raw, _ = request(base + "/balance")
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

    st, raw, _ = request(base + "/usage")
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
    st, raw, _ = request(base + "/session-state")
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
    st, raw, _ = request(base + "/?mac=%s" % mac, method="POST", body=token.encode("utf-8"))
    obj = jload(raw)
    kind = obj.get("kind") if isinstance(obj, dict) else None
    if st == 200 and kind == 1022:
        chk("paid:purchase-accepted", "PASS",
            "POST / -> 200 kind:1022 session event (the token has now been redeemed)")
    else:
        chk("paid:purchase-accepted", "FAIL",
            "POST / -> HTTP %s kind=%r body=%r" % (st, kind, raw[:200]))
        return

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
    return 0


if __name__ == "__main__":
    sys.exit(main())
