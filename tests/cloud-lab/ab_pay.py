#!/usr/bin/env python3
"""ab_pay.py — A/B driver for the degraded-mode pair (#400/#401).

Subcommands (run inside the client container):
  prepare        fund wallet, stash three 10-sat tokens in .ab-tokens.json
  pay <i> <mac>  pay token i; print status/kind/code/content for evidence
"""
import json
import subprocess
import sys
import tempfile

import requests

MINT = "http://mint:8085"
UPSTREAM = "http://upstream:2121"
TOKENS = "/tests/.ab-tokens.json"
WALLET = "/tmp/ab-wallet"


def run(args):
    p = subprocess.run(args, capture_output=True, text=True)
    if p.returncode != 0:
        sys.exit(f"cmd failed: {args}\n{p.stdout}\n{p.stderr}")
    return p.stdout


def prepare():
    run(["cdk-cli", "-w", WALLET, "mint", MINT, "10000"])
    tokens = [send(10) for _ in range(3)]
    with open(TOKENS, "w") as f:
        json.dump(tokens, f)
    print("prepared 3 tokens")


def send(amount):
    p = subprocess.run(
        ["cdk-cli", "-w", WALLET, "send", "--mint-url", MINT, "--v3",
         "--amount", str(amount)], capture_output=True, text=True)
    for line in reversed(p.stdout.splitlines()):
        if line.startswith("cashu"):
            return line
    sys.exit(f"no token: {p.stdout}\n{p.stderr}")


def pay(i, mac):
    token = json.load(open(TOKENS))[i]
    sec = run(["nak", "key", "generate"]).strip()
    pub = run(["nak", "key", "public", sec]).strip()
    upstream_pub = requests.get(UPSTREAM, timeout=5).json()["pubkey"]
    event = {
        "kind": 21000, "pubkey": pub, "content": "",
        "tags": [["p", upstream_pub], ["device-identifier", "mac", mac],
                 ["payment", token]],
    }
    p = subprocess.run(["nak", "event", "--sec", sec], input=json.dumps(event),
                       capture_output=True, text=True)
    r = requests.post(f"{UPSTREAM}?mac={mac}", json=json.loads(p.stdout), timeout=40)
    try:
        ev = r.json()
        codes = [t[1] for t in ev.get("tags", []) if t[0] == "code"]
        print(f"PAY#{i} http={r.status_code} kind={ev.get('kind')} "
              f"code={codes} content={ev.get('content', '')[:90]}")
    except ValueError:
        print(f"PAY#{i} http={r.status_code} non-json: {r.text[:90]}")


if __name__ == "__main__":
    {"prepare": prepare}.get(sys.argv[1], lambda: pay(int(sys.argv[2]), sys.argv[3]))()
