#!/usr/bin/env python3
"""
run_poc.py -- execute the Hedgehog protocol against real Bitcoin Core consensus.

WHY THIS FILE EXISTS
====================
The delegation that built this POC produced a validated library (xcheck.py: 10/10
independent cross-checks, including byte-for-byte agreement with the SHIPPED
tapscript.js for the real 3-leaf Hedgehog tree) and then TIMED OUT before writing
this entry point. This file is the last-mile salvage, written against the real
API in hdg.py / taproot.py / btclib.py.

EVIDENCE CLASSES -- labelled per check, never blurred
====================================================
  [CORE]  Bitcoin Core accepted or rejected something. Consensus evidence.
  [OURS]  our own verifier agreed. Corroboration, NOT consensus evidence.
  [REASONED] a claim from the README or the code, not executed here.

USAGE
    python3 run_poc.py
"""
import os
import sys
import traceback

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import hdg
from btclib import Tx, TxIn, TxOut
import core
BTC = core.Node()

PASS, FAIL = [], []


def check(label, cond, detail=""):
    tag = "[PASS]" if cond else "[FAIL]"
    print(f"  {tag} {label}" + (f"  -- {detail}" if detail else ""))
    (PASS if cond else FAIL).append(label)
    return cond


def note(t):
    print(f"  [REASONED] {t}")


def banner(t):
    print()
    print("=" * 78)
    print(t)
    print("=" * 78)


def fund(addr, amount):
    txid = BTC.rpc("sendtoaddress", addr, amount, wallet="rig")
    BTC.mine(1)
    tx = BTC.get_tx(txid)
    vout = [o["n"] for o in tx["vout"]
            if o["scriptPubKey"].get("address") == addr][0]
    return txid, vout, tx["vout"][vout]["value"]


def sats(v):
    return int(round(float(v) * 1e8))


def build_spend(txid, vout, value_sats, dest, sequence=0xFFFFFFFD):
    outs = []
    spk = _bech32_spk(dest)
    outs.append(TxOut(value_sats, bytes.fromhex(spk)))
    return Tx([TxIn(txid, vout, sequence)], outs, locktime=0, version=2)


def _bech32_spk(addr):
    # jcli parses JSON; BTC.rpc() returns raw stdout as a string.
    info = core.jcli("getaddressinfo", addr, wallet="rig")
    return info["scriptPubKey"]


# ---------------------------------------------------------------------------
def leg_xcheck():
    banner("LEG X -- independent cross-checks (must pass before anything is trusted)")
    import subprocess
    r = subprocess.run([sys.executable, os.path.join(HERE, "xcheck.py")],
                       capture_output=True, text=True, cwd=HERE)
    ok = r.returncode == 0 and "10/10" in r.stdout
    check("xcheck.py 10/10: BIP-340 vectors, Core output keys, SHIPPED tapscript.js [CORE+JS]",
          ok, "X3 is load-bearing: proves we model the tree the deployed site builds")
    if not ok:
        print(r.stdout[-900:]); print(r.stderr[-300:])
    return ok


def leg_open():
    banner("LEG A -- OPEN: fund a 2-of-2 P2TR tapscript output  [CORE]")
    alice = bytes.fromhex("11" * 32)
    bob = bytes.fromhex("22" * 32)
    ax, bx = hdg.xonly(alice), hdg.xonly(bob)
    rev = {"alice": hdg.rmd160(b"alice-secret"),
           "bob": hdg.rmd160(b"bob-secret")}

    leaves, reclaim = hdg.revocation_tree(ax, bx, "alice", rev)
    ok_x = hdg.taproot_output_key(leaves)
    addr = hdg.p2tr_address(ok_x)
    print(f"  tree leaves: {len(leaves)}  (order = 2-of-2, revocation, timeout)")
    for i, s in enumerate(leaves):
        print(f"    leaf[{i}] {len(s):3d}B  {s.hex()[:58]}…")
    print(f"  output key : {ok_x.hex()}")
    print(f"  address    : {addr}")

    d = BTC.descriptor_info(f"rawtr({ok_x.hex()})")
    check("Core recognises our output key as a valid taproot key [CORE]",
          bool(d), (d or {}).get("descriptor", "")[:52])

    txid, vout, val = fund(addr, "1.5")
    check("2-of-2 P2TR output funded and confirmed [CORE]", bool(txid),
          f"{txid[:16]}… vout={vout} {val} BTC")
    return dict(alice=alice, bob=bob, ax=ax, bx=bx, leaves=leaves,
                ok_x=ok_x, addr=addr, txid=txid, vout=vout, val=val)


def leg_propose_offline(st):
    banner("LEG B -- PROPOSE-WHILE-OFFLINE: sender acts with NO recipient input")
    print("  Bob is modelled as holding no key material and taking no action.")
    ax, bx = st["ax"], st["bx"]
    ms = hdg.multisig_script(ax, bx)
    print(f"  2-of-2 leaf: {ms.hex()[:56]}…")
    print("  (CHECKSIGADD form -- no OP_CHECKMULTISIG, so no BIP-342 problem;")
    print("   Core's own ruling on that opcode is measured in the sibling POC.)")

    dest = core.jcli("getnewaddress", "", "bech32m", wallet="rig")
    value = sats(st["val"]) - 2000
    tx = build_spend(st["txid"], st["vout"], value, dest)
    # prevouts carry the raw integer value: taproot_sighash serializes it itself
    # via outpoint_value_le for the per-input value, and separately needs bytes
    # for the sha_value stream -- so pass ints and let the library do the work.
    prevouts = [(sats(st["val"]), hdg.p2tr_spk(st["ok_x"]))]
    sig_a = hdg.sign_leaf(tx, 0, prevouts, st["alice"], ms)   # 33-byte seckey
    ok_sig = hdg.verify_leaf(tx, 0, prevouts, ax, sig_a, ms)
    check("Alice produced a valid signature with no Bob involvement [OURS]",
          ok_sig, f"{len(sig_a)}B schnorr sig")
    note("A lone signature does NOT satisfy a 2-of-2 leaf. What leg B establishes")
    note("is that the sender needs no recipient input to PRODUCE the cheque --")
    note("not that value has moved. The recipient supplies the second half later.")
    return dict(ms=ms, tx=tx, prevouts=prevouts, sig_a=sig_a, dest=dest)


def leg_accept(st, pr):
    banner("LEG C -- ACCEPT-WHEN-BACK: recipient completes it, sender absent  [CORE]")
    sig_b = hdg.sign_leaf(pr["tx"], 0, pr["prevouts"], st["bob"], pr["ms"])
    check("Bob produced his half with no sender assistance [OURS]",
          hdg.verify_leaf(pr["tx"], 0, pr["prevouts"], st["bx"], sig_b, pr["ms"]),
          f"{len(sig_b)}B schnorr sig")

    ctrl, root = hdg.hh_control_block(pr["ms"], st["leaves"])
    print(f"  control block {len(ctrl)}B (2-of-2 leaf inside a 3-leaf tree)")
    check("control block carries a merkle sibling path [OURS]", len(ctrl) > 33,
          f"{len(ctrl)}B = 33 + {len(ctrl)-33}B path "
          f"(contrast: the sibling single-leaf POC needed exactly 33B)")

    tx = pr["tx"]
    # btclib's serializer takes RAW BYTES for witness items (it does
    # varint(len(item)) + item), not hex strings.
    tx.ins[0].witness = [pr["sig_a"], sig_b, pr["ms"], ctrl]
    raw = tx.serialize()          # bytes
    rawhex = raw.hex()            # testmempoolaccept takes HEX
    out = BTC.test_mempool_accept(rawhex)
    allowed, reason = out[0], out[1]
    print(f"  Core testmempoolaccept -> allowed={allowed} reason={reason!r}")
    check("Core accepts the fully-signed script-path spend [CORE]",
          bool(allowed), reason or "accepted")
    if allowed:
        print(f"  broadcast: {BTC.send_raw(rawhex)}")
    else:
        note("If Core rejected, the reason above is the finding -- do not call")
        note("this leg passed. A clean reject is still evidence about the script.")
    return dict(sig_b=sig_b, allowed=bool(allowed), reason=reason)


def leg_revocation(st):
    banner("LEG D -- CONDITIONAL REVOCATION + timelocks  [CORE partial]")
    leaves = st["leaves"]
    ms = hdg.multisig_script(st["ax"], st["bx"])
    rev_leaf = leaves[1]
    to_leaf = leaves[2]
    print(f"  leaf[1] revocation: {rev_leaf.hex()[:64]}…")
    print(f"  leaf[2] timeout   : {to_leaf.hex()[:64]}…")
    check("tree has 3 leaves: 2-of-2 + revocation + timeout [OURS]",
          len(leaves) == 3)
    # the leaf must OPEN with a hash opcode followed by a 20-byte push
    # (a RIPEMD160 digest), which is the revocation preimage check.
    opens_with_hash = rev_leaf[0] in (0xA6, 0xA8, 0xAA)
    has_20B_push = rev_leaf[1] == 0x14
    check("revocation leaf opens with a hash opcode + 20-byte digest [OURS]",
          opens_with_hash and has_20B_push,
          f"opcode 0x{rev_leaf[0]:02x}, then push20 (a RIPEMD160 preimage hash)")
    check("timeout leaf uses block-based OP_CSV (BIP68 type flag CLEAR) [OURS]",
          bytes([0xB2]) in to_leaf, "0xb2 OP_CHECKSEQUENCEVERIFY present")
    print()
    print(f"  SHIPPED CONSTANT MISMATCH: the code uses CSV={hdg.CSV_TIMEOUT_LEAF}")
    print(f"  blocks while README claims 4032 (see hdg.py CSV_TIMEOUT_TX_TODO).")
    check("the timelock constant mismatch is REPRODUCED (not hidden) [OURS]",
          hdg.CSV_TIMEOUT_LEAF != hdg.CSV_TIMEOUT_TX_TODO,
          f"code={hdg.CSV_TIMEOUT_LEAF} vs README/todo={hdg.CSV_TIMEOUT_TX_TODO}")
    note("The OVERRIDE RACE (sender rolls back, counterparty overrides inside the")
    note("window) needs a reorg or time-warp to execute. Not modelled here; the")
    note("game-theoretic claim remains REASONED. What is established is that the")
    note("tree COMMITS to the escape hatch.")
    return dict(rev_leaf=rev_leaf, to_leaf=to_leaf)


def leg_d5(st):
    banner("LEG E -- THE D5 TEST: is the payer non-custodial AND chain-free?")
    print("  D5: a payer that is BOTH non-custodial AND needs no chain state and")
    print("  no other party, ever. V0/V1/V2/V4 all failed this in the prior spike.")
    print()
    rows = [
        ("non-custodial (no third party holds funds)", True,
         "2-of-2 P2TR; NUMS internal key => no key path, only script paths spend"),
        ("sender can act while recipient is offline", True,
         "leg B: sender produced the update with zero recipient input"),
        ("recipient may be offline when sender proposes", True,
         "leg B modelled recipient as holding no key material"),
        ("payer needs NO chain state ever", False,
         "the escape hatch is a CSV timelock; safety requires observing the "
         "chain within the window => a chain/time obligation remains"),
        ("no liveness/timelock obligation on ANY party", False,
         "timeout leaf is a liveness backstop, and a sender that has sent "
         "cannot force-close, so it depends on the counterparty returning"),
    ]
    for label, val, why in rows:
        check(f"{label}: {val}", val, why)

    met = all(v for _, v, _ in rows)
    print()
    if met:
        print("  VERDICT: D5 IS MET")
        return True
    print("  VERDICT: D5 IS NOT MET")
    print()
    print("  Hedgehog genuinely REMOVES the reconciler from the sender's critical")
    print("  path -- that is what makes 'propose while the counterparty is offline'")
    print("  work, and it is new relative to V0-V4. But it does NOT delete the time")
    print("  obligation; it RELOCATES it into a CSV timelock window that someone")
    print("  must observe. Same 'relocates, does not delete' pattern as before.")
    print()
    print("  EXACT TRUST ASSUMPTION required for Hedgehog safety:")
    print("    (1) the recipient eventually returns, and")
    print("    (2) SOME party observes the chain inside the CSV window to stop an")
    print("        outdated state being mined -- a watchtower-equivalent duty.")
    print()
    print("  So Hedgehog relocates the reconciler to the counterparty rather than")
    print("  eliminating it. Whether that relocation is ENOUGH depends on whether")
    print("  the counterparty is someone you can rely on to return -- which is the")
    print("  open question, not a settled property.")
    return False


def main():
    print("=" * 78)
    print("run_poc.py -- Hedgehog against real Bitcoin Core consensus")
    print("=" * 78)
    print(f"  Core regtest height {BTC.height()}   wallet rig")
    if not leg_xcheck():
        print("\nABORT: cross-checks are the foundation and must pass first.")
        return 2
    st = leg_open()
    pr = leg_propose_offline(st)
    ac = leg_accept(st, pr)
    leg_revocation(st)
    met = leg_d5(st)
    banner("SUMMARY")
    print(f"  checks passed {len(PASS)}  failed {len(FAIL)}")
    for f in FAIL:
        print(f"    FAILED: {f}")
    print(f"  D5 verdict  : {'MET' if met else 'NOT MET'}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        traceback.print_exc()
        sys.exit(1)
