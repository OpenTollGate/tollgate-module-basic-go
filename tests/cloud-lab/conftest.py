"""
conftest.py — Shared pytest fixtures for the TollGate cloud lab.

All tests run inside Docker (the 'client' container) and talk to the
TollGate and mint containers over the Docker bridge network.

Key endpoints:
  - Mint:     http://mint:8085       (cdk-mintd with FakeWallet)
  - Upstream: http://upstream:2121   (TollGate service)
  - Reseller: http://reseller:2121   (TollGate service, reseller mode)
"""

import json
import os
import subprocess
import tempfile
import time

import pytest
import requests

# --- Constants ---

MINT_URL = os.environ.get("MINT_URL", "http://mint:8085")
UPSTREAM_URL = os.environ.get("UPSTREAM_URL", "http://upstream:2121")
RESELLER_URL = os.environ.get("RESELLER_URL", "http://reseller:2121")

# Static MAC assigned to the client container in dhcp.leases
CLIENT_MAC = "02:00:00:00:00:20"

# How long to wait for containers to become healthy
HEALTH_TIMEOUT = 60


# --- Helpers ---

def wait_for(url, timeout=HEALTH_TIMEOUT, interval=2):
    """Poll a URL until it responds 200 or timeout."""
    deadline = time.time() + timeout
    last_err = None
    while time.time() < deadline:
        try:
            r = requests.get(url, timeout=5)
            if r.status_code == 200:
                return True
        except requests.RequestException as e:
            last_err = e
        time.sleep(interval)
    raise TimeoutError(f"{url} did not become healthy in {timeout}s: {last_err}")


def run_cmd(args, **kwargs):
    """Run a command, return stdout. Raises on failure."""
    result = subprocess.run(args, capture_output=True, text=True, **kwargs)
    if result.returncode != 0:
        raise RuntimeError(
            f"Command failed: {' '.join(args)}\n"
            f"stdout: {result.stdout}\nstderr: {result.stderr}"
        )
    return result.stdout.strip()


def generate_nostr_keypair():
    """Generate a Nostr keypair using nak."""
    secret = run_cmd(["nak", "key", "generate"])
    public = run_cmd(["nak", "key", "public", secret])
    return secret, public


def _expand_keyset_ids(token_str, mint_url=MINT_URL):
    """Replace short (8-byte) keyset IDs in a V3 token with full (33-byte) IDs.

    cdk-cli stores proofs with truncated 8-byte keyset IDs (v1 short format).
    However, cdk-mintd's swap endpoint only accepts full 33-byte (v2) keyset
    IDs in practice. This function fetches the mint's actual keyset IDs and
    expands any short IDs in the token to their full equivalent.
    """
    if not token_str.startswith("cashuA"):
        return token_str

    import base64 as b64
    payload = token_str[6:]
    payload += "=" * (4 - len(payload) % 4)
    decoded = b64.urlsafe_b64decode(payload)
    data = json.loads(decoded)

    try:
        r = requests.get(f"{mint_url}/v1/keysets", timeout=5)
        r.raise_for_status()
    except requests.RequestException:
        return token_str

    keysets = r.json().get("keysets", [])
    short_to_full = {}
    for ks in keysets:
        full_id = ks["id"]
        if len(full_id) == 66:
            short_to_full[full_id[:16]] = full_id

    for token_entry in data.get("token", []):
        for proof in token_entry.get("proofs", []):
            proof_id = proof.get("id", "")
            if len(proof_id) == 16 and proof_id in short_to_full:
                proof["id"] = short_to_full[proof_id]

    json_bytes = json.dumps(data).encode()
    return "cashuA" + b64.urlsafe_b64encode(json_bytes).decode()


def create_cashu_token(wallet_dir, amount, mint_url=MINT_URL):
    """Create a Cashu token for the given amount using cdk-cli.

    Uses --v3 to produce a V3 JSON token (cashuA prefix) and expands short
    keyset IDs to full format to work around cdk-mintd's strict 33-byte
    keyset ID requirement in swap requests.
    """
    proc = subprocess.run(
        ["cdk-cli", "-w", wallet_dir, "send", "--mint-url", mint_url,
         "--v3", "--amount", str(amount)],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"cdk-cli send failed:\nstdout: {proc.stdout}\nstderr: {proc.stderr}")

    token = None
    for line in reversed(proc.stdout.strip().split("\n")):
        if line.startswith("cashu"):
            token = line
            break
    if token is None:
        raise RuntimeError(f"No cashu token in output:\n{proc.stdout}")

    # Expand short keyset IDs to full format for mint compatibility
    return _expand_keyset_ids(token, mint_url)


def build_payment_event(customer_sec, customer_pub, tollgate_pub, mac, token):
    """Build and sign a Nostr payment event (kind 21000)."""
    event = {
        "kind": 21000,
        "pubkey": customer_pub,
        "tags": [
            ["p", tollgate_pub],
            ["device-identifier", "mac", mac],
            ["payment", token],
        ],
        "content": "",
    }
    proc = subprocess.run(
        ["nak", "event", "--sec", customer_sec],
        input=json.dumps(event),
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"nak event failed:\nstdout: {proc.stdout}\nstderr: {proc.stderr}")
    return json.loads(proc.stdout)


# --- Session-scoped fixtures ---

@pytest.fixture(scope="session")
def mint_health():
    """Wait for the mint to be healthy."""
    wait_for(f"{MINT_URL}/v1/keys")
    return MINT_URL


@pytest.fixture(scope="session")
def upstream_health():
    """Wait for the upstream TollGate to be healthy."""
    wait_for(UPSTREAM_URL)
    return UPSTREAM_URL


@pytest.fixture(scope="session")
def upstream_details(upstream_health):
    """Fetch the upstream TollGate's advertisement/discovery event."""
    r = requests.get(UPSTREAM_URL)
    assert r.status_code == 200
    return r.json()


@pytest.fixture(scope="session")
def upstream_pubkey(upstream_details):
    """Extract the upstream TollGate's Nostr pubkey."""
    pubkey = upstream_details.get("pubkey")
    assert pubkey, f"No pubkey in upstream details: {upstream_details}"
    return pubkey


@pytest.fixture(scope="session")
def ecash_wallet(mint_health):
    """Create a funded ecash wallet using cdk-cli."""
    wallet_dir = tempfile.mkdtemp(prefix="tg-wallet-")
    # Mint 10000 sats from the FakeWallet mint
    run_cmd(["cdk-cli", "-w", wallet_dir, "mint", MINT_URL, "10000"])
    yield wallet_dir


@pytest.fixture(scope="function")
def cashu_token(ecash_wallet):
    """Generate a fresh Cashu token worth 100 sats for each test."""
    return create_cashu_token(ecash_wallet, 100)


@pytest.fixture(scope="session")
def customer_identity():
    """Generate a Nostr identity for the test client."""
    return generate_nostr_keypair()


@pytest.fixture(scope="session")
def client_mac():
    """MAC address mapped to the client container's IP in dhcp.leases."""
    return CLIENT_MAC
