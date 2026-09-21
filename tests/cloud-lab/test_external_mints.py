"""
test_external_mints.py — live external-mint lane against a real,
internet-reachable Cashu mint.

Default fixture: https://testnut.cashu.exchange — nutshell's main branch
with a FakeWallet backend, so minting is free and invoices settle
instantly, but the HTTP surface, keyset handling and fee semantics are
production nutshell. Its active sat keyset charges an input fee
(input_fee_ppk > 0), which exercises the #409 swap-fee pre-check against
a real external mint — the reference-cdk lab cannot show the difference
between "our cdk-mintd" and "a stranger's nutshell".

Opt-in: the whole module is gated on EXTERNAL_MINTS=1 (exported by
./run-external-mints.sh) because it needs outbound internet and a
third-party mint; neither may hold up a default lab run. Every test
skips with a reason when the mint is unreachable — this is a canary
lane, not a gate.

Real-money mints are deliberately NOT probed here.
"""

import json
import os
import sys

import pytest
import requests

from conftest import UPSTREAM_URL, build_payment_event, create_cashu_token, run_cmd

TESTNUT = os.environ.get("TESTNUT_URL", "https://testnut.cashu.exchange")

pytestmark = pytest.mark.skipif(
    os.environ.get("EXTERNAL_MINTS") != "1",
    reason="live external-mint lane — run via ./run-external-mints.sh "
           "(needs outbound internet and a third-party test mint)",
)

# Per-run MACs against the dedicated upstream-ext service: locally
# administered, randomized per session so cumulative allotments from a
# previous run can never break the absolute assertions.
def _mac(suffix):
    rand = os.urandom(2).hex()
    return f"02:{rand[0:2]}:{rand[2:4]}:00:00:{suffix:02x}"


print(f"external-mint lane: testnut={TESTNUT} "
      f"upstream={UPSTREAM_URL}", file=sys.stderr)


def mint_is_reachable():
    try:
        r = requests.get(f"{TESTNUT}/v1/keysets", timeout=10)
        return r.status_code == 200
    except requests.RequestException:
        return False


def notice_code(event):
    for tag in event.get("tags", []):
        if len(tag) >= 2 and tag[0] == "code":
            return tag[1]
    return None


def fund_testnut_wallet():
    """A cdk-cli wallet holding testnut ecash (FakeWallet: the mint quote
    settles instantly). Cached in the container for the module's run."""
    wallet = "/tmp/testnut-wallet"
    if not os.path.exists(wallet):
        run_cmd(["cdk-cli", "-w", wallet, "mint", TESTNUT, "40"])
    return wallet


class TestTestnutMint:
    def test_active_sat_keyset_charges_input_fee(self):
        """The lane's fee assumption, checked against the live mint: the
        active sat keyset must charge an input fee, or the below-fee test
        below is asserting nothing."""
        if not mint_is_reachable():
            pytest.skip(f"{TESTNUT} unreachable — canary only")
        ks = requests.get(f"{TESTNUT}/v1/keysets", timeout=10).json()["keysets"]
        active_sat = [k for k in ks if k.get("active") and k.get("unit") == "sat"]
        assert active_sat, ks
        assert all(k.get("input_fee_ppk", 0) > 0 for k in active_sat), (
            f"testnut's fee policy moved; update this lane: {active_sat}")

    def test_below_fee_token_terminated_by_precheck(
        self, upstream_health, upstream_pubkey, customer_identity,
    ):
        """A 1-sat token cannot cover testnut's swap fee (1 proof at the
        active keyset's input_fee_ppk >= 10/1000 rounds up to 1 sat): the
        router must refuse it up front with the terminal coded notice —
        the #409 pre-check firing against a real external nutshell mint."""
        if not mint_is_reachable():
            pytest.skip(f"{TESTNUT} unreachable — canary only")
        wallet = fund_testnut_wallet()
        token = create_cashu_token(wallet, 1, mint_url=TESTNUT)

        mac = _mac(0xA1)
        sec, pub = customer_identity
        ev = build_payment_event(sec, pub, upstream_pubkey, mac, token)
        r = requests.post(f"{UPSTREAM_URL}?mac={mac}", json=ev, timeout=60)
        assert r.status_code == 400, f"{r.status_code}: {r.text[:300]}"
        event = r.json()
        assert event.get("kind") == 21023, json.dumps(event)[:300]
        assert notice_code(event) == "payment-error-below-swap-fee", (
            f"expected the terminal below-swap-fee code, got "
            f"{notice_code(event)}: {event.get('content', '')[:200]}")

    def test_paying_token_credits_fee_deducted_session(
        self, upstream_health, upstream_pubkey, customer_identity,
    ):
        """A 10-sat testnut token pays the live swap fee and credits a
        session: the full advertisement → pre-check → swap → kind-1022
        path against a real external mint."""
        if not mint_is_reachable():
            pytest.skip(f"{TESTNUT} unreachable — canary only")
        wallet = fund_testnut_wallet()
        token = create_cashu_token(wallet, 10, mint_url=TESTNUT)

        mac = _mac(0xA2)
        sec, pub = customer_identity
        ev = build_payment_event(sec, pub, upstream_pubkey, mac, token)
        r = requests.post(f"{UPSTREAM_URL}?mac={mac}", json=ev, timeout=60)
        assert r.status_code == 200, f"{r.status_code}: {r.text[:300]}"
        event = r.json()
        assert event.get("kind") == 1022, json.dumps(event)[:300]
        tags = {t[0]: t[1:] for t in event.get("tags", []) if len(t) >= 2}
        allotment = int(tags["allotment"][0])
        # 10 sats minus the live fee (ceil(proofs * ppk / 1000)); the
        # proof count depends on the wallet's decomposition, so pin the
        # fee-deducted range, not an exact figure.
        assert 8 * 60000 <= allotment <= 10 * 60000, (
            f"allotment {allotment} outside the fee-deducted 8-10 step "
            f"range: {tags}")
