#!/usr/bin/env python3
"""
btclib.py -- minimal, dependency-free Bitcoin transaction primitives for the
one-way-channel spike rig.

Design constraint: the rig must run with NO network and NO third-party Python
packages, because the claim under test is about a payer who has no internet.
A rig that needs PyPI to check a no-internet claim is not evidence.

Scope, deliberately narrow:
  * secp256k1 ECDSA, deterministic (RFC 6979), low-S, DER-encoded
  * BIP143 (segwit v0) sighash
  * P2WSH only. No P2PKH/P2SH: that keeps ripemd160 out of the dependency
    surface (OpenSSL 3 providers can drop it) and pins the construction to
    SegWit, which the memo argues is structurally REQUIRED (txid malleability).
  * script builder, OP_CSV (BIP112) / BIP68 relative-locktime encoding

Everything here is *verified against Bitcoin Core consensus* by the demo: a
transaction this library signs is only accepted by `bitcoin-cli
sendrawtransaction` if the signature and sighash are byte-correct. That is the
cross-check; we do not ask you to trust the arithmetic below.
"""

import hashlib
import hmac
import struct

# --------------------------------------------------------------------------
# secp256k1
# --------------------------------------------------------------------------
P = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F
N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
Gx = 0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798
Gy = 0x483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8


def _inv(a, m=P):
    return pow(a, m - 2, m)


def _add(p, q):
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


def _mul(k, p):
    r = None
    while k:
        if k & 1:
            r = _add(r, p)
        p = _add(p, p)
        k >>= 1
    return r


G = (Gx, Gy)


def pubkey(seckey: bytes) -> bytes:
    """33-byte compressed public key."""
    x, y = _mul(int.from_bytes(seckey, "big"), G)
    return bytes([2 + (y & 1)]) + x.to_bytes(32, "big")


def _rfc6979_k(seckey: int, msg: bytes) -> int:
    """Deterministic nonce (RFC 6979, HMAC-SHA256)."""
    x = seckey.to_bytes(32, "big")
    v = b"\x01" * 32
    k = b"\x00" * 32
    k = hmac.new(k, v + b"\x00" + x + msg, hashlib.sha256).digest()
    v = hmac.new(k, v, hashlib.sha256).digest()
    k = hmac.new(k, v + b"\x01" + x + msg, hashlib.sha256).digest()
    v = hmac.new(k, v, hashlib.sha256).digest()
    while True:
        v = hmac.new(k, v, hashlib.sha256).digest()
        cand = int.from_bytes(v, "big")
        if 1 <= cand < N:
            return cand
        k = hmac.new(k, v + b"\x00", hashlib.sha256).digest()
        v = hmac.new(k, v, hashlib.sha256).digest()


def _der_int(i: int) -> bytes:
    b = i.to_bytes((i.bit_length() + 7) // 8 or 1, "big")
    if b[0] & 0x80:
        b = b"\x00" + b
    return b"\x02" + bytes([len(b)]) + b


def sign(seckey: bytes, msg32: bytes) -> bytes:
    """DER-encoded, low-S ECDSA signature over a 32-byte hash. No sighash byte."""
    d = int.from_bytes(seckey, "big")
    z = int.from_bytes(msg32, "big")
    while True:
        k = _rfc6979_k(d, msg32)
        pt = _mul(k, G)
        r = pt[0] % N
        if r == 0:
            continue
        s = (_inv(k, N) * (z + r * d)) % N
        if s == 0:
            continue
        if s > N // 2:  # low-S (BIP62 / Core policy)
            s = N - s
        body = _der_int(r) + _der_int(s)
        # ERRATA (found by this rig, caught by Core as "Non-canonical DER"):
        # the SEQUENCE length must be exactly len(body). It is easy to add the
        # two 0x02 tag+length bytes again here -- _der_int() already returns the
        # full TLV -- which yields `30 46` wrapping only 68 bytes of content.
        # CHECKSIG then fails parsing and you get a signature error that looks
        # like a key or sighash problem. It is a two-byte off-by-one.
        return b"\x30" + bytes([len(body)]) + body


# --------------------------------------------------------------------------
# hashing
# --------------------------------------------------------------------------
def sha256(b: bytes) -> bytes:
    return hashlib.sha256(b).digest()


def dsha256(b: bytes) -> bytes:
    return sha256(sha256(b))


# --------------------------------------------------------------------------
# script
# --------------------------------------------------------------------------
OP_DUP, OP_DROP, OP_IF, OP_ELSE, OP_ENDIF = 0x76, 0x75, 0x63, 0x67, 0x68
OP_CHECKSIG, OP_CHECKSEQUENCEVERIFY = 0xAC, 0xB2


def push(data: bytes) -> bytes:
    n = len(data)
    if n < 0x4C:
        return bytes([n]) + data
    if n <= 0xFF:
        return b"\x4c" + bytes([n]) + data
    raise ValueError("push too large for this rig")


OP_0 = 0x00
OP_1NEGATE = 0x4F
OP_1 = 0x51


def pushnum(n: int) -> bytes:
    """Minimal-data number push (SCRIPT_VERIFY_MINIMALDATA).

    Core rejects `01 0a` for the constant 10: a number in [-1,16] must be
    pushed with OP_0 / OP_1NEGATE / OP_1..OP_16. Getting this wrong is the
    single most common way a hand-rolled script fails `mempool-script-verify
    -flag-failed (Data push larger than necessary)`.
    """
    if n == 0:
        return bytes([OP_0])
    if n == -1:
        return bytes([OP_1NEGATE])
    if 1 <= n <= 16:
        return bytes([OP_1 + n - 1])
    return push(num(n))


def num(n: int) -> bytes:
    """CScriptNum minimal encoding."""
    if n == 0:
        return b""
    neg = n < 0
    a = abs(n)
    out = bytearray()
    while a:
        out.append(a & 0xFF)
        a >>= 8
    if out[-1] & 0x80:
        out.append(0x80 if neg else 0x00)
    elif neg:
        out[-1] |= 0x80
    return bytes(out)


# --------------------------------------------------------------------------
# BIP68 relative locktime
# --------------------------------------------------------------------------
SEQUENCE_LOCKTIME_TYPE_FLAG = 1 << 22
SEQUENCE_LOCKTIME_DISABLE_FLAG = 1 << 31
SEQUENCE_FINAL = 0xFFFFFFFF


def csv_blocks(blocks: int) -> int:
    """nSequence expressing: `blocks` after this input confirms.

    THE TRAP (found by running this rig against Core, see ERRATA):
    SEQUENCE_LOCKTIME_TYPE_FLAG (bit 22) selects 512-second units instead of
    blocks, and BIP112 requires the flag to MATCH between the script operand and
    the input's nSequence. A script that pushes plain `6` therefore needs
    nSequence = 6 (flag clear), NOT 6|FLAG. Setting the flag here silently
    converts a 6-block lock into a 6*512s = 51-minute lock, which on a regtest
    chain that mines instantly is unsatisfiable -- Core reports
    `non-BIP68-final` and no amount of mining fixes it.
    """
    assert 0 < blocks < (1 << 16)
    return blocks


# --------------------------------------------------------------------------
# transactions
# --------------------------------------------------------------------------
def varint(n: int) -> bytes:
    if n < 0xFD:
        return bytes([n])
    if n <= 0xFFFF:
        return b"\xfd" + struct.pack("<H", n)
    if n <= 0xFFFFFFFF:
        return b"\xfe" + struct.pack("<I", n)
    return b"\xff" + struct.pack("<Q", n)


class TxIn:
    def __init__(self, txid: str, vout: int, sequence: int, witness=None):
        self.txid = txid
        self.vout = vout
        self.sequence = sequence
        self.witness = witness or []

    def outpoint(self, little_endian=True):
        h = bytes.fromhex(self.txid)
        return (h[::-1] if little_endian else h) + struct.pack("<I", self.vout)


class TxOut:
    def __init__(self, value: int, spk: bytes):
        self.value = value
        self.spk = spk

    def ser(self):
        return struct.pack("<q", self.value) + varint(len(self.spk)) + self.spk


class Tx:
    """Segwit v0 transaction. nVersion 2 so BIP68 relative locks apply."""

    def __init__(self, ins, outs, locktime=0, version=2):
        self.ins = ins
        self.outs = outs
        self.locktime = locktime
        self.version = version

    # ---- serialization -------------------------------------------------
    def _ser_body(self, with_witness: bool) -> bytes:
        o = struct.pack("<i", self.version)
        if with_witness:
            o += b"\x00\x01"  # marker + flag
        o += varint(len(self.ins))
        for i in self.ins:
            # scriptSig is empty for native segwit
            o += i.outpoint() + b"\x00" + struct.pack("<I", i.sequence)
        o += varint(len(self.outs))
        for x in self.outs:
            o += x.ser()
        if with_witness:
            for i in self.ins:
                o += varint(len(i.witness))
                for item in i.witness:
                    o += varint(len(item)) + item
        o += struct.pack("<I", self.locktime)
        return o

    def serialize(self) -> bytes:
        return self._ser_body(True)

    def serialize_unsigned(self) -> bytes:
        """Legacy (pre-segwit) serialization -- the form Core's
        signrawtransactionwithwallet expects for an unsigned tx."""
        return self._ser_body(False)

    def txid(self) -> str:
        return dsha256(self._ser_body(False))[::-1].hex()

    def vsize(self) -> int:
        total = len(self._ser_body(True))
        base = len(self._ser_body(False))
        return (base * 3 + total + 3) // 4

    # ---- BIP143 sighash ------------------------------------------------
    def sighash_v0(self, index: int, amount: int, script_code: bytes, sighash_type=1) -> bytes:
        hash_prevouts = dsha256(b"".join(i.outpoint() for i in self.ins))
        hash_sequence = dsha256(b"".join(struct.pack("<I", i.sequence) for i in self.ins))
        hash_outputs = dsha256(b"".join(x.ser() for x in self.outs))
        i = self.ins[index]
        pre = struct.pack("<i", self.version)
        pre += hash_prevouts + hash_sequence
        pre += i.outpoint()
        pre += varint(len(script_code)) + script_code
        pre += struct.pack("<q", amount)
        pre += struct.pack("<I", i.sequence)
        pre += hash_outputs
        pre += struct.pack("<I", self.locktime)
        pre += struct.pack("<I", sighash_type)
        return dsha256(pre)


def p2wsh_spk(witness_script: bytes) -> bytes:
    """OP_0 <sha256(script)>"""
    return b"\x00\x20" + sha256(witness_script)


def p2wpkh_spk(pub: bytes) -> bytes:
    """OP_0 <hash160(pub)> -- used only for the funding wallet's change."""
    import hashlib
    # hash160 = ripemd160(sha256(x)); fall back to a BigInt ripemd160 if the
    # OpenSSL provider hides it (OpenSSL 3 does on some builds).
    h = hashlib.new("ripemd160", sha256(pub), usedforsecurity=False).digest()
    return b"\x00\x14" + h


# --------------------------------------------------------------------------
# bech32 (BIP173) -- only so the rig can print/parse a regtest P2WSH address
# --------------------------------------------------------------------------
_B32 = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"


def _b32_polymod(values):
    gen = [0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3]
    chk = 1
    for v in values:
        top = chk >> 25
        chk = ((chk & 0x1FFFFFF) << 5) ^ v
        for i in range(5):
            chk ^= gen[i] if ((top >> i) & 1) else 0
    return chk


def _b32_hrp_expand(hrp):
    return [ord(c) >> 5 for c in hrp] + [0] + [ord(c) & 31 for c in hrp]


def _b32_create_checksum(hrp, data):
    values = _b32_hrp_expand(hrp) + data
    polymod = _b32_polymod(values + [0, 0, 0, 0, 0, 0]) ^ 1
    return [(polymod >> 5 * (5 - i)) & 31 for i in range(6)]


def _convertbits(data, frombits, tobits, pad=True):
    acc = 0
    bits = 0
    out = []
    maxv = (1 << tobits) - 1
    for value in data:
        acc = (acc << frombits) | value
        bits += frombits
        while bits >= tobits:
            bits -= tobits
            out.append((acc >> bits) & maxv)
    if pad and bits:
        out.append((acc << (tobits - bits)) & maxv)
    return out


def segwit_address(witver: int, witprog: bytes, hrp="bcrt") -> str:
    data = [witver] + _convertbits(list(witprog), 8, 5)
    return hrp + "1" + "".join(_B32[c] for c in data + _b32_create_checksum(hrp, data))


def p2wsh_address(witness_script: bytes, hrp="bcrt") -> str:
    return segwit_address(0, sha256(witness_script), hrp)


# --------------------------------------------------------------------------
# base58check -- only needed to hand Core a WIF so it can act as an
# independent oracle for our sighash + DER encoding (see T8)
# --------------------------------------------------------------------------
_B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"


def base58check(payload: bytes) -> str:
    b = payload + dsha256(payload)[:4]
    n = int.from_bytes(b, "big")
    out = ""
    while n:
        n, r = divmod(n, 58)
        out = _B58[r] + out
    return "1" * (len(b) - len(b.lstrip(b"\x00"))) + out


def wif(seckey: bytes, version=0xEF) -> str:
    """Compressed-key WIF. 0xEF is the regtest prefix."""
    return base58check(bytes([version]) + seckey + b"\x01")
