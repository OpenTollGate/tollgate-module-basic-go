#!/usr/bin/env python3
"""LUD-25 / LUD-26 official test-vector suite.

Stdlib only, no network. Run: python3 tests/test_vectors.py [--broken]

Every [PASS]/[FAIL] line names the vector field and the LUD section it comes
from. A [FAIL] is a finding, not something to hide: the suite never edits a
vector, and never asserts less than the vector states.
"""

import hashlib
import hmac
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from lnurlcash import bech32m, bip32, note, secp, tapscript  # noqa: E402
from lnurlcash.bctx import SEQUENCE_FINAL, taproot_sigmsg  # noqa: E402

VECDIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "vectors")

RESULTS = []
_SECTION = [""]


def section(title):
    _SECTION[0] = title
    print("\n" + "=" * 74)
    print(title)
    print("=" * 74)


def check(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    tag = "[PASS]" if ok else "[FAIL]"
    print("  %s %s" % (tag, name))
    if detail and not ok:
        for line in str(detail).splitlines():
            print("         %s" % line)
    return bool(ok)


def run_section(fn, *args):
    """Run a section; if it raises, record that as a FAIL rather than aborting,
    so a defect cannot hide the remaining assertions behind a traceback."""
    try:
        fn(*args)
    except Exception as e:  # noqa: BLE001
        import traceback
        check("SECTION %s raised %s" % (fn.__name__, type(e).__name__), False,
              "%s\n%s" % (e, traceback.format_exc().strip().splitlines()[-3]))


def eq(name, got, want, note_=""):
    return check(name, got == want, "got  %s\nwant %s\n%s" % (got, want, note_))


# --------------------------------------------------------------------------
def key_path_spend(v):
    section("V1  key_path_spend  --  LUD-25 'Output keys and spends' / 'Key-path notes'")
    Q = bytes.fromhex(v["Q"])
    sk = bytes.fromhex(v["sk"])
    domain = v["domain"]

    eq("cp1<Q> encodes to the vector cp1 (LUD-25 Encoding, 32-byte Q)",
       note.encode_cp1(Q), v["cp1"])
    eq("cp1 decodes back to Q and Q is a curve-point x (LUD-25 MUST-reject)",
       note.decode_cp1(v["cp1"]).hex(), v["Q"])

    txid = note.mint_txid(domain)
    # LUD-25 defines the prevout as `tagged_hash("LNURLcash/mint", domain)`
    # itself, and the vector's `prevout_txid` is those bytes in the order the
    # transaction serializer emits them (it is the literal prefix of the
    # vector's own `spend_tx`), i.e. NOT reversed the way a display txid is.
    eq("prevout_txid = tagged_hash('LNURLcash/mint', domain) (canonical spend table)",
       txid.hex(), v["prevout_txid"])
    check("the vector's spend_tx carries that prevout in this byte order",
          v["spend_tx"].startswith("02000000000101" + v["prevout_txid"]),
          v["spend_tx"][:80])

    eq("spent output scriptPubKey is OP_1 <Q> (canonical spend table)",
       note.spent_script_pubkey(Q).hex(), v["spent_script_pubkey"])

    tx = note.canonical_spend_tx(domain, Q)
    prevouts = note.canonical_prevouts(Q)
    msg = taproot_sigmsg(tx, 0, prevouts)
    eq("full SigMsg == vector sigmsg (BIP-341 SigMsg, ext_flag=0, SIGHASH_DEFAULT)",
       msg.hex(), v["sigmsg"])
    # BIP-341: 1 (hash_type) + 4 + 4 + 32*5 (sub-hashes) + 1 (spend_type) + 4
    # (input_index) = 174 for SIGHASH_DEFAULT with no annex; at most 206.
    eq("SigMsg total length is 174 bytes for SIGHASH_DEFAULT / no annex",
       len(msg), 174)

    # the individual SigMsg fields the vector lists
    f = v["sigmsg_fields"]
    eq("SigMsg.hash_type", "%02x" % msg[0], f["hash_type"])
    eq("SigMsg.nVersion (vector prints big-endian bytes)", msg[1:5].hex(), f["nVersion"])
    eq("SigMsg.nLockTime", msg[5:9].hex(), f["nLockTime"])
    eq("SigMsg.sha_prevouts", msg[9:41].hex(), f["sha_prevouts"])
    eq("SigMsg.sha_amounts", msg[41:73].hex(), f["sha_amounts"])
    eq("SigMsg.sha_scriptpubkeys", msg[73:105].hex(), f["sha_scriptpubkeys"])
    eq("SigMsg.sha_sequences", msg[105:137].hex(), f["sha_sequences"])
    eq("SigMsg.sha_outputs", msg[137:169].hex(), f["sha_outputs"])
    eq("SigMsg.spend_type (ext_flag*2 + annex_present)", "%02x" % msg[169], f["spend_type"])
    eq("SigMsg.input_index", msg[170:174].hex(), f["input_index"])

    from lnurlcash.bctx import taproot_sighash
    digest = taproot_sighash(tx, 0, prevouts)
    eq("hash_TapSighash == vector sighash", digest.hex(), v["sighash"])

    sig = secp.schnorr_sign(digest, sk, bytes.fromhex(v["aux_rand"]))
    eq("BIP-340 sign(sk, sighash, aux=0) == vector sig (all-zero aux_rand, LUD-25)",
       sig.hex(), v["sig"])

    ok, _ = note.verify_key_path_spend(domain, Q, sig)
    check("verify_key_path_spend accepts the vector signature", ok)
    ok_wrong, why = note.verify_key_path_spend("other.example", Q, sig)
    check("the same ck1 FAILS at any other domain (LUD-25 'so a signature can never be replayed')",
          not ok_wrong, "unexpectedly accepted at other.example: %s" % why)

    eq("ck1 = bech32m(Q || sig)", note.encode_ck1(Q, sig), v["ck1"])
    dq, dsig = note.decode_ck1(v["ck1"])
    eq("ck1 payload is exactly 96 bytes = Q || sig (LUD-25 Encoding)",
       (dq.hex(), dsig.hex()), (v["Q"], v["sig"]))

    ser = tx.serialize_with_witness([[sig]])
    eq("canonical spend tx (nVersion 2, empty scriptSig, vout[0] value 0, witness [sig])",
       ser.hex(), v["spend_tx"])
    return True


def certificates(v):
    section("V2  certificates (cs1)  --  LUD-25 'Offline verification'")
    sk = bytes.fromhex(v["service_sk"])
    Q = v["Q"]
    for case in v["cases"]:
        amt = case["amount_msat"]
        msg = note.cs1_message(amt, Q)
        eq("amount=%d: message = 'LNURLcash:' || amount || ':' || hex(Q)" % amt,
           msg.decode(), case["message"])
        dig = note.cs1_digest(msg)
        eq("amount=%d: digest = sha256(sha256('Lightning Signed Message:' || message))" % amt,
           dig.hex(), case["digest"])
        full = note.ecdsa_sign_recoverable(dig, sk)
        sig65 = full[1:].hex() + "%02x" % full[0]
        eq("amount=%d: RFC6979 recoverable sig with the recovery id moved to the end" % amt,
           sig65, case["signature"])
        got = note.cs1_sign(amt, Q, sk)
        eq("amount=%d: cs1 encoding (bech32m, HRP cs+amount, payload r||s||recid)" % amt,
           got, case["cs1"])
        # LUD-25 note: one vector redacts hex(Q) inside `message` because the
        # same Q already appears above; the digest/signature/cs1 are unredacted,
        # so the full offline-verification path is still checkable end to end.
        r = note.cs1_verify(case["cs1"], Q, v["mintPubkey"])
        check("amount=%d: cs1_verify rebuilds the message and recovers mintPubkey" % amt,
              r["ok"], "recovered %s want %s" % (r["recovered_pubkey"], v["mintPubkey"]))
        eq("amount=%d: cs1 HRP decodes back to amount_msat (BOLT-11 HRP rules)" % amt,
           r["amount_msat"], amt)
        eq("amount=%d: rebuilt message is 'LNURLcash:%d:%s'" % (amt, amt, Q),
           r["message"], "LNURLcash:%d:%s" % (amt, Q))
        eq("amount=%d: rebuilt digest == vector digest" % amt, r["digest"], case["digest"])
    eq("cs1_hrp(1000) is BOLT-11 minimal form", note.cs1_hrp(1000), "cs10n")
    eq("cs1_hrp(21000000) is BOLT-11 minimal form", note.cs1_hrp(21000000), "cs210u")


def bearer_note(v):
    section("V3  bearer_note  --  LUD-25 'Bearer notes' / 'Short forms'")
    pre = bytes.fromhex(v["preimage"])
    n = note.bearer_note(pre)

    eq("h = sha256(preimage)", n["h"], v["h"])
    eq("leaf = OP_SHA256 <h> OP_EQUAL  (a8 20 <h> 87)", n["leaf"], v["leaf"])
    eq("tapleaf_hash (leaf version 0xc0)", n["tapleaf_hash"], v["tapleaf_hash"])
    eq("H is the BIP-341 NUMS point", n["H"], v["H"])
    eq("tweak = tagged_hash('TapTweak', H || tapleaf_hash(leaf))", n["tweak"], v["tweak"])
    eq("Q = x(lift_x(H) + tweak*G)", n["Q"], v["Q"])
    eq("control = (0xc0 | parity(Q)) || H", n["control_block"], v["control_block"])
    eq("cp1<Q>", n["cp1"], v["cp1"])
    eq("cw1 (script path, locktime 0, sequence 0xffffffff, witness [preimage])",
       n["cw1"], v["cw1"])
    eq("cw1 is exactly 192 characters (LUD-25 Encoding table)", len(v["cw1"]), 192)
    eq("cw1 is exactly 192 characters, recomputed", len(n["cw1"]), 192)

    lt, seq, script, control, wit = note.decode_cw1(v["cw1"])
    eq("decode_cw1: u32 locktime big-endian", lt, 0)
    eq("decode_cw1: u32 sequence big-endian", seq, SEQUENCE_FINAL)
    eq("decode_cw1: script", script.hex(), v["leaf"])
    eq("decode_cw1: control block", control.hex(), v["control_block"])
    eq("decode_cw1: witness items bottom-of-stack-first = [preimage]", wit, [pre])

    eq("k1 SHORT FORM: sha256-only wallet rebuilds the same cw1 from hex preimage",
       note.bearer_cw1_from_hex_preimage(v["preimage"]), v["cw1"])
    eq("cp1 SHORT FORM: hex h rebuilds the same Q",
       note.bearer_Q_from_hex_h(v["h"]).hex(), v["Q"])
    eq("a short form is 64 hex chars and collides with no cp1/ck1/cw1",
       (len(v["h"]), note.is_short_form(v["h"]), note.is_short_form(v["cp1"])),
       (64, True, False))

    ok, reason, info = note.verify_script_path_spend(
        "mint.example", lt, seq, script, control, wit)
    check("cw1 script-path spend verifies against the canonical spend tx", ok,
          "reason=%s info=%s" % (reason, json.dumps(info, indent=8)))
    eq("Q recomputed from the control block == the vector Q", info.get("Q"), v["Q"])
    ok2, _, info2 = note.verify_script_path_spend(
        "totally.other.domain", lt, seq, script, control, wit)
    check("a bearer cw1 opens Q at ANY domain (LUD-25: 'a leaf that checks no signature"
          " is bound to no domain')", ok2 and info2.get("Q") == v["Q"])

    # the preimage is the only secret; a wrong preimage must not spend
    bad, why, _ = note.verify_script_path_spend(
        "mint.example", lt, seq, script, control, [b"\x00" * 32])
    check("a wrong preimage FAILS to spend Q (hashlock is load-bearing)", not bad, why)

    # certificate for the bearer note
    c = v["certificate"]
    eq("bearer certificate message = 'LNURLcash:1000:' || hex(Q)",
       note.cs1_message(c["amount_msat"], n["Q"]).decode(), c["message"])
    eq("bearer certificate digest", note.cs1_digest(note.cs1_message(
        c["amount_msat"], n["Q"])).hex(), c["digest"])
    eq("bearer cs1 from the certificates SERVICE key",
       note.cs1_sign(c["amount_msat"], n["Q"], bytes.fromhex(
           __import__("json").load(open(os.path.join(VECDIR, "25-vectors.json")))
           ["certificates"]["service_sk"])), c["cs1"])
    r = note.cs1_verify(c["cs1"], n["Q"], "035acdbd57663f858be6d61ec4bfcbc99492699010f1451e30a6550f26295e813d")
    check("bearer cs1_verify recovers mintPubkey (offline verification step 3)", r["ok"],
          "recovered %s" % r["recovered_pubkey"])
    check("note_url shape: lnurlw://<domain>/w?k1=<spend>&c=<cs1>",
          v["note_url"] == "lnurlw://mint.example/w?k1=%s&c=%s" % (v["preimage"], c["cs1"]))
    return True


def negative_vectors():
    section("V4  MUST-reject rules  --  LUD-25 Encoding table (not in the vector file)")
    # cp1 whose Q is not a curve point
    bad = b"\x00" * 32
    try:
        note.decode_cp1(note.encode_cp1(bad))
        check("cp1 with a non-curve-point Q is rejected", False, "accepted x=0")
    except ValueError as e:
        check("cp1 with a non-curve-point Q is rejected", "not the x coordinate" in str(e), str(e))
    try:
        note.decode_ck1(bech32m.encode_bytes("ck", b"\x11" * 95))
        check("ck1 with a payload of any length but 96 bytes is rejected", False)
    except ValueError:
        check("ck1 with a payload of any length but 96 bytes is rejected", True)
    try:
        note.decode_cw1(bech32m.encode_bytes("cw", (0).to_bytes(4, "big") + (0xffffffff).to_bytes(4, "big") + b"\x00\x03abc"))
        check("cw1 with a length prefix that does not consume the payload is rejected", False)
    except ValueError:
        check("cw1 with a length prefix that does not consume the payload is rejected", True)
    try:
        note.decode_cw1(bech32m.encode_bytes("cw", (0).to_bytes(4, "big") + (0xffffffff).to_bytes(4, "big") + b"\x00\x01a\x00\x21" + b"\xc0" * 33))
        check("cw1 missing the witness is accepted (witness-free leaf is legal)", True)
    except ValueError as e:
        check("cw1 missing the witness is accepted (witness-free leaf is legal)", False, str(e))
    try:
        bech32m.decode("cp1" + "q" * 61)
        check("a bad bech32m checksum is rejected (BIP-350)", False)
    except bech32m.Bech32Error:
        check("a bad bech32m checksum is rejected (BIP-350)", True)
    # an OP_SUCCESSx-carrying leaf must be refused by SERVICE even though
    # consensus accepts it unconditionally
    script = bytes([0x51, 0xBB])  # OP_1 OP_SUCCESS187
    ok, why, _ = note.verify_script_path_spend("mint.example", 0, SEQUENCE_FINAL,
                                               script, b"\xc0" + b"\x00" * 32 + b"\x11" * 32, [])
    check("a leaf with OP_SUCCESSx outside pushed data is REJECTED by SERVICE (LUD-25)",
          not ok and "OP_SUCCESSx" in why, why)
    # timelock policy (LUD-25 Timelocks: block heights and block counts refused)
    ok, why = note.check_timelock_policy(800000, 0)
    check("nLockTime as a BLOCK HEIGHT is refused", not ok, why)
    ok, why = note.check_timelock_policy(0, 6)
    check("nSequence as a BLOCK COUNT is refused (BIP-68 time-type flag required)", not ok, why)
    ok, why = note.check_timelock_policy(2000000000, 0, now=1000000000)
    check("nLockTime in the future is refused", not ok, why)
    ok, why = note.check_timelock_policy(1700000000, (1 << 22) | 10,
                                         now=1700000000 + 10 * 512 - 1,
                                         credited_at=1700000000)
    check("an enabled relative TIME lock that has not elapsed is refused", not ok, why)
    ok, why = note.check_timelock_policy(1700000000, (1 << 22) | 10,
                                         now=1700000000 + 10 * 512 + 1,
                                         credited_at=1700000000)
    check("the same relative TIME lock is accepted once elapsed", ok, why)


def lud26(v):
    section("V5  LUD-26 derivation + LUD-05 hashing  --  independent cross-check of"
            " LUD-25's sk/Q")
    # LUD-05's own published test vector, which needs no master key: it pins the
    # hmacSha256(hashingKey, domain) -> 4 big-endian uint32 step.
    hk05 = bytes.fromhex("7d417a6a5e9a6a4a879aeaba11a11838764c8fa2b959c242d43dea682b3e409b")
    mat = hmac.new(hk05, b"site.com", hashlib.sha256).digest()
    eq("LUD-05 test vector: pathSuffix = first 16 bytes of hmacSha256, 4 big-endian uint32",
       [int.from_bytes(mat[i * 4:i * 4 + 4], "big") for i in range(4)],
       [1588488367, 2659270754, 38110259, 4136336762])

    for entry in v["derivation"]:
        seed = bytes.fromhex(entry["seed"])
        master = bip32.from_seed(seed)
        # LUD-26: "cashHashingKey = derive(masterKey, m/139'/0)". The vectors pin
        # this to 139 HARDENED and 0 UNHARDENED (0' gives a different key).
        hk = bip32.derive_path(master, [139, 0], hardened=[True, False])
        eq("%s cashHashingKey = derive(m, m/139'/0)" % entry["seed"][:16],
           hk.privkey.hex(), entry["cashHashingKey"])
        material = hmac.new(hk.privkey, entry["domain"].encode(), hashlib.sha256).digest()
        d = [int.from_bytes(material[i * 4:i * 4 + 4], "big") for i in range(4)]
        eq("  (d1..d4) = first 16 bytes of hmacSha256(cashHashingKey, domain)",
           d, entry["d"])
        # LUD-26 prose says "this SERVICE's branch root, hardened". The vectors
        # require 139 HARDENED but d1..d4 UNHARDENED: see ERRATA E1.
        branch = bip32.derive_path(master, [139] + d,
                                   hardened=[True, False, False, False, False])
        eq("  p = derive(m, m/139'/d1/d2/d3/d4)  [d UNHARDENED, see ERRATA E1]",
           branch.privkey.hex(), entry["p"])
        eq("  chaincode", branch.chaincode.hex(), entry["chaincode"])
        Pc = secp.point_mul(int.from_bytes(branch.privkey, "big"), secp.G)
        Pcomp = bytes([2 + (Pc[1] & 1)]) + Pc[0].to_bytes(32, "big")
        eq("  P is on the curve", secp.is_on_curve(Pc), True)
        eq("  P (33-byte compressed) and its y parity", (Pcomp.hex(),
           "odd" if Pc[1] & 1 else "even"), (entry["P"], entry["P_y"]))
        eq("  cx1 = bech32m(P_x || chaincode)",
           bech32m.encode_bytes("cx", Pc[0].to_bytes(32, "big") + branch.chaincode),
           entry["cx1"])
        eq("  cx1 is 112 characters (LUD-26 watch-only export)", len(entry["cx1"]), 112)
        # ERRATA E2: LUD-26 writes `t = tagged_hash("LNURLcash/derive",
        # P || chaincode || ser32(purpose) || ser32(i))` without saying how P is
        # serialized. The vectors require the 32-byte X-ONLY P -- the same
        # encoding lift_x() consumes in `Q_i = lift_x(P) + t*G` -- while the
        # vector's own `P` field is the 33-byte COMPRESSED key. Using the vector's
        # P bytes verbatim reproduces nothing.
        Px = Pc[0].to_bytes(32, "big")
        for nt in entry["notes"]:
            t_note = secp.tagged_hash(
                "LNURLcash/derive",
                Px + branch.chaincode
                + nt["purpose"].to_bytes(4, "big") + nt["i"].to_bytes(4, "big"))
            eq("  purpose=%d i=%d: t = tagged_hash('LNURLcash/derive', P||chaincode||purpose||i)"
               % (nt["purpose"], nt["i"]), t_note.hex(), nt["t"])
            tv = int.from_bytes(t_note, "big") % secp.N
            # LUD-26 literally says `Q_i = lift_x(P) + t*G`. lift_x is the
            # EVEN-y lift, so for seed 1 (P_y odd) the note key is derived from
            # -P, not from the P that BIP-32 produced. Taking the prose at face
            # value is what reproduces the vectors; `sk_i = (n - p) + t` is the
            # matching private key. See ERRATA E3.
            Qi = secp.point_add(secp.lift_x(Px), secp.point_mul(tv, secp.G))
            Qcomp = bytes([2 + (Qi[1] & 1)]) + Qi[0].to_bytes(32, "big")
            eq("  purpose=%d i=%d: Q_i = lift_x(P) + t*G" % (nt["purpose"], nt["i"]),
               Qcomp.hex(), nt["Q"])
            p_int = int.from_bytes(branch.privkey, "big")
            sk_i = (p_int + tv) % secp.N if Pc[1] % 2 == 0 else ((secp.N - p_int) + tv) % secp.N
            eq("  purpose=%d i=%d: sk_i (parity-dependent formula)" % (nt["purpose"], nt["i"]),
               sk_i.to_bytes(32, "big").hex(), nt["sk"])
            eq("  purpose=%d i=%d: cp1<x(Q_i)>" % (nt["purpose"], nt["i"]),
               note.encode_cp1(Qi[0].to_bytes(32, "big")), nt["cp1"])
    v25 = json.load(open(os.path.join(VECDIR, "25-vectors.json")))
    n0 = v["derivation"][0]["notes"][0]
    eq("LUD-25's key-path sk IS LUD-26 derivation[0] purpose 0 index 0 sk",
       n0["sk"], v25["key_path_spend"]["sk"])
    eq("LUD-25's key-path Q IS LUD-26 derivation[0] purpose 0 index 0 Q "
       "(redacted to the 02||x form in 26-vectors)", n0["Q"][2:],
       v25["key_path_spend"]["Q"])
    check("LUD-25's key-path Q is the x-only part of that Q (LUD-25 key-path notes)",
          n0["Q"][2:] == v25["key_path_spend"]["Q"])

    reg = v["registration"]
    pw0 = v["derivation"][1]["notes"][0]
    pk0 = bytes.fromhex(pw0["Q"])[1:]  # x-only
    for kind in ("register", "unregister"):
        r = reg[kind]
        dig = hashlib.sha256(r["message"].encode()).digest()
        eq("registration.%s digest = sha256('LNURLcash:%s:' || domain || ':' || username)"
           % (kind, kind), dig.hex(), r["digest"])
        check("registration.%s BIP-340 sig verifies against pk_0 of that cx1 (LUD-26)"
              % kind, secp.schnorr_verify(dig, pk0, bytes.fromhex(r["sig"])),
              "pk0=%s" % pk0.hex())


def main():
    broken = "--broken" in sys.argv
    if broken:
        print("!!! --broken: THREE deliberate defects injected, to prove the suite can FAIL !!!")
        print("!!! D1  H is not the BIP-341 NUMS point (G's x used instead)")
        print("!!! D2  the bearer leaf ends OP_EQUALVERIFY (0x88) not OP_EQUAL (0x87)")
        print("!!! D3  the cs1 digest is single sha256, not sha256(sha256(...))")
        # D1: a VALID curve-point x, so nothing raises -- the vectors must simply
        # not match. (Using 0x00*32 would crash instead of failing, which would
        # hide the rest of the suite.)
        note.NUMS_X = secp.Gx.to_bytes(32, "big")
        # D2
        note.bearer_leaf = lambda h: b"\xa8\x20" + h + b"\x88"
        # D3
        note.cs1_digest = lambda m: __import__("hashlib").sha256(m).digest()
    v25 = json.load(open(os.path.join(VECDIR, "25-vectors.json")))
    v26 = json.load(open(os.path.join(VECDIR, "26-vectors.json")))
    print("LUD-25 test vectors: %s" % os.path.join(VECDIR, "25-vectors.json"))
    print("description: %s" % v25["description"])
    run_section(key_path_spend, v25["key_path_spend"])
    run_section(certificates, v25["certificates"])
    run_section(bearer_note, v25["bearer_note"])
    run_section(negative_vectors)
    run_section(lud26, v26)

    npass = sum(1 for _, ok, _ in RESULTS if ok)
    nfail = len(RESULTS) - npass
    print("\n" + "=" * 74)
    print("SUMMARY  %d assertions   %d PASS   %d FAIL" % (len(RESULTS), npass, nfail))
    if nfail:
        print("FAILED:")
        for name, ok, detail in RESULTS:
            if not ok:
                print("  - %s" % name)
    print("=" * 74)
    return 1 if nfail else 0


if __name__ == "__main__":
    sys.exit(main())
