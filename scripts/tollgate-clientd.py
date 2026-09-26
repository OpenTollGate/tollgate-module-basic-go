#!/usr/bin/env python3
"""tollgate-clientd — keep a laptop alive behind a TollGate.

A client-side companion for TollGate routers: detects a TollGate on the
default gateway, registers this machine with the captive portal, then
continuously shows how many bytes or seconds of connectivity remain and
automatically tops up with ecash from a local Cashu wallet before the
current allotment runs out.

Wallets supported (auto-detected, or forced with --wallet):
  * cdk-cli   — https://github.com/cashubtc/cdk (crates/cdk-cli)
  * nutshell  — `pip install cashu "marshmallow<4"` — the pin is required:
                marshmallow 4.x breaks cashu's environs dependency today

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
import hashlib
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
PAYMENT_TIMEOUT = 30.0        # a router may swap synchronously — don't
                              # abandon a live payment at 5 s (#423)
POLL_INTERVAL = 2.0          # seconds between /usage polls
PAYMENT_THROTTLE = 5.0       # minimum seconds between payment attempts
PAYMENT_BACKOFF_MAX = 60.0   # max seconds of failure backoff
PORTAL_PORT = 80             # hit once per payment so nodogsplash registers us
DEFAULT_MAX_BLIND_PAYMENTS = 3

DEFAULT_RENEW_BELOW_BYTES = 20 * 1024 * 1024   # 20 MiB
DEFAULT_RENEW_BELOW_MS = 30_000                # 30 s

# Router notice codes that retrying can never fix. Paying again with the
# same amount is futile (below-swap-fee), the token is already gone
# (spent/invalid), the keyset it was minted under is retired
# (keyset-expired: rotation makes re-POSTing the same note pointless
# forever), or the router itself could not decide the outcome
# (outcome-unknown: the POST may have been credited, and re-sending it
# is exactly the double-spend the router's message forbids).
TERMINAL_PAYMENT_CODES = {
    "payment-error-below-swap-fee",
    "payment-error-invalid-token",
    "payment-error-token-spent",
    "payment-error-keyset-expired",
    "payment-outcome-unknown",
}

RECV_BUF = 1 << 20


class ClientError(Exception):
    pass


class TerminalPaymentError(ClientError):
    """The router rejected the payment with a code retrying cannot fix."""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code


class BlindPaymentCapExceeded(ClientError):
    """Payments succeed but /usage never reflects them: stop paying."""


def default_state_dir() -> str:
    """Where unacknowledged payment tokens are preserved (XDG state)."""
    xdg = os.environ.get("XDG_STATE_HOME")
    base = xdg if xdg else os.path.expanduser("~/.local/state")
    return os.path.join(base, "tollgate-clientd")


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


def pay(gateway: str, mac: str, token: str, timeout: float = PAYMENT_TIMEOUT) -> dict:
    """POST a raw Cashu token; return the session event on success."""
    status, body = http_post(f"{_base(gateway)}/?mac={mac}", token, timeout=timeout)
    try:
        ev = json.loads(body)
    except json.JSONDecodeError:
        raise ClientError(f"payment reply not JSON (HTTP {status}): {body[:200]!r}")
    if status == 200 and ev.get("kind") == 1022:
        return ev
    # kind 21023 notice (or anything else) — surface the router's message
    # and its coded reason; terminal codes must stop the daemon instead
    # of feeding the retry loop (#423).
    message = ev.get("content") or ""
    code = None
    for tag in ev.get("tags", []):
        if tag[0] == "message":
            message = tag[1] if len(tag) > 1 else message
        elif tag[0] == "code" and len(tag) > 1:
            code = tag[1]
    if code in TERMINAL_PAYMENT_CODES:
        raise TerminalPaymentError(
            code, f"payment rejected (HTTP {status}, terminal code {code}): "
                  f"{message or body[:200]}")
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


def _expand_keyset_ids(token: str, mint_url: str) -> str:
    """Rewrite short 8-byte keyset IDs in a V3 token to full 33-byte IDs.

    cdk-cli stores proofs with truncated keyset IDs; cdk-mintd's swap
    endpoint only accepts full IDs. Mirrors the proven workaround in
    tests/cloud-lab/conftest.py. Best effort: any failure returns the
    token unchanged."""
    import base64
    if not token.startswith("cashuA"):
        return token
    try:
        payload = token[6:]
        payload += "=" * (4 - len(payload) % 4)
        data = json.loads(base64.urlsafe_b64decode(payload))
        keysets = json.loads(http_get(f"{mint_url}/v1/keysets")).get("keysets", [])
        short_to_full = {ks["id"][:16]: ks["id"] for ks in keysets
                         if len(ks["id"]) == 66}
        for entry in data.get("token", []):
            for proof in entry.get("proofs", []):
                pid = proof.get("id", "")
                if len(pid) == 16 and pid in short_to_full:
                    proof["id"] = short_to_full[pid]
        return "cashuA" + base64.urlsafe_b64encode(
            json.dumps(data).encode()).decode()
    except Exception as e:  # noqa: BLE001 — expansion is best effort by design
        # But not silent: cdk-mintd rejects short keyset IDs outright, so a
        # token that skips expansion is very likely to be refused with a
        # misleading error at pay time. Say why the token went out bare.
        print(f"warning: keyset-ID expansion failed ({e}); sending the "
              f"token with its original keyset IDs — cdk-mintd mints will "
              f"reject it", file=sys.stderr)
        return token


_CDK_AMOUNT_FLAG: bool | None = None


def _cdk_cli_has_amount_flag() -> bool:
    global _CDK_AMOUNT_FLAG
    if _CDK_AMOUNT_FLAG is None:
        proc = subprocess.run(["cdk-cli", "send", "--help"], capture_output=True,
                              text=True, check=False)
        _CDK_AMOUNT_FLAG = "--amount" in (proc.stdout + proc.stderr)
    return _CDK_AMOUNT_FLAG


def wallet_send_cdk_cli(mint_url: str, amount_sats: int,
                        wallet_dir: str | None) -> str:
    cmd = ["cdk-cli"]
    if wallet_dir:
        cmd += ["-w", wallet_dir]
    cmd += ["send", "--mint-url", mint_url]
    stdin = None
    if _cdk_cli_has_amount_flag():
        # modern cdk-cli: explicit amount, V3 token (cdk-mintd-friendly)
        cmd += ["--v3", "--amount", str(amount_sats)]
    else:
        # legacy cdk-cli (as used by the hardware e2e fleet): amount via stdin
        stdin = f"{amount_sats}\n"
    proc = subprocess.run(cmd, input=stdin, capture_output=True,
                          text=True, check=False)
    if proc.returncode != 0:
        raise ClientError(f"cdk-cli send failed: {proc.stderr.strip() or proc.stdout.strip()}")
    return _expand_keyset_ids(_extract_token(proc.stdout), mint_url)


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
        self.state_dir = args.state_dir or default_state_dir()
        self.max_blind_payments = getattr(
            args, "max_blind_payments", DEFAULT_MAX_BLIND_PAYMENTS)
        self.payment_timeout = getattr(
            args, "payment_timeout", PAYMENT_TIMEOUT)
        # A payment that succeeds while /usage keeps reporting no session
        # cannot be distinguished from one that vanished — count those and
        # stop after --max-blind-payments instead of re-paying forever (#422).
        self.blind_payments = 0
        # Allotment of the CURRENT session only (None between sessions):
        # the server creates a fresh session after expiry (#541), so an
        # all-time high-water mark would never be exceeded again and
        # healthy post-expiry renewals would trip the blind cap.
        self.last_allotment: int | None = None
        self._session_start: str | None = None

    def amount_sats(self) -> int:
        return self.steps * self.offer.price

    def _pending_path(self) -> str:
        digest = hashlib.sha1(
            f"{self.offer.mint_url}|{self.amount_sats()}".encode()).hexdigest()[:12]
        return os.path.join(self.state_dir, f"pending-{digest}.token")

    def _load_pending_token(self) -> str | None:
        try:
            with open(self._pending_path()) as f:
                return f.read().strip() or None
        except OSError:
            return None

    def _save_pending_token(self, token: str) -> None:
        os.makedirs(self.state_dir, mode=0o700, exist_ok=True)
        path = self._pending_path()
        with open(path, "w") as f:
            f.write(token + "\n")
        os.chmod(path, 0o600)

    def _clear_pending_token(self) -> None:
        try:
            os.unlink(self._pending_path())
        except OSError:
            pass

    def top_up(self) -> str:
        """Pay for self.steps; returns a short status message."""
        now = time.monotonic()
        if now - self.last_payment < PAYMENT_THROTTLE:
            return "throttled"
        self.last_payment = now
        register_with_portal(self.gateway)
        # A token minted for a previous attempt that never got acknowledged
        # is still money: reuse it instead of minting (and paying for)
        # another one (#423).
        token = self._load_pending_token()
        reused = token is not None
        if not reused:
            token = self.wallet_sender(self.offer.mint_url, self.amount_sats(),
                                       self.args.wallet_dir)
            self._save_pending_token(token)
        try:
            ev = pay(self.gateway, self.mac, token,
                     timeout=self.payment_timeout)
        except TerminalPaymentError as e:
            if e.code == "payment-error-token-spent":
                self._clear_pending_token()
            elif e.code == "payment-outcome-unknown":
                # The POST may have been credited: keep the token on disk
                # for reconciliation with the operator, but under a name
                # the reuse path will never pick up again.
                path = self._pending_path()
                try:
                    os.rename(path, path + ".outcome-unknown")
                except OSError:
                    pass
            raise
        self._clear_pending_token()
        self.backoff = PAYMENT_THROTTLE
        allotment = ""
        start_time = ""
        for tag in ev.get("tags", []):
            if tag[0] == "allotment" and len(tag) > 1:
                allotment = tag[1]
            elif tag[0] == "start-time" and len(tag) > 1:
                start_time = tag[1]
        # A new start-time means the server opened a fresh session (#541):
        # re-arm the per-session comparison so the next /usage observation
        # of this session counts as proof of credit again.
        if start_time and start_time != self._session_start:
            self._session_start = start_time
            self.last_allotment = None
        # The router's 1022 event is NOT proof of credit for this client:
        # in the #422 failure mode payments are accepted while /usage
        # stays -1/-1 forever. Only run()'s /usage observations reset the
        # counter; a payment that nothing observed counts as blind.
        self.blind_payments += 1
        if self.blind_payments >= self.max_blind_payments:
            raise BlindPaymentCapExceeded(
                f"{self.blind_payments} payment(s) accepted by the gateway "
                f"without /usage ever reflecting them — the router cannot "
                f"map this client to a session (its /usage stays -1/-1; "
                f"see #422). Not paying again. These payments were "
                f"ACCEPTED by the gateway, so the sats are in its wallet "
                f"— reconcile with the gateway operator, not with local "
                f"files. (Only a rejected attempt leaves a recoverable "
                f"token under {self.state_dir}.)")
        origin = "reused pending" if reused else "minted"
        msg = (f"paid {self.amount_sats()} sats ({origin} token, "
               f"allotment now {allotment or '?'})")
        if allotment.isdigit():
            credited_steps = int(allotment) // self.ad.step_size
            if credited_steps < self.steps:
                msg += (f"  [warning: {credited_steps} of {self.steps} "
                        f"paid-for step(s) credited — the mint's swap fee "
                        f"reduced the purchase; buy more per top-up]")
        return msg

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

    def observe(self, st: dict) -> None:
        """Feed one /usage observation into the blind-payment tracker.

        Growth within the current session proves the gateway credits
        this client — clear the blind counter. A missing last_allotment
        means a session not yet observed (fresh after expiry, #541): the
        first observation counts as proof the same way. Observing no
        session arms the reset for whatever session the server opens
        next."""
        if st["allotment"] is not None:
            if (self.last_allotment is None
                    or st["allotment"] > self.last_allotment):
                self.blind_payments = 0
            self.last_allotment = st["allotment"]
        else:
            self.last_allotment = None

    def run(self) -> None:
        intro = (f"TollGate {self.gateway} | {self.ad.metric} | "
                 f"{self.steps} step(s) = {self.amount_sats()} sats via "
                 f"{self.wallet_name} @ {self.offer.mint_url}")
        print(intro, file=sys.stderr)
        last_line = ""
        while True:
            try:
                st = self.status()
                self.observe(st)
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
                    except (TerminalPaymentError, BlindPaymentCapExceeded) as e:
                        print(f"FATAL: {e}", file=sys.stderr)
                        print("stopping — retrying cannot fix this; "
                              "see the message above.", file=sys.stderr)
                        raise
                    except (ClientError, OSError) as e:
                        self.backoff = min(self.backoff * 2, PAYMENT_BACKOFF_MAX)
                        self.last_payment = time.monotonic()
                        print(f"  !! top-up failed: {e} "
                              f"(retrying in {self.backoff:.0f}s)", file=sys.stderr)
            except (TerminalPaymentError, BlindPaymentCapExceeded):
                raise
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


def print_waybar(daemon: ClientDaemon) -> None:
    """One-shot waybar module JSON: re-run this via a custom/tollgate module
    with "interval": 5 and "return-type": "json"."""
    st = daemon.status()
    threshold = daemon.renew_below
    if threshold is None:
        threshold = (DEFAULT_RENEW_BELOW_MS if st["metric"] == "milliseconds"
                     else DEFAULT_RENEW_BELOW_BYTES)
    if st["remaining"] is None:
        text, module_class, pct = "no session", "critical", 0
    else:
        text = format_remaining(st["metric"], st["remaining"]).replace(" left", "")
        pct = int(100 * st["remaining"] / st["allotment"]) if st["allotment"] else 0
        module_class = "warning" if st["remaining"] <= 2 * threshold else "good"
    print(json.dumps({
        "text": f"TG {text}",
        "tooltip": (f"TollGate {st['gateway']} ({st['metric']})\n"
                    f"usage {st['usage']}/{st['allotment']}\n"
                    f"mint {st['mint']}\nwallet {st['wallet']}"),
        "class": module_class,
        "percentage": pct,
    }))


def print_offers(daemon: ClientDaemon) -> None:
    ad = daemon.ad
    print(f"gateway {daemon.gateway}  metric={ad.metric}  step_size={ad.step_size}")
    for o in ad.offers:
        print(f"  {o.price} {o.unit}/step  mint={o.mint_url}  min_steps={o.min_steps}")


# ---------------------------------------------------------------------------
# Selftest: mock TollGate + stub wallet, exercises the full loop in-process
# ---------------------------------------------------------------------------

def selftest() -> int:
    import shutil
    import tempfile
    import threading
    from http.server import BaseHTTPRequestHandler, HTTPServer

    state = {"usage": 0, "allotment": 0, "payments": [], "polls": 0,
             "session_open": False, "start_time": 0}
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
                    state["session_open"] = False
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
            if state["session_open"]:
                state["allotment"] += (amount_sats // PRICE) * STEP
            else:
                # Mirrors the server's #541 semantics: a payment with no
                # live session opens a fresh one at the paid amount.
                state["allotment"] = (amount_sats // PRICE) * STEP
                state["usage"] = 0
                state["session_open"] = True
                state["start_time"] += 1
            state["usage"] = 0
            ev = json.dumps({"kind": 1022, "tags": [
                ["allotment", str(state["allotment"])],
                ["start-time", str(state["start_time"])],
                ["metric", "bytes"]], "content": ""}).encode()
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
        state_dir = tempfile.mkdtemp(prefix="clientd-selftest-")

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
            d.observe(st)  # run()'s loop feeds every poll through observe()
            if d.needs_top_up(st["remaining"]):
                d.top_up()
            time.sleep(0.05)
        if len(state["payments"]) < 2:
            failures.append(f"renewal never fired (payments={len(state['payments'])})")
        if state["payments"][0]["mac"] != "02:00:00:00:00:02":
            failures.append("payment did not carry the client MAC")
        # burn past exhaustion so the session ends, then pay again: under
        # the server's fresh-session semantics (#541) the re-payment must
        # NOT trip the blind cap — the new session's first /usage
        # observation resets it (the pre-fix high-water mark broke here).
        deadline = time.monotonic() + 30
        while True:
            st = d.status()
            d.observe(st)
            if st["allotment"] is None or time.monotonic() > deadline:
                break
        try:
            d.last_payment = 0.0  # skip the inter-payment throttle
            d.top_up()
            st = d.status()
            assert st["allotment"] is not None and st["allotment"] > 0, \
                f"post-expiry payment did not open a session: {st}"
        except BlindPaymentCapExceeded as e:
            failures.append(f"post-expiry re-payment tripped the blind cap: {e}")
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
    ap.add_argument("--mac",
                    help="MAC to send with the payment (default: auto-detect). "
                         "Informational since the router resolves clients "
                         "from the socket (#548); kept for lab fixtures "
                         "that key sessions by MAC.")
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
    ap.add_argument("--payment-timeout", type=float, default=PAYMENT_TIMEOUT,
                    help=f"seconds to wait for a payment POST "
                         f"(default: {PAYMENT_TIMEOUT:.0f} — routers may "
                         f"swap synchronously)")
    ap.add_argument("--max-blind-payments", type=int,
                    default=DEFAULT_MAX_BLIND_PAYMENTS,
                    help="stop (non-zero exit) after this many payments the "
                         "gateway accepted without /usage ever reflecting "
                         "them (default: 3)")
    ap.add_argument("--state-dir",
                    help="where unacknowledged payment tokens are preserved "
                         "(default: ~/.local/state/tollgate-clientd)")
    ap.add_argument("--status", action="store_true",
                    help="print current status once and exit")
    ap.add_argument("--json", action="store_true",
                    help="with --status: emit JSON")
    ap.add_argument("--waybar", action="store_true",
                    help="print waybar module JSON once and exit "
                         "(for a custom/tollgate status-bar module)")
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
    if args.waybar:
        print_waybar(d)
        return 0
    if args.status:
        print_status(d, args.json)
        return 0

    try:
        d.run()
    except KeyboardInterrupt:
        print(file=sys.stderr)
        return 0
    except (TerminalPaymentError, BlindPaymentCapExceeded) as e:
        print(f"error: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
