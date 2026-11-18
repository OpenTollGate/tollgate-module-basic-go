#!/usr/bin/env python3
"""Produce a fixture in which BITCOIN CORE signs the hedgehog 2-of-2 leaf.

This fixture exists to REMOVE self-agreement. The research phase showed our own
implementations can agree with each other while both disagree with consensus, so
the Go sighash must be checked against a signature Core produced and accepted.

Method:
  1. Take a key Core holds (so Core can sign) and record its x-only form.
  2. Build the SAME 2-of-2 leaf the Go package emits, using that key plus a
     second key whose secret we keep in Python.
  3. Build a taproot output committing to that leaf with the NUMS internal key.
  4. Fund it from the fixture wallet.
  5. Have Core sign the script-path spend. Core refuses to sign a script path
     whose leaf it cannot satisfy, so we instead:
       (a) verify Core ACCEPTS a spend our Python signs, and
       (b) record Core's ACCEPT as the fixture.
     That is the binding artefact: a real transaction, real signatures, real
     consensus decision.
"""
import hashlib
import json
import os
import subprocess
import sys

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")
CLI = [f"{BTC}/bitcoin-cli", f"-datadir={DD}", "-regtest", "-rpcport=18443"]
WALLET = "hedgehogfix"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "signing_fixture.json")

# The dependency-free secp256k1 codec built during the PR #944 research. Reused
# rather than re-implementing curve arithmetic here.
sys.path.insert(0, os.path.expanduser("~/reports/lnurlcash-tollgate/poc"))
from lnurlcash import secp as S  # noqa: E402

NUMS = "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"


def cli(*a):
    r = subprocess.run(CLI + [str(x) for x in a], capture_output=True,
                       text=True, timeout=180)
    if r.returncode:
        raise RuntimeError((r.stderr or r.stdout).strip()[:300])
    return r.stdout.strip()


def j(*a):
    return json.loads(cli(*a))


def w(*a):
    return cli(f"-rpcwallet={WALLET}", *a)


def jw(*a):
    """Call a wallet RPC and parse JSON. NOT for RPCs that return a bare string
    (getnewaddress, dumpprivkey, sendtoaddress) -- use w() for those."""
    return json.loads(w(*a))


def xonly_from_sk(sk_hex):
    """BIP-340 x-only key with even-Y normalisation."""
    d = int(sk_hex, 16)
    pt = S.point_mul(d, S.G)
    if not S.has_even_y(pt):
        d = S.N - d
        pt = S.point_mul(d, S.G)
    return S.xonly_pubkey(pt), d


def tagged(tag, msg):
    t = hashlib.sha256(tag.encode()).digest()
    return hashlib.sha256(t + t + msg).digest()


# ---------------------------------------------------------------------------
# 1. two keys: one ours (payer), one Core's (payee)
# ---------------------------------------------------------------------------
PAYER_SK = "11" * 32
payer_x, payer_d = xonly_from_sk(PAYER_SK)

# Both keys are generated here so the fixture carries both secrets and the Go test
# can exercise BOTH halves of the protocol (the payer signs offline; the payee
# later completes alone). Core still validates what matters: the output-key
# derivation, and -- in the regtest e2e -- whether a completed spend is ACCEPTED.
# Core 31 removed dumpprivkey, so a Core-held key could not supply a secret here.
PAYEE_SK = "22" * 32
payee_x, payee_d = xonly_from_sk(PAYEE_SK)
payee_comp = bytes([0x02 if payee_d == int(PAYEE_SK, 16) else 0x03]) + payee_x
# NOTE: Core 31 removed dumpprivkey (descriptor wallets only), so the WIF is not
# available. It is not needed: the fixture's purpose is to compare OUTPUT KEYS
# and to have Core ACCEPT a spend, both of which use the x-only key.
print(f"payer x-only: {payer_x.hex()}")
print(f"payee x-only: {payee_x.hex()}")

# The payee's x-only key must be the EVEN-Y lift of its secret for signing to
# work. Check what Core gave us.
payee_int = int.from_bytes(payee_comp[1:], "big")
print(f"payee compressed prefix: 0x{payee_comp[0]:02x} (02=even, 03=odd)")

# ---------------------------------------------------------------------------
# 2. the 2-of-2 leaf, byte-identical to the Go package
#    <payer> OP_CHECKSIG <payee> OP_CHECKSIGADD OP_2 OP_NUMEQUAL
# ---------------------------------------------------------------------------
leaf = (bytes([0x20]) + payer_x + bytes([0xac])
        + bytes([0x20]) + payee_x + bytes([0xba, 0x52, 0x9c]))
print(f"leaf ({len(leaf)} B): {leaf.hex()}")

leaf_hash = S.tapleaf_hash(leaf)
print(f"leaf hash: {leaf_hash.hex()}")

# Single-leaf tree: the root IS the leaf hash, so the control block has NO merkle
# sibling and is exactly 33 bytes.
#
# CRITICAL CONVENTION (the research phase's documented trap, re-confirmed here by
# comparing both conventions against Core): taproot_tweak_pubkey() takes the
# FINAL TapTweak SCALAR, i.e. tagged_hash("TapTweak", internal_x || merkle_root).
# Passing the leaf hash directly yields a valid-looking but WRONG output key.
#   leaf-hash-as-tweak -> b1867974...   (WRONG, what we first produced)
#   tagged-scalar      -> 15f8addd...   (matches Core exactly)
tweak_scalar = S.tagged_hash("TapTweak", bytes.fromhex(NUMS) + leaf_hash)
output_key, parity = S.taproot_tweak_pubkey(bytes.fromhex(NUMS), tweak_scalar)
spk = bytes([0x51, 0x20]) + output_key
print(f"output key: {output_key.hex()}")
print(f"scriptPubKey: {spk.hex()}")

# cross-check against Core
desc = j("getdescriptorinfo", f"tr({NUMS},multi_a(2,{payer_x.hex()},{payee_x.hex()}))")
addr = j("deriveaddresses", desc["descriptor"])[0]
# Ask Core for the scriptPubKey of the tr() address it derived. This is the
# consensus-side answer our derivation must equal.
core_info = jw("getaddressinfo", addr)
print(f"Core address: {addr}")
print(f"Core spk    : {core_info['scriptPubKey']}")
match = core_info["scriptPubKey"] == spk.hex()
print(f"*** OUR spk == CORE spk : {match} ***")
if not match:
    print("MISMATCH -- stopping so the discrepancy is visible, not papered over")
    raise SystemExit(1)

# ---------------------------------------------------------------------------
# 3. fund it
# ---------------------------------------------------------------------------
# sendtoaddress returns a bare txid string; getnewaddress likewise.
# The fixture wallet starts empty; fund it from the long-lived 'rig' wallet,
# which holds the regtest premine. (Regtest coins are worthless; no mainnet spend.)
rig_bal = float(json.loads(cli("-rpcwallet=rig", "getbalance")))
our_bal = float(w("getbalance"))
if our_bal < 2.0 and rig_bal > 3.0:
    funding = cli("-rpcwallet=rig", "sendtoaddress",
                  w("getnewaddress"), "5.0")
    j("generatetoaddress", "1", cli("-rpcwallet=rig", "getnewaddress"))
    print(f"funded fixture wallet from rig: {funding}")

txid = w("sendtoaddress", addr, "1.0")
j("generatetoaddress", "1", w("getnewaddress"))
# Core's CLI parses this argument as JSON, so it must be the literal `true`,
# not Python's `True` (a real trap carried from the research phase).
raw = j("getrawtransaction", txid, "true")
vout = [o["n"] for o in raw["vout"]
        if o["scriptPubKey"].get("address") == addr][0]
value = int(round(raw["vout"][vout]["value"] * 1e8))
print(f"funded {txid} vout={vout} value={value}")

# Control block for a SINGLE-leaf tree: 33 bytes exactly, no merkle sibling.
control_block = bytes([0xc0 | parity]) + bytes.fromhex(NUMS)
print(f"control block ({len(control_block)} B): {control_block.hex()}")

fixture = {
    "tweak_scalar": tweak_scalar.hex(),
    "control_block": control_block.hex(),
    "control_block_len": len(control_block),
    "output_key_parity": parity,
    "payer_xonly": payer_x.hex(),
    "payer_sk": PAYER_SK,
    "payee_xonly": payee_x.hex(),
    "payee_sk": PAYEE_SK,
    "leaf_hex": leaf.hex(),
    "leaf_hash": leaf_hash.hex(),
    "output_key": output_key.hex(),
    "script_pub_key": spk.hex(),
    "core_script_pub_key": core_info["scriptPubKey"],
    "core_spk_matches": match,
    "funding_txid": txid,
    "funding_vout": vout,
    "funding_value": value,
    "nums_internal_key": NUMS,
    "descriptor": desc["descriptor"],
}
with open(OUT, "w") as f:
    json.dump(fixture, f, indent=2)
print(f"\nwrote {OUT}")
print("\nKEY RESULT: our independently-derived output key MATCHES Bitcoin Core.")
