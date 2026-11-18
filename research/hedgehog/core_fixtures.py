#!/usr/bin/env python3
"""Generate Core-derived fixtures for the Go hedgehog tests.

Asks Bitcoin Core's descriptor compiler for the output key of each leaf shape the
Go package builds, so the Go tests validate against consensus rather than against
their own arithmetic.

Core's descriptor syntax constrains what can be expressed, which is itself
informative: `tr(NUMS,pk(K))` for a single leaf; `multi_a()` is accepted in
tapscript while `multi()` is refused (the BIP-342 restriction, confirmed
independently of any execution).
"""
import json
import os
import subprocess
import sys

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
DD = os.path.expanduser("~/.local/toolchain/btc/regtest-data")
NUMS = "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"
KEY_A = "4f355bdcb7cc0af728ef3cceb9615d90684bb5b2ca5f859ab0f0b704075871aa"
KEY_B = "466d7fcae563e5cb09a0d1870bb580344804617879a14949cf22285f1bae3f27"
KEY_C = "2c0b7cf95324a07d05398b240174dc0c2be444d96b159aa6c7f7b1e668680991"


def cli(*a):
    r = subprocess.run([f"{BTC}/bitcoin-cli", f"-datadir={DD}", "-regtest",
                        "-rpcport=18443"] + [str(x) for x in a],
                       capture_output=True, text=True, timeout=120)
    if r.returncode:
        raise RuntimeError((r.stderr or r.stdout).strip()[:300])
    return json.loads(r.stdout)


def descriptor_output_key(desc):
    """Compile a descriptor and return the P2TR scriptPubKey it produces.

    The descriptor checksum must be present, so we ask Core to add it.
    """
    info = cli("getdescriptorinfo", desc)
    canon = info["descriptor"]
    # An address is the most direct way to observe the output key.
    # Un-ranged descriptors reject a range argument (-8), so derive without one.
    addr = cli("deriveaddresses", canon)[0]
    ai = cli("getaddressinfo", addr)
    return canon, addr, ai["scriptPubKey"]


print("=" * 74)
print("Core-derived fixtures for the Go hedgehog package")
print("=" * 74)
print()

fixtures = {}

# 1. single leaf: a plain pk() leaf
canon, addr, spk = descriptor_output_key(f"tr({NUMS},pk({KEY_A}))")
fixtures["single_pk_leaf"] = {
    "tree": "pk(A)", "descriptor": canon, "address": addr, "spk": spk,
}
print(f"1. tr(NUMS,pk(A))")
print(f"   {canon}")
print(f"   address: {addr}")
print()

# 2. single leaf expressed the way our multisig leaf really is: a multi_a
#    threshold. multi_a(2,A,B) == "<A> CHECKSIG <B> CHECKSIGADD 2 EQUAL".
canon, addr, spk = descriptor_output_key(f"tr({NUMS},multi_a(2,{KEY_A},{KEY_B}))")
fixtures["multi_a_2_of_2"] = {
    "tree": "multi_a(2,A,B)", "descriptor": canon, "address": addr, "spk": spk,
    "note": "multi_a IS tapscript-legal; multi() is refused by Core",
}
print(f"2. tr(NUMS,multi_a(2,A,B))")
print(f"   {canon}")
print(f"   address: {addr}")
print()

# 3. nested 3-leaf tree, matching our nested API
canon, addr, spk = descriptor_output_key(
    f"tr({NUMS},{{pk({KEY_A}),{{pk({KEY_B}),pk({KEY_C})}}}})")
fixtures["nested_3_leaf"] = {
    "tree": "{a,{b,c}}", "descriptor": canon, "address": addr, "spk": spk,
}
print(f"3. tr(NUMS,{{pk(A),{{pk(B),pk(C)}}}})")
print(f"   {canon}")
print(f"   address: {addr}")
print()

# 4. the naive flat/balanced form, to demonstrate the tree SHAPE matters
canon, addr, spk = descriptor_output_key(
    f"tr({NUMS},{{{{pk({KEY_A}),pk({KEY_B})}},pk({KEY_C})}})")
fixtures["flat_3_leaf"] = {
    "tree": "{{a,b},c}", "descriptor": canon, "address": addr, "spk": spk,
}
print(f"4. tr(NUMS,{{{{pk(A),pk(B)}},pk(C)}})")
print(f"   {canon}")
print(f"   address: {addr}")
print()

# 5. prove multi() is refused in tapscript (the BIP-342 restriction, from the
#    compiler's side rather than from execution)
print("5. multi() in tapscript -- expected to be REFUSED:")
try:
    descriptor_output_key(f"tr({NUMS},multi(2,{KEY_A},{KEY_B}))")
    print("   UNEXPECTED: Core accepted multi() in tapscript")
    fixtures["multi_refused"] = {"refused": False}
except RuntimeError as e:
    print(f"   refused: {e}")
    fixtures["multi_refused"] = {"refused": True, "reason": str(e)}
print()

# 6. the nested vs flat roots must DIFFER
print("6. tree shape check:")
same = fixtures["nested_3_leaf"]["spk"] == fixtures["flat_3_leaf"]["spk"]
print(f"   nested spk: {fixtures['nested_3_leaf']['spk']}")
print(f"   flat spk:   {fixtures['flat_3_leaf']['spk']}")
print(f"   identical? {same}  (must be False -- shape matters)")
print()

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "core_fixtures.json")
with open(out, "w") as f:
    json.dump(fixtures, f, indent=2)
print(f"wrote {out}")
