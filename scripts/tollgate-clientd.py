#!/usr/bin/env python3
"""tollgate-clientd — keep a laptop alive behind a TollGate.

A client-side companion for TollGate routers: detects a TollGate on the
default gateway, registers this machine with the captive portal, then
continuously shows how many bytes or seconds of connectivity remain and
automatically tops up with ecash from a local Cashu wallet before the
current allotment runs out.

Wallets supported (auto-detected, or forced with --wallet):
  * cdk-cli   — https://github.com/cashubtc/cdk (crates/cdk-cli)
  * nutshell  — `pip install cashu` (https://github.com/cashubtc/nutshell)

Wire protocol spoken (all against the TollGate gateway, port 2121):
  GET  /            -> kind 10021 advertisement (metric, step_size, price_per_step)
  GET  /usage       -> "used/allotment" ("-1/-1" when no session)
  POST /?mac=MAC    -> body: raw Cashu token; reply: kind 1022 session event
                       (kind 21023 notice event on rejection)

Examples:
  tollgate-clientd                      # daemon: auto top-up + live status line
  tollgate-clientd --status             # print current status once, exit
  tollgate-clientd --status --json      # one-shot JSON (waybar/script feeds)
  tollgate-clientd --steps 5            # buy 5 steps per top-up
  tollgate-clientd --renew-below 40MB   # custom renewal threshold
  tollgate-clientd --dry-run            # show status, never pay
  tollgate-clientd --list-offers        # just print the gateway's pricing
  tollgate-clientd --selftest           # run against a built-in mock TollGate
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

PORT = 2121
HTTP_TIMEOUT = 5.0
POLL_INTERVAL = 2.0          # seconds between /usage polls
PAYMENT_THROTTLE = 5.0       # minimum seconds between payment attempts
PAYMENT_BACKOFF_MAX = 60.0   # max seconds of failure backoff
PORTAL_PORT = 80             # hit once per payment so nodogsplash registers us

DEFAULT_RENEW_BELOW_BYTES = 20 * 1024 * 1024   # 20 MiB
DEFAULT_RENEW_BELOW_MS = 30_000                # 30 s

RECV_BUF = 1 << 20


class ClientError(Exception):
    pass


# ---------------------------------------------------------------------------
# Platform helpers: default gateway, interface MAC
# ---------------------------------------------------------------------------

def _run(cmd: list[str]) -> str:
    out = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if out.returncode != 0:
        raise ClientError(f"{' '.join(cmd)} failed: {out.stderr.strip()}")
    return out.stdout


def find_gateway() -> tuple[str, str]:
    """Return (gateway_ip, source_interface) of the default route."""
    if shutil.which("ip"):
        for line in _run(["ip", "route", "show", "default"]).splitlines():
            m = re.search(r"default via (\S+).*dev (\S+)", line)
            if m:
                return m.group(1), m.group(2)
        raise ClientError("no default route found (are you connected?)")
    # macOS
    out = _run(["route", "-n", "get", "default"])
    gw = re.search(r"gateway:\s*(\S+)", out)
    iface = re.search(r"interface:\s*(\S+)", out)
    if gw and iface:
        return gw.group(1), iface.group(1)
    raise ClientError("no default route found (are you connected?)")


def find_mac(interface: str) -> str:
    """Return the MAC address of the interface heading to the gateway."""
    sysfs = f"/sys/class/net/{interface}/address"
    if os.path.exists(sysfs):
        with open(sysfs) as f:
            mac = f.read().strip()
        if mac:
            return mac
    # macOS
    out = _run(["ifconfig", interface])
    m = re.search(r"ether\s+([0-9a-fA-F:]{17})", out)
    if m:
        return m.group(1).lower()
    raise ClientError(f"could not determine MAC of interface {interface}")


# ---------------------------------------------------------------------------
# HTTP helpers (stdlib only)
# ---------------------------------------------------------------------------

def http_get(url: str, timeout: float = HTTP_TIMEOUT) -> str:
    with urllib.request.urlopen(url, timeout=timeout) as resp:
        return resp.read(RECV_BUF).decode("utf-8", "replace")


def http_post(url: str, body: str, content_type: str = "text/plain",
              timeout: float = HTTP_TIMEOUT) -> tuple[int, str]:
    req = urllib.request.Request(
        url, data=body.encode("utf-8"), method="POST",
        headers={"Content-Type": content_type},
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read(RECV_BUF).decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read(RECV_BUF).decode("utf-8", "replace")


# ---------------------------------------------------------------------------
# TollGate wire protocol
# ---------------------------------------------------------------------------

class Offer:
    def __init__(self, price: int, unit: str, mint_url: str, min_steps: int):
        self.price = price          # sats per step
        self.unit = unit            # e.g. "sat"
        self.mint_url = mint_url
        self.min_steps = min_steps


class Advertisement:
    def __init__(self, pubkey: str, metric: str, step_size: int,
                 offers: list[Offer]):
        self.pubkey = pubkey
        self.metric = metric            # "bytes" | "milliseconds"
        self.step_size = step_size
        self.offers = offers

    def default_offer(self, mint_url: str | None = None) -> Offer:
        if mint_url:
            for o in self.offers:
                if o.mint_url.rstrip("/") == mint_url.rstrip("/"):
                    return o
            raise ClientError(f"mint {mint_url} not accepted by this TollGate")
        if self.offers:
            return self.offers[0]
        raise ClientError("TollGate advertises no mints")


def parse_advertisement(text: str) -> Advertisement:
    """Parse the kind 10021 discovery event served at GET /."""
    try:
        ev = json.loads(text)
    except json.JSONDecodeError as e:
        raise ClientError(f"gateway is not a TollGate (bad advertisement: {e})")
    if ev.get("kind") != 10021:
        raise ClientError("gateway is not a TollGate (no advertisement)")
    metric, step_size, offers = "bytes", 0, []
    for tag in ev.get("tags", []):
        name = tag[0]
        if name == "metric" and len(tag) >= 2:
            metric = tag[1]
        elif name == "step_size" and len(tag) >= 2:
            step_size = int(tag[1])
        elif name == "price_per_step" and len(tag) >= 5:
            offers.append(Offer(int(tag[2]), tag[3], tag[4],
                                int(tag[5]) if len(tag) >= 6 else 0))
    return Advertisement(ev.get("pubkey", ""), metric, step_size, offers)


def _base(gateway: str) -> str:
    """Base URL of the TollGate API on a gateway (host, or host:port for tests)."""
    if ":" in gateway:
        return f"http://{gateway}"
    return f"http://{gateway}:{PORT}"


def register_with_portal(gateway: str) -> None:
    """Best-effort GET on port 80 so nodogsplash registers our MAC
    (router issue #88 workaround — the session must authorize a MAC the
    portal has actually seen)."""
    try:
        http_get(f"http://{gateway}:{PORTAL_PORT}/", timeout=3.0)
    except Exception:  # noqa: BLE001, S110 — best effort by design
        pass


def get_usage(gateway: str) -> tuple[int, int] | None:
    """Return (used, allotment) or None when there is no session."""
    body = http_get(f"{_base(gateway)}/usage").strip()
    m = re.fullmatch(r"(-?\d+)/(-?\d+)", body)
    if not m:
        raise ClientError(f"unexpected /usage response: {body!r}")
    used, allotment = int(m.group(1)), int(m.group(2))
    if used < 0 or allotment < 0:
        return None
    return used, allotment


def pay(gateway: str, mac: str, token: str) -> dict:
    """POST a raw Cashu token; return the session event on success."""
    status, body = http_post(f"{_base(gateway)}/?mac={mac}", token)
    try:
        ev = json.loads(body)
    except json.JSONDecodeError:
        raise ClientError(f"payment reply not JSON (HTTP {status}): {body[:200]!r}")
    if status == 200 and ev.get("kind") == 1022:
        return ev
    # kind 21023 notice (or anything else) — surface the router's message
    message = ev.get("content") or ""
    for tag in ev.get("tags", []):
        if tag[0] == "message":
            message = tag[1] if len(tag) > 1 else message
    raise ClientError(f"payment rejected (HTTP {status}): {message or body[:200]}")


# ---------------------------------------------------------------------------
# Wallet adapters — create a Cashu token worth `amount_sats` at `mint_url`
# ---------------------------------------------------------------------------

def _extract_token(stdout: str) -> str:
    for line in reversed(stdout.strip().splitlines()):
        line = line.strip()
        if line.startswith("cashu"):
            return line
    raise ClientError(f"no token in wallet output: {stdout.strip()[-200:]!r}")


def wallet_send_cdk_cli(mint_url: str, amount_sats: int,
                        wallet_dir: str | None) -> str:
    cmd = ["cdk-cli"]
    if wallet_dir:
        cmd += ["-w", wallet_dir]
    cmd += ["send", "--mint-url", mint_url]
    proc = subprocess.run(cmd, input=f"{amount_sats}\n", capture_output=True,
                          text=True, check=False)
    if proc.returncode != 0:
        raise ClientError(f"cdk-cli send failed: {proc.stderr.strip() or proc.stdout.strip()}")
    return _extract_token(proc.stdout)


def wallet_send_nutshell(mint_url: str, amount_sats: int,
                         wallet_dir: str | None) -> str:
    # No --mint flag exists: mint selection is the global --host option and
    # must precede the subcommand; --yes keeps it non-interactive.
    cmd = ["cashu"]
    if wallet_dir:
        cmd += ["--wallet", wallet_dir]
    cmd += ["--host", mint_url, "--yes", "send", str(amount_sats)]
    proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        raise ClientError(f"cashu send failed: {proc.stderr.strip() or proc.stdout.strip()}")
    return _extract_token(proc.stdout)


def resolve_wallet(name: str):
    if name == "cdk-cli":
        if not shutil.which("cdk-cli"):
            raise ClientError("cdk-cli not found in PATH")
        return "cdk-cli", wallet_send_cdk_cli
    if name == "nutshell":
        if not shutil.which("cashu"):
            raise ClientError("nutshell (`cashu`) not found in PATH")
        return "nutshell", wallet_send_nutshell
    if name == "auto":
        for candidate, sender in (("cdk-cli", wallet_send_cdk_cli),
                                  ("nutshell", wallet_send_nutshell)):
            if shutil.which(candidate):
                return candidate, sender
        raise ClientError("no wallet found: install cdk-cli or nutshell (pip install cashu)")
    raise ClientError(f"unknown wallet: {name}")


# ---------------------------------------------------------------------------
# Formatting
# ---------------------------------------------------------------------------

def human_bytes(n: float) -> str:
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if n < 1024 or unit == "TB":
            return f"{n:.1f} {unit}" if unit != "B" else f"{int(n)} B"
        n /= 1024
    return f"{n:.1f} TB"


def human_ms(ms: float) -> str:
    s = int(ms // 1000)
    h, rem = divmod(s, 3600)
    m, sec = divmod(rem, 60)
    return f"{h}:{m:02d}:{sec:02d}" if h else f"{m}:{sec:02d}"


def format_remaining(metric: str, remaining: int) -> str:
    if metric == "milliseconds":
        return f"{human_ms(remaining)} left"
    return f"{human_bytes(remaining)} left"


def parse_renew_below(value: str, metric: str) -> int:
    """Accept '20MB', '512KB', '30s', '1m', or a plain byte/ms count."""
    v = value.strip().lower()
    m = re.fullmatch(r"([0-9]*\.?[0-9]+)\s*(b|kb|kib|mb|mib|gb|gib|s|m|h|ms)?", v)
    if not m:
        raise ClientError(f"bad --renew-below value: {value!r}")
    amount = float(m.group(1))
    unit = m.group(2) or ""
    if metric == "milliseconds":
        return int(amount * {"ms": 1, "s": 1000, "m": 60_000, "h": 3_600_000}.get(unit, 1))
    return int(amount * {"b": 1, "kb": 1024, "kib": 1024, "mb": 1024**2,
                         "mib": 1024**2, "gb": 1024**3, "gib": 1024**3}.get(unit, 1))


# ---------------------------------------------------------------------------
# Daemon
# ---------------------------------------------------------------------------

class ClientDaemon:
    def __init__(self, args, wallet_name: str, wallet_sender):
        self.args = args
        self.wallet_name = wallet_name
        self.wallet_sender = wallet_sender
        self.gateway, self.iface = (args.gateway, args.iface or "") if args.gateway \
            else find_gateway()
        if not self.iface and shutil.which("ip"):
            # derive interface from the route to the gateway
            m = re.search(r"dev (\S+)", _run(["ip", "route", "get", self.gateway]))
            self.iface = m.group(1) if m else ""
        self.mac = args.mac or find_mac(self.iface)
        self.ad = parse_advertisement(http_get(f"{_base(self.gateway)}/"))
        self.offer = self.ad.default_offer(args.mint)
        self.renew_below = parse_renew_below(args.renew_below, self.ad.metric) \
            if args.renew_below else None
        self.steps = max(args.steps, self.offer.min_steps)
        self.last_payment = 0.0
        self.backoff = PAYMENT_THROTTLE

    def amount_sats(self) -> int:
        return self.steps * self.offer.price

    def top_up(self) -> str:
        """Pay for self.steps; returns a short status message."""
        now = time.monotonic()
        if now - self.last_payment < PAYMENT_THROTTLE:
            return "throttled"
        self.last_payment = now
        register_with_portal(self.gateway)
        token = self.wallet_sender(self.offer.mint_url, self.amount_sats(),
                                   self.args.wallet_dir)
        ev = pay(self.gateway, self.mac, token)
        self.backoff = PAYMENT_THROTTLE
        allotment = ""
        for tag in ev.get("tags", []):
            if tag[0] == "allotment" and len(tag) > 1:
                allotment = tag[1]
        return f"paid {self.amount_sats()} sats (allotment now {allotment or '?'})"

    def status(self) -> dict:
        usage = get_usage(self.gateway)
        remaining = None if usage is None else usage[1] - usage[0]
        return {
            "gateway": self.gateway,
            "interface": self.iface,
            "mac": self.mac,
            "metric": self.ad.metric,
            "step_size": self.ad.step_size,
            "mint": self.offer.mint_url,
            "price_per_step": self.offer.price,
            "wallet": self.wallet_name,
            "session_active": usage is not None,
            "usage": usage[0] if usage else None,
            "allotment": usage[1] if usage else None,
            "remaining": remaining,
        }

    def needs_top_up(self, remaining: int | None) -> bool:
        threshold = self.renew_below
        if threshold is None:
            threshold = (DEFAULT_RENEW_BELOW_MS if self.ad.metric == "milliseconds"
                         else DEFAULT_RENEW_BELOW_BYTES)
        return remaining is None or remaining <= threshold

    def run(self) -> None:
        intro = (f"TollGate {self.gateway} | {self.ad.metric} | "
                 f"{self.steps} step(s) = {self.amount_sats()} sats via "
                 f"{self.wallet_name} @ {self.offer.mint_url}")
        print(intro, file=sys.stderr)
        last_line = ""
        while True:
            try:
                st = self.status()
                if st["remaining"] is None:
                    line = f"[TollGate {self.gateway}] no session — needs payment"
                else:
                    line = (f"[TollGate {self.gateway}] "
                            f"{format_remaining(self.ad.metric, st['remaining'])} "
                            f"({st['usage']}/{st['allotment']})")
                if self.needs_top_up(st["remaining"]):
                    if self.args.dry_run:
                        line += "  [dry-run: would top up]"
                    else:
                        line += "  [topping up...]"
                if line != last_line:
                    emit(line)
                    last_line = line
                if (self.needs_top_up(st["remaining"]) and not self.args.dry_run
                        and time.monotonic() - self.last_payment >= self.backoff):
                    try:
                        msg = self.top_up()
                        print(f"  -> {msg}", file=sys.stderr)
                    except (ClientError, OSError) as e:
                        self.backoff = min(self.backoff * 2, PAYMENT_BACKOFF_MAX)
                        self.last_payment = time.monotonic()
                        print(f"  !! top-up failed: {e} "
                              f"(retrying in {self.backoff:.0f}s)", file=sys.stderr)
            except (ClientError, urllib.error.URLError, OSError) as e:
                print(f"waiting: {e}", file=sys.stderr)
                time.sleep(self.args.interval)
                continue
            time.sleep(self.args.interval)


def emit(line: str) -> None:
    """Refresh a single status line on a TTY; plain lines when piped."""
    if sys.stdout.isatty():
        sys.stdout.write("\r\033[K" + line)
        sys.stdout.flush()
    else:
        print(line, flush=True)


# ---------------------------------------------------------------------------
# One-shot modes
# ---------------------------------------------------------------------------

def print_status(daemon: ClientDaemon, as_json: bool) -> None:
    st = daemon.status()
    if as_json:
        print(json.dumps(st))
    elif st["remaining"] is None:
        print(f"TollGate {st['gateway']}: no session (would pay "
              f"{daemon.amount_sats()} sats for {daemon.steps} step(s))")
    else:
        print(f"TollGate {st['gateway']}: {format_remaining(st['metric'], st['remaining'])} "
              f"(usage {st['usage']}/{st['allotment']})")


def print_offers(daemon: ClientDaemon) -> None:
    ad = daemon.ad
    print(f"gateway {daemon.gateway}  metric={ad.metric}  step_size={ad.step_size}")
    for o in ad.offers:
        print(f"  {o.price} {o.unit}/step  mint={o.mint_url}  min_steps={o.min_steps}")


# ---------------------------------------------------------------------------
# Selftest: mock TollGate + stub wallet, exercises the full loop in-process
# ---------------------------------------------------------------------------

def selftest() -> int:
    import threading
    from http.server import BaseHTTPRequestHandler, HTTPServer

    state = {"usage": 0, "allotment": 0, "payments": [], "polls": 0}
    STEP = 21 * 1024 * 1024   # 21 MiB per step, matching e2e defaults
    PRICE = 1

    class MockTollGate(BaseHTTPRequestHandler):
        def log_message(self, *a):  # silence
            pass

        def do_GET(self):
            if self.path == "/":
                adv = {"kind": 10021, "pubkey": "00" * 32, "tags": [
                    ["metric", "bytes"], ["step_size", str(STEP)],
                    ["price_per_step", "cashu", str(PRICE), "sat",
                     "https://mockmint.example", "1"],
                ], "content": ""}
                body = json.dumps(adv).encode()
            elif self.path == "/usage":
                state["polls"] += 1
                if state["payments"]:
                    state["usage"] += 8 * 1024 * 1024  # burn 8 MiB per poll
                if state["usage"] >= state["allotment"] and state["payments"]:
                    body = b"-1/-1"   # session exhausted
                else:
                    body = f"{state['usage']}/{state['allotment']}".encode()
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self):
            length = int(self.headers.get("Content-Length", 0))
            token = self.rfile.read(length).decode()
            mac = self.path.split("mac=")[-1]
            assert token.startswith("cashu"), f"not a token: {token[:40]!r}"
            assert re.fullmatch(r"[0-9a-f:]{17}", mac), f"bad mac: {mac!r}"
            state["payments"].append({"token": token, "mac": mac})
            amount_sats = int(token.split("MOCK")[-1])
            state["allotment"] += (amount_sats // PRICE) * STEP
            state["usage"] = 0
            ev = json.dumps({"kind": 1022, "tags": [
                ["allotment", str(state["allotment"])], ["metric", "bytes"]],
                "content": ""}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(ev)

    server = HTTPServer(("127.0.0.1", 0), MockTollGate)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    port = server.server_address[1]

    def stub_wallet(mint_url, amount_sats, wallet_dir):
        assert mint_url == "https://mockmint.example"
        return f"cashuB64MOCK{amount_sats}"

    class Args:
        gateway = f"127.0.0.1:{port}"   # _base() passes host:port through
        iface = "lo0"
        mac = "02:00:00:00:00:02"
        mint = None
        renew_below = "8MB"     # renewal threshold above the 8MiB/poll burn rate
        steps = 4
        wallet_dir = None
        dry_run = False
        interval = 0.05

    failures = []
    try:
        d = ClientDaemon(Args(), "stub", stub_wallet)
        assert d.ad.metric == "bytes" and d.ad.step_size == STEP
        assert d.offer.price == PRICE and d.amount_sats() == 4
        # initial state: no session -> needs payment
        assert d.needs_top_up(None), "no-session should require payment"
        d.top_up()
        time.sleep(0.2)
        st = d.status()
        assert st["session_active"], "session should be active after payment"
        # one /usage poll happened since payment: 8 MiB of the 4 steps burned
        assert st["remaining"] == 4 * STEP - 8 * 1024 * 1024, \
            f"unexpected remaining {st['remaining']}"
        # burn down until renewal fires
        deadline = time.monotonic() + 30
        while len(state["payments"]) < 2 and time.monotonic() < deadline:
            st = d.status()
            if d.needs_top_up(st["remaining"]):
                d.top_up()
            time.sleep(0.05)
        if len(state["payments"]) < 2:
            failures.append(f"renewal never fired (payments={len(state['payments'])})")
        if state["payments"][0]["mac"] != "02:00:00:00:00:02":
            failures.append("payment did not carry the client MAC")
        # display formatting sanity
        assert "MB left" in format_remaining("bytes", 5 * 1024 * 1024)
        assert "1:30" == human_ms(90_000)
    except AssertionError as e:
        failures.append(f"assertion: {e}")
    except Exception as e:  # noqa: BLE001
        failures.append(f"exception: {e!r}")
    finally:
        server.shutdown()

    if failures:
        print("SELFTEST FAIL:")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"SELFTEST PASS: {len(state['payments'])} payments, "
          f"{state['polls']} usage polls, renewal + status verified")
    return 0


# ---------------------------------------------------------------------------

def main() -> int:
    ap = argparse.ArgumentParser(
        prog="tollgate-clientd",
        description="Laptop client for TollGate: shows remaining access and "
                    "auto-tops-up with ecash (cdk-cli or nutshell).")
    ap.add_argument("--gateway", help="TollGate IP (default: default-route gateway)")
    ap.add_argument("--iface", help="network interface toward the gateway")
    ap.add_argument("--mac", help="MAC to authorize (default: auto-detect)")
    ap.add_argument("--wallet", choices=["auto", "cdk-cli", "nutshell"],
                    default="auto", help="wallet to create tokens with")
    ap.add_argument("--wallet-dir",
                    help="cdk-cli wallet dir (-w) or nutshell wallet name "
                         "(--wallet)")
    ap.add_argument("--mint", help="mint URL to pay with (default: first advertised)")
    ap.add_argument("--steps", type=int, default=1,
                    help="steps to buy per top-up (raised to the mint minimum)")
    ap.add_argument("--renew-below",
                    help="renewal threshold, e.g. '20MB' or '30s' "
                         "(default: 20MB / 30s)")
    ap.add_argument("--interval", type=float, default=POLL_INTERVAL,
                    help="seconds between usage polls")
    ap.add_argument("--status", action="store_true",
                    help="print current status once and exit")
    ap.add_argument("--json", action="store_true",
                    help="with --status: emit JSON")
    ap.add_argument("--dry-run", action="store_true",
                    help="show status but never pay")
    ap.add_argument("--list-offers", action="store_true",
                    help="print the gateway's pricing and exit")
    ap.add_argument("--selftest", action="store_true",
                    help="run against a built-in mock TollGate (no hardware)")
    args = ap.parse_args()

    if args.selftest:
        return selftest()

    try:
        name, sender = resolve_wallet(args.wallet)
        d = ClientDaemon(args, name, sender)
    except ClientError as e:
        print(f"error: {e}", file=sys.stderr)
        return 1

    if args.list_offers:
        print_offers(d)
        return 0
    if args.status:
        print_status(d, args.json)
        return 0

    try:
        d.run()
    except KeyboardInterrupt:
        print(file=sys.stderr)
        return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
