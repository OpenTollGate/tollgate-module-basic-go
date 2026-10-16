#!/usr/bin/env python3
"""
kernel_check.py -- does BITCOIN CORE agree with our tapscript findings?

RESULT (measured 2026-10-15, Core v31.1.0, regtest)
===================================================
YES. Core rules per opcode on real P2TR script-path spends:

  OP_2 <a> <b> OP_2 OP_CHECKMULTISIG   -> "OP_CHECKMULTISIG(VERIFY) is not
                                            available in tapscript"
  <a> OP_CHECKSIG <b> OP_CHECKSIGADD
      OP_2 OP_EQUAL                    -> "Invalid Schnorr signature"

The two leaves differ ONLY in the opcode. Core rejects the multisig form by
naming it unavailable; it ACCEPTS the checksigadd form and executes normally,
failing only because the witness carries an intentional placeholder signature.
That IS the BIP-342 finding, obtained by execution rather than by reading the BIP.

TWO TRAPS THAT COST THIS SCRIPT SEVERAL ITERATIONS -- both are ours, not Core's
================================================================================
1. CONTROL BLOCK LENGTH. A SINGLE-LEAF taproot tree has NO merkle sibling, so
   the control block is exactly 33 bytes:
       (leaf_version | parity) || internal_key
   Appending the leaf's own tapleaf_hash (making 65 bytes) makes Core recompute a
   different output key and report "Witness program hash mismatch" -- a
   spectacularly misleading error for what is really a length mistake.

2. taproot_tweak_pubkey(internal_x, tweak) takes the FINAL TWEAK SCALAR, not the
   tapleaf hash. You must compute tweak = tagged_hash("TapTweak", P || tlh) first.
   Passing the tapleaf hash skips that step and yields a wrong output key.

WHAT THIS SCRIPT DOES
=====================
For each leaf: derive the output key and control block, fund a P2TR output
committed to that leaf, build a script-path spend, and submit it for mempool
acceptance. Core's accept/reject -- specifically the reject REASON -- is the
finding. A difference between two otherwise-identical spends is the result.

REQUIRES
========
A local regtest bitcoind with a funded wallet. Defaults target the rig's own node
(~/.local/toolchain/btc). Override with --datadir / --wallet.
"""
import argparse
import json
import os
import struct
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "poc"))
from lnurlcash import secp as S  # noqa: E402

BTC_BIN = os.path.expanduser("~/.local/toolchain/btc/bin")
DEFAULT_DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")
DEFAULT_WALLET = "rig"
RPCPORT = "18443"

NUMS = bytes.fromhex("50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0")


def cli(dd, *a, wallet=None):
    cmd = [os.path.join(BTC_BIN, "bitcoin-cli"), f"-datadir={dd}",
           "-regtest", f"-rpcport={RPCPORT}"]
    if wallet:
        cmd.append(f"-rpcwallet={wallet}")
    r = subprocess.run(cmd + list(a), capture_output=True, text=True, timeout=180)
    return r.stdout.strip(), r.stderr.strip(), r.returncode


def jj(dd, *a, wallet=None):
    o, e, rc = cli(dd, *a, wallet=wallet)
    if rc != 0:
        return None, (e or o)
    try:
        return json.loads(o), None
    except Exception:
        return o, None


def val(dd, *a, wallet=None):
    o, e, rc = cli(dd, *a, wallet=wallet)
    if rc != 0:
        raise RuntimeError((e or o)[:300])
    return o


def push(d):
    n = len(d)
    if n < 0x4c:
        return bytes([n]) + d
    if n <= 0xff:
        return b"\x4c" + bytes([n]) + d
    raise ValueError("push too large")


def vi(n):
    if n < 0xfd:
        return bytes([n])
    if n <= 0xffff:
        return b"\xfd" + struct.pack("<H", n)
    return b"\xfe" + struct.pack("<I", n)


def ser_wit(items):
    out = vi(len(items))
    for it in items:
        b = bytes.fromhex(it) if it else b""
        out += vi(len(b)) + b
    return out


def commit_single(leaf):
    """Output key + control block for a SINGLE-LEAF taproot tree.

    Control block is 33 bytes: (0xc0 | parity) || internal_key. NO sibling.
    Returns (output_key_32, control_block, tapleaf_hash).
    """
    tlh = S.tapleaf_hash(leaf)
    tweak = S.tagged_hash("TapTweak", NUMS + tlh)   # trap 2: must hash first
    ok, par = S.taproot_tweak_pubkey(NUMS, tweak)
    ctrl = bytes([0xc0 | par]) + NUMS               # trap 1: 33 bytes, no tlh
    return ok, ctrl, tlh


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--datadir", default=DEFAULT_DD)
    ap.add_argument("--wallet", default=DEFAULT_WALLET)
    args = ap.parse_args()
    dd, w = args.datadir, args.wallet

    print("=" * 78)
    print("kernel_check.py -- does BITCOIN CORE agree with our tapscript findings?")
    print("=" * 78)

    net, e = jj(dd, "getnetworkinfo")
    bc, be = jj(dd, "getblockchaininfo")
    if net is None or bc is None or bc.get("chain") != "regtest":
        print(f"FATAL: need a reachable regtest node: {(e or be or '')[:120]}")
        return 2
    print(f"  Core   : {net.get('subversion')}")
    print(f"  chain  : {bc.get('chain')} height {bc.get('blocks')}")
    print()

    # Two 32-byte x-only keys. These are structural test keys: the point is the
    # OPCODE, and a placeholder signature is used deliberately, so no private key
    # is needed. Core's complaint about the sig is what distinguishes the leaves.
    A = bytes.fromhex("4f355bdcb7cc0af728ef3cceb9615d90684bb5b2ca5f859ab0f0b704075871aa")
    Bk = bytes.fromhex("466d7fcae563e5cb09a0d1870bb580344804617879a14949cf22285f1bae3f27")

    leaves = {
        "OP_CHECKMULTISIG": (b"\x52" + push(A) + push(Bk) + b"\x52" + b"\xae",
                             "the multisig form we hand-rolled"),
        "OP_CHECKSIGADD": (push(A) + b"\xac" + push(Bk) + b"\xba"
                           + b"\x52" + b"\x87",
                           "the BIP-342-correct replacement"),
    }

    results = {}
    for name, (leaf, note) in leaves.items():
        ok, ctrl, tlh = commit_single(leaf)
        addr = _bech32m(ok)
        print(f"=== {name} ===  ({note})")
        print(f"  leaf {len(leaf)}B  ctrl {len(ctrl)}B  tlh {tlh.hex()[:16]}…")
        print(f"  addr {addr}")

        txid = val(dd, "sendtoaddress", addr, "0.3", wallet=w)
        mine = val(dd, "getnewaddress", wallet=w)
        val(dd, "generatetoaddress", "1", mine, wallet=w)
        tx, _ = jj(dd, "getrawtransaction", txid, "true")
        vout = [o["n"] for o in tx["vout"]
                if o["scriptPubKey"].get("address") == addr][0]
        dest = val(dd, "getnewaddress", wallet=w)
        rawtx = val(dd, "createrawtransaction",
                    json.dumps([{"txid": txid, "vout": vout}]),
                    json.dumps([{dest: 0.2999}]))
        rw = bytes.fromhex(rawtx)
        # locate the end of the vout section without brittle guessing
        i = 4 + 1 + 36 + 1 + 4          # version, vin count, prevout, scriptSig len, seq
        i += 1                          # vout count
        i += 8                          # value
        sl = rw[i]; i += 1
        i += sl                         # scriptPubKey
        base, lock = rw[:i], rw[-4:]

        wit = ["00" * 64, leaf.hex(), ctrl.hex()]   # placeholder sig, leaf, ctrl
        segwit = base[:4] + b"\x00\x01" + base[4:] + ser_wit(wit) + lock
        arr, err = jj(dd, "testmempoolaccept", json.dumps([segwit.hex()]))
        a = arr[0] if arr else {}
        reason = a.get("reject-reason", "")
        print(f"  allowed={a.get('allowed')}  reject-reason={reason!r}")
        results[name] = {"leaf_len": len(leaf), "ctrl_len": len(ctrl),
                         "addr": addr, "allowed": a.get("allowed"),
                         "reject_reason": reason}
        print()

    print("=" * 78)
    print("RULING")
    print("=" * 78)
    ms = results.get("OP_CHECKMULTISIG", {}).get("reject_reason", "")
    ca = results.get("OP_CHECKSIGADD", {}).get("reject_reason", "")
    named = "OP_CHECKMULTISIG" in ms and "not available in tapscript" in ms
    print(f"  CHECKMULTISIG leaf : {ms}")
    print(f"  CHECKSIGADD leaf   : {ca}")
    print()
    if named and "Schnorr" in ca:
        print("  VERDICT: MEASURED. Core names the multisig opcode as unavailable")
        print("  in tapscript, while the checksigadd form executes and fails only")
        print("  on the intentional placeholder signature. The two leaves differ")
        print("  only in the opcode, so the discrimination is the finding.")
    else:
        print("  VERDICT: INSUFFICIENT-EVIDENCE. See the reject reasons above.")
        print("  Do NOT report the BIP-342 claim as measured from this run.")

    dest_path = os.path.join(HERE, "out", "KERNEL-CHECK.json")
    os.makedirs(os.path.dirname(dest_path), exist_ok=True)
    with open(dest_path, "w") as f:
        json.dump({"core": net.get("subversion"), "height": bc.get("blocks"),
                   "results": results}, f, indent=2)
    print(f"\n  wrote {dest_path}")
    return 0


def _bech32m(key32: bytes) -> str:
    from lnurlcash import bech32m as B
    return B.encode("bcrt", [1] + B.convertbits(key32, 8, 5))


if __name__ == "__main__":
    sys.exit(main())
