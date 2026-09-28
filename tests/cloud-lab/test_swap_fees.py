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
    `payment-error-below-swap-fee` notice — pinned as the pre-check's
    message (which names the fee), not the post-swap classifier's
    (which does not) — and the token is proven unspent via the mint's
    NUT-07 /v1/checkstate, not just by a deterministic retry
  - an above-fee payment succeeds and the fee is deducted from the allotment
"""

import base64 as b64
import hashlib
import json
import os
import struct

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


# --- NUT-07 spentness oracle --------------------------------------------
#
# Spentness is proven against the mint (POST /v1/checkstate needs each
# proof's Y point), and the client image ships no ec library, so NUT-00
# hash_to_curve lives here in pure python. It must stay byte-compatible
# with gonuts' crypto.HashToCurve — the wallet the router actually runs:
#   msg = SHA256("Secp256k1_HashToCurve_Cashu_" || secret)
#   Y   = decompress(0x02 || SHA256(msg || le32(counter)))   # first valid x
#
# Two guards keep that compatibility honest (NUT-07 answers UNSPENT for
# points it has never seen, so a drifting oracle would pass vacuously):
#   - test_hash_to_curve_matches_gonuts_vectors pins this implementation
#     against gonuts' own test vectors (crypto/bdhke_test.go), and
#   - test_above_swap_fee_succeeds_with_fee_deducted ends with a negative
#     control: a token the payment DID spend must make assert_token_unspent
#     fail, proving the oracle can detect spentness at all.

_SECP256K1_P = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F
_HASH_TO_CURVE_DOMAIN = b"Secp256k1_HashToCurve_Cashu_"


def _hash_to_curve_y(secret):
    """Compressed 33-byte hex Y point for a proof secret (NUT-00)."""
    msg = hashlib.sha256(_HASH_TO_CURVE_DOMAIN + secret.encode()).digest()
    for counter in range(1 << 16):
        digest = hashlib.sha256(msg + struct.pack("<I", counter)).digest()
        x = int.from_bytes(digest, "big")
        if x >= _SECP256K1_P:
            continue
        y_sq = (pow(x, 3, _SECP256K1_P) + 7) % _SECP256K1_P
        y = pow(y_sq, (_SECP256K1_P + 1) // 4, _SECP256K1_P)
        if (y * y - y_sq) % _SECP256K1_P != 0:
            continue  # x is not a curve abscissa: next counter
        if y % 2:  # 0x02 prefix selects the even-Y point
            y = _SECP256K1_P - y
        return f"02{x:064x}"
    raise RuntimeError("hash_to_curve: no valid point in 2^16 iterations")


def _token_secrets(token_str):
    """Secrets of every proof in a V3 (cashuA…) token."""
    assert token_str.startswith("cashuA"), (
        f"expected a V3 token, got: {token_str[:20]}…"
    )
    payload = token_str[6:]
    payload += "=" * (4 - len(payload) % 4)
    data = json.loads(b64.urlsafe_b64decode(payload))
    secrets = []
    for entry in data.get("token", []):
        for proof in entry.get("proofs", []):
            secrets.append(proof["secret"])
    assert secrets, f"no proofs in token: {json.dumps(data)[:200]}"
    return secrets


def assert_token_unspent(token_str, mint_url=MINT_FEES_URL):
    """Every proof of `token_str` must report UNSPENT at the mint (NUT-07)."""
    ys = [_hash_to_curve_y(s) for s in _token_secrets(token_str)]
    r = requests.post(f"{mint_url}/v1/checkstate", json={"Ys": ys}, timeout=10)
    r.raise_for_status()
    states = r.json().get("states", [])
    assert len(states) == len(ys), (
        f"checkstate returned {len(states)} states for {len(ys)} Ys: {r.text}"
    )
    for y, state in zip(ys, states):
        assert state.get("state") == "UNSPENT", (
            f"proof {y} is {state.get('state')} at the mint — the refusal "
            f"path spent it"
        )


# Session allotments are cumulative per MAC, so every test pays from a
# distinct MAC to assert absolute amounts independently of test order. The
# handler takes the MAC from the ?mac= query param (the way the splash page
# passes it from nodogsplash preauth); without it every payment falls back
# to the client IP's lease entry and lands on one shared MAC.
#
# The MACs are randomized per test-session (locally-administered 02:…): the
# lab is a shared compose project, and two suites running concurrently
# against one upstream would otherwise accumulate each other's allotments
# on the same hardcoded MAC — observed live when two agents validated this
# very file.
def _mac(suffix):
    rand = os.urandom(2).hex()
    return f"02:{rand[0:2]}:{rand[2:4]}:00:00:{suffix:02x}"


BELOW_FEE_MAC = _mac(0x21)
ABOVE_FEE_MAC = _mac(0x22)
FREE_MINT_MAC = _mac(0x23)


@pytest.fixture(scope="session", autouse=True)
def _log_session_macs():
    """Surface the randomized MACs so a red run can be replayed.

    The MACs are re-randomized every session; printing them (setup-phase
    capture, shown for any failing test, and live under -s) lets a failed
    run be reproduced with the same identities.
    """
    print(
        f"swap-fee suite MACs: below-fee={BELOW_FEE_MAC} "
        f"above-fee={ABOVE_FEE_MAC} free-mint={FREE_MINT_MAC}"
    )


def pay(token, upstream_pubkey, customer_identity, mac):
    """POST a payment event attributed to `mac`; returns the Response."""
    customer_sec, customer_pub = customer_identity
    event = build_payment_event(
        customer_sec, customer_pub, upstream_pubkey, mac, token
    )
    return requests.post(f"{UPSTREAM_URL}?mac={mac}", json=event, timeout=30)


class TestSwapFees:

    def test_hash_to_curve_matches_gonuts_vectors(self):
        """The spentness oracle's hash_to_curve must be byte-compatible
        with gonuts' crypto.HashToCurve — the wallet the router runs.

        These are gonuts' own vectors (crypto/bdhke_test.go,
        TestHashToCurve). They pin the counter start (gonuts begins at
        0), the domain separator, the little-endian counter encoding and
        the even-Y selection: the first vector resolves at counter=0, so
        an off-by-one in the loop already fails here instead of making
        every checkstate query miss the real points (NUT-07 answers
        UNSPENT for points it has never seen).
        """
        vectors = [
            ("\x00" * 32,
             "024cce997d3b518f739663b757deaec95bcd9473c30a14ac2fd04023a739d1a725"),
            ("\x00" * 31 + "\x01",
             "022e7158e11c9506f1aa4248bf531298daa7febd6194f003edcd9b93ade6253acf"),
        ]
        for secret, expected_y in vectors:
            assert _hash_to_curve_y(secret) == expected_y, (
                f"hash_to_curve drifted from gonuts for secret {secret!r}: "
                f"got {_hash_to_curve_y(secret)}, expected {expected_y} — "
                "every spentness assertion in this suite is now vacuous"
            )

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
        """A 1-sat token against a 1-sat swap fee is refused by the
        pre-flight check, and the refusal is not a spend.

        The notice's content pins WHICH path refused: the pre-check's
        message names the fee ("charges a 1 sat swap fee, so there is
        nothing left to spend") while the post-swap classifier's does not
        ("charges a swap fee that leaves nothing left to spend") — so
        deleting merchant.go's pre-flight fails this assertion even though
        the code tag stays identical. Spentness itself is proven against
        the mint (NUT-07), not inferred from the identical retry."""
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
        assert "charges a 1 sat swap fee, so there is nothing left to spend" in event.get("content", ""), (
            "Notice content does not match the pre-check message (which names "
            f"the fee); got: {event.get('content')!r} — if this shows the "
            "post-swap classifier wording ('charges a swap fee that leaves "
            "nothing left to spend'), the pre-flight in merchant.go is gone "
            "and only the classifier is answering"
        )

        assert_token_unspent(token)

        r2 = pay(token, upstream_pubkey, customer_identity, BELOW_FEE_MAC)
        assert r2.status_code == 400
        assert notice_code(r2.json()) == "payment-error-below-swap-fee", (
            "Token was consumed by the first attempt (second refusal should be "
            f"identical, got: {notice_code(r2.json())})"
        )
        assert_token_unspent(token)

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

        # Negative control for the spentness oracle: this payment SWAPPED
        # the token's proofs, so the mint must report them SPENT. If
        # assert_token_unspent cannot fail here, its UNSPENT passes in the
        # refusal test prove nothing.
        with pytest.raises(AssertionError, match="SPENT"):
            assert_token_unspent(token)

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
