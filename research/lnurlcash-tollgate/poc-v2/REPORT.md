# LNURLcash LUD-25 v2 (`t` in `cs1`) — experiment report, PR #944

Machine-generated evidence lives in `research/lnurlcash-tollgate/poc-v2/out/`.
Every number below came from a run on 2026-11-21/22 on `x280`, `python3 -S`
(no site-packages), Bitcoin Core v31.1.0 `regtest`.

## What changed between what #944 reviewed and the spec head

`poc/vectors/25.md` is byte-identical to `lnurl/luds@2f17578` (2026-09-30).
The branch head at review time is `fd80cdd` (2026-11-16). Six commits touch
`25.md` in between (`11c8ed0` 2026-10-30; `cc186e5`, `92abbb3`, `92e430f`
2026-11-03; `4da9530`, `fd80cdd` 2026-11-16).

The delta that matters:

| | `2f17578` (reviewed) | `fd80cdd` (now) |
|---|---|---|
| `cs1` message | `LNURLcash:‖amount‖:‖Q` | `LNURLcash:‖domain‖:‖amount‖:‖Q‖:‖t` |
| `cs1` payload | 65 B (`r‖s‖recid`) | **69 B** (`r‖s‖recid‖u32 t`) |
| domain binding | none | **in the signed message** |
| domain canonicalisation | none | host of `withdrawLink`, A-label, no port/userinfo/trailing dot |
| certificate | SHOULD | **MUST** certify; `WALLET` MUST refuse without `mintPubkey` |
| `ck1` privacy | unaddressed | still unaddressed |
| mint fees | `<base>,<ppm>` | `<base>,<ppm>` + explicit split/merge algebra |
| second-mint move | — | new section |
| security considerations | — | new section (URL logging, redirects, RNG) |
| timelocks | SERVICE-clock warning | unchanged (same wording) |

The feedback #944 gave that was **not** acted on: the `ck1` linkability /
deterministic-`aux_rand` privacy note, and a top-level Security Model section
for the timelock. The POS exposure the review called "High" is now partially
addressed — but by `t`, which as E2 below shows does not close it.

## Runs

| suite | assertions | result |
|---|---|---|
| `poc/tests/test_vectors.py` vs the pinned `2f17578` vectors | 141 | **141 PASS / 0 FAIL** (6.4 s) |
| `poc-v2/tests/test_vectors_v2.py` vs the `fd80cdd` vectors | 27 | **27 PASS / 0 FAIL** |
| `poc-v2/tests/exp_timestamps.py` | 7 experiments | transcript in `out/EXP-TIMESTAMPS.txt` |
| `poc-v2/tests/exp_consensus.py` | Core v31.1.0 regtest | transcript in `out/CONSENSUS.txt` |

The v2 codec is a from-text reimplementation of the delta only
(`poc-v2/lnurlcash/cs1v2.py`); it reproduces the official message, digest,
69-byte payload, both certificates, the bearer certificate and the timelock
claims byte-for-byte. That is the interoperability claim, and it is measured.

## Findings

### F1 — `t` is free to renew, so it is not a scarcity signal

Four certificates for **one** unspent `Q`, at t = 1800000000 / +60 / +3600 /
+90000, all valid, all produced from one read-only GET. The spec says
"SERVICE returns certificates on the informational GET of an outstanding note"
and "MAY answer a retry with a fresh one" — with no cap on count or rate. A
double-spender fetches a fresh certificate at the counter and passes any age
policy.

### F2 — the window `t` bounds is attestation age, not possession age

Same holder, same note, certificate fetched at handover: age at handover is 0 s
whether the note was received 1 second ago, held 1 hour, or held 30 days. The
mint signs what it knows (the note is outstanding) and does not know, and cannot
know, when the holder acquired it. `t` cannot distinguish "I just received
this" from "I have held this and just re-certified it".

### F3 — what v2 genuinely fixes: certificate replay across services

A v1 certificate verifies at **any** SERVICE (no domain in the message). A v2
certificate recovered against the wrong domain yields a different key and fails.
Measured both directions. This is a real fix and it is worth having — but it
comes from `domain` entering the message, not from `t`.

### F4 — the upgrade is a silent hard break with no negotiation path

v1 parser requires exactly 65 bytes, v2 requires exactly 69; **each rejects the
other** and both use the same `cs<amount>` HRP, so a wallet cannot tell which
generation it holds without trying both. `fd80cdd` specifies no version tag, no
"MUST accept both during transition", no flag day. Every v1 certificate in
circulation becomes a signature failure the day a client implements v2 — on a
bearer note that is value loss, not a cosmetic incompatibility. If this ships as
draft-final without a transition rule, it will break deployments.

### F5 — `t` widens trust in `SERVICE` without bounding it

v1: "this `Q` exists with this value". v2: "as of `t`, by my clock". The only
normative clock rule is one-directional — `SERVICE` MUST NOT sign a `t` ahead of
its own clock. There is no receiver-side MUST, no maximum age, no rounding rule.
Offline-verification step 4 says only "weighs the age of `t` against its own
policy": the security parameter of the whole feature is undefined by the spec,
and backdating is undetectable because nothing binds `t` to any fact beyond the
signature itself.

### F6 — encoding footguns for a Go implementation

`t` appears twice with two encodings: minimal base-10 in the message, `u32`
big-endian in the payload. `t` is `u32` seconds (max 2106-02-07T06:28:15Z), so a
re-encode bug (`0000001` vs `1`) is invisible in the payload and fatal in the
digest. `cs1` grows 116 → 123 chars at 1000 msat.

### F7 — the two-gate double spend is untouched by `t`

Gate A and Gate B, both offline, both handed the same fresh certificate, both
correct to accept; the note is still outstanding at the mint until somebody
rotates, and the second rotate is answered `already in use` **after** access was
given. This is the same exposure #944 named; a timestamp is orthogonal to it.

### F8 — consensus view (Core v31.1.0, regtest) — the bearer claim is now MEASURED

Previously the spike's own `KERNEL-CHECK-FINDINGS.md` recorded "the gap is NOT
closed… the BIP-342 claim remains REASONED". For the v2 vector set that gap is
now closed for the bearer note:

- All three note output keys expand in Core to `witness_v1_taproot` outputs, and
  Core's `rawtr(Q)` descriptor produces spk bytes **identical** to the spec's
  `OP_1 <Q>` (`spk-identical=True`).
- A P2TR output was funded at the bearer note's own spk (0.5 BTC, confirmed).
- The note's `cw1` from `fd80cdd`'s vectors — `locktime=0 sequence=0xffffffff`,
  leaf `OP_SHA256 <h> OP_EQUAL`, 33-byte control block, one witness item — was
  spent as input 0 of a hand-serialised segwit tx and **Core v31.1.0 ACCEPTED
  it** (`txid=0d270abd47a34f36`, mined).

So: the container is real, the vector is correct, and a bearer note's spend is
accepted by Bitcoin consensus. What consensus does **not** see is `now` — the
timelock leaf decodes as `1800000000 OP_CHECKLOCKTIMEVERIFY OP_DROP …` and
whether a SERVICE honours it is, exactly as the spec warns, the SERVICE's clock.

Harness history, recorded because it is the same trap the repo has hit before:
four of the five failures on the way to this accept were mine, not the spec's
(`NUMS` is not a valid descriptor key name — the literal H point must be used;
`deriveaddresses` wants the descriptor without a second `#checksum`;
`getnewaddress` returns a bare string not JSON; and the hand serializer omitted
the segwit marker/flag, the per-input scriptSig length, and the witness item
count). Each was Core *rejecting* something plausible, which is why the accept
is worth more than a self-consistent simulator would have been.

## For TollGate: how far does v2 get us?

**It does not make LNURLcash a bearer instrument in the sense that matters.**
A bearer instrument transfers value by transferring the thing, and the holder
needs no one's cooperation. Here the thing is a claim on a SERVICE's database;
consumption is still adjudicated by that SERVICE, online, by rotation. `t`
changes what the certificate attests about *time*; it does not change *who
adjudicates*.

What v2 does enable, concretely, is **deferral with a stated age policy**:

1. Gate accepts a note offline, verifies spend + `domain`-bound `cs1`, opens the
   valve, and persists `(Q, t, amount, gate_id)`.
2. At next connectivity the gate rotates. Loss is bounded by how many gates
   accepted the same `Q` before the first rotation — which `t` does not limit.
3. The age bound is **our** parameter. A defensible one for a captive portal is
   short (tens of seconds), which means the note must be counter-fresh — i.e.
   the customer must have had connectivity moments before, which is exactly the
   case where Cashu already works.

So for the customer with no internet at all, v2 buys nothing over the current
30-second `Receive` block. For the customer who had connectivity at the counter
and loses it at the gate, v2 turns "replayable, unknown-age instrument" into
"replayable, age-bounded instrument" — an improvement in the *reporting* of
risk, not in the *elimination* of it.

Needs, in order:

- **A consumption proof the gate can present later without the holder.** Not
  present in v2. This is the one property that would make the note bearer.
- **A certificate that binds one named consumption** (an ephemeral recipient
  key), which makes the instrument non-transferable *after* acceptance. Doesn't
  close same-service replay, does close most of POS.
- **Receiver-side normative text**: a maximum age, and a stated threat model
  for what an old-but-valid `cs1` means. Without it every integrator invents a
  different policy and the "offline verification" feature is not portable.
- **A transition rule** for 65 → 69 bytes (F4) before this leaves draft.
- **Chain-enforced consumption** — the one-way-channel leaf — if the requirement
  is genuinely non-custodial. `fd80cdd` still says a SERVICE-honoured timelock
  "is a custodial policy, not a consensus guarantee", so nothing in this
  revision moves that line.

**Recommended position:** adopt v2 as the *offline provenance* layer (it is
better at that: domain-bound, certifiable, age-reportable) and do **not**
describe it to customers as bearer value. Keep the deferral design, run it with
a short age policy, and treat the double-spend window as an accepted, priced
risk per note rather than a solved one.
