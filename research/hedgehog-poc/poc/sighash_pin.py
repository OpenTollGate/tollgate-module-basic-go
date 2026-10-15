#!/usr/bin/env python3
"""Pin the sighash bug exactly, then characterise Hedgehog's online requirements.

Part A: our BIP-341 script-path sighash does not reproduce Core's message. Find
        the byte that differs by computing the sighash fields by hand.
Part B: read the SHIPPED hedgehog code for who must be online when.
"""
import hashlib
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import taproot
from taproot import _tagged_hash, leaf_hash
from btclib import Tx, TxIn, TxOut

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")


def jj(*a):
    cmd = [f"{BTC}/bitcoin-cli", f"-datadir={DD}", "-regtest", "-rpcport=18443"]
    r = subprocess.run(cmd + [str(x).strip() for x in a],
                       capture_output=True, text=True, timeout=120)
    return json.loads(r.stdout)


print("=" * 74)
print("PART A -- is our sighash structurally correct?")
print("=" * 74)
print("Check the easy suspects first, then the fields.")
print()

# The TapSighash tag must be exactly b"TapSighash"
print("TapSighash tag in use:", getattr(taproot, "TAG_TAPSIGHASH", None))

# leaf_hash must include the 0xc0 leaf-version byte
script = bytes.fromhex("204f355bdcb7cc0af728ef3cceb9615d90684bb5b2ca5f859ab0f0b704075871aaac")
ours_lh = leaf_hash(script)
expect = _tagged_hash(b"TapLeaf", b"\xc0" + bytes([len(script)]) + script)
print(f"leaf_hash(script)      : {ours_lh.hex()}")
print(f"manual (0xc0 prefix)   : {expect.hex()}")
print(f"leaf_hash correct      : {ours_lh == expect}")
print()

# Now compare against a known-good reference: bitcoin-core's own sighash for the
# SAME tx, obtained by having Core sign a 1-leaf taproot output. We already have
# that in /tmp/kc_sp.json. Recompute with an INDEPENDENT hand implementation.
sp = json.load(open("/tmp/kc_sp.json"))
signed = sp["signed"]
wit = sp["witness"]
core_sig = bytes.fromhex(wit[0])
leaf = bytes.fromhex(wit[1])

dec = jj("decoderawtransaction", signed)
vin = dec["vin"][0]
prevtx = jj("getrawtransaction", vin["txid"], "true")
pv = [o for o in prevtx["vout"] if o["n"] == vin["vout"]][0]
prev_value = int(round(pv["value"] * 1e8))
prev_spk = bytes.fromhex(pv["scriptPubKey"]["hex"])


def dsha(b):
    return hashlib.sha256(hashlib.sha256(b).digest()).digest()


def hand_sighash(dec, vin, prev_value, prev_spk, leaf):
    """Independent BIP-341 SIGHASH_DEFAULT script-path sighash."""
    epoch = bytes([0])
    hash_type = bytes([0])
    version = dec["version"].to_bytes(4, "little")
    locktime = dec["locktime"].to_bytes(4, "little")
    # sha_amounts / sha_scriptpubkeys over ALL inputs (1 input here)
    sha_amounts = dsha(prev_value.to_bytes(8, "little"))
    sha_spks = dsha(prev_spk)
    # outpoint: reversed txid + LE vout
    outpoint = bytes.fromhex(vin["txid"])[::-1] + vin["vout"].to_bytes(4, "little")
    scriptcode = b"\x00"  # empty push for taproot
    value = prev_value.to_bytes(8, "little")
    seq = vin["sequence"].to_bytes(4, "little")
    # sha_outputs
    outs = b""
    for o in dec["vout"]:
        v = int(round(o["value"] * 1e8)).to_bytes(8, "little")
        s = bytes.fromhex(o["scriptPubKey"]["hex"])
        outs += v + bytes([len(s)]) + s
    sha_outputs = dsha(outs)
    spend_type = bytes([2])  # ext_flag=1 (script path) * 2 + annex(0)
    sha_annex = dsha(b"")
    m = (epoch + hash_type + version + locktime + sha_amounts + sha_spks
         + outpoint + scriptcode + value + seq + sha_outputs
         + spend_type + sha_annex
         + leaf_hash(leaf) + b"\x00" + (0xFFFFFFFF).to_bytes(4, "little"))
    return _tagged_hash(b"TapSighash", m)


hand = hand_sighash(dec, vin, prev_value, prev_spk, leaf)
print(f"hand-built sighash : {hand.hex()}")

from taproot import schnorr_verify
print(f"Core's sig verifies against HAND sighash : "
      f"{schnorr_verify(hand, leaf[1:33], core_sig)}")

# and against ours
ins = [TxIn(vin["txid"], vin["vout"], vin["sequence"])]
outs = [TxOut(int(round(o["value"] * 1e8)),
              bytes.fromhex(o["scriptPubKey"]["hex"])) for o in dec["vout"]]
tx = Tx(ins, outs, locktime=dec["locktime"], version=dec["version"])
import hdg
ours = hdg.leaf_sighash(tx, 0, [(prev_value, prev_spk)], leaf)
print(f"harness sighash    : {ours.hex()}")
print(f"Core's sig verifies against HARNESS sighash: "
      f"{schnorr_verify(ours, leaf[1:33], core_sig)}")
print()
print(f"hand == harness : {hand == ours}")
