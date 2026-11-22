"""LNURLcash LUD-25 codec: cp1 / ck1 / cw1 / cs1, bearer notes, canonical spend.

Stdlib only. Every behaviour cites its LUD-25 section. Nothing here has been
checked against libbitcoinkernel (not installed on this host) -- see README.
"""

import hashlib
import hmac
from fractions import Fraction

from . import bech32m
from .bctx import (
    LOCKTIME_THRESHOLD,
    SEQUENCE_FINAL,
    SEQUENCE_LOCKTIME_DISABLE_FLAG,
    SEQUENCE_LOCKTIME_TYPE_FLAG,
    SIGHASH_DEFAULT,
    Tx,
    TxIn,
    TxOut,
    sha256,
    taproot_sighash,
    taproot_sigmsg,
)
from .secp import (
    G,
    N,
    NUMS_X,
    P,
    SecpError,
    compact_size,
    has_even_y,
    lift_x,
    point_add,
    point_mul,
    schnorr_verify,
    tagged_hash,
    tapleaf_hash,
    xonly_pubkey,
)

OP_1 = 0x51
OP_SHA256 = 0xA8
OP_EQUAL = 0x87

CS1_PREFIX = "cs"
CK1_PAYLOAD_LEN = 96
CW1_BEARER_LEN = 192


# --------------------------------------------------------------------------
# LUD-25 "Encoding": cp1 / ck1 / cw1
# --------------------------------------------------------------------------
def encode_cp1(Q: bytes) -> str:
    """`cp1` = bech32m of the 32-byte x-only output key Q. 61 chars."""
    assert len(Q) == 32
    return bech32m.encode_bytes("cp", Q)


def decode_cp1(s: str) -> bytes:
    """Decode cp1. LUD-25 MUST-reject: "Q not the x coordinate of a curve point"."""
    hrp, payload = bech32m.decode_bytes(s)
    if hrp != "cp":
        raise ValueError("not a cp1: hrp %r" % hrp)
    if len(payload) != 32:
        raise ValueError("cp1 payload must be 32 bytes, got %d" % len(payload))
    if lift_x(payload) is None:
        raise ValueError("cp1 rejected: Q is not the x coordinate of a curve point")
    return payload


def encode_ck1(Q: bytes, sig: bytes) -> str:
    """`ck1` = bech32m of Q (32) || sig (64). Implies locktime 0, seq 0xffffffff."""
    assert len(Q) == 32 and len(sig) == 64
    return bech32m.encode_bytes("ck", Q + sig)


def decode_ck1(s: str):
    hrp, payload = bech32m.decode_bytes(s)
    if hrp != "ck":
        raise ValueError("not a ck1: hrp %r" % hrp)
    if len(payload) != CK1_PAYLOAD_LEN:
        raise ValueError("ck1 MUST reject a payload of any length but 96 bytes (got %d)" % len(payload))
    return payload[:32], payload[32:]


# --------------------------------------------------------------------------
# cw1
# --------------------------------------------------------------------------
def encode_cw1(locktime: int, sequence: int, script: bytes, control: bytes, witness) -> str:
    """`cw1` payload:
        u32 locktime || u32 sequence || (u16 len || item)* over
        script, control block, then witness items bottom-of-stack-first.
    Integers big-endian.  (LUD-25 "Encoding")
    """
    items = [script, control] + list(witness)
    out = locktime.to_bytes(4, "big") + sequence.to_bytes(4, "big")
    for it in items:
        out += len(it).to_bytes(2, "big") + it
    return bech32m.encode_bytes("cw", out)


def decode_cw1(s: str):
    """Decode cw1, enforcing LUD-25's MUST-reject conditions."""
    hrp, payload = bech32m.decode_bytes(s)
    if hrp != "cw":
        raise ValueError("not a cw1: hrp %r" % hrp)
    if len(payload) < 8:
        raise ValueError("cw1 payload shorter than the 8-byte header")
    locktime = int.from_bytes(payload[0:4], "big")
    sequence = int.from_bytes(payload[4:8], "big")
    rest = payload[8:]
    items = []
    i = 0
    while i < len(rest):
        if i + 2 > len(rest):
            raise ValueError("cw1: truncated length prefix")
        ln = int.from_bytes(rest[i:i + 2], "big")
        i += 2
        if i + ln > len(rest):
            raise ValueError("cw1: length prefix does not consume the payload exactly")
        items.append(rest[i:i + ln])
        i += ln
    if len(items) == 0:
        raise ValueError("cw1: missing script")
    if len(items) == 1:
        raise ValueError("cw1: missing control block")
    script, control = items[0], items[1]
    witness = items[2:]
    if len(control) < 33 or (len(control) - 33) % 32 != 0:
        raise ValueError("cw1: malformed control block length")
    return locktime, sequence, script, control, witness


def cw1_payload_len(script: bytes, control: bytes, witness) -> int:
    return 8 + sum(2 + len(x) for x in [script, control] + list(witness))


# --------------------------------------------------------------------------
# LUD-25 "Bearer notes"
# --------------------------------------------------------------------------
def bearer_leaf(h: bytes) -> bytes:
    """leaf = OP_SHA256 <h> OP_EQUAL  ->  a8 20 <h> 87   (32-byte h)."""
    assert len(h) == 32
    return bytes([OP_SHA256]) + bytes([len(h)]) + h + bytes([OP_EQUAL])


def taproot_output_key(internal_x: bytes, merkle_root: bytes):
    """BIP-341 output key from an x-only internal key and a merkle root.

    tweak = tagged_hash("TapTweak", P || merkle_root)   (merkle_root may be b"")
    Q     = lift_x(P) + tweak*G ; returns (Q_x, parity_of_the_tweaked_point)
    """
    tweak = tagged_hash("TapTweak", internal_x + merkle_root)
    Pt = lift_x(internal_x)
    if Pt is None:
        raise SecpError("internal key not on curve")
    t = int.from_bytes(tweak, "big")
    if t >= N:
        raise SecpError("tweak >= n")
    Q = point_add(Pt, point_mul(t, G))
    if Q is None:
        raise SecpError("tweak produced the point at infinity")
    return xonly_pubkey(Q), Q[1] % 2


def bearer_Q(h: bytes):
    """Q = x(lift_x(H) + tagged_hash("TapTweak", H || tapleaf_hash(leaf))·G)."""
    root = tapleaf_hash(bearer_leaf(h))
    return taproot_output_key(NUMS_X, root)


def bearer_control(h: bytes) -> bytes:
    """control = (0xc0 | parity(Q)) || H."""
    _, parity = bearer_Q(h)
    return bytes([0xC0 | parity]) + NUMS_X


def bearer_note(preimage: bytes) -> dict:
    """The whole bearer note, from the 32-byte preimage."""
    assert len(preimage) == 32
    h = sha256(preimage)
    leaf = bearer_leaf(h)
    root = tapleaf_hash(leaf)
    tweak = tagged_hash("TapTweak", NUMS_X + root)
    Q, parity = bearer_Q(h)
    control = bytes([0xC0 | parity]) + NUMS_X
    cw1 = encode_cw1(0, SEQUENCE_FINAL, leaf, control, [preimage])
    return {
        "preimage": preimage.hex(),
        "h": h.hex(),
        "leaf": leaf.hex(),
        "tapleaf_hash": root.hex(),
        "H": NUMS_X.hex(),
        "tweak": tweak.hex(),
        "Q": Q.hex(),
        "control_block": control.hex(),
        "cp1": encode_cp1(Q),
        "cw1": cw1,
        "cw1_len": len(cw1),
        "cw1_payload_len": cw1_payload_len(leaf, control, [preimage]),
    }


def bearer_cw1_from_hex_preimage(hex_preimage: str) -> str:
    """`k1` short form (LUD-25 "Short forms"): hex is the preimage."""
    if len(hex_preimage) != 64:
        raise ValueError("k1 short form must be exactly 64 hex characters")
    return bearer_note(bytes.fromhex(hex_preimage))["cw1"]


def bearer_Q_from_hex_h(hex_h: str) -> bytes:
    """`cp1` short form: hex is h."""
    if len(hex_h) != 64:
        raise ValueError("cp1 short form must be exactly 64 hex characters")
    return bearer_Q(bytes.fromhex(hex_h))[0]


def is_short_form(s: str) -> bool:
    return len(s) == 64 and all(c in "0123456789abcdefABCDEF" for c in s)


# --------------------------------------------------------------------------
# cs1 -- offline verification certificates (LUD-25 "Offline verification")
# --------------------------------------------------------------------------
LSM_PREFIX = b"Lightning Signed Message:"


def cs1_message(amount_msat: int, Q_hex: str) -> bytes:
    return b"LNURLcash:" + str(amount_msat).encode() + b":" + Q_hex.lower().encode()


def cs1_digest(message: bytes) -> bytes:
    return sha256(sha256(LSM_PREFIX + message))


# BOLT-11 human-readable-part multipliers, as exact rationals.
_MULT = [
    ("", Fraction(1)),
    ("m", Fraction(1, 10 ** 3)),
    ("u", Fraction(1, 10 ** 6)),
    ("n", Fraction(1, 10 ** 9)),
    ("p", Fraction(1, 10 ** 12)),
]


def cs1_hrp(amount_msat: int) -> str:
    """`cs` + amount + BOLT-11 multiplier, amount in minimal base-10 form.

    1 BTC = 10^11 msat, so msat -> BTC is msat/10^11.
    """
    msat = Fraction(amount_msat, 10 ** 11)
    for suffix, mult in _MULT:
        v = msat / mult
        if v.denominator == 1 and v.numerator > 0:
            return CS1_PREFIX + str(v.numerator) + suffix
    raise ValueError("amount not representable in BOLT-11 base units")


def cs1_amount(hrp: str) -> int:
    """Decode a `cs...` HRP back to msat, per BOLT-11's HRP rules."""
    if not hrp.startswith(CS1_PREFIX):
        raise ValueError("not a cs1 HRP")
    body = hrp[len(CS1_PREFIX):]
    if body == "":
        raise ValueError("amountless cs1 (no amount in HRP)")
    mult = Fraction(1)
    if body[-1] in "munp":
        mult = Fraction(10) ** {"m": -3, "u": -6, "n": -9, "p": -12}[body[-1]]
        body = body[:-1]
    if not body.isdigit():
        raise ValueError("invalid amount in cs1 HRP")
    btc = Fraction(int(body)) * mult
    msat = btc * 10 ** 11
    if msat.denominator != 1:
        raise ValueError("cs1 amount is not a whole number of msat")
    return int(msat)


def _rfc6979_nonce(d: int, msg32: bytes) -> int:
    x = d.to_bytes(32, "big")
    v = b"\x01" * 32
    k = b"\x00" * 32
    k = hmac.new(k, v + b"\x00" + x + msg32, hashlib.sha256).digest()
    v = hmac.new(k, v, hashlib.sha256).digest()
    k = hmac.new(k, v + b"\x01" + x + msg32, hashlib.sha256).digest()
    v = hmac.new(k, v, hashlib.sha256).digest()
    while True:
        v = hmac.new(k, v, hashlib.sha256).digest()
        cand = int.from_bytes(v, "big")
        if 1 <= cand < N:
            return cand
        k = hmac.new(k, v + b"\x00", hashlib.sha256).digest()
        v = hmac.new(k, v, hashlib.sha256).digest()


def ecdsa_sign_recoverable(msg32: bytes, seckey: bytes) -> bytes:
    """RFC6979 deterministic recoverable ECDSA. Returns recid || r || s (lnwire).

    LUD-25: "A Lightning node's signmessage produces this signature; SERVICE
    moves its leading recovery id to the end before encoding."
    """
    d = int.from_bytes(seckey, "big")
    z = int.from_bytes(msg32, "big")
    for _ in range(16):
        k = _rfc6979_nonce(d, msg32)
        R = point_mul(k, G)
        r = R[0] % N
        if r == 0:
            continue
        recid = (0 if has_even_y(R) else 1) | (2 if R[0] >= N else 0)
        s = (pow(k, -1, N) * (z + r * d)) % N
        if s == 0:
            continue
        if s > N // 2:  # low-S
            s = N - s
            recid ^= 1
        return bytes([recid]) + r.to_bytes(32, "big") + s.to_bytes(32, "big")
    raise SecpError("could not produce a signature")


def ecdsa_recover_pubkey(msg32: bytes, r: int, s: int, recid: int) -> bytes:
    """Recover the 33-byte compressed pubkey. BIP-340 style point recovery."""
    x = r + (N if (recid & 2) else 0)
    if x >= P:
        raise SecpError("recovery x out of range")
    alpha = (pow(x, 3, P) + 7) % P
    beta = pow(alpha, (P + 1) // 4, P)
    if (beta * beta) % P != alpha:
        raise SecpError("recovery x is not on the curve")
    y = beta if (beta % 2) == (recid & 1) else P - beta
    R = (x, y)
    z = int.from_bytes(msg32, "big")
    r_inv = pow(r, -1, N)
    Q = point_mul(r_inv, point_add(point_mul(s, R), point_mul(N - (z % N), G)))
    if Q is None:
        raise SecpError("recovered point at infinity")
    prefix = 2 + (Q[1] & 1)
    return bytes([prefix]) + Q[0].to_bytes(32, "big")


def cs1_sign(amount_msat: int, Q_hex: str, seckey: bytes) -> str:
    """Produce a cs1 for a note. Moves the recovery id to the end (LUD-25)."""
    msg = cs1_message(amount_msat, Q_hex)
    sig65 = ecdsa_sign_recoverable(cs1_digest(msg), seckey)
    recid, rs = sig65[0], sig65[1:]
    return bech32m.encode_bytes(cs1_hrp(amount_msat), rs + bytes([recid]))


def cs1_recover(s: str):
    """Recover (amount_msat, Q_hex, pubkey_bytes, message, digest) from a cs1."""
    hrp, payload = bech32m.decode_bytes(s)
    if len(payload) != 65:
        raise ValueError("cs1 payload must be 65 bytes (r || s || recovery-id)")
    if not hrp.startswith("cs"):
        raise ValueError("not a cs1: hrp %r" % hrp)
    amount = cs1_amount(hrp)
    r = int.from_bytes(payload[0:32], "big")
    s_ = int.from_bytes(payload[32:64], "big")
    recid = payload[64]
    if recid > 3:
        raise ValueError("invalid recovery id")
    return hrp, amount, r, s_, recid


def cs1_verify(s: str, Q_hex: str, expected_pubkey_hex: str):
    """Full offline verification step 3 (LUD-25): rebuild the message from the
    cs1 amount and Q, recover a key, compare to mintPubkey."""
    hrp, amount, r, s_, recid = cs1_recover(s)
    msg = cs1_message(amount, Q_hex)
    digest = cs1_digest(msg)
    pk = ecdsa_recover_pubkey(digest, r, s_, recid)
    return {
        "hrp": hrp,
        "amount_msat": amount,
        "message": msg.decode(),
        "digest": digest.hex(),
        "recovered_pubkey": pk.hex(),
        "mintPubkey": expected_pubkey_hex,
        "ok": pk.hex() == expected_pubkey_hex.lower(),
    }


# --------------------------------------------------------------------------
# The canonical spend transaction (LUD-25 "The canonical spend transaction")
# --------------------------------------------------------------------------
def mint_txid(domain: str) -> bytes:
    """prevout txid = tagged_hash("LNURLcash/mint", domain), vout 0."""
    return tagged_hash("LNURLcash/mint", domain.lower().encode())


def spent_script_pubkey(Q: bytes) -> bytes:
    """(OP_1 <Q>, 0) -- LUD-25's spent output."""
    assert len(Q) == 32
    return bytes([OP_1, 0x20]) + Q


def canonical_spend_tx(domain: str, Q: bytes, locktime: int = 0, sequence: int = SEQUENCE_FINAL) -> Tx:
    return Tx(
        version=2,
        ins=[TxIn(mint_txid(domain), 0, sequence, b"")],
        outs=[TxOut(0, b"")],
        locktime=locktime,
    )


def canonical_prevouts(Q: bytes):
    return [(0, spent_script_pubkey(Q))]


# --------------------------------------------------------------------------
# Spend verification
# --------------------------------------------------------------------------
def verify_key_path_spend(domain: str, Q: bytes, sig: bytes):
    """ck1 / key-path spend: BIP-340 Schnorr sig by Q over the canonical tx.

    LUD-25: "Key-path signatures MUST use SIGHASH_DEFAULT (64 bytes)."
    """
    if len(sig) != 64:
        return False, "key-path signature must be 64 bytes (SIGHASH_DEFAULT)"
    tx = canonical_spend_tx(domain, Q, 0, SEQUENCE_FINAL)
    msg = taproot_sighash(tx, 0, canonical_prevouts(Q), hash_type=SIGHASH_DEFAULT)
    return schnorr_verify(msg, Q, sig), msg.hex()


def verify_script_path_spend(domain: str, locktime: int, sequence: int,
                             script: bytes, control: bytes, witness,
                             script_pubkeys=None, allow_disabled_opcodes=False):
    """cw1 script-path spend.

    Returns (ok, reason, dict) where dict carries the recomputed Q and the
    commitment checks. Does NOT apply the SERVICE-clock timelock policy; call
    `check_timelock_policy` for that.
    """
    from .tapscript import verify_leaf

    info = {}
    if len(control) < 33 or (len(control) - 33) % 32 != 0:
        return False, "control block length must be 33 + 32k", info
    leaf_version = control[0] & 0xFE
    if leaf_version != 0xC0:
        return False, "LUD-25: leaf version %#x is not 0xc0" % leaf_version, info

    from .tapscript import scan_op_success

    hits = scan_op_success(script)
    if hits:
        return False, "LUD-25: leaf contains OP_SUCCESSx outside pushed data: %s" % hits, info

    internal = control[1:33]
    path = [control[33 + 32 * i:33 + 32 * (i + 1)] for i in range((len(control) - 33) // 32)]
    root = tapleaf_hash(script, 0xC0)
    for sib in path:
        lo, hi = (root, sib) if root < sib else (sib, root)
        root = tagged_hash("TapBranch", lo + hi)
    Q, q_parity = taproot_output_key(internal, root)
    info["Q"] = Q.hex()
    info["parity"] = q_parity
    info["control_parity"] = control[0] & 1
    info["tapleaf_hash"] = tapleaf_hash(script, 0xC0).hex()
    info["merkle_root"] = root.hex()
    if q_parity != (control[0] & 1):
        return False, "control block parity does not match the tweaked output key", info

    tx = canonical_spend_tx(domain, Q, locktime, sequence)
    witness = list(witness)
    # BIP-341: an annex is the topmost stack element and its first byte is 0x50.
    # The LUD-25 witness stack is [*witness, script, control]; the control block
    # always starts 0xc0/0xc1, so a well-formed cw1 can never carry an annex.
    assembled = witness + [script, control]
    if len(assembled) >= 2 and assembled[-1][:1] == b"\x50":
        return False, "annex is not valid for a note spend", info
    ok, reason = verify_leaf(script, witness, tx, 0, canonical_prevouts(Q),
                             allow_disabled_opcodes=allow_disabled_opcodes)
    info["reason"] = reason
    info["stack"] = reason
    return ok, reason, info


def check_timelock_policy(locktime: int, sequence: int, now: int = None,
                          credited_at: int = None):
    """LUD-25 "Timelocks" -- the SERVICE-clock part. CUSTODIAL, not consensus.

    * a non-zero nLockTime MUST be a Unix time (>= 500,000,000): block heights
      MUST be refused;
    * an enabled relative lock MUST carry BIP-68's TIME-type flag: block counts
      MUST be refused;
    * SERVICE MUST reject a locktime in the future, or a relative lock that has
      not yet elapsed since SERVICE credited the note.
    """
    problems = []
    if locktime != 0:
        if locktime < LOCKTIME_THRESHOLD:
            problems.append("nLockTime %d is a block height; LUD-25 requires a Unix time (>= 500000000)" % locktime)
        if now is not None and locktime > now:
            problems.append("nLockTime %d is in the future (now=%d)" % (locktime, now))
    relative = (sequence & SEQUENCE_LOCKTIME_DISABLE_FLAG) == 0 and sequence != SEQUENCE_FINAL
    if relative:
        if not (sequence & SEQUENCE_LOCKTIME_TYPE_FLAG):
            problems.append("nSequence %#x enables a BLOCK-count relative lock; LUD-25 requires the BIP-68 time-type flag" % sequence)
        if credited_at is not None and now is not None:
            span = credited_at + (sequence & 0xFFFF) * 512
            if span > now:
                problems.append("relative lock not yet elapsed (%d > %d)" % (span, now))
    return (len(problems) == 0), "; ".join(problems)
