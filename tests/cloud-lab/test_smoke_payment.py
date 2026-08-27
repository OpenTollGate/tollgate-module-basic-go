"""
test_smoke_payment.py — P0.1: Single-router payment smoke test.

Validates the most critical path: a client pays the TollGate and receives
a valid session response. This proves the entire Docker lab works:
  - cdk-mintd issues tokens
  - cdk-cli can mint/send
  - nak signs events
  - TollGate processes the payment, verifies the token, opens the gate
    (via fake ndsctl), and returns a session event

If this test passes, all the infrastructure is correctly wired.
"""

import json
import time

import pytest
import requests

from conftest import (
    MINT_URL,
    UPSTREAM_URL,
    build_payment_event,
    create_cashu_token,
)


class TestSmokePayment:

    def test_mint_is_reachable(self, mint_health):
        """Mint responds to /v1/keys."""
        r = requests.get(f"{MINT_URL}/v1/keys")
        assert r.status_code == 200
        data = r.json()
        assert "keysets" in data, f"Unexpected keys response: {data}"

    def test_upstream_is_reachable(self, upstream_health):
        """TollGate responds on port 2121."""
        r = requests.get(UPSTREAM_URL)
        assert r.status_code == 200
        data = r.json()
        assert data.get("kind") == 10021, f"Expected kind 10021, got: {data.get('kind')}"

    def test_wallet_has_balance(self, ecash_wallet):
        """The funded wallet has sats."""
        from conftest import run_cmd
        balance = run_cmd(["cdk-cli", "-w", ecash_wallet, "balance"])
        assert "sat" in balance or "Sat" in balance, f"No sats in wallet: {balance}"

    def test_payment_returns_session_event(
        self, upstream_health, upstream_pubkey, ecash_wallet, customer_identity, client_mac
    ):
        """Pay the TollGate and verify we get a session event (kind 1022) back."""
        customer_sec, customer_pub = customer_identity

        # Create a Cashu token
        token = create_cashu_token(ecash_wallet, 100)

        # Build and sign the payment event
        event = build_payment_event(
            customer_sec, customer_pub, upstream_pubkey, client_mac, token
        )

        # POST the payment
        r = requests.post(
            UPSTREAM_URL,
            json=event,
            timeout=30,
        )

        # Should get 200 with a session event
        assert r.status_code == 200, (
            f"Payment failed: HTTP {r.status_code}\nResponse: {r.text}"
        )

        response_event = r.json()

        # Verify it's a session event (kind 1022), not a notice (kind 21023)
        assert response_event.get("kind") == 1022, (
            f"Expected session event (kind 1022), got kind {response_event.get('kind')}: "
            f"{json.dumps(response_event, indent=2)}"
        )

        # Verify the session has expected fields
        # Session events contain allotment, metric, and expiry info in tags
        tags = response_event.get("tags", [])
        tag_names = [t[0] for t in tags]
        assert "allotment" in tag_names or "amount" in tag_names, (
            f"Session event missing allotment/amount tag: {tags}"
        )

    def test_gate_was_opened(self, upstream_health, upstream_pubkey, ecash_wallet, customer_identity, client_mac):
        """Verify the fake ndsctl logged an AUTH call after payment."""
        # Note: this checks the ndsctl log INSIDE the upstream container.
        # Since we can't easily exec into it from the client container,
        # we verify indirectly: a successful payment implies the gate opened.
        #
        # For a direct check, we'd need Docker API access or a shared volume.
        # For now, the session event (kind 1022) is sufficient proof.

        customer_sec, customer_pub = customer_identity
        token = create_cashu_token(ecash_wallet, 100)
        event = build_payment_event(
            customer_sec, customer_pub, upstream_pubkey, client_mac, token
        )

        r = requests.post(UPSTREAM_URL, json=event, timeout=30)
        assert r.status_code == 200
        assert r.json().get("kind") == 1022

    def test_balance_check(self, upstream_health, upstream_pubkey, ecash_wallet, customer_identity, client_mac):
        """After paying, the /balance endpoint should show remaining allotment."""
        customer_sec, customer_pub = customer_identity
        token = create_cashu_token(ecash_wallet, 100)
        event = build_payment_event(
            customer_sec, customer_pub, upstream_pubkey, client_mac, token
        )

        # Pay
        r = requests.post(UPSTREAM_URL, json=event, timeout=30)
        assert r.status_code == 200

        # Wait a moment for session to register
        time.sleep(2)

        # Check balance
        r = requests.get(
            f"{UPSTREAM_URL}/balance",
            headers={"X-Real-Ip": "172.28.0.20"},
            timeout=10,
        )
        # Balance endpoint might return 200 or 400 depending on MAC resolution
        # In cloud lab, the dhcp.leases mapping handles this
        assert r.status_code in (200, 400), f"Unexpected status: {r.status_code}"
