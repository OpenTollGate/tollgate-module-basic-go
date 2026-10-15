#!/usr/bin/env python3
"""wit_order.py -- isolate the Hedgehog spend failure.

Hypothesis under test: the witness STACK ORDER is wrong, not the sighash and not
Hedgehog itself.

The 2-of-2 leaf is  <A> OP_CHECKSIG <B> OP_CHECKSIGADD OP_2 OP_EQUAL
BIP-342 pop discipline: CHECKSIG consumes the pubkey from the TOP of the stack
after the key push, and CHECKSIGADD consumes the public key then pushes n.
Witness [w1, w2] pushes w1 first, so w2 is on top.

  A CHECKSIG    pairs A with the TOP item   -> w2 must be A's signature
  B CHECKSIGADD pairs B with the next item  -> w1 must be B's signature

so the correct order is [sig_B, sig_A]. We shipped [sig_A, sig_B].
"""
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hdg
from btclib import Tx, TxIn, TxOut
from taproot import outpoint_value_le

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


alice = bytes.fromhex("11" * 32)
bob = bytes.fromhex("22" * 32)
ax, bx = hdg.xonly(alice), hdg.xonly(bob)
rev = {"alice": hdg.rmd160(b"alice-secret"), "bob": hdg.rmd160(b"bob-secret")}
leaves, _ = hdg.revocation_tree(ax, bx, "alice", rev)
ms = hdg.multisig_script(ax, bx)
ok_x = hdg.taproot_output_key(leaves)
addr = hdg.p2tr_address(ok_x)

txid = cli("sendtoaddress", addr, "1.0", wallet="rig")
cli("generatetoaddress", "1", cli("getnewaddress", wallet="rig"), wallet="rig")
tx = jj("getrawtransaction", txid, "true")
vout = [o["n"] for o in tx["vout"]
        if o["scriptPubKey"].get("address") == addr][0]
value = int(round(tx["vout"][vout]["value"] * 1e8))
dest = cli("getnewaddress", "", "bech32m", wallet="rig")
spk = jj("getaddressinfo", dest, wallet="rig")["scriptPubKey"]
# raw int: taproot_sighash serializes it itself via outpoint_value_le
prevouts = [(value, hdg.p2tr_spk(ok_x))]

t = Tx([TxIn(txid, vout, 0xFFFFFFFD)], [TxOut(value - 2000, bytes.fromhex(spk))])
sig_a = hdg.sign_leaf(t, 0, prevouts, alice, ms)
sig_b = hdg.sign_leaf(t, 0, prevouts, bob, ms)
ctrl, root = hdg.hh_control_block(ms, leaves)

print("leaf:", ms.hex())
print("      <A> OP_CHECKSIG <B> OP_CHECKSIGADD OP_2 OP_EQUAL")
print("ctrl:", len(ctrl), "bytes (33 + path)")
print("root:", root.hex())
print()

results = {}
for label, wit in [("[sig_A, sig_B]  (as shipped)", [sig_a, sig_b]),
                   ("[sig_B, sig_A]  (hypothesis)", [sig_b, sig_a])]:
    t.ins[0].witness = wit + [ms, ctrl]
    out = jj("testmempoolaccept", json.dumps([t.serialize().hex()]))
    a = out[0] if out else {}
    results[label] = a.get("reject-reason", "")
    print(f"{label:30} allowed={a.get('allowed')}  reason={a.get('reject-reason','')!r}")

print()
if results.get("[sig_B, sig_A]  (hypothesis)") == "" or \
   results.get("[sig_B, sig_A]  (hypothesis)") is None:
    print("VERDICT: witness order was the bug -- an IMPLEMENTATION error in this")
    print("harness, not a flaw in Hedgehog.")
else:
    print("VERDICT: reordering did NOT fix it. The fault is elsewhere (sighash")
    print("plumbing or the tree commitment), still our side -- Core accepted a")
    print("script-path spend of the SAME node earlier, so the node is sound.")

json.dump(results, open("/tmp/wit_order.json", "w"), indent=2)
