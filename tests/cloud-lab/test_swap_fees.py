"""
test_swap_fees.py — Fee-charging mint coverage for the payment path (#409).

Real-world mints (e.g. mint.coinos.io) charge an input fee per swapped proof
(input_fee_ppk). The `mint-fees` service mirrors that with
CDK_MINTD_INPUT_FEE_PPK=100: a single-proof swap costs ceil(100/1000) = 1 sat,
so a 1-sat token is entirely consumed by the fee.

This suite validates end-to-end — real cdk-mintd keysets, real gonuts wallet,
real TollGate HTTP surface — what the unit tests stub:
  - the fee is visible in the mint's keyset info
  - a below-fee payment is refused BEFORE the swap with the coded
    `payment-error-below-swap-fee` notice, and the token stays unspent
  - an above-fee payment succeeds and the fee is deducted from the allotment
"""

import json

import pytest
import requests

from conftest import (
    MINT_FEES_URL,
    UPSTREAM_URL,
    build_payment_event,
    create_cashu_token,
    run_cmd,
)


def notice_code(event):
    """Extract the code tag from a kind-21023 notice event."""
    for tag in event.get("tags", []):
        if len(tag) >= 2 and tag[0] == "code":
            return tag[1]
    return None


# Session allotments are cumulative per MAC, so every test pays from a
# distinct MAC to assert absolute amounts independently of test order. The
# handler takes the MAC from the ?mac= query param (the way the splash page
# passes it from nodogsplash preauth); without it every payment falls back
# to the client IP's lease entry and lands on one shared MAC.
BELOW_FEE_MAC = "02:00:00:00:00:21"
ABOVE_FEE_MAC = "02:00:00:00:00:22"
FREE_MINT_MAC = "02:00:00:00:00:23"


def pay(token, upstream_pubkey, customer_identity, mac):
    """POST a payment event attributed to `mac`; returns the Response."""
    customer_sec, customer_pub = customer_identity
    event = build_payment_event(
        customer_sec, customer_pub, upstream_pubkey, mac, token
    )
    return requests.post(f"{UPSTREAM_URL}?mac={mac}", json=event, timeout=30)


class TestSwapFees:

    def test_fee_mint_charges_input_fee(self, fees_mint_health):
        """The mint-fees keysets must actually carry the fee this suite pins.

        If this fails, the CDK_MINTD_INPUT_FEE_PPK wiring changed and every
        other test in this file is testing the wrong fixture.
        """
        r = requests.get(f"{MINT_FEES_URL}/v1/keysets", timeout=5)
        assert r.status_code == 200
        keysets = r.json().get("keysets", [])
        assert keysets, f"No keysets: {r.json()}"
        sat_keysets = [k for k in keysets if k.get("unit") == "sat"]
        assert sat_keysets, f"No sat keysets: {keysets}"
        for ks in sat_keysets:
            assert ks.get("input_fee_ppk") == 100, (
                f"Expected input_fee_ppk=100 on {ks}"
            )

    def test_below_swap_fee_refused_before_spend(
        self, upstream_health, upstream_pubkey, fees_ecash_wallet,
        customer_identity,
    ):
        """A 1-sat token against a 1-sat swap fee is refused up front, and
        the refusal is not a spend: the same token refuses identically twice.
        (If the token had been swapped, the second attempt would fail with
        payment-error-token-spent instead.)"""
        token = create_cashu_token(fees_ecash_wallet, 1, mint_url=MINT_FEES_URL)

        r1 = pay(token, upstream_pubkey, customer_identity, BELOW_FEE_MAC)
        assert r1.status_code == 400, (
            f"Expected HTTP 400, got {r1.status_code}: {r1.text}"
        )
        event = r1.json()
        assert event.get("kind") == 21023, (
            f"Expected notice (kind 21023), got: {json.dumps(event, indent=2)}"
        )
        assert notice_code(event) == "payment-error-below-swap-fee", (
            f"Expected payment-error-below-swap-fee, got: {notice_code(event)}"
        )

        r2 = pay(token, upstream_pubkey, customer_identity, BELOW_FEE_MAC)
        assert r2.status_code == 400
        assert notice_code(r2.json()) == "payment-error-below-swap-fee", (
            "Token was consumed by the first attempt (second refusal should be "
            f"identical, got: {notice_code(r2.json())})"
        )

    def test_above_swap_fee_succeeds_with_fee_deducted(
        self, upstream_health, upstream_pubkey, fees_ecash_wallet,
        customer_identity,
    ):
        """A 100-sat token pays the 1-sat swap fee and succeeds; the session
        event must reflect the post-fee amount, not the token face value."""
        token = create_cashu_token(fees_ecash_wallet, 100, mint_url=MINT_FEES_URL)

        r = pay(token, upstream_pubkey, customer_identity, ABOVE_FEE_MAC)
        assert r.status_code == 200, (
            f"Payment failed: HTTP {r.status_code}\nResponse: {r.text}"
        )
        event = r.json()
        assert event.get("kind") == 1022, (
            f"Expected session event (kind 1022), got: "
            f"{json.dumps(event, indent=2)}"
        )

        tags = {t[0]: t[1:] for t in event.get("tags", []) if len(t) >= 2}
        assert "allotment" in tags, f"Session event missing allotment tag: {tags}"
        # cdk-cli decomposes 100 sats into powers of two (64+32+4 = 3 proofs);
        # 3 proofs x 100 ppk = ceil(300/1000) = 1 sat fee -> 99 sats credited
        # at price_per_step=1, step_size=60000ms.
        allotment = int(tags["allotment"][0])
        assert allotment == 99 * 60000, (
            f"Allotment {allotment} does not reflect the 1-sat swap fee "
            f"(expected {99 * 60000} = 99 steps): {tags}"
        )

    def test_fee_mint_payments_do_not_break_the_free_mint_path(
        self, upstream_health, upstream_pubkey, ecash_wallet,
        customer_identity, mint_health,
    ):
        """Accepting a fee-charging mint must not change zero-fee payments:
        the free mint still credits the full token face value."""
        token = create_cashu_token(ecash_wallet, 100)

        r = pay(token, upstream_pubkey, customer_identity, FREE_MINT_MAC)
        assert r.status_code == 200, f"Free-mint payment failed: {r.text}"
        event = r.json()
        assert event.get("kind") == 1022

        tags = {t[0]: t[1:] for t in event.get("tags", []) if len(t) >= 2}
        allotment = int(tags["allotment"][0])
        assert allotment == 100 * 60000, (
            f"Free-mint allotment {allotment} should equal the full 100 steps "
            f"({100 * 60000}): {tags}"
        )
