#!/usr/bin/env python3
"""What does the `t` in a v2 cs1 actually buy a TollGate? — arithmetic, not opinion.

Every claim below is computed from the fd80cdd vector set and the spec's own
sentences. Prints a transcript; exit 0 always (this is a measurement, not a test).
"""
import json, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

from lnurlcash import note as v1
from lnurlcash import cs1v2

V = json.load(open(os.path.join(os.path.dirname(HERE), "vectors", "25-vectors.json")))
C = V["certificates"]
SK = bytes.fromhex(C["service_sk"])          # the SERVICE's signing key (test key)
PK = C["mintPubkey"]
DOMAIN = C["domain"]
B = V["bearer_note"]
QB = B["Q"]
AMT = 1000

bar = "=" * 78
def hdr(s):
    print("\n" + bar); print(s); print(bar)

# ---------------------------------------------------------------- E1
hdr("E1  A certificate is renewable for free — `t` is not a scarcity signal")
print("""
Spec (25.md @ fd80cdd):
  * "SERVICE returns certificates on the informational GET of an outstanding note"
  * "SERVICE MUST NOT sign a t ahead of its own clock, and MAY answer a retry
     with a fresh one."
Neither sentence limits how many certificates one Q may have, nor how often.
So the only cost of a fresh t is one read-only GET. Measured, holding ONE note:
""")
certs = []
for i, t in enumerate([1800000000, 1800000060, 1800003600, 1800090000]):
    c = cs1v2.sign(DOMAIN, AMT, QB, t, SK)
    r = cs1v2.verify(c, DOMAIN, QB, PK)
    certs.append((t, c, r["ok"]))
    print("  t=%-12d  cs1=%s…  verifies=%s" % (t, c[:26], r["ok"]))
print("""
  → %d distinct certificates for the SAME unspent note, all valid, none costing a
    burn. An attacker who will double-spend at handover simply fetches a new one
    immediately before it. Freshness of `t` therefore measures how recently the
    SERVICE was polite, not how long the holder has held the note.""" % len(certs))

# ---------------------------------------------------------------- E2
hdr("E2  The window `t` bounds is attestation age, not possession age")
t_issue = 1800000000
# same holder, same note, same certificate age at the moment of handover
for held_s, label in [(0, "received just now"), (3600, "held 1 h"), (86400 * 30, "held 30 d")]:
    t_handover = t_issue + held_s
    cert = cs1v2.sign(DOMAIN, AMT, QB, t_handover, SK)      # fetched at handover
    age = t_handover - cs1v2.verify(cert, DOMAIN, QB, PK)["t"]
    print("  %-20s  held for %8d s -> cert age %d s  policy(age<300) ACCEPT"
          % (label, held_s, age))
print("""
  Note what is identical in all three rows: the holder. A thief who took the note
  30 days ago passes the same policy by fetching one certificate. `t` cannot
  distinguish "I just received this" from "I have held this for a month and have
  just re-certified it", because the signer knows neither fact.""")

# ---------------------------------------------------------------- E3
hdr("E3  What `t` DOES buy: certificate replay across domains is now closed")
v1cert = v1.cs1_sign(AMT, QB, SK)                      # v1: no domain, no t
print("  v1 certificate (no domain, no t):")
print("    at mint.example  ->", v1.cs1_verify(v1cert, QB, PK)["ok"])
print("    at gate.other    ->", v1.cs1_verify(v1cert, QB, PK)["ok"],
      " <- same cert, different SERVICE: v1 cannot tell")
v2cert = cs1v2.sign(DOMAIN, AMT, QB, t_issue, SK)
print("  v2 certificate (domain + t):")
print("    at mint.example  ->", cs1v2.verify(v2cert, DOMAIN, QB, PK)["ok"])
print("    at gate.other    ->", cs1v2.verify(v2cert, "gate.other", QB, PK)["ok"],
      " <- bound: message rebuilt with the wrong domain recovers a different key")
print("""
  This is a genuine improvement and it is the strongest thing v2 adds. Note it
  comes from `domain` being in the message, not from `t`.""")

# ---------------------------------------------------------------- E4
hdr("E4  The upgrade is a hard break: no version negotiation in cs1")
print("  v2 cs1 payload = 69 bytes;  v1 parser demands exactly 65.")
try:
    v1.cs1_recover(v2cert)
    print("  v1 parser ACCEPTED a v2 certificate  (unexpected)")
except ValueError as e:
    print("  v1 parser REJECTED a v2 certificate:", e)
try:
    cs1v2.recover(v1cert)
    print("  v2 parser accepted a v1 certificate")
except ValueError as e:
    print("  v2 parser REJECTED a v1 certificate:", e)
print("""
  Both directions reject, and the HRP is the same `cs<amount>`, so a wallet
  cannot tell which generation a certificate is without trying both. The spec
  says nothing about a transition: no version tag, no "MUST accept both", no
  flag day. Any deployed v1 certificate becomes unverifiable the day a client
  implements v2 — silently, as a signature failure. For a bearer note that is
  value loss, not a cosmetic incompatibility.""")

# ---------------------------------------------------------------- E5
hdr("E5  `t` widens the trust in SERVICE: it now attests a clock too")
print("""  v1 cs1 said: "this Q existed with this value".
  v2 cs1 says: "…as of t, by my clock".

  Consequences for a verifier:
    * SERVICE can backdate (sign t in the past) with no detection — the message
      only binds t to the signature, not t to any fact.
    * SERVICE can extend a note's apparent freshness by re-signing; a hostile or
      compromised mint makes every note look one minute old, forever.
    * A verifier needs its own trustworthy clock to apply any age policy at all.
      A TollGate has one (it is online, NTP-synced, and sells the link). The
      customer device that has no internet may not — but in the TollGate flow the
      gate is the verifier, so this is the direction that happens to be fine.

  The spec's clock requirement is one-directional only:
      "SERVICE MUST NOT sign a t ahead of its own clock"
  There is no MUST on the receiver, no maximum age, no required rounding, and no
  statement of what a receiver SHOULD do with an old-but-valid t. §4 of the
  offline-verification steps says only "weighs the age of t against its own
  policy" — i.e. the security parameter of the whole feature is left undefined.""")

# ---------------------------------------------------------------- E6
hdr("E6  Encoding facts a TollGate integration must live with")
t_max = 2**32 - 1
print("  t field       : u32 seconds, big-endian, appended after the recovery id")
print("  payload       : 65 -> 69 bytes (+4)")
print("  cs1 length    : %d chars (amount 1000 msat) -> %d chars"
      % (len(v1cert), len(v2cert)))
print("  t range       : 0 … %d  (= %s UTC)" % (t_max, "2106-02-07T06:28:15Z"))
print("  t is minimal base-10 in the message, u32 in the payload — two encodings")
print("  of one value, so a re-encode bug is possible where a decimal-format bug")
print("  would be caught: 0000001 and 1 differ in the message, not in the payload.")

# ---------------------------------------------------------------- E7
hdr("E7  The two-merchant double spend that a timestamp does not touch")
print("""
  Alice holds one note, Q_B, 1000 msat. She is at two TollGates with no
  connectivity between them (the normal case: they are separate captive portals).

    t=0     Alice takes a FRESH certificate for Q_B from the mint (one GET)
    t=1     Gate A: offline verify — spend opens Q_B, cert fresh  -> ACCEPT, open
    t=2     Gate B: offline verify — same bytes, same fresh cert  -> ACCEPT, open
    t=3     Mint: Q_B is still OUTSTANDING (nobody rotated)
            One of A or B loses its 1000 msat when it next reaches the mint.

  The only spec-level defence is the "SHOULD rotate as soon as online" sentence.
  Rotation is a callback the GATE must issue, and if both gates issue it, the
  second is answered `already in use` — after it already gave away access.
  `t` is orthogonal: both gates see a certificate 2 seconds old and are correct
  to accept it.""")

print("\n" + bar)
print("VERDICT OF THE MEASUREMENT")
print(bar)
print("""
  `t` converts "proof of issuance" into "proof of issuance, recent as of the
  signer's clock". That closes two things (cross-domain certificate replay via
  `domain`, and unbounded-age certificates a naive verifier might accept) and
  closes neither of the two things a point-of-sale needs:
    (i)  non-consumption   — unchanged, still requires an online rotate
    (ii) possession age    — not attested, and not attestable by the mint
""")
