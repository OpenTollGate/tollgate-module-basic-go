"""cdk_e2e_quote.py — LEG C: a Lightning quote survives a sidecar restart.

POST /ln-invoice creates a mint quote through the module (RequestMintQuote
on the sidecar). The leg then restarts cdk-walletd — the quote's durable
state lives in the sidecar's sqlite — and asserts the module can still
drive the same quote afterwards: the status poll answers (never
"unknown quote") and a paid quote still grants access.

Run in two phases from the driver:
  create   → prints the quote JSON (id, invoice, state)
  poll ID  → prints the status poll JSON for the quote
"""

import json
import os
import sys

import requests

BASE = os.environ["UPSTREAM_URL"]
MAC = "02:00:00:00:00:20"


def quote_body(r: requests.Response) -> dict:
    try:
        body = dict(r.json())
    except ValueError:
        body = {"raw": r.text}
    body["http"] = r.status_code
    return body


def create() -> None:
    r = requests.post(
        f"{BASE}/ln-invoice",
        json={"amount": 100, "mac": MAC, "mint_url": "http://mint:8085"},
        timeout=30,
    )
    body = quote_body(r)
    print(json.dumps(body))


def poll(quote_id: str) -> None:
    r = requests.get(
        f"{BASE}/ln-invoice",
        params={"quote": quote_id, "mac": MAC},
        timeout=30,
    )
    body = quote_body(r)
    print(json.dumps(body))


if __name__ == "__main__":
    if len(sys.argv) < 2 or sys.argv[1] not in ("create", "poll"):
        raise SystemExit("usage: cdk_e2e_quote.py create | poll <quote-id>")
    if sys.argv[1] == "create":
        create()
    else:
        if len(sys.argv) < 3:
            raise SystemExit("poll needs a quote id")
        poll(sys.argv[2])
