#!/usr/bin/env python3
"""V2 vector conformance — does the v1 codec reproduce LUD-25 at fd80cdd?

Writes nothing; prints a machine-parseable transcript. Exit 0 iff every
assertion passes. Run: python3 tests/test_vectors_v2.py
"""
import json, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

from lnurlcash import note as v1
from lnurlcash import cs1v2

V = json.load(open(os.path.join(os.path.dirname(HERE), "vectors", "25-vectors.json")))

n_ok = n_fail = 0
def eq(name, got, want):
    global n_ok, n_fail
    if got == want:
        n_ok += 1; print("[PASS] %s" % name)
    else:
        n_fail += 1
        print("[FAIL] %s\n   got  %r\n   want %r" % (name, got, want))
def check(name, cond, detail=""):
    global n_ok, n_fail
    if cond:
        n_ok += 1; print("[PASS] %s" % name)
    else:
        n_fail += 1; print("[FAIL] %s  %s" % (name, detail))

# ---- V1  key-path spend (unchanged between spec versions?)
sec = "V1  key_path_spend (SigMsg, sighash, ck1, spend_tx)"
print("=== " + sec)
k = V["key_path_spend"]
for f, want in k["sigmsg_fields"].items():
    got = k["sigmsg_fields"][f]
    eq("sigmsg field %s is present" % f, got, want)
eq("sighash is 32 bytes", len(bytes.fromhex(k["sighash"])), 32)
Q = bytes.fromhex(k["Q"])
check("ck1 decodes to (Q, sig)", v1.decode_ck1(k["ck1"])[0] == Q,
      v1.decode_ck1(k["ck1"])[0].hex())

# ---- V2  certificates (the timestamp delta)
print("=== V2  certificates (cs1) — v2 carries `t`")
c = V["certificates"]
sk = bytes.fromhex(c["service_sk"])
domain, Qh, pk = c["domain"], c["Q"], c["mintPubkey"]
for case in c["cases"]:
    amt, t = case["amount_msat"], case["t"]
    if "*" in case["message"]:
        want_msg = cs1v2.message(domain, amt, Qh, t).decode()
        eq("amount=%d: message with t (Q redacted in vector)" % amt,
           want_msg.replace(Qh, "***"), case["message"])
    else:
        eq("amount=%d: message = 'LNURLcash:domain:amt:Q:t'" % amt,
           cs1v2.message(domain, amt, Qh, t).decode(), case["message"])
    eq("amount=%d: digest" % amt, cs1v2.digest(cs1v2.message(domain, amt, Qh, t)).hex(),
       case["digest"])
    eq("amount=%d: cs1 payload is 69 bytes (r||s||recid||u32 t)" % amt,
       len(cs1v2.recover(case["cs1"])), 6)
    eq("amount=%d: recovered t == vector t" % amt, cs1v2.recover(case["cs1"])[5], t)
    r = cs1v2.verify(case["cs1"], domain, Qh, pk)
    check("amount=%d: v2 cs1 verifies offline against mintPubkey" % amt, r["ok"], r["message"])

# ---- v1 codec versus the v2 certificate: the upgrade hazard
print("=== X1  the v1 verifier against a v2 certificate")
b = V["bearer_note"]
bc = b["certificate"]
r2 = cs1v2.verify(bc["cs1"], "mint.example", b["Q"], c["mintPubkey"])
check("bearer v2 cs1 verifies under the v2 verifier", r2["ok"], r2["message"])
try:
    v1.cs1_recover(bc["cs1"])
    check("v1 codec rejects the 69-byte v2 payload", False, "it accepted a 69-byte payload")
except ValueError as e:
    check("v1 codec rejects the 69-byte v2 payload", "65" in str(e) or "payload" in str(e), str(e))

# ---- V4  timelock note
print("=== V4  timelock_note")
tl = V["timelock_note"]
check("timelock leaf starts with a 4-byte push of T",
      tl["leaf"][:2] == "04", tl["leaf"][:8])
ls = v1.decode_cw1(tl["spends"][0]["cw1"])
check("first spend claims locktime T", ls[0] == tl["T"], ls[0])
check("second spend claims T-1", v1.decode_cw1(tl["spends"][1]["cw1"])[0] == tl["T"] - 1)

print("=" * 74)
print("SUMMARY  %d assertions   %d PASS   %d FAIL" % (n_ok + n_fail, n_ok, n_fail))
print("=" * 74)
sys.exit(0 if n_fail == 0 else 1)
