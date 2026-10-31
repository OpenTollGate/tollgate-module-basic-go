"""cdk_e2e_pay.py — one-shot payment helper for run-cdk-backend-e2e.sh.

Mints a fresh 100-sat token from the lab mint, builds and signs the
kind-21000 payment event, POSTs it to UPSTREAM_URL, and prints a one-line
JSON verdict ({"status": int, "kind": int|None}). Optionally stages the
event for a later replay (--event-out) so the kill-leg can re-present the
identical proofs.
"""

import argparse
import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import requests  # noqa: E402

from conftest import build_payment_event, create_cashu_token, generate_nostr_keypair  # noqa: E402


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mac", default="02:00:00:00:00:20")
    ap.add_argument("--event-out", default=None)
    ap.add_argument("--verbose", action="store_true",
                    help="include session tags (discriminates a fresh grant from a cached re-serve)")
    args = ap.parse_args()

    url = os.environ["UPSTREAM_URL"]
    tollgate_pub = requests.get(url, timeout=15).json()["pubkey"]
    sec, pub = generate_nostr_keypair()
    wallet = tempfile.mkdtemp(prefix="cdk-e2e-")
    # Fund the throwaway wallet first (mirrors conftest's ecash_wallet
    # fixture) — cdk-cli send on a fresh wallet is "Insufficient funds".
    from conftest import MINT_URL, run_cmd

    run_cmd(["cdk-cli", "-w", wallet, "mint", MINT_URL, "10000"])
    token = create_cashu_token(wallet, 100)
    event = build_payment_event(sec, pub, tollgate_pub, args.mac, token)
    if args.event_out:
        with open(args.event_out, "w") as f:
            json.dump(event, f)

    r = requests.post(url, json=event, timeout=60)
    body = {}
    try:
        body = r.json()
    except ValueError:
        pass
    out = {"status": r.status_code, "kind": body.get("kind")}
    if args.verbose:
        tags = {t[0]: t[1:] for t in body.get("tags", [])}
        keep = ("expiry", "expires", "allotment", "amount", "session")
        out["tags"] = {k: tags[k] for k in tags if k in keep}
        out["id"] = body.get("id")
    print(json.dumps(out))


if __name__ == "__main__":
    main()
