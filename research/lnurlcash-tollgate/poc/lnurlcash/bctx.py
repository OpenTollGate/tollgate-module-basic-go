"""Bitcoin transaction serialization + BIP-341/342 taproot sighash.

Stdlib only. Enough of the consensus surface for LUD-25's canonical spend
transaction and for running a real tapscript VM.

Cited:
  * BIP-341 "Common Signature Message" / "SigMsg" / "TapSighash"
  * BIP-342 "Common Signature Message Extension" (ext_flag = 1)
  * BIP-68 relative locktime (LUD-25 "Timelocks")
"""

import hashlib
import struct

from .secp import compact_size, tagged_hash

SIGHASH_DEFAULT = 0x00
SIGHASH_ALL = 0x01
SIGHASH_NONE = 0x02
SIGHASH_SINGLE = 0x03
SIGHASH_ANYONECANPAY = 0x80

SEQUENCE_FINAL = 0xFFFFFFFF
SEQUENCE_LOCKTIME_DISABLE_FLAG = 1 << 31
SEQUENCE_LOCKTIME_TYPE_FLAG = 1 << 22
SEQUENCE_LOCKTIME_MASK = 0x0000FFFF
LOCKTIME_THRESHOLD = 500_000_000


def sha256(b: bytes) -> bytes:
    return hashlib.sha256(b).digest()


def dsha256(b: bytes) -> bytes:
    return sha256(sha256(b))


class TxOut:
    def __init__(self, value: int, script_pubkey: bytes):
        self.value = value
        self.script_pubkey = script_pubkey

    def serialize(self) -> bytes:
        return struct.pack("<q", self.value) + compact_size(len(self.script_pubkey)) + self.script_pubkey


class TxIn:
    def __init__(self, prevout_txid: bytes, prevout_vout: int, sequence: int, script_sig: bytes = b""):
        """prevout_txid is the 32-byte txid in *internal* (little-endian) order."""
        assert len(prevout_txid) == 32
        self.prevout_txid = prevout_txid
        self.prevout_vout = prevout_vout
        self.sequence = sequence
        self.script_sig = script_sig

    def outpoint(self) -> bytes:
        return self.prevout_txid + struct.pack("<I", self.prevout_vout)


class Tx:
    def __init__(self, version: int, ins, outs, locktime: int):
        self.version = version
        self.ins = list(ins)
        self.outs = list(outs)
        self.locktime = locktime

    def serialize_without_witness(self) -> bytes:
        o = struct.pack("<i", self.version)
        o += compact_size(len(self.ins))
        for i in self.ins:
            o += i.outpoint() + compact_size(len(i.script_sig)) + i.script_sig + struct.pack("<I", i.sequence)
        o += compact_size(len(self.outs))
        for x in self.outs:
            o += x.serialize()
        o += struct.pack("<I", self.locktime)
        return o

    def serialize_with_witness(self, witnesses) -> bytes:
        """witnesses: list (one per input) of lists of byte strings."""
        o = struct.pack("<i", self.version) + b"\x00\x01"
        o += compact_size(len(self.ins))
        for i in self.ins:
            o += i.outpoint() + compact_size(len(i.script_sig)) + i.script_sig + struct.pack("<I", i.sequence)
        o += compact_size(len(self.outs))
        for x in self.outs:
            o += x.serialize()
        for w in witnesses:
            o += compact_size(len(w))
            for item in w:
                o += compact_size(len(item)) + item
        o += struct.pack("<I", self.locktime)
        return o

    def txid(self) -> bytes:
        return dsha256(self.serialize_without_witness())[::-1]


# --------------------------------------------------------------------------
# BIP-341/342 signature message
# --------------------------------------------------------------------------
def _hash_prevouts(ins) -> bytes:
    return sha256(b"".join(i.outpoint() for i in ins))


def _hash_amounts(amounts) -> bytes:
    return sha256(b"".join(struct.pack("<q", a) for a in amounts))


def _hash_scriptpubkeys(spks) -> bytes:
    return sha256(b"".join(compact_size(len(s)) + s for s in spks))


def _hash_sequences(ins) -> bytes:
    return sha256(b"".join(struct.pack("<I", i.sequence) for i in ins))


def _hash_outputs(outs) -> bytes:
    return sha256(b"".join(o.serialize() for o in outs))


def taproot_sigmsg(
    tx: Tx,
    input_index: int,
    prevouts,               # list of (value, script_pubkey)
    hash_type: int = SIGHASH_DEFAULT,
    ext_flag: int = 0,
    annex: bytes = None,
    tapleaf_hash: bytes = None,
    key_version: int = 0,
    codesep_pos: int = 0xFFFFFFFF,
    anyonecanpay: bool = None,
):
    """BIP-341 SigMsg.

    Returns the raw message; the caller prepends the 0x00 epoch byte and (for
    ext_flag=1) appends the BIP-342 extension, then tagged-hashes TapSighash.
    """
    if anyonecanpay is None:
        anyonecanpay = bool(hash_type & SIGHASH_ANYONECANPAY)
    if len(prevouts) != len(tx.ins):
        raise ValueError("prevouts must cover every input")

    if anyonecanpay:
        out_hash_type = hash_type & 0x03
        if out_hash_type == SIGHASH_DEFAULT:
            out_hash_type = SIGHASH_ALL
    else:
        out_hash_type = hash_type & 0x03
        if hash_type in (SIGHASH_DEFAULT, SIGHASH_ALL):
            out_hash_type = SIGHASH_ALL

    m = bytearray()
    m += struct.pack("<B", hash_type)
    m += struct.pack("<i", tx.version)
    m += struct.pack("<I", tx.locktime)

    if not anyonecanpay:
        m += _hash_prevouts(tx.ins)
        m += _hash_amounts([p[0] for p in prevouts])
        m += _hash_scriptpubkeys([p[1] for p in prevouts])
        m += _hash_sequences(tx.ins)

    if out_hash_type not in (SIGHASH_NONE, SIGHASH_SINGLE):
        m += _hash_outputs(tx.outs)

    spend_type = (ext_flag * 2) + (1 if annex is not None else 0)
    m += bytes([spend_type])

    if anyonecanpay:
        i = tx.ins[input_index]
        m += i.outpoint()
        m += struct.pack("<q", prevouts[input_index][0])
        m += compact_size(len(prevouts[input_index][1])) + prevouts[input_index][1]
        m += struct.pack("<I", i.sequence)
    else:
        m += struct.pack("<I", input_index)

    if annex is not None:
        m += sha256(compact_size(len(annex)) + annex)

    if out_hash_type == SIGHASH_SINGLE and input_index < len(tx.outs):
        m += sha256(tx.outs[input_index].serialize())

    if ext_flag == 1:
        if tapleaf_hash is None:
            raise ValueError("ext_flag=1 requires tapleaf_hash (BIP-342 ext)")
        m += tapleaf_hash
        m += bytes([key_version])
        m += struct.pack("<I", codesep_pos)

    return bytes(m)


def taproot_sighash(tx, input_index, prevouts, hash_type=SIGHASH_DEFAULT, **kw):
    """hash_TapSighash(0x00 || SigMsg(hash_type, ext_flag) || ext)."""
    msg = taproot_sigmsg(tx, input_index, prevouts, hash_type=hash_type, **kw)
    return tagged_hash("TapSighash", b"\x00" + msg)
