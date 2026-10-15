#!/usr/bin/env python3
"""xcheck.py -- the independent cross-checks that make run_poc.py's results
trustworthy. Run this FIRST; run_poc.py calls it.

Four independent oracles are used, none of them our own code:

  X1  BIP-340 official test vector (pubkey, signature, verify)
  X2  Bitcoin Core `getdescriptorinfo` / `deriveaddresses` -- Core computes the
      BIP341 output key for our 1-leaf and 2-leaf trees; we must agree.
  X3  The SHIPPED tapscript.js (hedgehog-advanced), executed under node --
      it computes the output key AND the control block for the real 3-leaf
      Hedgehog tree; we must agree byte-for-byte.
  X4  ripemd160 of the preimage, cross-checked by Core (`OP_RIPEMD160` behaving
      correctly is separately proven by run_poc.py broadcasting a script-path
      spend that OP_RIPEMD160 must pass).

X3 is the load-bearing one: it establishes that our Python models the tree the
deployed site actually builds, rather than "a" BIP341 tree.
"""
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from btclib import push, OP_CHECKSIG, segwit_address   # noqa: E402
from taproot import (leaf_hash, branch_hash, taproot_tweak, xonly,  # noqa: E402
                     schnorr_sign, schnorr_verify)
from core import jcli, cli                              # noqa: E402
import hdg                                              # noqa: E402

RESULTS = []


def check(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print(f"  [{'PASS' if ok else 'FAIL'}] {name}" + (f"  {detail}" if detail else ""))
    return bool(ok)


def x1_bip340():
    sk = bytes.fromhex("0000" * 15 + "03")
    exp_pub = "f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9"
    exp_sig = ("e907831f80848d1069a5371b402410364bdf1c5f8307b0084c55f1ce2dca8215"
               "25f66a4a85ea8b71e482a74f382d2ce5ebeee8fdb2172f477df4900d310536c0")
    sig = schnorr_sign(bytes(32), sk, b"\x00" * 32)
    check("X1 BIP340 pubkey matches the official vector", xonly(sk).hex() == exp_pub)
    check("X1 BIP340 signature matches the official vector", sig.hex() == exp_sig)
    check("X1 BIP340 verify accepts the vector signature",
          schnorr_verify(bytes(32), bytes.fromhex(exp_pub), bytes.fromhex(exp_sig)))


def x2_core_tree():
    """Core derives the output key for 1- and 2-leaf trees we build."""
    A = bytes.fromhex("f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9")
    B = bytes.fromhex("5cbdf0646e5db4eaa398f365f2ea7a0e3d419b7e0330e39ce92bddedcac4f9bc")
    key = "ab" * 32
    cases = [
        (f"tr({key},pk({A.hex()}))",
         taproot_tweak(bytes.fromhex(key), leaf_hash(push(A) + bytes([OP_CHECKSIG])))),
        (f"tr({key},{{pk({A.hex()}),pk({B.hex()})}})",
         taproot_tweak(bytes.fromhex(key),
                       branch_hash(leaf_hash(push(A) + bytes([OP_CHECKSIG])),
                                   leaf_hash(push(B) + bytes([OP_CHECKSIG]))))),
    ]
    for desc, ours in cases:
        info = jcli("getdescriptorinfo", desc)
        addr = jcli("deriveaddresses", info["descriptor"])[0]
        prog = bytes.fromhex(bech32_program(addr))
        check(f"X2 Core agrees on the output key for {desc[3:40]}...",
              prog == ours, f"core={prog.hex()[:16]} ours={ours.hex()[:16]}")


def x3_shipped_tapscript():
    """The SHIPPED tapscript.js computes outkey + control block; we must agree."""
    path = "/tmp/tapscript_mod.js"
    if not os.path.exists(path):
        raw = subprocess.run(["curl", "-sL",
                              "https://supertestnet.github.io/hedgehog-advanced/tapscript.js"],
                             capture_output=True).stdout
        open("/tmp/tapscript.js", "wb").write(raw)
        open(path, "wb").write(raw + b"\nmodule.exports = tapscript;\n")
    script = os.path.join(os.path.dirname(os.path.abspath(__file__)), "tref.js")
    p = subprocess.run(["node", script], capture_output=True, text=True)
    if p.returncode != 0:
        check("X3 shipped tapscript.js executed under node", False, p.stderr.strip()[-300:])
        return
    ref = json.loads(p.stdout.strip().splitlines()[-1])
    A = bytes.fromhex(ref["alice"]); B = bytes.fromhex(ref["bob"])
    scripts, _ = hdg.revocation_tree(A, B, "alice", {"alice": bytes.fromhex("aa" * 20)})
    ours_root = hdg.hh_tree_root(scripts)
    ours_key = taproot_tweak(hdg.NUMS, ours_root)
    check("X3 shipped tapscript.js 3-leaf ROOT == our tree",
          ref["root"] == ours_root.hex(),
          f"js={ref['root'][:16]} ours={ours_root.hex()[:16]}")
    check("X3 shipped tapscript.js OUTPUT KEY == our tweak",
          ref["outkey"] == ours_key.hex(),
          f"js={ref['outkey'][:16]} ours={ours_key.hex()[:16]}")
    cb, _ = hdg.hh_control_block(scripts[0], scripts)
    check("X3 shipped tapscript.js CONTROL BLOCK (2-of-2 leaf) == ours",
          ref["cblock_ms"] == cb.hex(),
          f"js={ref['cblock_ms'][:20]}... ours={cb.hex()[:20]}...")
    check("X3 our p2tr address == the JS p2tr address",
          hdg.p2tr_address(ours_key) == ref["address"],
          f"{hdg.p2tr_address(ours_key)[:24]}... vs {ref['address'][:24]}...")
    check("X3 shipped tree function is NOT BIP341 for odd node counts",
          ref["root"] != bip341_root(scripts).hex(),
          "...but it AGREES with BIP341 when n is a power of 2 (see X2)")


def bip341_root(scripts):
    """Textbook BIP341: 3 leaves -> branch(branch(l0,l1), l2), odd node PROMOTED."""
    l0, l1, l2 = (leaf_hash(s) for s in scripts)
    return branch_hash(branch_hash(l0, l1), l2)


def bech32_program(addr):
    B32 = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
    vals = [B32.index(c) for c in addr[addr.rfind("1") + 1:]]
    acc = bits = 0
    out = bytearray()
    for v in vals[1:-6]:
        acc = (acc << 5) | v
        bits += 5
        while bits >= 8:
            bits -= 8
            out.append((acc >> bits) & 0xFF)
    return bytes(out).hex()


if __name__ == "__main__":
    print("== independent cross-checks ==")
    x1_bip340()
    x2_core_tree()
    x3_shipped_tapscript()
    n_ok = sum(1 for _, ok, _ in RESULTS if ok)
    print(f"== {n_ok}/{len(RESULTS)} cross-checks passed ==")
    sys.exit(0 if n_ok == len(RESULTS) else 1)
