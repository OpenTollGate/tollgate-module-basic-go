"""Minimal BIP-32 HD derivation (stdlib: hmac + hashlib).

Needed for LUD-26: cashHashingKey = derive(masterKey, m/139'/0), then the
SERVICE branch m/139'/d1'/d2'/d3'/d4', per LUD-05's hashing steps.
"""

import hashlib
import hmac
import struct

from .secp import G, N, point_mul

HARDENED = 0x80000000


class Xprv:
    def __init__(self, privkey: bytes, chaincode: bytes, depth: int, index: int, parent_fp: bytes):
        assert len(privkey) == 32 and len(chaincode) == 32
        self.privkey = privkey
        self.chaincode = chaincode
        self.depth = depth
        self.index = index
        self.parent_fp = parent_fp

    def pubkey_compressed(self) -> bytes:
        pt = point_mul(int.from_bytes(self.privkey, "big"), G)
        return bytes([2 + (pt[1] & 1)]) + pt[0].to_bytes(32, "big")

    def fingerprint(self) -> bytes:
        import hashlib as h
        sha = h.sha256(self.pubkey_compressed()).digest()
        # hash160 = ripemd160(sha256(pub)); ripemd160 may be missing on OpenSSL 3
        try:
            return h.new("ripemd160", sha, usedforsecurity=False).digest()[:4]
        except (ValueError, TypeError):  # OpenSSL 3 can hide ripemd160
            raise RuntimeError("ripemd160 unavailable; fingerprints are not asserted "
                               "by any test in this suite, but BIP-32 fingerprints need it")


def _sha512(b: bytes) -> bytes:
    return hashlib.sha512(b).digest()


def from_seed(seed: bytes) -> Xprv:
    I = hmac.new(b"Bitcoin seed", seed, hashlib.sha512).digest()
    il, ir = I[:32], I[32:]
    if int.from_bytes(il, "big") == 0 or int.from_bytes(il, "big") >= N:
        raise ValueError("invalid master key")
    return Xprv(il, ir, 0, 0, b"\x00\x00\x00\x00")


def ckd_priv(node: Xprv, index: int) -> Xprv:
    """BIP-32 CKDpriv. index >= 0x80000000 means hardened."""
    if index >= HARDENED:
        data = b"\x00" + node.privkey + struct.pack(">I", index)
    else:
        data = node.pubkey_compressed() + struct.pack(">I", index)
    I = hmac.new(node.chaincode, data, hashlib.sha512).digest()
    il = int.from_bytes(I[:32], "big")
    if il >= N:
        raise ValueError("derived key out of range")
    child = (il + int.from_bytes(node.privkey, "big")) % N
    if child == 0:
        raise ValueError("derived key is zero")
    return Xprv(child.to_bytes(32, "big"), I[32:], node.depth + 1, index, node.fingerprint())


def derive_path(node: Xprv, path, hardened=None) -> Xprv:
    """derive_path(m, [139, 0], hardened=[True, True]) == m/139'/0'."""
    for i, idx in enumerate(path):
        h = bool(hardened[i]) if hardened is not None else False
        node = ckd_priv(node, idx + (HARDENED if h else 0))
    return node
