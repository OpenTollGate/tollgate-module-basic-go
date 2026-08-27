"""
test_mint_failure.py — P0.2: Mint failure and degraded mode test.

Validates that the TollGate handles a mint going down mid-operation:
  1. Mint is healthy → TollGate starts in full mode
  2. Mint is killed → TollGate should detect unreachable mint
  3. Degraded mode activates (or payments fail gracefully)
  4. Mint is restarted → TollGate should recover

This test requires the ability to stop/start the mint container from within
the test. Since the client container runs tests, it needs Docker API access
or we use a health-polling approach.

Approach: We poll the mint's /v1/keys endpoint to detect when it's down,
then verify the TollGate's behavior changes accordingly.
"""

import json
import subprocess
import time

import pytest
import requests

from conftest import (
    MINT_URL,
    UPSTREAM_URL,
    build_payment_event,
    create_cashu_token,
    wait_for,
)


def mint_is_reachable():
    """Check if the mint responds to /v1/keys."""
    try:
        r = requests.get(f"{MINT_URL}/v1/keys", timeout=3)
        return r.status_code == 200
    except requests.RequestException:
        return False


def wait_for_mint_down(timeout=30):
    """Wait until the mint stops responding."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if not mint_is_reachable():
            return True
        time.sleep(1)
    return False


def wait_for_mint_up(timeout=60):
    """Wait until the mint comes back online."""
    return wait_for(f"{MINT_URL}/v1/keys", timeout=timeout)


class TestMintFailure:

    def test_mint_initially_healthy(self, mint_health):
        """Precondition: mint is running and healthy."""
        assert mint_is_reachable()

    def test_payment_works_when_mint_healthy(
        self, upstream_health, upstream_pubkey, ecash_wallet, customer_identity, client_mac
    ):
        """Baseline: payment succeeds when mint is up."""
        customer_sec, customer_pub = customer_identity
        token = create_cashu_token(ecash_wallet, 50)
        event = build_payment_event(
            customer_sec, customer_pub, upstream_pubkey, client_mac, token
        )

        r = requests.post(UPSTREAM_URL, json=event, timeout=30)
        assert r.status_code == 200
        assert r.json().get("kind") == 1022, f"Expected session event: {r.text}"

    @pytest.mark.requires_docker
    def test_payment_fails_when_mint_down(
        self, upstream_health, upstream_pubkey, ecash_wallet, customer_identity, client_mac
    ):
        """
        Kill the mint container, then attempt a payment.
        The TollGate should fail gracefully (return a notice/error, not crash).
        """
        # This test requires Docker socket access from the client container.
        # If Docker is not available, skip with a clear message.
        try:
            subprocess.run(
                ["docker", "stop", "tg-mint"],
                check=True, capture_output=True, timeout=30,
            )
        except (FileNotFoundError, subprocess.CalledProcessError) as e:
            pytest.skip(f"Docker access not available from client container: {e}")

        try:
            # Wait for mint to be fully down
            assert wait_for_mint_down(timeout=30), "Mint did not go down"

            # Wait for the TollGate to detect the unreachable mint
            # (health check interval is ~30s by default, but we poll faster)
            time.sleep(5)

            # Try to pay — should fail or return notice event
            customer_sec, customer_pub = customer_identity
            token = create_cashu_token(ecash_wallet, 50)
            event = build_payment_event(
                customer_sec, customer_pub, upstream_pubkey, client_mac, token
            )

            r = requests.post(UPSTREAM_URL, json=event, timeout=60)

            # We expect either:
            # - HTTP 400 with a notice event (kind 21023) — payment rejected
            # - HTTP 500 — internal error during mint verification
            # - HTTP 200 with notice event — graceful rejection
            #
            # The important thing is the TollGate didn't crash.
            assert r.status_code >= 400 or r.json().get("kind") == 21023, (
                f"Expected error/notice when mint is down, got: "
                f"HTTP {r.status_code}: {r.text[:500]}"
            )

            # Verify TollGate is still running (didn't crash)
            r = requests.get(UPSTREAM_URL, timeout=10)
            assert r.status_code == 200, "TollGate crashed after mint failure!"

        finally:
            # Always restart the mint so other tests aren't affected
            subprocess.run(
                ["docker", "start", "tg-mint"],
                check=True, capture_output=True, timeout=30,
            )
            assert wait_for_mint_up(timeout=60), "Mint did not recover"

    @pytest.mark.requires_docker
    def test_tollgate_recovers_after_mint_restart(
        self, upstream_health, upstream_pubkey, ecash_wallet, customer_identity, client_mac
    ):
        """
        After the mint comes back, the TollGate should eventually recover
        and accept payments again.
        """
        # Ensure mint is up (previous test should have restarted it)
        assert wait_for_mint_up(timeout=60), "Mint should be up for recovery test"

        # The TollGate's mint health tracker has a recovery check interval.
        # We may need to wait for it to re-detect the mint.
        # Try payment with retries.
        customer_sec, customer_pub = customer_identity

        max_attempts = 10
        for attempt in range(1, max_attempts + 1):
            try:
                token = create_cashu_token(ecash_wallet, 50)
                event = build_payment_event(
                    customer_sec, customer_pub, upstream_pubkey, client_mac, token
                )

                r = requests.post(UPSTREAM_URL, json=event, timeout=30)

                if r.status_code == 200 and r.json().get("kind") == 1022:
                    # Payment succeeded — TollGate recovered
                    return

                # Payment failed but TollGate is still running
                print(f"Attempt {attempt}/{max_attempts}: Payment not yet working: "
                      f"HTTP {r.status_code}")

            except requests.RequestException as e:
                print(f"Attempt {attempt}/{max_attempts}: Request failed: {e}")

            time.sleep(5)

        pytest.fail(
            f"TollGate did not recover within {max_attempts * 5}s after mint restart"
        )
