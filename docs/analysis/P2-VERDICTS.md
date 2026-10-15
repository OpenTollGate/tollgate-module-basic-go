# P2 open questions — verdicts

Three questions the ralph brief listed as "still lacking an answer". Each gets a
verdict of **YES / NO / INSUFFICIENT-EVIDENCE**, citing the run or artefact that
produced it. Two were **already settled by committed work** before this pass; per
the loop's honesty contract those are cited rather than re-derived.

---

## P2.6 — Does any construction make a payer BOTH non-custodial AND offline?

**VERDICT: NO — for every construction examined.**

| Construction | Non-custodial? | Payer/sender offline? | Verdict |
|---|---|---|---|
| V0 naive payee-signs-alone | ✗ (payee can take 100%) | — | fails custody |
| V1 Spilman + per-granule cosign | ✓ | ✗ (needs per-granule interaction) | fails offline |
| V2 pre-signed ladder | ✓ | ✓ | fails: 60% of a 5-state ladder extractable after 2 granules (`RED.txt` T9) |
| V4 duplex / LN-penalty | ✓ | ✗ | fails offline; needs a watchtower |
| **Hedgehog** | ✓ | ✓ | **fails: the time obligation is RELOCATED, not removed** |

**Hedgehog is the closest** — the first construction here to achieve
non-custodial **and** sender-offline **and** recipient-offline at once. But its
conditional-revocation escape hatch is a CSV timelock, so safety requires *some*
party to observe the chain inside the window — a watchtower-equivalent duty.

**Exact trust assumption hedgehog requires:** (1) the recipient eventually
returns, and (2) some party watches the chain inside the CSV window. Hedgehog
**relocates the reconciler to the counterparty** rather than eliminating it —
the same "relocates, does not delete" pattern the original spike found, refined:
the reconciler is *moved*, not *removed*.

**Evidence:** `docs/analysis/D5-RETEST.md`; `research/hedgehog-poc/poc/run_poc.py`
(13 checks, self-labelling each as `[CORE]` / `[OURS]` / `[REASONED]`). The
strongest supporting evidence is `xcheck.py`, 10/10 independent cross-checks
including byte-for-byte agreement with the *shipped* `tapscript.js`.

**Correction:** this leg is now **accepted end-to-end**. The earlier reject was
our own bug (a BIP-341 sighash missing `sha_sequences`, plus a witness-order
mistake); both fixed, Core accepts, and the spend is broadcast. The D5 conclusion
never rested on this leg — it rests on the timelock structure, directly
observable in the tree. See `SIGHASH-FIXED.md`.

**Second correction, more important:** the *direction* objection in an earlier
draft of this file is **retracted**. Hedgehog *does* support the direction
TollGate needs — the payer constructs locally with no network while the payee
completes later — proven by execution in `RETRACTION-DIRECTION.md`. So the
remaining obstacle to D5 is **cost and the CSV window**, not interactivity, and
the router being a 24/7 service that can watch the chain is a *good* fit for the
watchtower duty.

---

## P2.7 — Is an on-chain channel economic at a realistic price?

**VERDICT: DEPENDS ON PURCHASE SIZE — no for micropayments, yes for prepaid bulk.
Already settled; see `docs/architecture/one-way-channel-spike.md` §10 and
`research/one-way-channel-spike/granularity.py`.**

The measurement that settles it, read **live from the router**
(`tollgate-A04E`, `tollgate-wrt v0.6.0-rc4-gcf7cad3`):

```json
{ "metric": "bytes", "step_size": 22020096, "price_per_step": 1 }
```

The shipped metric is **bytes**, so **1 satoshi buys 21 MiB** — *not* the
"6.12 hours" the original spike claimed by misreading `step_size` as
milliseconds. `22020096 = 21 × 2²⁰` exactly, which is itself evidence the bytes
reading is intended.

Consequence: the fixed ~716-sat on-chain settlement cost is a sat-to-sat ratio
that does *not* improve with granularity, but the **size of a purchase** scales
with price. At the shipped default a channel buys months of internet; at a
realistic retail price (~1000 sat per step) the minimum viable channel buys
~2–3 weeks. So the answer is a **purchase-size property, not a protocol
property** — which is why the two instruments are best seen as the two ends of
one dial rather than competitors.

**Caveat carried forward:** the shipped default is a *default*, not necessarily
what operators charge (it comes from a validator test constant plus
`config_manager_config.go`). Real operator pricing would move the crossing point.

---

## P2.8 — What, if anything, should TollGate take from LNURLcash?

**VERDICT: NOT the two things this brief hypothesised. Both hypotheses are
REJECTED by measurement.**

The brief proposed testing whether the strongest fit is **(a)** `cs1` as a cheap
offline existence pre-check, and **(b)** the LUD-03 withdraw link as a
receipt/refund format. `docs/analysis/OPTIONS-PAPER.md` tested both with
measurements, and **both fail** — which corrects an earlier, more optimistic lean
of mine.

**(a) `cs1` as an offline pre-check — REJECTED as a *grant* basis.**
`cs1` verifies **issuance**, not **outstandingness**: it is recoverable ECDSA
over `"LNURLcash:" || amount || ":" || Q`, checked offline against `mintPubkey`.
It answers "was this `Q` credited this amount" and nothing more. It carries **no
nonce, no timestamp, no recipient, no counter** — measured, not inferred. So
accepting on it means accepting a **replayable** instrument, which is precisely
the documented "naive is-it-spendable check" the architecture work warns against.
The spec itself does not bound the replay window; only an acceptor-side durable
nullifier journal would, and that journal is new durable money state on a module
whose existing crash-recovery lane is red (#719).

**(b) LUD-03 withdraw link as a receipt/refund format — REJECTED as unnecessary.**
TollGate **already has a strictly better receipt**: `#502`'s receive-intent
journal, written *before* the money moves, with a temp-file → fsync → rename →
fsync-parent-directory commit sequence (`src/merchant/receive_intent.go:84-137`)
and states `granted` / `owed` / `abandoned` / `pending`, already wired into the
owed-grant path at `src/merchant/merchant.go:1794-1799`. That carries more
information than a `cs1` and ships today. A withdraw link would be a *second,
weaker* receipt format.

**What the paper does recommend (evidence-backed, and it is not "adopt"):**
bounded experiment then watch. Specifically: vendor `lnurlcash-conformance`'s
vectors into `tests/` (free win), run X4 (journal-vs-round-trip cost), and — the
highest-leverage action, costing nothing — **send the already-drafted review to
the LUD-25 author**.

**The structural finding that explains all of this:**
> LNURLcash's benefits accrue to the **WALLET** side of the protocol. TollGate is
> the **SERVICE** side. TollGate's requirement — consumption state at the moment
> of grant — is the one thing `cs1` structurally cannot supply.

Of five claimed gains: one is real-but-weak-for-a-redeemer, two belong to a
counterparty role TollGate is not in, and two are absent.

**Watch triggers that would flip this to "adopt":** LUD-25 lands a normative
locked-note or one-time-`cs1` variant; TollGate acquires a chain view; the #719
crash-recovery lane goes green *and* a durable nullifier store ships for some
other reason; or X4 shows the journal is ~100 lines with no schema change.

**One thing genuinely worth taking, for a different role:** the note **is** a
LUD-03 withdraw link, and `lnurl-mint` already advertises a rail where a payer
holding a note on the *same* mint skips Lightning entirely (no invoice, no round
trip). That is real value — but for **value movement between TollGates**
(reseller → upstream), a role the module has no reachable call path for today.

---

## Summary

| Question | Verdict | Evidence |
|---|---|---|
| P2.6 non-custodial AND offline? | **NO** | `D5-RETEST.md`, `run_poc.py` 13 checks |
| P2.7 channel economic at realistic price? | **depends on purchase size** | live config + §10 + `granularity.py` |
| P2.8 what to take from LNURLcash? | **not the hypothesised two; bounded experiment then watch** | `OPTIONS-PAPER.md` |

None of the three required new derivation in this pass: P2.6 was closed by P1.4,
P2.7 by the §10 erratum, and P2.8 by the options paper. What this pass adds is
the explicit verdict with its evidence label, and the correction that P2.8's two
hypotheses are **rejected**, not promising.
