#!/usr/bin/env python3
"""Get Core's OWN sighash for a taproot script-path spend, as ground truth.

METHOD (removes all guesswork): build a taproot output committed to a script Core
can sign, let Core SIGN the spend, dump the witness, then we hold BOTH the exact
message Core signed (recoverable via its signature) and the tx. We then binary-
search the SigMsg construction against that signature.

Better still: use `bitcoin-cli` to reveal the sighash directly. There is no RPC
for it, but `signrawtransactionwithwallet` on a taproot output tells us the
message implicitly. So instead we PIN the construction by testing an exhaustive
but small set of structural variants against Core's accepted signature.
"""
import hashlib
import itertools
import json
import os
import struct
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from taproot import _tagged_hash, leaf_hash, schnorr_verify
from btclib import Tx, TxIn, TxOut, varint

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")


def jj(*a):
    r = subprocess.run([f"{BTC}/bitcoin-cli", f"-datadir={DD}", "-regtest",
                        "-rpcport=18443"] + [str(x).strip() for x in a],
                       capture_output=True, text=True, timeout=120)
    return json.loads(r.stdout)


S1 = lambda b: hashlib.sha256(b).digest()

sp = json.load(open("/tmp/kc_sp.json"))
dec = jj("decoderawtransaction", sp["signed"])
wit = dec["vin"][0]["txinwitness"]
core_sig = bytes.fromhex(wit[0])
leaf = bytes.fromhex(wit[1])
vin = dec["vin"][0]
prevtx = jj("getrawtransaction", vin["txid"], "true")
pv = [o for o in prevtx["vout"] if o["n"] == vin["vout"]][0]
pv_value = int(round(pv["value"] * 1e8))
pv_spk = bytes.fromhex(pv["scriptPubKey"]["hex"])

ins = [TxIn(vin["txid"], vin["vout"], vin["sequence"])]
outs = [TxOut(int(round(o["value"] * 1e8)),
              bytes.fromhex(o["scriptPubKey"]["hex"])) for o in dec["vout"]]
tx = Tx(ins, outs, locktime=dec["locktime"], version=dec["version"])

outpoint_bytes = bytes.fromhex(vin["txid"])[::-1] + struct.pack("<I", vin["vout"])
sha_prevouts = S1(outpoint_bytes)
sha_amounts = S1(pv_value.to_bytes(8, "little"))
sha_spks = S1(varint(len(pv_spk)) + pv_spk)
sha_outputs = S1(b"".join(o.ser() for o in tx.outs))
lh = leaf_hash(leaf)

hits = []


def test(label, msg):
    h = _tagged_hash(b"TapSighash", msg)
    if schnorr_verify(h, leaf[1:33], core_sig):
        hits.append(label)
        print(f"  HIT: {label}")


base_head = (bytes([0]) + bytes([0])
             + tx.version.to_bytes(4, "little")
             + tx.locktime.to_bytes(4, "little"))

# Try many plausible orderings/subsets of the SigMsg for the DEFAULT, non-ACP case.
variants = {
    "spec order, input_index": (base_head + sha_prevouts + sha_amounts + sha_spks
                                + sha_outputs + bytes([2])
                                + struct.pack("<I", 0) + lh + bytes([0])
                                + struct.pack("<I", 0xFFFFFFFF)),
    "no sha_prevouts": (base_head + sha_amounts + sha_spks + sha_outputs
                        + bytes([2]) + struct.pack("<I", 0) + lh + bytes([0])
                        + struct.pack("<I", 0xFFFFFFFF)),
    "amounts before prevouts": (base_head + sha_amounts + sha_prevouts + sha_spks
                                + sha_outputs + bytes([2])
                                + struct.pack("<I", 0) + lh + bytes([0])
                                + struct.pack("<I", 0xFFFFFFFF)),
    "spks before amounts": (base_head + sha_prevouts + sha_spks + sha_amounts
                            + sha_outputs + bytes([2])
                            + struct.pack("<I", 0) + lh + bytes([0])
                            + struct.pack("<I", 0xFFFFFFFF)),
    "spend_type 0": (base_head + sha_prevouts + sha_amounts + sha_spks
                     + sha_outputs + bytes([0]) + struct.pack("<I", 0)
                     + lh + bytes([0]) + struct.pack("<I", 0xFFFFFFFF)),
    "no key_version": (base_head + sha_prevouts + sha_amounts + sha_spks
                       + sha_outputs + bytes([2]) + struct.pack("<I", 0)
                       + lh + struct.pack("<I", 0xFFFFFFFF)),
    "no codesep": (base_head + sha_prevouts + sha_amounts + sha_spks
                   + sha_outputs + bytes([2]) + struct.pack("<I", 0)
                   + lh + bytes([0])),
    "input_index before streams": (base_head + struct.pack("<I", 0) + sha_prevouts
                                   + sha_amounts + sha_spks + sha_outputs
                                   + bytes([2]) + lh + bytes([0])
                                   + struct.pack("<I", 0xFFFFFFFF)),
}

for label, msg in variants.items():
    test(label, msg)

# Also: 1-input case may make some streams collapse; test with sha_annex present
annex = bytes([2]) + S1(b"")
for label, msg in {
    "spec + sha_annex": (base_head + sha_prevouts + sha_amounts + sha_spks
                         + sha_outputs + annex + struct.pack("<I", 0) + lh
                         + bytes([0]) + struct.pack("<I", 0xFFFFFFFF)),
}.items():
    test(label, msg)

print()
print("hits:", hits if hits else "NONE")
