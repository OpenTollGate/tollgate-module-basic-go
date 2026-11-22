#!/usr/bin/env python3
"""sighash_diag.py -- decide whether the failure is OUR sighash or Hedgehog.

METHOD (the decisive one): obtain a signature that Bitcoin Core ITSELF produced
for a script-path spend of a taproot output WE built, then check it against our
own sighash implementation. If our sighash reproduces the message Core signed,
our plumbing is right and any remaining failure is a witness/tree detail. If it
does not, the bug is unambiguously in our BIP-341 sighash code -- which is our
side, not Hedgehog's.

This is the same "ask Core, not our code" discipline that caught four bugs in
the sibling rig.
"""
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from taproot import schnorr_verify, leaf_hash
import hdg

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")


def cli(*a, wallet=None):
    cmd = [f"{BTC}/bitcoin-cli", f"-datadir={DD}", "-regtest", "-rpcport=18443"]
    if wallet:
        cmd.append(f"-rpcwallet={wallet}")
    r = subprocess.run(cmd + [str(x).strip() for x in a],
                       capture_output=True, text=True, timeout=120)
    if r.returncode:
        raise RuntimeError((r.stderr or r.stdout)[:200])
    return r.stdout.strip()


def jj(*a, wallet=None):
    return json.loads(cli(*a, wallet=wallet))


# Reuse the spend Core already SIGNED AND ACCEPTED in the taprootverify work.
# That gives us (tx, leaf, ctrl, Core's signature) with no ambiguity: whatever
# message Core signed, our sighash must produce the same bytes.
sp = json.load(open("/tmp/kc_sp.json"))
signed_hex = sp["signed"]
wit = sp["witness"]
core_sig = bytes.fromhex(wit[0])
leaf = bytes.fromhex(wit[1])
ctrl = bytes.fromhex(wit[2])

print("=" * 74)
print("Does OUR BIP-341 sighash reproduce what CORE signed?")
print("=" * 74)
print(f"Core accepted this spend, so its signature is by construction correct")
print(f"for the message Core computed.")
print()
print(f"  leaf      : {leaf.hex()}")
print(f"  ctrl      : {len(ctrl)} bytes")
print(f"  core sig  : {core_sig.hex()[:40]}...")
print()

# Decode the signed tx to rebuild our Tx object
dec = jj("decoderawtransaction", signed_hex)
print(f"  txid      : {dec['txid']}")
print(f"  vin count : {len(dec['vin'])}")
vin = dec["vin"][0]
print(f"  prevout   : {vin['txid']}:{vin['vout']}")
print(f"  sequence  : {vin['sequence']}")
print(f"  vout[0]   : {dec['vout'][0]['value']} -> {dec['vout'][0]['scriptPubKey']['hex']}")
print(f"  locktime  : {dec['locktime']}   version: {dec['version']}")
print()

# The prevout value and spk for the INPUT being spent
prevtx = jj("getrawtransaction", vin["txid"], "true")
prev_vout = [o for o in prevtx["vout"] if o["n"] == vin["vout"]][0]
prev_value = int(round(prev_vout["value"] * 1e8))
prev_spk = bytes.fromhex(prev_vout["scriptPubKey"]["hex"])
print(f"  prevout value : {prev_value} sats")
print(f"  prevout spk   : {prev_spk.hex()}")
print()

# Now rebuild our Tx and compute OUR sighash for this input.
from btclib import Tx, TxIn, TxOut

ins = [TxIn(vin["txid"], vin["vout"], vin["sequence"])]
outs = [TxOut(int(round(o["value"] * 1e8)), bytes.fromhex(o["scriptPubKey"]["hex"]))
        for o in dec["vout"]]
tx = Tx(ins, outs, locktime=dec["locktime"], version=dec["version"])

prevouts = [(prev_value, prev_spk)]
ours = hdg.leaf_sighash(tx, 0, prevouts, leaf)
print(f"OUR sighash   : {ours.hex()}")
print()

# The pubkey that Core's signature verifies against, taken from the leaf.
# leaf is <32B xonly> OP_CHECKSIG, so byte 1..33 is the key.
xonly = leaf[1:33]
print(f"leaf xonly key: {xonly.hex()}")
ok = schnorr_verify(ours, xonly, core_sig)
print(f"OUR sighash + Core's signature verifies? {ok}")
print()
if ok:
    print("VERDICT: our sighash is CORRECT. The Hedgehog failure is therefore a")
    print("witness-construction or tree-path detail in the harness, NOT the sighash,")
    print("and NOT a flaw in Hedgehog.")
else:
    print("VERDICT: our sighash does NOT reproduce Core's message. The bug is")
    print("unambiguously in OUR BIP-341 sighash code -- a harness bug, not Hedgehog.")
    print()
    print("Most likely cause, given the leaf carries a 32-byte x-only key and the")
    print("sighash for a SCRIPT PATH needs the TAPLEAF HASH over the tree the output")
    print("committed to: our hdg.leaf_sighash passes leaf_hash(script) directly, but")
    print("BIP-341 requires the leaf hash as committed in THAT tree, which for a")
    print("single-leaf tree is the same value -- so the suspect is the")
    print("key_version/code_separator suffix or the sha_scriptpubkeys stream.")
