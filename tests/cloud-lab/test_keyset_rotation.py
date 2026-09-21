"""
test_keyset_rotation.py — Keyset-rotation e2e for the swap-fee path (#409).

Run via ./run-keyset-rotation.sh, which boots the mint-rotate service in two
phases (the client detects which from /v1/keysets):

  Phase A — one active keyset at input_fee_ppk=100: mint proofs on it and
  stash a token on the bind mount for phase B.

  Phase B — that keyset is expired (same ID, fee kept) and a zero-fee keyset
  is active: paying with the stashed phase-A proofs must fail cleanly,
  because cdk-mintd 0.17.6 refuses swaps on expired keysets outright
  ("Keyset has expired") — pre-rotation proofs become mint-side unspendable.
  This pins the refusal's current classification (see the note on the test)
  so any refinement is a conscious change.

  Note: exercising the wallet's inactive-but-not-expired keyset fee
  resolution against a real mint needs a backend that keeps rotated
  keysets swappable (nutshell does; cdk 0.17.6 does not) — tracked in the
  signet-lane issue (#415).

Also pins the sum-then-ceil fee boundary on mint-fees: whatever proof
decomposition cdk-cli produces, the credited allotment must equal
amount - ceil(proofs * 100 / 1000) exactly.
"""

import base64 as b64
import json
import math
import os

import pytest
import requests

from conftest import MINT_FEES_URL, build_payment_event, create_cashu_token, run_cmd

MINT_ROTATE_URL = os.environ.get("MINT_ROTATE_URL", "http://mint-rotate:8085")
UPSTREAM_URL = os.environ.get("UPSTREAM_URL", "http://upstream:2121")

# Survives client-container restarts via the read-write /tests bind mount.
STATE_FILE = "/tests/.rotation-state.json"


# Allotments are cumulative per MAC and the lab is a shared compose
# project: randomized per-session MACs (locally-administered 02:…) keep
# concurrent suite runs from accumulating on one another's MACs — the
# hardcoded constants did exactly that during validation.
def _mac(suffix):
    rand = os.urandom(2).hex()
    return f"02:{rand[0:2]}:{rand[2:4]}:00:00:{suffix:02x}"


ROTATION_A_MAC = _mac(0x31)
ROTATION_B_MAC = _mac(0x32)


def keysets(mint_url):
    r = requests.get(f"{mint_url}/v1/keysets", timeout=5)
    r.raise_for_status()
    return r.json().get("keysets", [])


def active_sat_keysets(mint_url):
    return [k for k in keysets(mint_url) if k.get("unit") == "sat" and k.get("active")]


def pay(token, upstream_pubkey, customer_identity, mac):
    customer_sec, customer_pub = customer_identity
    event = build_payment_event(
        customer_sec, customer_pub, upstream_pubkey, mac, token
    )
    return requests.post(f"{UPSTREAM_URL}?mac={mac}", json=event, timeout=30)


def allotment_steps(event):
    tags = {t[0]: t[1:] for t in event.get("tags", []) if len(t) >= 2}
    return int(tags["allotment"][0]) // 60000


def token_proof_count(token):
    """Decode a cashuA token and count its proofs."""
    payload = token[6:]
    payload += "=" * (4 - len(payload) % 4)
    data = json.loads(b64.urlsafe_b64decode(payload))
    return sum(len(entry.get("proofs", [])) for entry in data.get("token", []))


class TestKeysetRotation:

    # The phase tests only make sense inside ./run-keyset-rotation.sh's
    # two-phase container choreography. A default `docker compose run --rm
    # client` collects the whole directory: phase A would stash a bearer
    # token into the working tree and phase B would fail against the
    # never-rotated keyset — poisoning the default suite. Gate them on the
    # env var the runner exports.
    pytestmark = pytest.mark.skipif(
        os.environ.get("ROTATION_LANE") != "1",
        reason="phase tests need ./run-keyset-rotation.sh (ROTATION_LANE=1)",
    )

    def test_phase_a_mint_and_stash_proofs_on_fee_keyset(
        self, upstream_health, upstream_pubkey, customer_identity
    ):
        """Phase A: exactly one active sat keyset at 100 ppk. Mint proofs on
        it and stash a token for phase B to spend after the rotation."""
        active = active_sat_keysets(MINT_ROTATE_URL)
        assert len(active) == 1, (
            f"Phase A expects exactly one active sat keyset, got: {active} "
            "(did run-keyset-rotation.sh start, or is this phase B?)"
        )
        assert active[0]["input_fee_ppk"] == 100

        wallet_dir = "/tmp/rotation-wallet"
        run_cmd(["cdk-cli", "-w", wallet_dir, "mint", MINT_ROTATE_URL, "64"])
        token = create_cashu_token(wallet_dir, 4, mint_url=MINT_ROTATE_URL)

        state = {"token": token, "keyset_id": active[0]["id"]}
        with open(STATE_FILE, "w") as f:
            json.dump(state, f)

    def test_phase_b_expired_keyset_proofs_fail_with_classified_error(
        self, upstream_health, upstream_pubkey, customer_identity
    ):
        """Phase B: the phase-A keyset is expired and a zero-fee keyset is
        active. cdk-mintd 0.17.6 refuses swaps on expired keysets outright,
        so the stashed 4-sat token must be refused — not spent, not an
        opaque crash. The refusal's current classification is WRONG for the
        customer: 'could not swap proofs: Keyset has expired' matches the
        mint-unreachable classifier, so they are told the mint is down and
        to retry or use another mint — the mint is fine and the token is
        permanently dead. A dedicated expired-keyset code is being landed
        as #447 (filed as #440); until then this pins the misclassification
        so the refinement consciously updates it."""
        active = active_sat_keysets(MINT_ROTATE_URL)
        inactive = {
            k["id"]: k["input_fee_ppk"]
            for k in keysets(MINT_ROTATE_URL)
            if k.get("unit") == "sat" and not k.get("active")
        }
        assert inactive, (
            f"Phase B expects inactive sat keysets, got: {keysets(MINT_ROTATE_URL)} "
            "(this test must run second — use run-keyset-rotation.sh)"
        )
        assert all(k["input_fee_ppk"] == 0 for k in active), active

        with open(STATE_FILE) as f:
            state = json.load(f)
        assert state["keyset_id"] in inactive, (
            f"stashed keyset {state['keyset_id']} must be expired by phase B; "
            f"active={active} inactive={inactive}"
        )

        r = pay(state["token"], upstream_pubkey, customer_identity, ROTATION_B_MAC)
        assert r.status_code == 400, (
            f"Expired-keyset payment must be refused, got {r.status_code}: {r.text}"
        )
        event = r.json()
        assert event.get("kind") == 21023, json.dumps(event, indent=2)
        codes = {t[1] for t in event.get("tags", []) if t[0] == "code"}
        # With the #447 classification landed: "could not swap proofs:
        # Keyset has expired" carries the dedicated
        # payment-error-keyset-expired code — the mint is healthy and the
        # token is permanently dead, so the old mint-unreachable advice
        # (retry / another mint) was wrong on both counts.
        assert codes == {"payment-error-keyset-expired"}, codes


class TestSwapFeeBoundaries:
    """Sum-then-ceil fee boundaries on mint-fees, independent of the
    wallet's proof decomposition: the credited allotment must equal
    amount - ceil(proofs * 100 / 1000) for whatever token was sent."""

    @pytest.mark.parametrize("amount", [255, 1023, 2047])
    def test_fee_is_sum_then_ceil(
        self, upstream_health, upstream_pubkey, fees_ecash_wallet,
        customer_identity, amount,
    ):
        token = create_cashu_token(
            fees_ecash_wallet, amount, mint_url=MINT_FEES_URL
        )
        proofs = token_proof_count(token)
        expected_fee = math.ceil(proofs * 100 / 1000)
        assert expected_fee >= 1

        # Allotments are cumulative per MAC, and the lab is a shared
        # compose project: a deterministic per-amount MAC flaked on every
        # re-run against a lab that was not torn down (the second run's
        # allotments accumulated onto the same three addresses). A
        # per-session random MAC with the amount as its suffix keeps the
        # parametrized cases distinct AND re-runnable.
        mac = _mac(amount & 0xFF)
        r = pay(token, upstream_pubkey, customer_identity, mac)
        assert r.status_code == 200, f"{amount}-sat payment failed: {r.text}"
        event = r.json()
        assert event.get("kind") == 1022

        expected_steps = amount - expected_fee
        assert allotment_steps(event) == expected_steps, (
            f"{amount} sats in {proofs} proofs via {mac}: expected "
            f"{expected_steps} steps (fee {expected_fee}), "
            f"got {allotment_steps(event)}"
        )
