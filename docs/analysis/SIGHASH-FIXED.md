# The Hedgehog Core-reject: FOUND AND FIXED — our bug, two of them

**Bottom line: two implementation bugs in our harness. Not a Hedgehog defect.**
End-to-end `[CORE]` acceptance is now achieved and the spend is broadcast.

```
Core testmempoolaccept -> allowed=True reason=''
broadcast: d9ddaa98a8d754cb30d5d1c7baeffcbd2121f28157cf719b2cc72c73f8a4234d
```

---

## Bug 1 — the BIP-341 sighash (the hard one)

Our `taproot_sighash()` had **four defects from one misreading of BIP-341**. All
four were *silently self-consistent*, which is exactly why a hand-written
"independent" implementation agreed with ours while **both** disagreed with Core:
the second implementation was written from the same wrong reading, so it was not
independent at all. That is the methodological lesson here, and it is why
"our code agrees with our code" is not evidence.

| # | Defect | What BIP-341 actually says |
|---|---|---|
| D1 | `sha_prevouts` omitted entirely | present (32 B) |
| **D2** | **`sha_sequences` omitted entirely** | **present (32 B), between `sha_scriptpubkeys` and `sha_outputs`** |
| D3 | per-input section emitted `outpoint ‖ scriptPubKey ‖ amount ‖ nSequence` | that is the **ANYONECANPAY** form; the default form emits **`input_index` (4 B) only** |
| D4 | every stream used **double** SHA256 | the spec uses a **single** SHA256 for `sha_prevouts` / `sha_amounts` / `sha_scriptpubkeys` / `sha_sequences` / `sha_outputs` / `sha_annex` |

Plus: a `sha_annex` stream was emitted even when no annex was present. When
`annex_present == 0`, no annex hash is included.

**D2 was the decisive one.** Every earlier debugging attempt — reordering the
witness, testing single-vs-double sha256, brute-forcing 24 stream/spk/spend_type
combinations — failed because `sha_sequences` was missing from *all* of them.
One absent 32-byte field makes every other variant wrong too.

**How it was found:** stop guessing. Read the `SigMsg` field list in the BIP text
directly (`bip-0341.mediawiki:99-121`), which enumerates every item in order.
The omission was visible immediately once the source was read rather than
reasoned about.

**Verified against Core, not against ourselves:**
```
core sig    : 59bad615997f8ef51282995e81632fe4c1ae9c12...
CORRECT sighash : 23e68d59dde185a271d0a0bb6caf7f5135e9ca4af7f2df6cd64bd7dc4a31085e
CORE's signature verifies against the CORRECTED sighash: True
```

## Bug 2 — the witness stack order

The 2-of-2 leaf is `<A> OP_CHECKSIG <B> OP_CHECKSIGADD OP_2 OP_EQUAL`.
BIP-342 pop discipline: `OP_CHECKSIG` takes its pubkey from the **top** of the
stack. Witness `[w1, w2]` pushes `w1` then `w2`, so `w2` is on top:

- `A OP_CHECKSIG` pairs **A** with the **top** item → `w2` must be **A's** signature
- `B OP_CHECKSIGADD` pairs **B** with the **next** item → `w1` must be **B's** signature

So the correct order is **`[sig_B, sig_A]`**. We shipped `[sig_A, sig_B]`.

Measured both ways (`wit_order.py`):

```
[sig_A, sig_B]  (as shipped)   allowed=False  reason='... (Invalid Schnorr signature)'
[sig_B, sig_A]  (correct)      allowed=True   reason=''
```

This was masked while bug 1 was present: with a wrong sighash *both* orderings
fail, so reordering looked like a non-fix. Fixing the sighash exposed it.

---

## What this means for the D5 verdict

**D5 is unchanged: NOT MET.** 14 checks pass; the two remaining "failures" are the
*correct* D5 negatives:

- `payer needs NO chain state ever: False`
- `no liveness/timelock obligation on ANY party: False`

D5 rests on the **timelock structure** — the conditional-revocation third tapleaf
and the CSV window — which is directly observable in the tree and was
independently validated against the shipped `tapscript.js`. Whether our harness
can complete a spend was never load-bearing for that conclusion; it is now fixed
anyway, so the harness is no longer a soft spot in the evidence.

## Status of every claim about this leg

| Claim | Status |
|---|---|
| "Core rejects our Hedgehog script-path spend" | **was true, now FIXED** — `allowed=True`, broadcast |
| "Is it a Hedgehog flaw?" | **No** — two harness bugs |
| "The local signature agrees only with our own verifier" | **superseded** — now agrees with Core |
| "D5 conclusion does not depend on that leg" | **still true**, and the leg now passes so it is moot |

## Method note

This cost far more iterations than it should have, because the first several
attempts tried to *reason* the construction correct and *brute-force* the
difference. Both failed for the same reason: a missing field makes every variant
of the rest wrong. **Reading the specification's field list took one step and
found it immediately.** The transferable lesson: when two of your own
implementations agree and disagree with the reference, do not write a third —
read the reference.
