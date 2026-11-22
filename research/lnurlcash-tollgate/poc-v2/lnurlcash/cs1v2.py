"""LUD-25 v2 (PR lnurl/luds#301 @ fd80cdd, 2026-11-16) — cs1 carries a timestamp `t`.

The v1 codec in ../poc/ has no `t` anywhere. This module is the v2 delta only:
the message, the 69-byte payload, and the verifier — so the two versions can be
run against the SAME note and their behaviour compared arithmetically.

Spec text this implements (25.md @ fd80cdd):
  message = "LNURLcash:" || domain || ":" || string(amount_msat) || ":" || hex(Q) || ":" || string(t)
  digest  = sha256(sha256("Lightning Signed Message:" || message))
  cs1 payload = r || s || recovery-id || u32 t   (69 bytes, t big-endian)
  "A certificate attests that Q was outstanding with that value at t.
   SERVICE MUST NOT sign a t ahead of its own clock, and MAY answer a retry
   with a fresh one."
  "A sender SHOULD fetch a fresh certificate just before a handover, and a new
   holder SHOULD rotate (or merge) the note as soon as it is online."
"""
import hashlib

from . import note as v1
from .note import sha256, ecdsa_sign_recoverable, ecdsa_recover_pubkey
from .bech32m import encode_bytes, decode_bytes

LSM_PREFIX = v1.LSM_PREFIX


def message(domain: str, amount_msat: int, Q_hex: str, t: int) -> bytes:
    return (b"LNURLcash:" + domain.encode() + b":" + str(amount_msat).encode()
            + b":" + Q_hex.lower().encode() + b":" + str(t).encode())


def digest(msg: bytes) -> bytes:
    return sha256(sha256(LSM_PREFIX + msg))


def sign(domain: str, amount_msat: int, Q_hex: str, t: int, seckey: bytes) -> str:
    d = digest(message(domain, amount_msat, Q_hex, t))
    sig65 = ecdsa_sign_recoverable(d, seckey)
    recid, rs = sig65[0], sig65[1:]
    return encode_bytes(v1.cs1_hrp(amount_msat), rs + bytes([recid]) + t.to_bytes(4, "big"))


def recover(s: str):
    hrp, payload = decode_bytes(s)
    if len(payload) != 69:
        raise ValueError("v2 cs1 payload must be 69 bytes (r||s||recid||u32 t), got %d" % len(payload))
    if not hrp.startswith("cs"):
        raise ValueError("not a cs1")
    amount = v1.cs1_amount(hrp)
    r = int.from_bytes(payload[0:32], "big")
    s_ = int.from_bytes(payload[32:64], "big")
    recid = payload[64]
    if recid > 3:
        raise ValueError("invalid recovery id")
    t = int.from_bytes(payload[65:69], "big")
    return hrp, amount, r, s_, recid, t


def verify(s: str, domain: str, Q_hex: str, expected_pubkey_hex: str):
    """Offline verification steps 1-3 plus what the spec leaves to policy (step 4)."""
    hrp, amount, r, s_, recid, t = recover(s)
    msg = message(domain, amount, Q_hex, t)
    d = digest(msg)
    pk = ecdsa_recover_pubkey(d, r, s_, recid)
    return {
        "amount_msat": amount,
        "t": t,
        "message": msg.decode(),
        "digest": d.hex(),
        "recovered_pubkey": pk.hex(),
        "mintPubkey": expected_pubkey_hex.lower(),
        "ok": pk.hex() == expected_pubkey_hex.lower(),
    }


def verify_v1_against_v2(s_v2: str, Q_hex: str, expected_pubkey_hex: str):
    """The v1 verifier's verdict on the same bytes — the upgrade-compat direction."""
    return v1.cs1_verify(s_v2, Q_hex, expected_pubkey_hex)


def verify_v2_against_v1(s_v1: str, domain: str, Q_hex: str, expected_pubkey_hex: str):
    """A v1 certificate fed to the v2 verifier — the downgrade-compat direction."""
    return verify(s_v1, domain, Q_hex, expected_pubkey_hex)
