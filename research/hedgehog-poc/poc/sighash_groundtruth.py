#!/usr/bin/env python3
"""Ground truth: Core signs OUR tx, so we can test our sighash against it.

The /tmp/kc_sp.json signature was produced by Core for a tx Core BUILT. There may
be a mismatch between what Core signed and what we reconstructed from the decoder
(e.g. a different output value or a fee-adjusted amount). Removing that ambiguity
is the whole point here.

PLAN:
  1. Build a taproot output with a SINGLE script leaf whose key we control, using
     OUR commitment math (already proven to match Core).
  2. Fund it.
  3. Import the descriptor WITH the private key so Core holds the leaf key.
  4. Construct the spending tx ourselves with createrawtransaction (valid wire
     format guaranteed), then let Core sign it via signrawtransactionwithwallet.
  5. Recover exactly what Core signed by testing OUR sighash against Core's sig --
     now with no reconstruction ambiguity at all.
"""
import hashlib
import json
import os
import struct
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from taproot import _tagged_hash, leaf_hash, schnorr_verify, xonly
from btclib import varint

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")


def cli(*a, wallet=None):
    cmd = [f"{BTC}/bitcoin-cli", f"-datadir={DD}", "-regtest", "-rpcport=18443"]
    if wallet:
        cmd.append(f"-rpcwallet={wallet}")
    r = subprocess.run(cmd + [str(x).strip() for x in a],
                       capture_output=True, text=True, timeout=180)
    if r.returncode:
        raise RuntimeError((r.stderr or r.stdout)[:300])
    return r.stdout.strip()


def jj(*a, wallet=None):
    return json.loads(cli(*a, wallet=wallet))


def b58(payload):
    chk = hashlib.sha256(hashlib.sha256(payload).digest()).digest()[:4]
    full = payload + chk
    n = int.from_bytes(full, "big")
    A = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
    s = ""
    while n:
        n, r = divmod(n, 58)
        s = A[r] + s
    zeros = len(full) - len(full.lstrip(b"\x00"))
    return "1" * zeros + s


def S1(b):
    return hashlib.sha256(b).digest()


# ---- 1. our own keypair so we can sign too ----
priv = bytes.fromhex("33" * 32)
from taproot import xonly_from_seckey_even
# returns (xonly_bytes, effective_seckey_INT) with even-Y semantics
x, priv_eff_int = xonly_from_seckey_even(priv)
priv_even = priv_eff_int.to_bytes(32, "big")

wif = b58(b"\xef" + priv_even + b"\x01")
print("x-only key  :", x.hex())
print("wif         :", wif)

# leaf: <key> OP_CHECKSIG
leaf = bytes([32]) + x + bytes([0xAC])
print("leaf        :", leaf.hex())

NUMS = "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"

# ---- 2. descriptor Core can sign, committed to the SAME leaf ----
desc_info = jj("getdescriptorinfo", f"tr({NUMS},pk({wif}))")
ck = desc_info["checksum"]
priv_desc = f"tr({NUMS},pk({wif}))#{ck}"
pub_desc = desc_info["descriptor"]
print("core desc   :", pub_desc)

addr = jj("deriveaddresses", pub_desc)[0]
print("address     :", addr)

imp = jj("importdescriptors",
         json.dumps([{"desc": priv_desc, "timestamp": "now", "active": False}]),
         wallet="rig")
print("import      :", json.dumps(imp)[:120])

# ---- 3. fund and mine ----
txid = cli("sendtoaddress", addr, "0.5", wallet="rig")
cli("generatetoaddress", "1", cli("getnewaddress", wallet="rig"), wallet="rig")
tx = jj("getrawtransaction", txid, "true")
vout = [o["n"] for o in tx["vout"]
        if o["scriptPubKey"].get("address") == addr][0]
pv_value = int(round(tx["vout"][vout]["value"] * 1e8))
pv_spk = bytes.fromhex(tx["vout"][vout]["scriptPubKey"]["hex"])
print(f"funded      : {txid[:20]}… vout={vout} value={pv_value}")

# ---- 4. Core builds and signs the spend ----
dest = cli("getnewaddress", wallet="rig")
rawtx = cli("createrawtransaction",
            json.dumps([{"txid": txid, "vout": vout}]),
            json.dumps([{dest: 0.4999}]))
prevouts_json = json.dumps([{"txid": txid, "vout": vout,
                             "scriptPubKey": pv_spk.hex(),
                             "amount": pv_value / 1e8}])
signed = jj("signrawtransactionwithwallet", rawtx, prevouts_json, wallet="rig")
print("signed complete:", signed.get("complete"))
if not signed.get("complete"):
    print("errors:", json.dumps(signed.get("errors"))[:300])
    sys.exit(1)

sd = jj("decoderawtransaction", signed["hex"])
swit = sd["vin"][0]["txinwitness"]
core_sig = bytes.fromhex(swit[0])
signed_leaf = bytes.fromhex(swit[1])
print("core sig    :", core_sig.hex()[:40], "…")
print("signed leaf :", signed_leaf.hex())
print("leaf match  :", signed_leaf == leaf)
print()

# ---- 5. test our sighash against Core's signature, no reconstruction guesses ----
# Decode Core's SIGNED tx as the authority for version/locktime/outputs.
outs = [o for o in sd["vout"]]
body_outputs = b"".join(
    int(round(o["value"] * 1e8)).to_bytes(8, "little")
    + varint(len(bytes.fromhex(o["scriptPubKey"]["hex"])))
    + bytes.fromhex(o["scriptPubKey"]["hex"])
    for o in outs)

sigmsg = (bytes([0]) + bytes([0])
          + sd["version"].to_bytes(4, "little")
          + sd["locktime"].to_bytes(4, "little")
          + S1(bytes.fromhex(txid)[::-1] + struct.pack("<I", vout))   # sha_prevouts
          + S1(pv_value.to_bytes(8, "little"))                        # sha_amounts
          + S1(varint(len(pv_spk)) + pv_spk)                          # sha_scriptpubkeys
          + S1(body_outputs)                                          # sha_outputs
          + bytes([2])                                                # spend_type
          + struct.pack("<I", 0)                                      # input_index
          + leaf_hash(signed_leaf)                                    # tapleaf_hash
          + bytes([0])                                                # key_version
          + struct.pack("<I", 0xFFFFFFFF))                            # codesep_pos

h = _tagged_hash(b"TapSighash", sigmsg)
print("OUR sighash :", h.hex())
print("Core's sig verifies against OUR sighash :",
      schnorr_verify(h, signed_leaf[1:33], core_sig))
print()

if schnorr_verify(h, signed_leaf[1:33], core_sig):
    print("*** BIP-341 CONSTRUCTION CONFIRMED CORRECT against Core's own signature ***")
else:
    print("Still differs. Dump for diffing:")
    print("  sigmsg:", sigmsg.hex())
    json.dump({"sigmsg": sigmsg.hex(), "sig": core_sig.hex(),
               "leaf": signed_leaf.hex(), "txid": txid, "vout": vout,
               "pv_value": pv_value, "pv_spk": pv_spk.hex(),
               "tx": sd}, open("/tmp/sighash_gt.json", "w"), indent=2)
