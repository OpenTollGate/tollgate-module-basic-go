"""
test_cdk_backend_e2e.py — P4: the FULL module on the CDK wallet backend.

The service under test is `upstream-cdk` (docker-compose.cdk-backend.yml):
the same Dockerfile.tollgate build as `upstream`, with wallet.backend=cdk
selecting the cdk-walletd sidecar through the OpenWallet seam (tbmg#1155).
The sidecar is the Amperstrand musl daemon; the module speaks AF_UNIX RPC.

These cases run inside the client container (the standard cloud-lab lane);
the daemon-kill boundary legs live in run-cdk-backend-e2e.sh, which drives
this file plus host-side `docker compose kill` (a container cannot kill
its sibling).
"""

import json

import pytest
import requests

from conftest import (
    UPSTREAM_URL,
    build_payment_event,
    create_cashu_token,
)


@pytest.fixture(scope="session")
def upstream_cdk_health():
    """Wait for the cdk-backend upstream to answer its advertisement."""
    import time

    deadline = time.time() + 60
    last = None
    while time.time() < deadline:
        try:
            r = requests.get(UPSTREAM_URL, timeout=5)
            if r.status_code == 200 and r.json().get("kind") == 10021:
                return UPSTREAM_URL
            last = f"HTTP {r.status_code}: {r.text[:200]}"
        except Exception as e:  # noqa: BLE001 - retry until deadline
            last = str(e)
        time.sleep(1)
    pytest.fail(f"upstream-cdk never became healthy: {last}")


@pytest.fixture(scope="session")
def upstream_cdk_pubkey(upstream_cdk_health):
    r = requests.get(UPSTREAM_URL, timeout=10)
    pubkey = r.json().get("pubkey")
    assert pubkey, f"No pubkey in upstream-cdk advertisement: {r.text[:300]}"
    return pubkey


class TestCdkBackendE2E:
    def test_advertisement_is_served(self, upstream_cdk_health):
        """Kind 10021 advertisement: the service booted on the cdk backend."""
        r = requests.get(UPSTREAM_URL, timeout=10)
        assert r.status_code == 200
        assert r.json().get("kind") == 10021

    def test_payment_returns_session_event(
        self,
        upstream_cdk_health,
        upstream_cdk_pubkey,
        ecash_wallet,
        customer_identity,
        client_mac,
    ):
        """The critical path: pay through the module running on CDK.

        Token verify → Receive (sidecar RPC) → session grant → session
        event — the full service on the cdk backend, not just the wallet
        layer.
        """
        customer_sec, customer_pub = customer_identity
        token = create_cashu_token(ecash_wallet, 100)
        event = build_payment_event(
            customer_sec, customer_pub, upstream_cdk_pubkey, client_mac, token
        )
        r = requests.post(UPSTREAM_URL, json=event, timeout=60)
        assert r.status_code == 200, (
            f"Payment failed on the cdk backend: HTTP {r.status_code}\n"
            f"Response: {r.text}"
        )
        response_event = r.json()
        assert response_event.get("kind") == 1022, (
            f"Expected session event (kind 1022), got kind "
            f"{response_event.get('kind')}: {json.dumps(response_event, indent=2)}"
        )
        tag_names = [t[0] for t in response_event.get("tags", [])]
        assert "allotment" in tag_names or "amount" in tag_names, (
            f"Session event missing allotment/amount tag: {response_event}"
        )

    def test_token_replay_does_not_double_grant(
        self,
        upstream_cdk_health,
        upstream_cdk_pubkey,
        ecash_wallet,
        customer_identity,
        client_mac,
    ):
        """Spent-token replay must not open a second session.

        This exercises the sidecar CheckTokenSpent path (NUT-07 check_state
        over AF_UNIX) wired in the parity lane: the second presentation of
        the same proofs must be refused by the mint via the sidecar.
        """
        customer_sec, customer_pub = customer_identity
        token = create_cashu_token(ecash_wallet, 100)
        event = build_payment_event(
            customer_sec, customer_pub, upstream_cdk_pubkey, client_mac, token
        )

        first = requests.post(UPSTREAM_URL, json=event, timeout=60)
        assert first.status_code == 200 and first.json().get("kind") == 1022, (
            f"First payment did not grant: {first.status_code} {first.text}"
        )

        # Replay the identical event. The proofs are spent at the mint;
        # the module must ask the sidecar (check_state) and refuse.
        second = requests.post(UPSTREAM_URL, json=event, timeout=60)
        body = {}
        try:
            body = second.json()
        except ValueError:
            pass
        assert body.get("kind") != 1022, (
            "DOUBLE GRANT on token replay through the cdk backend — the "
            "spent check did not refuse a second session"
        )
