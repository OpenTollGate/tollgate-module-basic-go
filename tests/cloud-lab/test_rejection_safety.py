"""
test_rejection_safety.py — a rejected payment must never burn the token.

The #423-class property, pinned at the tollgate HTTP surface: a payment
refused while the mint is unreachable must leave the customer's ecash
intact — every proof still UNSPENT at the mint (NUT-07), and the very
same token still able to buy a session once the mint recovers.

The lane is three-phase, driven by ./run-rejection-safety.sh from the
host (the client container has no docker control, and `compose run`
would restart a stopped mint through the client's depends_on — hence the
--no-deps step): A1 mints and stashes the token; A2 pays during the
outage and must be refused without a crash; B proves the token survived
byte for byte. The phase tests are gated on REJECTION_LANE=1 and skip in
a default `client` run, exactly like the keyset-rotation lane.

test_mint_failure.py covers graceful degradation with FRESH tokens per
attempt (its docker-gated legs skip in-container); test_swap_fees.py
covers the below-fee refusal's spentness. The same-token-across-outage
leg is pinned only here.
"""

import json
import os
import random

import pytest
import requests

from conftest import (
    MINT_URL,
    UPSTREAM_URL,
    build_payment_event,
    create_cashu_token,
    wait_for,
)
from test_swap_fees import assert_token_unspent

STATE_FILE = os.path.join(os.path.dirname(__file__), ".rejection-state.json")

REJECTION_LANE = os.environ.get("REJECTION_LANE") == "1"


def _mac():
    # Locally-administered, session-random: allotments are cumulative per
    # MAC, so a stable MAC would couple this suite to every other run.
    return "02:%02x:%02x:%02x:%02x:%02x" % tuple(random.randbytes(5))


def pay(token, upstream_pubkey, customer_identity, mac):
    customer_sec, customer_pub = customer_identity
    event = build_payment_event(customer_sec, customer_pub, upstream_pubkey, mac, token)
    return requests.post(f"{UPSTREAM_URL}?mac={mac}", json=event, timeout=60)


def _load_state():
    with open(STATE_FILE) as f:
        return json.load(f)


def _save_state(state):
    with open(STATE_FILE, "w") as f:
        json.dump(state, f)


class TestOutageDoesNotBurnToken:

    @pytest.mark.skipif(not REJECTION_LANE, reason="phase tests need ./run-rejection-safety.sh (REJECTION_LANE=1)")
    def test_phase_a1_mint_and_stash_token(
        self, upstream_health, ecash_wallet
    ):
        """Mint is up: fund the wallet, stash the token and its paying MAC
        for the outage phase."""
        token = create_cashu_token(ecash_wallet, 50)
        _save_state({"token": token, "mac": _mac()})

    @pytest.mark.skipif(not REJECTION_LANE, reason="phase tests need ./run-rejection-safety.sh (REJECTION_LANE=1)")
    def test_phase_a2_refusal_during_outage(
        self, upstream_health, upstream_pubkey, customer_identity
    ):
        """Mint is down (the script stopped it, and this phase runs with
        --no-deps so compose cannot restart it). The payment must be
        refused — not crash, not 500 — and the tollgate must stay up.

        A 1022 session event here would mean the tollgate now accepts
        payments during mint outages (offline acceptance); if that
        becomes intended behavior, this pin must be consciously updated
        alongside the settlement guarantees such a mode needs."""
        state = _load_state()
        r = pay(state["token"], upstream_pubkey, customer_identity, state["mac"])

        assert r.status_code >= 400 or r.json().get("kind") == 21023, (
            f"refusal expected during outage, got HTTP {r.status_code}: {r.text[:300]}"
        )
        assert requests.get(UPSTREAM_URL, timeout=10).status_code == 200, (
            "tollgate crashed on the outage refusal"
        )

    @pytest.mark.skipif(not REJECTION_LANE, reason="phase tests need ./run-rejection-safety.sh (REJECTION_LANE=1)")
    def test_phase_b_same_token_still_spends(
        self, upstream_health, upstream_pubkey, customer_identity
    ):
        """Mint is back (the script restarted it). The token refused
        during the outage must be fully UNSPENT at the mint and must
        still buy a session, byte for byte."""
        assert wait_for(f"{MINT_URL}/v1/keys", timeout=60), "mint did not recover"

        state = _load_state()

        # The mint is the authority: the refusal path consumed nothing.
        assert_token_unspent(state["token"], mint_url=MINT_URL)

        r = pay(state["token"], upstream_pubkey, customer_identity, state["mac"])
        assert r.status_code == 200, (
            f"token refused during the outage no longer spends: HTTP {r.status_code}: {r.text[:300]}"
        )
        assert r.json().get("kind") == 1022, (
            f"expected a session event, got: {r.text[:300]}"
        )


class TestReplaySafety:

    def test_replayed_token_grants_no_second_session(
        self, upstream_health, upstream_pubkey, ecash_wallet, customer_identity
    ):
        """A token that already bought a session must not buy another."""
        token = create_cashu_token(ecash_wallet, 50)
        mac = _mac()

        r1 = pay(token, upstream_pubkey, customer_identity, mac)
        assert r1.status_code == 200 and r1.json().get("kind") == 1022, (
            f"first payment failed: HTTP {r1.status_code}: {r1.text[:300]}"
        )

        r2 = pay(token, upstream_pubkey, customer_identity, mac)
        assert not (r2.status_code == 200 and r2.json().get("kind") == 1022), (
            f"replayed token bought a second session: HTTP {r2.status_code}: {r2.text[:300]}"
        )
        assert requests.get(UPSTREAM_URL, timeout=10).status_code == 200, (
            "tollgate crashed on the replay"
        )
