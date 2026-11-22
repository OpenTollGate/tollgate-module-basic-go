"""secp256k1 field/point arithmetic, BIP-340/341 primitives.

Stdlib only. Pure-Python big-int arithmetic -- slow but exact and auditable.

Cited: BIP-340 (x-only keys, tagged hashes, lift_x, challenge),
       BIP-341 (TapTweak, tapleaf_hash, NUMS point H).
"""

import hashlib

P = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F
N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
Gx = 0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798
Gy = 0x483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8
G = (Gx, Gy)

# BIP-341 "nothing up my sleeve" point: x of H, y even.
NUMS_X = bytes.fromhex(
    "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"
)


class SecpError(ValueError):
    pass


def tagged_hash(tag: str, msg: bytes) -> bytes:
    """BIP-340 tagged hash: sha256(sha256(tag) || sha256(tag) || msg)."""
    t = hashlib.sha256(tag.encode()).digest()
    return hashlib.sha256(t + t + msg).digest()


def _inv(a, m=P):
    return pow(a, m - 2, m)


def point_add(p, q):
    if p is None:
        return q
    if q is None:
        return p
    if p[0] == q[0]:
        if (p[1] + q[1]) % P == 0:
            return None
        lam = (3 * p[0] * p[0] * _inv(2 * p[1])) % P
    else:
        lam = ((q[1] - p[1]) * _inv(q[0] - p[0])) % P
    x = (lam * lam - p[0] - q[0]) % P
    y = (lam * (p[0] - x) - p[1]) % P
    return (x, y)


def point_mul(k, p):
    if k % N == 0 or p is None:
        return None
    r = None
    while k:
        if k & 1:
            r = point_add(r, p)
        p = point_add(p, p)
        k >>= 1
    return r


def is_on_curve(pt) -> bool:
    if pt is None:
        return False
    x, y = pt
    return (y * y - (x * x * x + 7)) % P == 0


def has_even_y(pt) -> bool:
    return pt[1] % 2 == 0


def lift_x(x: bytes):
    """BIP-340 lift_x: the point with this x and EVEN y, or None.

    Returns None when x >= p or x^3+7 is not a QR. This is exactly the
    condition LUD-25 uses to reject a `cp1` ("Q not the x coordinate of a
    curve point").
    """
    if len(x) != 32:
        return None
    xc = int.from_bytes(x, "big")
    if xc >= P:
        return None
    c = (pow(xc, 3, P) + 7) % P
    y = pow(c, (P + 1) // 4, P)
    if (y * y) % P != c:
        return None
    if y % 2 != 0:
        y = P - y
    return (xc, y)


def xonly_pubkey(pt) -> bytes:
    """32-byte x-only encoding of a point (BIP-340: x of the even-y lift)."""
    if pt is None:
        raise SecpError("point at infinity has no x-only encoding")
    x, y = pt
    if y % 2 != 0:
        pt = (x, (P - y) % P)
        x = pt[0]
    return x.to_bytes(32, "big")


def schnorr_sign(msg32: bytes, seckey: bytes, aux_rand: bytes = b"\x00" * 32) -> bytes:
    """BIP-340 sign. Returns 64 bytes."""
    d0 = int.from_bytes(seckey, "big")
    if not 1 <= d0 <= N - 1:
        raise SecpError("seckey out of range")
    Pt = point_mul(d0, G)
    d = d0 if has_even_y(Pt) else N - d0
    t = (d ^ int.from_bytes(tagged_hash("BIP0340/aux", aux_rand), "big"))
    rand = tagged_hash("BIP0340/nonce", t.to_bytes(32, "big") + xonly_pubkey(Pt) + msg32)
    k0 = int.from_bytes(rand, "big") % N
    if k0 == 0:
        raise SecpError("nonce is zero")
    R = point_mul(k0, G)
    k = k0 if has_even_y(R) else N - k0
    e = int.from_bytes(
        tagged_hash(
            "BIP0340/challenge",
            R[0].to_bytes(32, "big") + xonly_pubkey(Pt) + msg32,
        ),
        "big",
    ) % N
    sig = R[0].to_bytes(32, "big") + ((k + e * d) % N).to_bytes(32, "big")
    if not schnorr_verify(msg32, xonly_pubkey(Pt), sig):
        raise SecpError("self-verify failed")
    return sig


def schnorr_verify(msg32: bytes, pubkey_x: bytes, sig: bytes) -> bool:
    """BIP-340 verify. pubkey_x is 32 bytes."""
    if len(sig) != 64 or len(pubkey_x) != 32:
        return False
    r = int.from_bytes(sig[:32], "big")
    s = int.from_bytes(sig[32:], "big")
    if r >= P or s >= N:
        return False
    Ppt = lift_x(pubkey_x)
    if Ppt is None:
        return False
    e = int.from_bytes(
        tagged_hash("BIP0340/challenge", sig[:32] + pubkey_x + msg32), "big"
    ) % N
    R = point_add(point_mul(s, G), point_mul(N - e, Ppt))
    if R is None or not has_even_y(R) or R[0] != r:
        return False
    return True


# --------------------------------------------------------------------------
# BIP-341 taproot
# --------------------------------------------------------------------------
LEAF_VERSION_TAPSCRIPT = 0xC0


def tapleaf_hash(script: bytes, leaf_version: int = LEAF_VERSION_TAPSCRIPT) -> bytes:
    """BIP-341 tapleaf_hash = tagged_hash("TapLeaf", ver || compact_size(len) || script).

    LUD-25's `tapleaf_hash` uses leaf version 0xc0.
    """
    return tagged_hash("TapLeaf", bytes([leaf_version]) + compact_size(len(script)) + script)


def compact_size(n: int) -> bytes:
    import struct

    if n < 0xFD:
        return bytes([n])
    if n <= 0xFFFF:
        return b"\xfd" + struct.pack("<H", n)
    if n <= 0xFFFFFFFF:
        return b"\xfe" + struct.pack("<I", n)
    return b"\xff" + struct.pack("<Q", n)


def control_block(Q_x: bytes, internal_x: bytes, parity: int, path: list = ()) -> bytes:
    assert parity in (0, 1) and len(internal_x) == 32
    return bytes([0xC0 | parity]) + internal_x + b"".join(path)


def taproot_tweak_pubkey(internal_x: bytes, tweak: bytes):
    """BIP-341 output key: Q = lift_x(P) + t*G, with t = tagged_hash("TapTweak", P||h).

    Returns (Q_x_32, parity_of_Q) or raises if t >= n or Q is infinity.
    """
    Pt = lift_x(internal_x)
    if Pt is None:
        raise SecpError("internal key not on curve")
    t = int.from_bytes(tweak, "big")
    if t >= N:
        raise SecpError("tweak >= n")
    Q = point_add(Pt, point_mul(t, G))
    if Q is None:
        raise SecpError("tweak produced point at infinity")
    Qp = Q if has_even_y(Q) else (Q[0], (P - Q[1]) % P)
    return Qp[0].to_bytes(32, "big"), (Q[1] % 2)
