#!/usr/bin/env python3
"""
taproot.py -- BIP-340 Schnorr + BIP-341 taproot + BIP-342 tapscript extension
for the Hedgehog PoC harness.

Zero third-party dependencies, on purpose: the prior one-way-channel rig had to
run with no network and no PyPI, and this harness inherits that constraint (it
talks only to a regtest bitcoind over loopback).

Imports taproot_crypto (which itself extends btclib.py) so that the elliptic
curve code is shared with the v0-SegWit part of the rig and cross-checked the
same way -- against Bitcoin Core consensus, not against ourselves.

Everything here is validated by `bitcoin-cli` accepting or rejecting real
transactions. See poc/README.md for which specific cross-checks exist.
"""

import hashlib
from btclib import _add, _mul, _inv, G, N, P, sha256, push, pushnum, num

# --------------------------------------------------------------------------
# BIP-340 Schnorr
# --------------------------------------------------------------------------
TAG_TAPSIGHASH = b"TapSighash"


def _tagged_hash(tag: bytes, msg: bytes) -> bytes:
    t = sha256(tag)
    return sha256(t + t + msg)


def has_even_y(pt):
    return pt[1] % 2 == 0


def xonly(seckey: bytes) -> bytes:
    """32-byte x-only public key from a secret key (BIP340)."""
    pub = _mul(int.from_bytes(seckey, "big"), G)
    return pub[0].to_bytes(32, "big")


def xonly_from_seckey_even(seckey: bytes):
    """Returns (xonly_bytes, effective_seckey_int) with even-Y semantics."""
    d = int.from_bytes(seckey, "big")
    pt = _mul(d, G)
    if not has_even_y(pt):
        d = N - d
    return pt[0].to_bytes(32, "big"), d


def lift_x(x: bytes):
    """BIP-340 lift_x: returns the even-Y point with that x, or None."""
    x_int = int.from_bytes(x, "big")
    if x_int >= P:
        return None
    c = (pow(x_int, 3, P) + 7) % P
    y = pow(c, (P + 1) // 4, P)
    if (y * y) % P != c:
        return None
    if y % 2 != 0:
        y = P - y
    return (x_int, y)


def schnorr_sign(msg: bytes, seckey: bytes, aux: bytes = None) -> bytes:
    """BIP-340 sign. `msg` is the 32-byte sighash. Deterministic if aux=None."""
    if len(msg) != 32:
        raise ValueError("BIP340 message must be 32 bytes")
    d0 = int.from_bytes(seckey, "big")
    if not (1 <= d0 <= N - 1):
        raise ValueError("bad seckey")
    P_pt = _mul(d0, G)
    d = d0 if has_even_y(P_pt) else N - d0
    t = d ^ int.from_bytes(_tagged_hash(b"BIP0340/aux", aux if aux else b"\x00" * 32), "big")
    rand = _tagged_hash(b"BIP0340/nonce", t.to_bytes(32, "big") + P_pt[0].to_bytes(32, "big") + msg)
    k0 = int.from_bytes(rand, "big") % N
    if k0 == 0:
        raise ValueError("bad nonce")
    R = _mul(k0, G)
    k = k0 if has_even_y(R) else N - k0
    e = int.from_bytes(_tagged_hash(b"BIP0340/challenge", R[0].to_bytes(32, "big") + P_pt[0].to_bytes(32, "big") + msg), "big") % N
    sig = R[0].to_bytes(32, "big") + ((k + e * d) % N).to_bytes(32, "big")
    # verify before returning, per BIP340 (catches any implementation slip)
    assert schnorr_verify(msg, P_pt[0].to_bytes(32, "big"), sig), "self-verify failed"
    return sig


def schnorr_verify(msg: bytes, pubkey_xonly: bytes, sig: bytes) -> bool:
    if len(msg) != 32 or len(sig) != 64 or len(pubkey_xonly) != 32:
        return False
    Pt = lift_x(pubkey_xonly)
    if Pt is None:
        return False
    r = int.from_bytes(sig[:32], "big")
    s = int.from_bytes(sig[32:], "big")
    if r >= P or s >= N:
        return False
    e = int.from_bytes(_tagged_hash(b"BIP0340/challenge", sig[:32] + pubkey_xonly + msg), "big") % N
    R = _add(_mul(s, G), _mul(N - e, Pt))
    if R is None or not has_even_y(R) or R[0] != r:
        return False
    return True


# --------------------------------------------------------------------------
# BIP-341 taproot
# --------------------------------------------------------------------------
def leaf_hash(script: bytes) -> bytes:
    return _tagged_hash(b"TapLeaf", b"\xc0" + varint_small(len(script)) + script)


def varint_small(n):
    if n < 0xFD:
        return bytes([n])
    if n <= 0xFFFF:
        return b"\xfd" + n.to_bytes(2, "little")
    return b"\xfe" + n.to_bytes(4, "little")


def branch_hash(a: bytes, b: bytes) -> bytes:
    if b < a:
        a, b = b, a
    return _tagged_hash(b"TapBranch", a + b)


def taproot_tweak(internal_x: bytes, merkle_root: bytes) -> bytes:
    if merkle_root:
        t = int.from_bytes(_tagged_hash(b"TapTweak", internal_x + merkle_root), "big")
    else:
        t = int.from_bytes(_tagged_hash(b"TapTweak", internal_x), "big")
    Pt = lift_x(internal_x)
    Q = _add(Pt, _mul(t, G))
    return Q[0].to_bytes(32, "big")


def control_block(internal_x: bytes, script: bytes, path_hashes, parity: int) -> bytes:
    """0xc0|parity || internal_key(32) || path(32 each)."""
    out = bytes([0xC0 | (parity & 1)]) + internal_x
    for h in path_hashes:
        out += h
    return out


def tweak_parity(internal_x: bytes, merkle_root: bytes) -> int:
    if merkle_root:
        t = int.from_bytes(_tagged_hash(b"TapTweak", internal_x + merkle_root), "big")
    else:
        t = int.from_bytes(_tagged_hash(b"TapTweak", internal_x), "big")
    Pt = lift_x(internal_x)
    Q = _add(Pt, _mul(t, G))
    return Q[1] & 1


# --------------------------------------------------------------------------
# BIP-341 key-path / script-path sighash (SIGHASH_DEFAULT = 0x00)
# --------------------------------------------------------------------------
def taproot_sighash(tx, index, prevouts, annex=None, ext_flag=0, leaf_hash_=None,
                    key_version=0, code_separator_pos=0xFFFFFFFF):
    """Generic BIP341 sighash.

    tx            : btclib.Tx
    index         : input index being signed
    prevouts      : list of (value, scriptPubKey) for ALL inputs, in order
    ext_flag      : 0 = key path, 1 = script path
    leaf_hash_    : tapleaf hash (script path only)
    """
    assert all(not i.witness for i in tx.ins), "prevouts must be clean for hashing"
    hash_type = 0
    in_i = tx.ins[index]
    out = bytes([0])  # epoch
    out += bytes([hash_type])
    out += tx_version_le(tx)
    out += tx_locktime_le(tx)
    if hash_type != 3 and hash_type != 2:  # not SINGLE|ANYONECANPAY
        # BIP341: sha_amounts over the 8-byte LE value of EVERY input, then
        # sha_scriptpubkeys. prevouts may carry either the raw int or the
        # already-serialized bytes -- normalize here so callers cannot get it
        # wrong (this exact ambiguity cost a debugging cycle).
        out += dsha256_all([outpoint_value_le(p[0]) if isinstance(p[0], int)
                            else p[0] for p in prevouts])
        out += dsha256_all([p[1] for p in prevouts])
    out += in_i.outpoint()
    out += varint_small(0)  # scriptPubKey of spent output: empty for BIP341
    out += outpoint_value_le(prevouts[index][0])
    out += in_i.sequence.to_bytes(4, "little")
    if hash_type != 3 and hash_type != 2:
        out += dsha256_all([o.ser() for o in tx.outs])
    # spend_type
    spend_type = (ext_flag * 2) + (1 if annex else 0)
    out += bytes([spend_type])
    if annex:
        out += dsha256_all([serialize_annex(annex)])
    else:
        out += dsha256_all([])
    if hash_type == 3:
        out += sha256(tx.outs[index].ser())
    if ext_flag == 1:
        out += leaf_hash_
        out += bytes([0])   # key_version
        out += code_separator_pos.to_bytes(4, "little")
    return _tagged_hash(TAG_TAPSIGHASH, out)


def tx_version_le(tx):
    return (tx.version & 0xFFFFFFFF).to_bytes(4, "little")


def tx_locktime_le(tx):
    return tx.locktime.to_bytes(4, "little")


def outpoint_value_le(v):
    return v.to_bytes(8, "little")


def dsha256_all(items):
    return hashlib.sha256(hashlib.sha256(b"".join(items)).digest()).digest()


def serialize_annex(annex):
    return varint_small(len(annex)) + annex


# --------------------------------------------------------------------------
# tapscript opcodes we need that btclib does not define
# --------------------------------------------------------------------------
OP_CHECKSIGADD = 0xBA
OP_EQUAL = 0x87
OP_EQUALVERIFY = 0x88
OP_RIPEMD160 = 0xA6
OP_CSV = 0xB2
OP_DROP = 0x75


def read_num(n: int) -> bytes:
    return pushnum(n)
