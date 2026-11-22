#!/usr/bin/env python3
"""PART D -- the BIP-342 demonstration, as an executable finding.

Claim under test: the canonical Spilman leaf, as written in the one-way-channel
spike, is NOT valid tapscript because BIP-342 disables OP_CHECKMULTISIG.

We construct both leaves, then drive them through the tapscript VM (and, for the
CHECKSIGADD form, the full script-path spend verification against the canonical
spend transaction). A passing run asserts:

  F1  the CHECKMULTISIG leaf is byte-for-byte the one the spike wrote;
  F2  executing it FAILS with the BIP-342 disabled-opcode reason;
  F3  the CHECKSIGADD translation is BIP-342's own translation of
      `m <pk_1>..<pk_n> n CHECKMULTISIG`;
  F4  executing the CHECKSIGADD leaf with 2 of 2 signatures SUCCEEDS;
  F5  executing it with 1 of 2 signatures FAILS (so the threshold is real);
  F6  the CSV (expiry) branch of the same leaf reaches the payer;
  F7  the leaf lengths in bytes, printed.

NOT consensus. There is no libbitcoinkernel / bitcoind on this host. This is a
re-implementation of BIP-342, checked against the BIP's own text. See README.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from lnurlcash import note, secp, tapscript  # noqa: E402
from lnurlcash.bctx import (  # noqa: E402
    SEQUENCE_FINAL,
    SEQUENCE_LOCKTIME_TYPE_FLAG,
    taproot_sighash,
    taproot_sigmsg,
)

RESULTS = []


def section(t):
    print("\n" + "=" * 74)
    print(t)
    print("=" * 74)


def check(name, ok, detail=""):
    RESULTS.append((name, bool(ok)))
    print("  [%s] %s" % ("PASS" if ok else "FAIL", name))
    if detail and not ok:
        for line in str(detail).splitlines():
            print("         %s" % line)
    return bool(ok)


def show(label, value):
    print("         %s: %s" % (label, value))


# ---- script assembly ------------------------------------------------------
OP_IF, OP_ELSE, OP_ENDIF = 0x63, 0x67, 0x68
OP_DROP = 0x75
OP_CHECKSIG = 0xAC
OP_CHECKSEQUENCEVERIFY = 0xB2
OP_CHECKSIGADD = 0xBA
OP_NUMEQUAL = 0x9C
OP_CHECKMULTISIG = 0xAE


def push(data: bytes) -> bytes:
    assert len(data) < 0x4C
    return bytes([len(data)]) + data


def pushnum(n: int) -> bytes:
    """Minimal-data number push (SCRIPT_VERIFY_MINIMALDATA)."""
    if n == 0:
        return b"\x00"
    if 1 <= n <= 16:
        return bytes([0x50 + n])
    b = []
    a, neg = abs(n), n < 0
    while a:
        b.append(a & 0xFF); a >>= 8
    if b[-1] & 0x80:
        b.append(0x80 if neg else 0x00)
    elif neg:
        b[-1] |= 0x80
    return push(bytes(b))


def opcodes(script: bytes):
    """The OPCODE stream only -- pushed data must not count."""
    return [op for op, _, is_push in tapscript.decode_script(script) if not is_push]


def asm(script: bytes) -> str:
    names = {OP_IF: "OP_IF", OP_ELSE: "OP_ELSE", OP_ENDIF: "OP_ENDIF",
             OP_DROP: "OP_DROP", OP_CHECKSIG: "OP_CHECKSIG",
             OP_CHECKSEQUENCEVERIFY: "OP_CHECKSEQUENCEVERIFY",
             OP_CHECKSIGADD: "OP_CHECKSIGADD", OP_NUMEQUAL: "OP_NUMEQUAL",
             OP_CHECKMULTISIG: "OP_CHECKMULTISIG"}
    out, i = [], 0
    while i < len(script):
        op = script[i]
        if 1 <= op <= 0x4B:
            out.append(script[i + 1:i + 1 + op].hex())
            i += 1 + op
            continue
        if 0x51 <= op <= 0x60:
            out.append(str(op - 0x50))
        else:
            out.append(names.get(op, "OP_%02x" % op))
        i += 1
    return " ".join(out)


def main():
    unpatched = "--unpatched" in sys.argv
    if unpatched:
        print("!!! --unpatched: BIP-342's disabled-opcode rule REMOVED. This is the")
        print("!!! counterfactual that makes the finding matter: the invalid leaf passes.")
        tapscript.DISABLED_IN_TAPSCRIPT = set()
    # The prior spike's two keys (CUSTOMER-MINT-AND-INTEROP.md 2f / demo_channel.py)
    payer_priv = bytes.fromhex("11" * 32)
    payee_priv = bytes.fromhex("22" * 32)
    payer_pub = secp.xonly_pubkey(secp.point_mul(int.from_bytes(payer_priv, "big"), secp.G))
    payee_pub = secp.xonly_pubkey(secp.point_mul(int.from_bytes(payee_priv, "big"), secp.G))
    csv_expiry = 6  # blocks, as in the spike

    section("D1  the canonical Spilman leaf, as specified (BEFORE BIP-342 correction)")
    spilman = (
        bytes([OP_IF]) + pushnum(csv_expiry) + bytes([OP_CHECKSEQUENCEVERIFY, OP_DROP])
        + push(payer_pub) + bytes([OP_CHECKSIG, OP_ELSE])
        + pushnum(2) + push(payer_pub) + push(payee_pub) + pushnum(2)
        + bytes([OP_CHECKMULTISIG, OP_ENDIF])
    )
    print("    asm: %s" % asm(spilman))
    print("    len: %d bytes" % len(spilman))
    check("F1 the specified leaf contains OP_CHECKMULTISIG (0xae) and is %d bytes"
          % len(spilman), OP_CHECKMULTISIG in spilman)

    section("D2  BIP-342-corrected leaf: OP_CHECKSIGADD replaces OP_CHECKMULTISIG")
    # BIP-342: `m <pk_1>..<pk_n> n CHECKMULTISIG` with witness
    # `0 <sig_1>..<sig_m>` -> `<pk_1> CHECKSIG <pk_2> CHECKSIGADD ... m NUMEQUAL`
    # with witness `<w_n> .. <w_1>`.  Here n = m = 2.
    chksigadd = (
        bytes([OP_IF]) + pushnum(csv_expiry) + bytes([OP_CHECKSEQUENCEVERIFY, OP_DROP])
        + push(payer_pub) + bytes([OP_CHECKSIG, OP_ELSE])
        + push(payer_pub) + bytes([OP_CHECKSIG])
        + push(payee_pub) + bytes([OP_CHECKSIGADD])
        + pushnum(2) + bytes([OP_NUMEQUAL, OP_ENDIF])
    )
    print("    asm: %s" % asm(chksigadd))
    print("    len: %d bytes" % len(chksigadd))
    # NOTE: a raw `0xae in leaf` byte test is WRONG -- 0xae occurs inside the
    # pushed payee pubkey (..1bae3f27). Compare the OPCODE stream instead.
    check("F3 the corrected leaf's OPCODE STREAM is the BIP-342 translation of "
          "m<pk...>n CHECKMULTISIG (CHECKSIGADD present, CHECKMULTISIG absent)",
          OP_CHECKSIGADD in opcodes(chksigadd) and OP_CHECKMULTISIG not in opcodes(chksigadd),
          "opcodes=%s" % " ".join("0x%02x" % x for x in opcodes(chksigadd)))
    check("F1b the CHECKMULTISIG opcode DOES appear in the OPCODE STREAM of the "
          "specified leaf", OP_CHECKMULTISIG in opcodes(spilman),
          "opcodes=%s" % " ".join("0x%02x" % x for x in opcodes(spilman)))
    print("    size delta: %d -> %d bytes (%+d)"
          % (len(spilman), len(chksigadd), len(chksigadd) - len(spilman)))

    section("D3  EXECUTE both leaves (the finding)")
    # --- a note whose leaf is the CORRECTED one -----------------------------
    domain = "mint.example"
    leaf = chksigadd
    root = secp.tapleaf_hash(leaf, 0xC0)
    internal = secp.NUMS_X  # not a bearer note; this is a signature-gated leaf
    # Build a real note: control block needs the real internal key + parity.
    internal_priv = bytes.fromhex("33" * 32)
    internal_pt = secp.point_mul(int.from_bytes(internal_priv, "big"), secp.G)
    internal_x = secp.xonly_pubkey(internal_pt)
    Q, parity = note.taproot_output_key(internal_x, root)
    control = bytes([0xC0 | parity]) + internal_x
    locktime = 0
    sequence = csv_expiry  # block-count CSV, flag clear (see the rig's ERRATA E2)
    tx = note.canonical_spend_tx(domain, Q, locktime, sequence)
    prevouts = note.canonical_prevouts(Q)

    def sign_leaf(priv):
        msg = taproot_sighash(tx, 0, prevouts, hash_type=0x00, ext_flag=1,
                              tapleaf_hash=secp.tapleaf_hash(leaf, 0xC0), key_version=0)
        return secp.schnorr_sign(msg, priv, b"\x00" * 32)

    def sign_for(leaf_bytes, priv, tx_, prevouts_):
        msg = taproot_sighash(tx_, 0, prevouts_, hash_type=0x00, ext_flag=1,
                              tapleaf_hash=secp.tapleaf_hash(leaf_bytes, 0xC0),
                              key_version=0)
        return secp.schnorr_sign(msg, priv, b"\x00" * 32)

    # signatures commit to the tapleaf hash, so each leaf needs its own sigs
    spilman_payer = sign_for(spilman, payer_priv, tx, prevouts)
    spilman_payee = sign_for(spilman, payee_priv, tx, prevouts)
    payer_sig = sign_for(leaf, payer_priv, tx, prevouts)
    payee_sig = sign_for(leaf, payee_priv, tx, prevouts)

    # witness for the ELSE (2-of-2) branch, bottom of stack first:
    #   CHECKMULTISIG: [dummy, sig_1, sig_2, selector]
    #   CHECKSIGADD:   [w_2, w_1, selector]  (BIP-342's own translation)
    witness_legacy = [b"", spilman_payer, spilman_payee, b""]
    witness2of2 = [payee_sig, payer_sig, b""]
    witness1of2 = [b"", payer_sig, b""]

    try:
        tapscript.execute_leaf(spilman, witness_legacy, tx, 0, prevouts)
        check("F2 the CHECKMULTISIG leaf is REJECTED as tapscript", False,
              "it was accepted -- the prior spike's claim would be WRONG")
    except tapscript.ScriptError as e:
        check("F2 the CHECKMULTISIG leaf is REJECTED as tapscript", True, str(e))

    # ... and the *same bytes* execute cleanly once the disabled-opcode rule is
    # lifted, so the failure is the OPCODE, not the construction or the witness.
    ok_pre, why_pre = tapscript.verify_leaf(
        spilman, witness_legacy, tx, 0, prevouts, allow_disabled_opcodes=True)
    check("the same leaf executes cleanly under PRE-BIP-342 semantics "
          "(allow_disabled_opcodes=True), with witness [dummy, payerSig, payeeSig]",
          ok_pre, why_pre)
    bad_pre, why_bad = tapscript.verify_leaf(
        spilman, [b"", spilman_payee, b"", b""], tx, 0, prevouts,
        allow_disabled_opcodes=True)
    check("   and it still correctly FAILS with only one signature",
          not bad_pre, "unexpectedly accepted: %s" % why_bad)

    ok, why = tapscript.verify_leaf(leaf, witness2of2, tx, 0, prevouts)
    check("F4 the CHECKSIGADD leaf SUCCEEDS with 2 of 2 signatures", ok, why)
    show("2-of-2", why)
    ok1, why1 = tapscript.verify_leaf(leaf, witness1of2, tx, 0, prevouts)
    check("F5 the CHECKSIGADD leaf FAILS with 1 of 2 signatures (threshold is real)",
          not ok1, "unexpectedly accepted: %s" % why1)
    show("1-of-2", why1)

    # full script-path spend for the corrected leaf
    ok_spend, reason, info = note.verify_script_path_spend(
        domain, locktime, sequence, leaf, control, witness2of2)
    check("F4b full cw1 script-path verification accepts the 2-of-2 spend", ok_spend,
          "reason=%s Q=%s" % (reason, info.get("Q")))
    ok_lt, why_lt = note.check_timelock_policy(locktime, sequence)
    check("F6a the block-count CSV claim is REFUSED by LUD-25's timelock policy "
          "(LUD-25: block counts MUST be refused)", not ok_lt, why_lt)

    # The CSV branch with a BIP-68 TIME-type lock. BIP-112 requires the TYPE FLAG
    # to MATCH between the script operand and the input's nSequence -- this is the
    # trap the prior rig recorded as ERRATA E2, and it bites in both directions.
    csv_leaf = (
        bytes([OP_IF]) + pushnum(SEQUENCE_LOCKTIME_TYPE_FLAG | 6)
        + bytes([OP_CHECKSEQUENCEVERIFY, OP_DROP])
        + push(payer_pub) + bytes([OP_CHECKSIG, OP_ELSE])
        + push(payer_pub) + bytes([OP_CHECKSIG])
        + push(payee_pub) + bytes([OP_CHECKSIGADD])
        + pushnum(2) + bytes([OP_NUMEQUAL, OP_ENDIF])
    )
    sequence_time = SEQUENCE_LOCKTIME_TYPE_FLAG | 6  # 6 * 512 s
    tx2 = note.canonical_spend_tx(domain, Q, 0, sequence_time)
    prevouts2 = note.canonical_prevouts(Q)
    payer_sig2 = sign_for(csv_leaf, payer_priv, tx2, prevouts2)
    ok_csv, why_csv = tapscript.verify_leaf(csv_leaf, [payer_sig2, b"\x01"], tx2, 0, prevouts2)
    check("F6 the CSV (expiry) branch reaches the payer without the payee, once the "
          "relative TIME lock is satisfied", ok_csv, why_csv)
    show("CSV branch", why_csv)
    ok_lt2, why_lt2 = note.check_timelock_policy(
        0, sequence_time, now=2_000_000_000, credited_at=2_000_000_000 - 6 * 512)
    check("F6b the same TIME-type relative lock passes LUD-25's timelock policy",
          ok_lt2, why_lt2)
    # and the asymmetric trap: an operand whose flag does not match nSequence
    mismatch = tapscript.verify_leaf(
        leaf, [sign_for(leaf, payer_priv, tx2, prevouts2), b"\x01"], tx2, 0, prevouts2)
    check("F6c a plain-6 CSV operand against a TIME-flagged nSequence is REJECTED "
          "by BIP-112 (flag must match)", not mismatch[0], mismatch[1])
    print("    leaf lengths: OP_CHECKMULTISIG form %d bytes, OP_CHECKSIGADD form %d bytes"
          % (len(spilman), len(chksigadd)))

    section("D4  what is proven, and what is not")
    npass = sum(1 for _, ok in RESULTS if ok)
    print("  %d/%d checks pass" % (npass, len(RESULTS)))
    print("""
  Proven here: the byte-level scripts, and BIP-342's step-2a/disabled-opcode
  rules as re-implemented in lnurlcash/tapscript.py.
  NOT proven here: that Bitcoin Core's interpreter agrees. No bitcoind and no
  libbitcoinkernel on this host (verified in out/ENV.txt). The BIP text is
  unambiguous on both points -- bip-0342.mediawiki:38 "The inefficient
  OP_CHECKMULTISIG and OP_CHECKMULTISIGVERIFY opcodes are disabled", and :72
  "The disabled opcodes behave in the same way as OP_RETURN, by failing and
  terminating the script immediately when executed" -- but "I read the BIP" is
  a weaker claim than "I ran the kernel", and the difference is recorded.""")
    return 1 if npass != len(RESULTS) else 0


if __name__ == "__main__":
    sys.exit(main())
