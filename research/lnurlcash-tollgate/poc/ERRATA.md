# ERRATA — what building this POC against the official vectors actually caught

Every entry below was either a bug in this POC or an ambiguity in LUD-25/LUD-26
that only shows up when a real implementation is forced through the official
vectors. They are kept because a re-implementer will hit every one of them.

If this POC had used a *simulator*, or had stopped at "the spec looks
self-consistent", E1–E3 would have passed silently and the codec would have
been wrong against the vectors it was written to satisfy.

---

## E1 — LUD-26's branch-root prose contradicts the vectors: `139'/0`, not `139'/0'`

**Symptom:** `cashHashingKey` mismatched for every seed.

**Cause:** LUD-26 writes

```
cashHashingKey = derive(masterKey, m/139'/0)   // LUD-05's step 1, own purpose
```

The trailing `'` is on `139` only — `0` is **not** hardened. The vectors prove
it: `m/139'/0'` yields `39359da4…` while the vector wants `45a46de7…`.

**Fix:** `derive_path(master, [139, 0], hardened=[True, False])`.

**Lesson:** in a docstring, `139'/0` and `139'/0'` differ by one glyph and one
completely different key.

---

## E2 — LUD-26's prose says the branch root is "hardened"; the vectors require `d1..d4` UNHARDENED

**Symptom:** `p` and `chaincode` mismatched. With all five path elements
hardened — exactly what the prose says — nothing reproduces.

**Cause:** LUD-26:

> `(d1, d2, d3, d4) = first 16 bytes of domainMaterial as 4 uint32 // exactly as LUD-05`
> `p, chaincode = derive(masterKey, m/139'/d1/d2/d3/d4)   // this SERVICE's branch root, hardened`

The word "hardened" does not survive contact with the vectors. Only `139` is
hardened; `d1..d4` are plain indices — which is also what LUD-05's own Scala
example does (`hardened(138L) +: pathSuffix`, one hardened element then four
soft ones).

**Fix:** `hardened=[True, False, False, False, False]`.

**Why this matters beyond the codec:** LUD-26's watch-only `cx1` export is only
useful *because* index-level derivation is non-hardened. If the branch root
really were fully hardened the feature would not work at all. The prose is
self-contradicting and the vectors side with the design.

---

## E3 — `Q_i = lift_x(P) + t·G` really does discard the sign of P (and takes x-only P)

**Symptom:** `Q_i`, `sk_i` and `cp1` mismatched for seed 1 (whose `P_y` is
`odd`) while seed 2 (`P_y` even) matched.

**Cause:** `lift_x` is BIP-340's **even-y** lift. For an odd-y `P`, `lift_x(P)`
is `-P`, so the note key is derived from `-P` and the matching secret is
`(n - p) + t`, not `p + t`. LUD-26 states this literally and the vectors confirm
it — but the phrase "lift_x(P)" reads like "the point whose x is P" and it is
very easy to write `P` instead.

A second, sharper trap sits in the same line: `t` is hashed as

```
t = tagged_hash("LNURLcash/derive", P || chaincode || ser32(purpose) || ser32(i))
```

and the `P` there is the **32-byte X-ONLY** key — the same form `lift_x`
consumes — while the vectors' own `P` *field* is the 33-byte COMPRESSED key.
Hashing the vector's `P` bytes verbatim reproduces nothing. LUD-26 never says
which encoding is meant; the vectors do.

**Fix:** use `Px = P.x.to_bytes(32)` for the hash, and `lift_x(Px)` for the point.

**Lesson:** "P" is three different byte strings in three places in this one
paragraph (x-only, compressed, curve point). Only the vectors disambiguate.

---

## E4 — `prevout_txid` is the tagged hash *un-reversed*

**Not a bug in the POC — a reading that had to be confirmed.** LUD-25's table says

```
vin[0]  prevout (tagged_hash("LNURLcash/mint", domain), 0)
```

and `prevout_txid` is that value `d5ac2de3…`. A txid is normally displayed
byte-reversed relative to its serialized form, so the natural instinct is to
reverse it when writing it into a transaction — which produces a *different*
transaction and a different sighash.

**The vectors settle it:** `spend_tx` literally begins
`02000000000101d5ac2de3423432e3…`, i.e. the serialized outpoint carries the
`prevout_txid` bytes verbatim. No reversal. Confirmed by an assertion in
`tests/test_vectors.py` that does not depend on our sighash being right at all.

---

## E5 — testing "the leaf contains OP_CHECKMULTISIG" by byte membership is unsound

**Found by this POC, and it cuts against the prior spike's own evidence style.**
The natural check is

```python
assert 0xAE in leaf          # WRONG
```

The trailing `ae3f27` of the *payee pubkey* contains `0xae`. So the test passes
for the CHECKMULTISIG leaf for the wrong reason and, worse, would pass for a
corrected leaf whose pubkey happened to contain the byte. This POC initially
made the mirror-image mistake in the other direction (asserting the corrected
leaf contained no `0xae`, which failed) — see `tests/test_bip342.py` F3, which
now compares the **opcode stream** via `tapscript.decode_script`.

**Lesson:** for any opcode-presence claim, decode the script; never scan bytes.
Pushed data is not code.

---

## E6 — the vector file redacts `hex(Q)` inside one `message` field, but not the cryptography

`certificates.cases[1].message` is `"LNURLcash:21000000:***"` while its
`digest`, `signature` and `cs1` are unredacted and are computed over the real
`Q` (the same `Q` printed at `certificates.Q`). The consequence is that the
full offline-verification path — rebuild message → digest → recover key →
compare to `mintPubkey` — is still checkable end to end, and this POC asserts
it that way rather than skipping the case.

**Lesson:** a redacted *display* field is not a redacted test vector. Check what
the assertion actually needs before declaring a vector untestable.

---

## E7 — (a bug in this POC's own harness, kept for honesty)

The first RED run printed detail text only on failure, but `check()` appended
`detail` positionally such that deliberate *passes* printed their mismatch text
("unexpectedly accepted"), which reads like a FAIL. Worse, the RED run
originally aborted on the first `SecpError` raised by the injected defect, so 24
of the 25 failures were hidden behind one traceback — a RED capture that proved
almost nothing.

**Fixes:** `run_section()` wraps each section so a crash is recorded as a FAIL
instead of aborting; `check()` prints detail only when the check fails; and the
injected NUMS-point defect was changed from `0x00*32` (which *raises* — an
invalid curve point — and therefore hides everything downstream) to a **valid**
curve point, so the vectors simply fail to match while the suite runs to
completion. A RED capture whose only failure is "it crashed" is not evidence.
