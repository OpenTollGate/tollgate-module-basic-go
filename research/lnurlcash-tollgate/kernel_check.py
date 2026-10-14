#!/usr/bin/env python3
"""
kernel_check.py -- does BITCOIN CORE agree with our tapscript findings?

THE ONE QUESTION
================
The LNURLcash POC (poc/) executed 141 assertions against the LUD-25/26 official
vectors and built a BIP-342 tapscript interpreter showing the OP_CHECKMULTISIG
leaf form is invalid tapscript while the OP_CHECKSIGADD form is valid. Its README
records the honest limit:

    "NOT proven here: that Bitcoin Core's interpreter agrees. No bitcoind and no
     libbitcoinkernel on this host. 'I read the BIP' is a weaker claim than
     'I ran the kernel', and the difference is recorded."

THE Point: ask CORE, not our code.

HOW, AND WHY THIS SHAPE
=======================
Core will not tell us "this script is valid tapscript" via any introspection RPC.
The only way to have Core rule on tapscript SEMANTICS is to make it EXECUTE the
script during script verification. So the test is a SCRIPT-PATH spend, submitted
for mempool acceptance.

Rather than hand-roll the sighash (error-prone, and the thing under test is not
our sighash code), we let CORE build and sign a real spend of a real P2TR output,
then REWRITE the witness to place our script+control block in the script path.
That isolates the variable: the transaction and signature are Core's own, and the
only thing we change is which tapscript Core is asked to execute.

We then compare Core's ruling on the two leaves. If BIP-342 disables
OP_CHECKMULTISIG in tapscript, the CHECKMULTISIG leg must be rejected with an
opcode/disabled error and the CHECKSIGADD leg must NOT be rejected for that
reason. Core's own error string is the evidence, and it is quoted verbatim.

A DIFFERENCE in verdict between two otherwise-identical spends is the finding.
"""
import argparse
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "poc"))
from lnurlcash import secp as S  # noqa: E402

BTC_BIN = os.path.expanduser("~/.local/toolchain/btc/bin")
DEFAULT_DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")
DEFAULT_WALLET = "rig"
RPCPORT = "18443"


def cli(dd, *args, wallet=None):
    cmd = [os.path.join(BTC_BIN, "bitcoin-cli"), f"-datadir={dd}",
           "-regtest", f"-rpcport={RPCPORT}"]
    if wallet:
        cmd += [f"-rpcwallet={wallet}"]
    cmd += list(args)
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    return r.returncode, r.stdout.strip(), r.stderr.strip()


def jcli(dd, *args, wallet=None):
    rc, out, err = cli(dd, *args, wallet=wallet)
    if rc != 0:
        return None, (err or out)
    try:
        return json.loads(out), None
    except Exception:
        return out, None


def push(d):
    n = len(d)
    if n < 0x4c:
        return bytes([n]) + d
    if n <= 0xff:
        return b"\x4c" + bytes([n]) + d
    raise ValueError("push too large")


OP_IF, OP_ELSE, OP_ENDIF = b"\x63", b"\x67", b"\x68"
OP_CSV, OP_DROP, OP_2, OP_EQUAL = b"\xb2", b"\x75", b"\x52", b"\x87"
OP_CHECKSIG, OP_CHECKSIGADD, OP_CHECKMULTISIG = b"\xac", b"\xba", b"\xae"

PK_A = bytes.fromhex("f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9")
PK_B = bytes.fromhex("c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5")
NUMS = bytes.fromhex("50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0")


def leaf_multisig(expiry, a, b):
    return (OP_IF + push(bytes([expiry])) + OP_CSV + OP_DROP + push(a) + OP_CHECKSIG
            + OP_ELSE + OP_2 + push(a) + push(b) + OP_2 + OP_CHECKMULTISIG + OP_ENDIF)


def leaf_checksigadd(expiry, a, b):
    return (OP_IF + push(bytes([expiry])) + OP_CSV + OP_DROP + push(a) + OP_CHECKSIG
            + OP_ELSE + push(a) + OP_CHECKSIG + push(b) + OP_CHECKSIGADD
            + OP_2 + OP_EQUAL + OP_ENDIF)


def commit(leaf):
    tlh = S.tapleaf_hash(leaf)
    out_key, parity = S.taproot_tweak_pubkey(NUMS, tlh)
    ctrl = bytes([0xc0 | parity]) + NUMS + tlh
    return out_key, ctrl


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--datadir", default=DEFAULT_DD)
    ap.add_argument("--wallet", default=DEFAULT_WALLET)
    args = ap.parse_args()
    dd, w = args.datadir, args.wallet

    print("=" * 76)
    print("kernel_check.py -- does BITCOIN CORE agree with our tapscript findings?")
    print("=" * 76)

    net, e = jcli(dd, "getnetworkinfo")
    if net is None:
        print(f"FATAL: no regtest node: {e}")
        return 2
    bc, e = jcli(dd, "getblockchaininfo")
    if bc is None or bc.get("chain") != "regtest":
        print(f"FATAL: need regtest, got {bc and bc.get('chain')}: {e}")
        return 2
    print(f"  Core   : {net.get('subversion')}")
    print(f"  chain  : regtest height {bc.get('blocks')}")
    print()

    results = {"core": net.get("subversion"), "height": bc.get("blocks")}

    # ---- LEG 1: structural -------------------------------------------------
    print("LEG 1 -- Core decodescript (structural only)")
    print("-" * 76)
    for name, leaf in (("OP_CHECKMULTISIG", leaf_multisig(6, PK_A, PK_B)),
                       ("OP_CHECKSIGADD", leaf_checksigadd(6, PK_A, PK_B))):
        out, _ = jcli(dd, "decodescript", leaf.hex())
        t = out.get("type") if isinstance(out, dict) else "?"
        print(f"  {name:18} {len(leaf):3d} B   type={t!r}")
        results.setdefault("leg1", {})[name] = {"len": len(leaf), "type": t}
    print("  -> proves our BYTES parse. Does NOT prove the opcodes are allowed.")
    print()

    # ---- LEG 2: the ruling -------------------------------------------------
    print("LEG 2 -- Core script verification of a P2TR SCRIPT-PATH spend")
    print("-" * 76)
    print("  Build a real P2TR output whose ONLY leaf is the script under test.")
    print("  Then spend it via the script path and submit for mempool acceptance.")
    print("  Core's verdict on each leaf IS the finding.")
    print()

    for name, leaf in (("OP_CHECKMULTISIG", leaf_multisig(6, PK_A, PK_B)),
                       ("OP_CHECKSIGADD", leaf_checksigadd(6, PK_A, PK_B))):
        out_key, ctrl = commit(leaf)
        addr = None
        # derive the bech32m address Core will fund
        try:
            from lnurlcash import bech32m as B
            words = [1] + B.convertbits(out_key, 8, 5)  # witness v1
            addr = B.encode("bcrt", words)
        except Exception:
            pass
        if not addr:
            print(f"  {name}: cannot derive address; SKIP (report as insufficient)")
            results.setdefault("leg2", {})[name] = {"status": "no-address"}
            continue

        # fund it from the wallet
        txid, e = jcli(dd, "sendtoaddress", addr, "1.0", wallet=w)
        if txid is None:
            print(f"  {name}: sendtoaddress failed: {e[:70]}")
            results.setdefault("leg2", {})[name] = {"status": "fund-failed"}
            continue
        mine, _ = jcli(dd, "getnewaddress", wallet=w)
        jcli(dd, "generatetoaddress", "1", mine, wallet=w)

        # a structurally-valid 64-byte schnorr sig placeholder over nothing real:
        # we are testing the SCRIPT's opcode legality, and Core reports
        # signature failures distinctly from disabled-opcode failures.
        sig = "00" * 64
        # script-path witness: [ ...stack items..., script, control ]
        # For OP_IF to take the ELSE branch we need a FALSE top-of-stack item.
        # Push an empty item to select the ELSE (payments) branch.
        witness = ["", sig, sig, leaf.hex(), ctrl.hex()]

        print(f"  {name}: script={len(leaf)}B ctrl={len(ctrl)}B addr={addr[:26]}…")
        results.setdefault("leg2", {})[name] = {
            "script_len": len(leaf), "ctrl_len": len(ctrl), "addr": addr,
            "output_key": out_key.hex()}

    print()
    print("=" * 76)
    print("STATUS")
    print("=" * 76)
    print("  LEG 1 measured. LEG 2 scaffolding built (real P2TR outputs, real")
    print("  addresses, real funding, real control blocks).")
    print()
    print("  HONEST BOUNDARY: submitting the script-path spend needs a witness")
    print("  whose stack actually satisfies whichever branch executes, built over")
    print("  the real sighash of Core's transaction. That is the remaining step,")
    print("  and until it runs, the BIP-342 claim stays REASONED (resting on the")
    print("  BIP text), not MEASURED. out/KERNEL-CHECK.json records exactly which")
    print("  legs ran so this boundary cannot be misread later.")

    dest = os.path.join(HERE, "out", "KERNEL-CHECK.json")
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    json.dump(results, open(dest, "w"), indent=2)
    print(f"\n  wrote {dest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
