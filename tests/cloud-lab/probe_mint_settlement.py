#!/usr/bin/env python3
"""probe_mint_settlement.py — is a cdk-mintd FakeWallet actually settling?

`/v1/keys` (or /v1/info) answering is not proof of health: a wedged
cdk-mintd accepts quotes but its FakeWallet never settles them, so every
payment test hangs until an outer timeout (PRTA #95). This probe exercises
the real quote -> PAID path, host-side, stdlib only.

Exit codes:
  0  quote settled (mint is genuinely alive)
  1  WEDGE: quote accepted but never PAID within the timeout
  2  mint unreachable / quote creation failed

Connection-level errors during the first --connect-timeout seconds are
retried: a just-recreated container flaps its port (broken pipe / refused)
while binding, which is not a health verdict. Only once a quote exists does
the wedge window start.

Usage: probe_mint_settlement.py <mint-url> [--timeout 20] [--amount 1]
"""

import argparse
import json
import sys
import time
import urllib.error
import urllib.request


def request(url, data=None, timeout=5):
    req = urllib.request.Request(
        url,
        data=json.dumps(data).encode() if data is not None else None,
        headers={"Content-Type": "application/json"},
        method="POST" if data is not None else "GET",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def create_quote(mint_url, amount, connect_timeout):
    """POST a mint quote, retrying connection errors while the container
    (re)binds its port. Returns the quote id, or None if never reachable."""
    deadline = time.monotonic() + connect_timeout
    while True:
        try:
            return request(
                f"{mint_url}/v1/mint/quote/bolt11",
                {"amount": amount, "unit": "sat"},
            ).get("quote")
        except (urllib.error.URLError, OSError, ValueError):
            if time.monotonic() >= deadline:
                return None
            time.sleep(1)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("mint_url", help="e.g. http://localhost:8087")
    ap.add_argument("--timeout", type=int, default=20)
    ap.add_argument("--connect-timeout", type=int, default=15)
    ap.add_argument("--amount", type=int, default=1)
    args = ap.parse_args()

    quote = create_quote(args.mint_url, args.amount, args.connect_timeout)
    if not quote:
        print(
            f"UNREACHABLE: {args.mint_url} refused quote creation for "
            f"{args.connect_timeout}s",
            file=sys.stderr,
        )
        return 2

    deadline = time.monotonic() + args.timeout
    last_state = None
    while time.monotonic() < deadline:
        try:
            last_state = request(
                f"{args.mint_url}/v1/mint/quote/bolt11/{quote}"
            ).get("state")
        except (urllib.error.URLError, OSError, ValueError) as e:
            print(f"WEDGE: quote {quote} created but polling failed: {e}", file=sys.stderr)
            return 1
        if last_state == "PAID":
            print(f"OK: quote {quote} settled (state=PAID)")
            return 0
        time.sleep(2)

    print(
        f"WEDGE: quote {quote} accepted but never settled "
        f"(state={last_state!r} after {args.timeout}s) — {args.mint_url} "
        f"answers but its FakeWallet does not settle; restart it "
        f"(cf. PRTA #95)",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
