"""fund_reseller.py — credit the reseller's wallet so it can autopay upstream.

Run inside the client container against the two-router lab:

    docker compose -f docker-compose.yml run --rm --entrypoint python3 \
        client fund_reseller.py

Pays the reseller AMOUNT sats for the client MAC. The reseller's wallet
keeps the tokens (payout sweeps are disabled in the lab config), giving
it the balance it needs to purchase the upstream session when discovery
fires — mirroring the real reseller bootstrap where selling to customers
funds the upstream autopay.
"""

import sys
import time

import requests

from conftest import (
    MINT_URL,
    RESELLER_URL,
    build_payment_event,
    create_cashu_token,
    generate_nostr_keypair,
    run_cmd,
    wait_for,
)

AMOUNT = 200  # sats; reseller price is 2 sats/step -> 100 steps for the client
CLIENT_MAC = "02:00:00:00:00:20"


def main() -> int:
    wait_for(RESELLER_URL)

    r = requests.get(RESELLER_URL, timeout=10)
    r.raise_for_status()
    reseller_pubkey = r.json().get("pubkey")
    if not reseller_pubkey:
        print("FUND-FAIL: reseller advertisement has no pubkey")
        return 1

    wallet = "/tmp/fund-wallet"
    run_cmd(["cdk-cli", "-w", wallet, "mint", MINT_URL, "10000"])
    customer_sec, customer_pub = generate_nostr_keypair()
    token = create_cashu_token(wallet, AMOUNT)
    event = build_payment_event(
        customer_sec, customer_pub, reseller_pubkey, CLIENT_MAC, token
    )

    deadline = time.time() + 120
    while time.time() < deadline:
        r = requests.post(RESELLER_URL, json=event, timeout=60)
        if r.status_code == 200 and r.json().get("kind") == 1022:
            print(f"FUND-OK: paid {AMOUNT} sats to reseller, session granted")
            return 0
        print(f"FUND-RETRY: status={r.status_code} body={r.text[:200]}")
        time.sleep(5)

    print("FUND-FAIL: reseller never accepted the payment")
    return 1


if __name__ == "__main__":
    sys.exit(main())
