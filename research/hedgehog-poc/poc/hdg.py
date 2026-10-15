#!/usr/bin/env python3
"""
hdg.py -- Hedgehog protocol primitives, reimplemented in dependency-free Python
from the shipped reference implementation (supertestnet/hedgehog HEAD ea6686d,
index.html). Every constant below is cited to a line of index.html.

This module contains NO tests and NO RPC. It is the protocol; run_poc.py is the
harness. The split exists so a reader can check the protocol model against the
58KB HTML file without reading the harness.

Canonical deployment (hdg.CONFIG) mirrors the LIVE public instance, which
loads tapscript.js / noble-secp256k1.js / rmd160.js from
https://supertestnet.github.io/hedgehog-advanced/ (index.html:14-16), not the
older github.io/hedgehog/ bundle the comments at index.html:7-9 name.
"""

import hashlib
from btclib import push, pushnum, OP_CHECKSIG, OP_DROP
from taproot import (
    leaf_hash, _tagged_hash, xonly, schnorr_sign, schnorr_verify,
    taproot_sighash, taproot_tweak, tweak_parity, varint_small,
    OP_CHECKSIGADD, OP_RIPEMD160, OP_EQUAL, OP_EQUALVERIFY, OP_CSV,
)

# --------------------------------------------------------------------------
# constants lifted from index.html
# --------------------------------------------------------------------------
NUMS = bytes.fromhex("ab" * 32)   # index.html:84  internal key (BIP341 NUMS point)
INITIAL_AMOUNT = 100_000          # index.html:45  README: "10_000 sats" -- see NOTES
FEE_PER_TX = 500                  # index.html:288,298,489,657  "original_amnt - 500"
FEE_X2 = 1000                     # index.html:322,326,504  "- 500 - 500"

# index.html:92,98 timeout tapleaf: `<10> OP_CSV OP_DROP <pk> OP_CHECKSIG`
# index.html:91,97 carry: "TODO: change the 10 to 4032"
CSV_TIMEOUT_LEAF = 10             # README says 4032  <-- DELIBERATE MISMATCH
# index.html:315,496  tx1 input nSequence: `getVin(tx0_id, 0, ..., revocable_address, 5)`
# index.html:314,495 carry: "TODO: change the sequence number ... from 5 to 2016"
CSV_TX1_SEQUENCE = 5              # README says 2016  <-- DELIBERATE MISMATCH
# index.html:297  timeout_tx input nSequence = 10 (matches the timeout tapleaf)
CSV_TIMEOUT_TX_SEQUENCE = 10
# index.html:296 carry: "TODO: change the sequence number ... from 10 to 4032"
CSV_TIMEOUT_TX_TODO = 4032

# index.html:66-77  getVin(); index.html:253,254 Tx defaults `sequence:4294967293`
SEQUENCE_DEFAULT = 0xFFFFFFFD
SEQUENCE_FINAL = 0xFFFFFFFF


# --------------------------------------------------------------------------
# scripts
# --------------------------------------------------------------------------
def multisig_script(alice_x: bytes, bob_x: bytes) -> bytes:
    """index.html:89,95,150,182,198

        <alice> OP_CHECKSIG <bob> OP_CHECKSIGADD 2 OP_EQUAL

    BIP-342 CHECKSIGADD, NOT OP_CHECKMULTISIG -- so the BIP-342
    NULLFAIL/CHECKMULTISIG problem does not arise. Alice's key is ALWAYS first.
    """
    return (push(alice_x) + bytes([OP_CHECKSIG])
            + push(bob_x) + bytes([OP_CHECKSIGADD])
            + pushnum(2) + bytes([OP_EQUAL]))


def timeout_leaf_script(csv_blocks: int, key_x: bytes) -> bytes:
    """index.html:92,98,302  `<n> OP_CHECKSEQUENCEVERIFY OP_DROP <pk> OP_CHECKSIG`"""
    return pushnum(csv_blocks) + bytes([OP_CSV, OP_DROP]) + push(key_x) + bytes([OP_CHECKSIG])


def revocation_leaf_script(preimage_hash: bytes, key_x: bytes) -> bytes:
    """index.html:90,96  `OP_RIPEMD160 <hash> OP_EQUALVERIFY <pk> OP_CHECKSIG`

    preimage_hash is RIPEMD160 of a 16-byte secret (index.html:397-398: the
    secret is `.substring(0,32)` of 32 random bytes = 16 bytes).
    """
    assert len(preimage_hash) == 20, "RIPEMD160 output is 20 bytes"
    return (bytes([OP_RIPEMD160]) + push(preimage_hash) + bytes([OP_EQUALVERIFY])
            + push(key_x) + bytes([OP_CHECKSIG]))


def rmd160(b: bytes) -> bytes:
    """index.html:46-51 -- RIPEMD160 only, no SHA256 wrapper. NOT hash160."""
    return hashlib.new("ripemd160", b, usedforsecurity=False).digest()


def revocation_tree(alice_x: bytes, bob_x: bytes,
                    recipient: str, rev_hashes: dict,
                    csv_blocks: int = CSV_TIMEOUT_LEAF):
    """Rebuilds makeAlicesRevocationScript (index.html:88-93) and
    makeBobsRevocationScript (index.html:94-99).

    `recipient` names the party the address is built FOR (the party the PoC
    calls `{name}_can_revoke`, index.html:272,276,472,476). Note the PoC's
    naming is inverted relative to its scripts: in `{recipient}_can_revoke`
    BOTH the revocation leaf and the timeout leaf are keyed to the OTHER party
    -- the one who PAID in that state. The address is thus named for the
    recipient but is unilaterally spendable only by the sender.

    Returns (leaves, reclaim_key_x). `leaves` is ordered exactly as index.html
    emits it: [ 2-of-2, revocation, timeout ].

    * recipient="alice" -> leaf1 key = bob,   uses alice's revocation hash
    * recipient="bob"   -> leaf1 key = alice, uses bob's   revocation hash
    """
    ms = multisig_script(alice_x, bob_x)
    if recipient == "alice":
        rv = revocation_leaf_script(rev_hashes["alice"], bob_x)
        to = timeout_leaf_script(csv_blocks, bob_x)
        reclaim = bob_x
    elif recipient == "bob":
        rv = revocation_leaf_script(rev_hashes["bob"], alice_x)
        to = timeout_leaf_script(csv_blocks, alice_x)
        reclaim = alice_x
    else:
        raise ValueError("recipient must be alice|bob")
    return [ms, rv, to], reclaim


def _branch(a: bytes, b: bytes) -> bytes:
    if b < a:
        a, b = b, a
    return _tagged_hash(b"TapBranch", a + b)


def hh_tree_root(scripts: list) -> bytes:
    """The ACTUAL tree function in the shipped tapscript.js (hedgehog-advanced).

    Takes RAW SCRIPTS and returns the BIP341 merkle root, reproducing
    `Tap.getPubKey("ab"*32, { tree: scripts.map(Tap.encodeScript) })`.

    Verified byte-for-byte against the shipped library (`node tref.js`) -- see
    xcheck.py X3. Two details make this non-obvious:

      * `Tap.encodeScript(s)` returns `sha256(TapLeaf_tag||TapLeaf_tag||0xc0||s).hex()`
        -- i.e. it is the BIP341 LEAF HASH, not the script. index.html:83 feeds
        those hex strings to `getPubKey`, which uses them as leaves WITHOUT
        re-hashing. So the tree is over correct BIP341 leaf hashes.
      * The odd-node rule is `if (n % 2) push(last)`: the LAST element is paired
        with ITSELF. BIP341 promotes the odd node and hashes it with nothing.
        For 1 and 2 leaves the two rules agree; every real Hedgehog revocation
        tree has 3 leaves, where they diverge.
      * Each level is `sorted()` before pairing (tags in index.html order).
    """
    n = sorted(leaf_hash(s) for s in scripts)
    while len(n) > 1:
        if len(n) % 2:
            n = n + [n[-1]]
        n = [_branch(n[i], n[i + 1]) for i in range(0, len(n) - 1, 2)]
    return n[0]


def hh_tree(leaves: list):
    return hh_tree_root(leaves), None


def hh_control_block(target_script: bytes, scripts: list, internal_x: bytes = NUMS):
    """Replicates `Tap.getPubKey(key, {tree, target: Tap.encodeScript(target_script)})`.

    Same odd-node rule; path emitted bottom-up, each level sorted.
    """
    path = []
    level = sorted(leaf_hash(s) for s in scripts)
    e = leaf_hash(target_script)
    while len(level) > 1:
        if len(level) % 2:
            level = level + [level[-1]]
        nxt = []
        for i in range(0, len(level) - 1, 2):
            a, b = level[i], level[i + 1]
            h = _branch(a, b)
            nxt.append(h)
            if e == a:
                path.append(b)
                e = h
            elif e == b:
                path.append(a)
                e = h
        level = nxt
    root = level[0]
    parity = tweak_parity(internal_x, root)
    return bytes([0xC0 | parity]) + internal_x + b"".join(path), root


def taproot_output_key(scripts: list, internal_x: bytes = NUMS) -> bytes:
    return taproot_tweak(internal_x, hh_tree_root(scripts))


def p2tr_spk(output_key_x: bytes) -> bytes:
    return b"\x51\x20" + output_key_x


def p2tr_address(output_key_x: bytes, hrp="bcrt") -> str:
    from btclib import segwit_address
    return segwit_address(1, output_key_x, hrp)


# --------------------------------------------------------------------------
# sighash wrappers (BIP341, SIGHASH_DEFAULT, script path)
# --------------------------------------------------------------------------
def leaf_sighash(tx, idx, prevouts, script, internal_x=NUMS):
    """The tapleaf hash must be taken over the taptree the OUTPUT was committed
    to, so the caller passes the tree-derived root; we recompute per leaf."""
    return taproot_sighash(tx, idx, prevouts, ext_flag=1,
                           leaf_hash_=leaf_hash(script))


def sign_leaf(tx, idx, prevouts, seckey, script):
    return schnorr_sign(leaf_sighash(tx, idx, prevouts, script), seckey)


def verify_leaf(tx, idx, prevouts, pubkey_x, sig, script) -> bool:
    return schnorr_verify(leaf_sighash(tx, idx, prevouts, script), pubkey_x, sig)
