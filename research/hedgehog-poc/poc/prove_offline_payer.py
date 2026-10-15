#!/usr/bin/env python3
"""prove_offline_payer.py -- EXECUTE the direction TollGate needs.

Demonstrates against real Bitcoin Core consensus:
  * Alice (the PAYER) signs the state update while Bob takes no action at all.
  * Alice then stops. She contributes nothing further.
  * Bob (the PAYEE) completes the spend alone, using only Alice's captured
    signature, and Core ACCEPTS it.

This is the direction that matters for a TollGate purchase: the customer is the
payer and may have no WAN, while the router is a service and is online.
"""
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hdg
from btclib import Tx, TxIn, TxOut

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")


def cli(*a, wallet=None):
    cmd = [f"{BTC}/bitcoin-cli", f"-datadir={DD}", "-regtest", "-rpcport=18443"]
    if wallet:
        cmd.append(f"-rpcwallet={wallet}")
    r = subprocess.run(cmd + [str(x).strip() for x in a],
                       capture_output=True, text=True, timeout=180)
    if r.returncode:
        raise RuntimeError((r.stderr or r.stdout)[:250])
    return r.stdout.strip()


def jj(*a, wallet=None):
    return json.loads(cli(*a, wallet=wallet))


# ---- roles: Alice pays, Bob is the vendor ----
alice = bytes.fromhex("44" * 32)   # PAYER  (the customer)
bob = bytes.fromhex("55" * 32)     # PAYEE  (the router)
ax, bx = hdg.xonly(alice), hdg.xonly(bob)
rev = {"alice": hdg.rmd160(b"a-secret"), "bob": hdg.rmd160(b"b-secret")}
leaves, _ = hdg.revocation_tree(ax, bx, "alice", rev)
ms = hdg.multisig_script(ax, bx)
ok_x = hdg.taproot_output_key(leaves)
addr = hdg.p2tr_address(ok_x)

print("=" * 74)
print("PROVING THE DIRECTION TOLLGATE NEEDS")
print("  payer (customer) acts OFFLINE, payee (router) completes ONLINE")
print("=" * 74)
print(f"  payer key : {ax.hex()[:20]}…")
print(f"  payee key : {bx.hex()[:20]}…")
print()

# fund the channel
txid = cli("sendtoaddress", addr, "2.0", wallet="rig")
cli("generatetoaddress", "1", cli("getnewaddress", wallet="rig"), wallet="rig")
tx = jj("getrawtransaction", txid, "true")
vout = [o["n"] for o in tx["vout"]
        if o["scriptPubKey"].get("address") == addr][0]
value = int(round(tx["vout"][vout]["value"] * 1e8))
print(f"  channel funded: {txid[:24]}… vout={vout} ({value} sats)")

dest = cli("getnewaddress", "", "bech32m", wallet="rig")
spk = jj("getaddressinfo", dest, wallet="rig")["scriptPubKey"]

t = Tx([TxIn(txid, vout, 0xFFFFFFFD)], [TxOut(value - 2000, bytes.fromhex(spk))])
prevouts = [(value, hdg.p2tr_spk(ok_x))]
ctrl, root = hdg.hh_control_block(ms, leaves)

# ---------------------------------------------------------------- PAYER SIDE
print()
print("PAYER SIDE (Alice) -- Bob takes NO action and is NOT contacted:")
sig_a = hdg.sign_leaf(t, 0, prevouts, alice, ms)
print(f"  payer signs the update locally: {sig_a.hex()[:32]}…")
print("  ...and hands it over as text. The payer's work is now DONE.")
print("  The payer then goes away. Nothing below involves her again.")

# ---------------------------------------------------------------- PAYEE SIDE
print()
print("PAYEE SIDE (Bob) -- alone, later, using ONLY the captured signature:")
sig_b = hdg.sign_leaf(t, 0, prevouts, bob, ms)
# correct witness order for <A> CHECKSIG <B> CHECKSIGADD 2 EQUAL is [sig_B, sig_A]
t.ins[0].witness = [sig_b, sig_a, ms, ctrl]
raw = t.serialize().hex()

result = jj("testmempoolaccept", json.dumps([raw]))[0]
print(f"  Core testmempoolaccept: allowed={result.get('allowed')} "
      f"reason={result.get('reject-reason', '')!r}")
if result.get("allowed"):
    bcast = cli("sendrawtransaction", raw)
    print(f"  BROADCAST: {bcast}")
    print()
    print("  *** EXECUTED: the payer acted offline; the payee completed alone. ***")
    print("  The payer was not reachable and not consulted for the completion.")
else:
    print("  (did not complete -- see reason)")
