"""
test_mint_matrix.py — one suite, many mints.

Runs the core payment acceptance loop against any reachable Cashu mint
declared via MINT_MATRIX (comma-separated name=url pairs) while the
router (upstream-ext) accepts exactly those mints. Built for the signet
zoo matrix (cdk-mintd 0.17/0.18/0.18.1 + nutshell 0.20.3/0.21.0), where
invoices auto-settle within ~a minute: a session float is minted ONCE
per mint (see conftest.fund_mint_wallet) and every test then spends
offline.

Fee expectations are DERIVED from each mint's live /v1/keysets, not
hardcoded — a 100-ppk nutshell and a fee-free cdk pass the same
assertions with different bounds.

Gate: skipped entirely unless MINT_MATRIX is set (run via
./run-mint-matrix.sh, which wires the zoo containers onto the lab
network and builds the router's accepted-mint config).
"""
import base64 as b64
import json
import os
import sys

import pytest
import requests

from conftest import UPSTREAM_URL, build_payment_event, create_cashu_token

STEP_MS = 60000  # upstream-matrix config: milliseconds metric, 1 sat/step


def mint_wallet_balance(wallet_dir):
    """Total balance of a pre-funded float wallet (runner funds it)."""
    import subprocess
    import re
    proc = subprocess.run(
        ["cdk-cli", "-w", wallet_dir, "balance"],
        capture_output=True, text=True, timeout=30)
    return sum(int(m) for m in re.findall(r"(\d+) sat", proc.stdout))

pytestmark = pytest.mark.skipif(
    not os.environ.get("MINT_MATRIX"),
    reason="mint matrix lane — run via ./run-mint-matrix.sh (needs the zoo "
           "or other mints wired onto the lab network)",
)

MINTS = []
for pair in os.environ.get("MINT_MATRIX", "").split(","):
    pair = pair.strip()
    if pair and "=" in pair:
        name, url = pair.split("=", 1)
        MINTS.append((name.strip(), url.strip()))

print(f"mint matrix: {MINTS}", file=sys.stderr)


def _mac(suffix):
    rand = os.urandom(2).hex()
    return f"02:{rand[0:2]}:{rand[2:4]}:00:0m:{suffix:02x}" if False else \
        f"02:{rand[0:2]}:{rand[2:4]}:00:{suffix >> 8:02x}:{suffix & 0xff:02x}"


def notice_code(event):
    for tag in event.get("tags", []):
        if len(tag) >= 2 and tag[0] == "code":
            return tag[1]
    return None


def token_proof_count(token):
    payload = token[6:]
    payload += "=" * (4 - len(payload) % 4)
    data = json.loads(b64.urlsafe_b64decode(payload))
    return sum(len(entry.get("proofs", [])) for entry in data.get("token", []))


def mint_fee_ppk(mint_url):
    ks = requests.get(f"{mint_url}/v1/keysets", timeout=10).json()["keysets"]
    active_sat = [k for k in ks if k.get("active") and k.get("unit") == "sat"]
    assert active_sat, f"no active sat keysets at {mint_url}: {ks}"
    return max(int(k.get("input_fee_ppk", 0)) for k in active_sat)


@pytest.fixture(scope="session", params=MINTS, ids=[n for n, _ in MINTS])
def mint(request):
    """One funded wallet + facts per target mint."""
    name, url = request.param
    wallet = f"/matrix-wallets/{name}"
    assert mint_wallet_balance(wallet) >= 50, (
        f"float wallet {wallet} empty or missing -- the runner funds it "
        f"host-side via the zoo's pay-and-mint.sh before the suite runs")
    return {"name": name, "url": url, "wallet": wallet,
            "ppk": mint_fee_ppk(url)}


class TestMintMatrix:

    def test_keyset_facts(self, mint):
        """The mint serves active sat keysets; the recorded fee policy is
        the one the payment assertions use (0 for cdk zoo mints, 100 for
        nutshell zoo mints — derived, never hardcoded)."""
        assert mint["ppk"] >= 0
        print(f"[{mint['name']}] input_fee_ppk={mint['ppk']}")

    def test_payment_accepted_fee_accounted(self, mint, upstream_health,
                                            upstream_pubkey,
                                            customer_identity):
        """A 50-sat token from the target mint is accepted and credited
        net of the mint's real swap fee."""
        sec, pub = customer_identity
        token = create_cashu_token(mint["wallet"], 50, mint_url=mint["url"])
        proofs = token_proof_count(token)
        fee = -(-proofs * mint["ppk"] // 1000)  # ceil

        mac = _mac(0xB1)
        ev = build_payment_event(sec, pub, upstream_pubkey, mac, token)
        r = requests.post(f"{UPSTREAM_URL}?mac={mac}", json=ev, timeout=90)
        assert r.status_code == 200, f"{r.status_code}: {r.text[:300]}"
        event = r.json()
        assert event.get("kind") == 1022, json.dumps(event)[:300]
        tags = {t[0]: t[1:] for t in event.get("tags", []) if len(t) >= 2}
        allotment = int(tags["allotment"][0])
        assert (50 - fee) * STEP_MS <= allotment <= 50 * STEP_MS, (
            f"[{mint['name']}] allotment {allotment} outside the "
            f"fee-deducted range [{(50 - fee) * STEP_MS}, {50 * STEP_MS}] "
            f"(proofs={proofs}, fee={fee})")
        print(f"[{mint['name']}] 50 sats / {proofs} proofs / fee {fee} -> "
              f"{allotment // STEP_MS} steps")

    def test_second_payment_same_mint(self, mint, upstream_health,
                                      upstream_pubkey, customer_identity):
        """A second token from the same mint pays again — the float model
        (one host-side funding, offline sends) holds across tests."""
        sec, pub = customer_identity
        token = create_cashu_token(mint["wallet"], 30, mint_url=mint["url"])
        mac = _mac(0xB2)
        ev = build_payment_event(sec, pub, upstream_pubkey, mac, token)
        r = requests.post(f"{UPSTREAM_URL}?mac={mac}", json=ev, timeout=90)
        assert r.status_code == 200, f"{r.status_code}: {r.text[:300]}"
        assert r.json().get("kind") == 1022
