<!-- markdownlint-disable MD013 -->

# Architecture fit: can a one-way payment channel be a `WalletPort` backend?

**Status:** spike memo, 2026-10-14. Answers the research question in
[`SPIKE-BRIEF.md`](SPIKE-BRIEF.md). Prior-art revision:
`OpenTollGate/tollgate-module-basic-go` @ `cf7cad3`
(`cf7cad34677e87e775e7f16786b1bbf18c4bc8bc`, tarball of `main`, fetched
2026-10-14).

**Reproduce the one measurement in this memo:**

```bash
cd research/bearer-port-demo && go test -count=1 ./...   # 15 tests
```

Captured output: [`evidence/bearer-port-demo-test.txt`](evidence/bearer-port-demo-test.txt)
and [`evidence/bearer-port-demo-run.txt`](evidence/bearer-port-demo-run.txt).
Real run: **RUN 15 / PASS 15 / FAIL 0**, runner `go1.25.0 linux/amd64`,
wall time `0.003s` on the package.

The §2 design delta itself also type-checks: `evidence/archfit_delta.go` (the Go
from §2, verbatim) compiled beside the demo's real types gives `go vet` OK,
`go build` OK, `gofmt` clean — [`evidence/archfit-delta-typecheck.txt`](evidence/archfit-delta-typecheck.txt),
reproducible via [`evidence/verify_delta.sh`](evidence/verify_delta.sh). It is
deliberately **not** wired into the port, so it proves *well-typedness*, not
*behaviour*; §6.3 is explicit that no channel code was written.

**Structure** (requested shape → this memo): (1) the 3-question test → §1;
(2) the 4th field, as Go → §2; (3) the mapping table → §3; (4) the increment
over #239 → §4; (5) what breaks, with `file:line` → §5; (6) recommendation →
§6. §0 is the verdict.

## 0. Verdict, up front

A one-way channel **can be described** to the bearer-instrument port's three
questions, but only by destroying the thing the port is for.

- The port requires **memory** of an authority, and says so explicitly: "An
  'authority' with neither memory is not an authority; it is a signed sheet of
  paper, and no port can use it"
  (`docs/architecture/bearer-instrument-port.md:30-31`).
- A one-way channel's authority **is** a signed sheet of paper — pre-signed
  transactions plus a `nSequence` deadline. Refereeing is delegated to the
  chain, which holds no per-instrument record (`docs/architecture/bearer-instrument-port.md:26-27`
  require a signing log and a consumption log; a channel has neither).
- Therefore the channel does not *extend* the port. It **sidesteps it**. The
  correct verdict is **nowhere in `WalletPort`; beside it**, as a new settlement
  module — see §6, with the measured disqualifier named in §5.6.

The single sentence worth carrying forward:

> The bearer port models a **live memory** verifying a bearer instrument; a
> one-way channel models a **dead deadline** verifying a chain. They answer the
> same question ("what will happen if I hand this over?") from opposite
> temporal directions, and the port is built to assume the answer is available
> *now*.

## 1. The 3-question test applied to a one-way channel

The port asks three things (`docs/architecture/bearer-instrument-port.md:24-28`,
mirrored in `research/bearer-port-demo/authorities.go:25-78`). Applied to a
Spilman-style one-way channel (funder `P` locks `X`; payee `Q` holds a series of
pre-signed, monotonically-increasing partial spends; `P` holds a refund branch
spendable only after a relative timelock):

### Q1 — "What did you **sign**?" → **NO, not in the required sense**

The port's requirement (`bearer-instrument-port.md:26`) is an **issuance log**
enumerable by a deterministic derivation: `Issued(commitment) -> {amount, spent}`
(`research/bearer-port-demo/authorities.go:56`, and the recovery loop at
`research/bearer-port-demo/port.go:359-391` iterates commitments
`CommitmentOf(seed, i, name)`).

A channel's "signing log" is the **sequence of pre-signed commitment
transactions**. It is real, but it is:

1. **Held by the funder, not by the counterparty.** `Q` holds only the latest
   commitment; it cannot enumerate the series, and no third party can.
2. **Not a value ledger.** The series is one UTXO with a *decreasing* residual
   balance. There is no per-index `{amount, spent}` triple. The port's recovery
   shape has no analogue.
3. **Not queried — verified.** The port *asks* an authority what it signed; a
   channel *checks the chain* for what a script will accept. Q1 is answerable
   only by a verifier with chain access, which is not an "authority" in the
   port's sense.

**Verdict: FAILS as specified.** A reduced proxy can be synthesised — see the
`pre-signed-tx-set` recovery mode justified in §2 — but the synthesis is only
recoverable by the party that already holds the material, so it recovers
**nothing for a holder that lost it**. That is the opposite of what NUT-09 is
for.

### Q2 — "What did you **consume**?" → **PROBE mode, exact structural match**

This is the one answer that lands cleanly, and it is worth being precise about
*why*.

The port defines `probe` as: "the authority has no token-level liveness API. The
only sound way to learn the state is to attempt the consumption, which is a
MUTATION and must be declared as such (fedimint: reissue)"
(`research/bearer-port-demo/types.go:51-54`). And the demo's implementation of it
is: *learn the state only by trying to spend* (`research/bearer-port-demo/port.go:300-307`),
where the second attempt yields `StateSpent` because the authority refuses with
`ErrAlreadyConsumed` (`research/bearer-port-demo/authorities.go:212-214`).

A channel's redeemer `Q` learns the channel's state **exactly and only** by
broadcasting: a different commitment transaction confirms, or it loses the race
to the funder's timelocked refund. There is no read-only oracle, and the check
**is** the spend. So:

**Verdict: PASSES — `probe` in the port's own vocabulary.** The mapping is
structural, not analogical, and no new mode is needed for Q2.

### Q3 — "How can you **answer**?" → **declarable, but the declaration is not honest**

Today `CheckMode` has exactly three values — `query | probe | none`
(`research/bearer-port-demo/types.go:47-58`), declared per authority through
`Capability` (`research/bearer-port-demo/types.go:80-90`). The reason the field
exists is stated as a rule: "SPENT and UNSPENT are things the authority *said*.
Anything else is `UNKNOWN`" (`bearer-instrument-port.md:65-66`).

A channel backend must declare `probe`. Under the existing closed set that is
*syntactically* legal. It is **semantically false**, and this is the crux of the
whole memo:

- Declaring `probe` in the demo's sense means: *I can find out right now, by
  spending.*
- A channel means: *nothing can find out anything right now without landing a
  Bitcoin transaction.* `Q` may be offline, `P` certainly is, and the outcome is
  decided **LATER by a third party**, not **NOW by an authority**.

**Verdict: PASSES ONLY IF A FOURTH FIELD IS ADDED** that records *who decides*.
That field is §2, and it is the only genuinely new idea this spike produces.

**3-question summary:** 1 of 3 passes cleanly (Q2), 1 fails as specified (Q1),
1 passes only under a new field (Q3).

### The rule that really kills it: silence → `UNKNOWN` → fail closed

There is a second load-bearing rule, and it is the one that makes the channel
idea *fail rather than merely fit badly*:

> `SPENT` and `UNSPENT` are things the authority *said*. Anything else is
> `UNKNOWN`, and `UNKNOWN` fails closed.
> — `bearer-instrument-port.md:65-66`

For a channel the honest answer to *"have you consumed it?"* asked at any moment
before adjudication is `UNKNOWN` — and `UNKNOWN` fails closed
(`research/bearer-port-demo/types.go:55-58`). So the channel's admission into the
port, done correctly, would produce a backend that **cannot be used**
(`CheckState` returns `StateUnknown, ErrStateUnavailable` for `CheckModeNone`;
`research/bearer-port-demo/port.go:298-299`). The construction is admissible
only if it is useless.

### The fourth field: the ADJUDICATOR

Stated as the memo's core finding: the port's third question conflates two
different facts.

| Fact | Port's current encoding | Channel's value |
|---|---|---|
| **When** can the state be known? | implicit in the mode (`query` = now, `probe` = now-but-mutating, `none` = never) | **after adjudication only** |
| **Who** can know it? | the authority itself | **a third party: the chain** |

`CheckMode` answers "who" for exactly three answers (`the authority` via a read,
`the authority` via a mutation, `nobody`). A channel needs a fourth: **a
third party, later**. That is the `SettlementMode` in §2.

## 2. The proposed fourth field: `Adjudicator`

Two additions are needed, and they are the minimum. They are deliberately
**orthogonal to the existing `Capability`** rather than folded into `CheckMode`,
because `CheckMode` is a closed set that the port's semantics depend on and
widening it would change the meaning of code that already ships
(`research/bearer-port-demo/port.go:297-308`).

```go
// --- Proposed addition #1: the ADJUDICATOR -----------------------------------
//
// SettlementMode says WHO decides an instrument's state, and WHEN. It is
// orthogonal to CheckMode: CheckMode says how the authority can be ASKED;
// SettlementMode says whether being asked is even the mechanism.
//
// The existing authority family is entirely SettlementAuthority: a mint, a
// federation, and a miner's pool all answer for themselves, now.
type SettlementMode string

const (
	// SettlementAuthority — the authority answers for its own instruments.
	// This is every backend in the tree today: cashu, fedi, pow
	// (research/bearer-port-demo/authorities.go:291-421).
	SettlementAuthority SettlementMode = "authority"

	// SettlementChain — an EXTERNAL, PERMISSIONLESS verifier adjudicates:
	// the instrument carries its own state, in the form of pre-signed
	// transactions and relative timelocks (Spilman; Poon-Dryja OP_CSV).
	// The authority holds no per-instrument record at all, so:
	//
	//   - CheckState can only ever answer UNKNOWN before adjudication, and
	//     UNKNOWN fails closed (types.go:55-58). A SettlementChain backend
	//     is therefore unusable for any pre-flight decision, by construction.
	//   - CheckMode MUST declare probe: the check IS the spend (port.go:300-307).
	//   - the "authority" name per the port's own rule is a signed sheet of
	//     paper (bearer-instrument-port.md:30-31).
	SettlementChain SettlementMode = "chain"

	// SettlementCustody — a third party holds and adjudicates (a bank, a
	// sequencer, an escrow). Named for completeness; out of scope here.
	SettlementCustody SettlementMode = "custody"
)

// Adjudicator is the whole of the fourth field.
type Adjudicator struct {
	// Mode is who decides.
	Mode SettlementMode `json:"mode"`
	// Ref is the namespaced verifier identity, e.g. "bitcoin:mainnet".
	// Empty for SettlementAuthority. Present so a caller can decide
	// whether it is even ABLE to verify (a chain-blind router cannot).
	Ref string `json:"ref,omitempty"`
	// VerifiableByHolder reports whether the HOLDER of the instrument can
	// establish the outcome unaided. False for a Spilman channel held by an
	// offline redeemer; true for an Ark-style receipt where the holder can
	// exit unilaterally. This is the field that separates "offline payer,
	// online redeemer" from "offline payer AND offline redeemer".
	VerifiableByHolder bool `json:"verifiable_by_holder"`
	// LearnableBeforeAdjudication is always false under SettlementChain.
	// Spelled out rather than implied, because the temptation in review is
	// to treat probe mode as a pre-flight.
	LearnableBeforeAdjudication bool `json:"learnable_before_adjudication"`
	// Notes is the declared honest cost, mirroring Capability.Notes.
	Notes string `json:"notes,omitempty"`
}

// --- Proposed addition #2: a recovery mode that describes a channel ----------
//
// RecoverMode currently names WHERE the memory lives
// (research/bearer-port-demo/types.go:62-78): issuance-log, authority-backup,
// account-ledger, none. A pre-signed channel has a memory that is neither the
// authority's nor the holder's nor an account's: the FLOW of commitment
// transactions, which only the FUNDER holds in full. Name it, so the port can
// at least refuse honestly instead of reporting a recoverable-zero.
const (
	// RecoverModePreSignedTxSet — the material that makes this instrument
	// spendable is a set of pre-signed transactions carried out of band.
	// Recovery is impossible for a holder that lost them, because there is
	// no server-side log to replay (bearer-instrument-port.md:26 assumes
	// one). A port that has this value and still returns 0 silently turns
	// "you lost everything" into "you own nothing" — the caller must
	// distinguish them.
	RecoverModePreSignedTxSet RecoverMode = "pre-signed-tx-set"
)

// Capability gains exactly two fields. Everything else is unchanged, and the
// three existing adapters still compile with a zero-valued Adjudicator that
// reads as SettlementAuthority.
type CapabilityV2 struct {
	Authority  string      `json:"authority"`
	Kind       string      `json:"kind"`
	Unit       string      `json:"unit"`
	StateCheck CheckMode   `json:"state_check"`
	Recover    RecoverMode `json:"recover"`
	// NEW. Zero value = SettlementAuthority = today's behaviour, so this is
	// additive and the existing 15 assertions keep meaning what they meant.
	Adjudicator Adjudicator `json:"adjudicator"`
	Notes       string      `json:"notes"`
}

// SettleResult is what a SettlementChain backend returns, and the shape is
// chosen so that both facts are carried at once. Compare the port's `Acquire`,
// which returns `(uint64, error)` — a single amount and a verdict
// (research/bearer-port-demo/port.go:177). For a chain that shape is a lie:
// there is no single amount, and the error does not mean "nothing happened".
//
// Fields are exported on purpose: a caller in another package must be able to
// read them, which is exactly what would have been impossible had these stayed
// unexported.
//
//   - Known[Y] IS the NUT-07 answer for that nullifier. It is a fact about
//     CONSUMPTION and never about EXISTENCE (bearer-instrument-port.md:68-70).
//     A Y with Known[Y]==StateUnspent means "not consumed"; it does NOT mean
//     "this channel exists". Existence is a separate question and, for a
//     channel, it is a chain query, not an authority query.
//   - Err is the reservation failure. On ANY error the caller holds an
//     ambiguous asset: the tx set may already be in the mempool. Returning
//     only an error here would be the port lying by omission.
type SettleResult struct {
	// Known maps a nullifier (outpoint:txid:vout under a per-adjudicator
	// label, mirroring NullifierOf) to what the ADJUDICATOR knows.
	Known map[string]State
	// SettlementTx is the pre-signed transaction to publish. It is bearer
	// material in exactly the sense of ReissueBlob: publishing it IS the spend.
	SettlementTx []byte
	// Err is non-nil when the reservation failed; Known is then partial and
	// MUST be read as UNKNOWN for every absent key.
	Err error
}

// SettlementPort is the interface a one-way channel would implement. It is
// NOT WalletPort, and it is NOT an Authority (see §6).
type SettlementPort interface {
	// CheckState asks the ADJUDICATOR. Before adjudication this returns
	// StateUnknown for every nullifier — including ones the channel has
	// never seen, and including its own. It is not a liveness API.
	CheckState(nullifiers []string) (map[string]State, error)

	// Settle is irreversible and is the only way state is ever learned.
	// The caller must have journaled the intent first:
	// src/merchant/receive_intent.go:1-31 (persist-before-effect, #502).
	Settle(instrument Instrument) SettleResult

	// Adjudicator is the declared fourth field.
	Adjudicator() Adjudicator
}
```

**Is `Adjudicator` a lie of the same kind the port refuses?** No — and the test
is the one the port already applies. `CheckModeNone` earns its place because "a
backend that cannot answer has no way to say so" (`bearer-instrument-port.md:49`)
was a real defect. `SettlementChain` is the same species of honesty: a channel
**cannot be asked**, and the only truthful thing the port can do is let it say
so, in a field, so no caller mistakes `UNKNOWN` for "probably unspent"
(`bearer-instrument-port.md:65-66`).

## 3. Mapping table

`existence` / `consumption` are the port's two questions
(`bearer-instrument-port.md:26-28`, rule at `:68-70`). `recovery` is NUT-09
(`:26`). `adjudication` is the new field (§2). `offline-*` is the spike's
question (`SPIKE-BRIEF.md:9-11`). Cashu authority facts:
`research/bearer-port-demo/authorities.go:293-350`. Fedimint authority facts:
`authorities.go:364-389` and `docs/architecture/wallet-backend-fedimint.md:18-32`.

| | **Cashu authority** (mint) | **Fedimint authority** (federation) | **One-way channel** |
|---|---|---|---|
| **existence** | issuance log; `Decode` asks `Issued(commitment)` and refuses unknown (`port.go:81-124`). Honest: a plain mint's `/checkstate` says `UNSPENT` for a nullifier it never saw, so existence is answered **only** from the signing log (`authorities.go:321-350`, test `TestNut07AnswersConsumptionNotExistence`) | identical interface, different memory: the federation's encrypted backup of the note set (`types.go:70-72`) | **No authority. Existence = the funding outpoint is confirmed on the chain.** A verifier with chain access answers; the instrument does not. Chain-blind ⇒ unanswerable. |
| **consumption** | `query` — read-only `/checkstate`, free, side-effect-free (`types.go:48-50`) | `probe` — no token-state API, so the reissue IS the check, and it **credits** (`types.go:51-54`; demo: `DEMO-OUTPUT.txt:57-63`) | **`probe`, and only `probe`** — publication of the settlement tx is the check, and it is not credit-preserving, it is **winner-take-all** (§5.2). A settlement tx, once in the mempool, cannot be un-published. |
| **recovery** | `issuance-log` — replay the deterministic derivation against the mint's log; sweep from index 0 (`types.go:64-69`, `port.go:329-340`) | `authority-backup` — the federation holds the holder's client state (`types.go:70-72`) | **`pre-signed-tx-set`** — held by the **funder**, not the payee, and not enumerable by anyone else. A payee that loses its tx set recovers nothing, and the funder need not help. **This is a funds-loss asymmetry Cashu does not have.** |
| **adjudication** | `SettlementAuthority`; the mint is the oracle and there is no separate adjudicator (`bearer-instrument-port.md:30-31`: an authority with no memory "is a signed sheet of paper, and no port can use it") | `SettlementAuthority`; the guardians vote. Threshold custody, not chain adjudication (`wallet-backend-fedimint.md:19-22`) | **`SettlementChain`** — the chain adjudicates by relative timelock (`OP_CSV`, Poon-Dryja 2016). **No human or institutional actor decides.** |
| **offline-payer** | not applicable — minting needs a quote round-trip (`WalletPort.RequestMintQuote`/`MintTokens`, `src/tollwallet/port.go:227-237`); spending locally is offline but unverifiable | not applicable | **YES, and this is the channel's actual advantage**: `P` signs all commitments up front and then disappears. Pre-signed txs are bearer material in the strict sense. |
| **offline-redeemer** | works: pay the Cashu token offline; the mint sees it later. Best-in-class, and it is why the incumbent wins | works via the federation's reissue on reconnect (a mutation, so it needs reachability) | **NO. `Q` must broadcast to learn anything and to be paid at all.** The whole state of the channel is a property of Bitcoin's mempool and block height, neither of which is available offline. |

### The table's punchline

The channel is **worse where Cashu already wins** and **equal where the spike
looked for an improvement**:

| Axis | Cashu | channel |
|---|---|---|
| offline-payer | ✗ | ✓ |
| **offline-redeemer** | **✓** | ✗ |
| no live mint in the customer path | ✗ (probe = mint round-trip) | ✓ |
| non-custodial | ✗ (mint custodies until swap) | ✓ (no third party) |

TollGate's redeemer **is the router** (`SPIKE-BRIEF.md:49-53`), and the router is
therefore invited to be offline exactly at the moment it must adjudicate. The
construction is only viable if adjudication can be **deferred** (broadcast later,
once paid). Deferring is not free: §5.3 and §5.5.

## 4. The increment over #239 `ForwardFirstProof`

Read first: `src/upstream_session_manager/bootstrap.go:13-28` (the design),
`:35-36` (the point of no return), `:194-204` (the money-path notes), `:239-249`
(refusing to re-forward an ambiguous proof), `:267-288` (the forward and its two
failure branches). Also `bootstrap_test.go:35-119` (the state machine is tested
against a fake forwarder; the mint is never real).

**What #239 already does.** A reseller with *no session and no ecash* forwards
the customer's first proof **whole** to the upstream TollGate: no swap, no
split, no margin (`bootstrap.go:72-74`, `:188-204`). Upstream validates the proof
**at the mint** and returns an allotment (`bootstrap.go:21-24`). Cold start with
no balance is therefore already answered.

**What it still needs.** The word *at the mint*. Upstream's validation
(`bootstrap.go:22`) is a mint round-trip, so the mint is live on the critical
path. That is precisely the gap `SPIKE-BRIEF.md:25-29` names.

**The narrow increment a one-way channel would add.** It removes the live mint
from the *customer→router* path, replacing "ask the mint now" with "adjudicate on
the chain later". Concretely:

| | #239 bootstrap | one-way channel |
|---|---|---|
| customer's payment is valid only if | mint is reachable *now* (`bootstrap.go:22`) | a pre-signed tx is broadcastable *eventually* |
| who decides the outcome | the mint | the chain |
| failure mode at the point of no return | **ambiguous-against-a-mint** — the POST is sent, the proof may be spent, the answer lost (`bootstrap.go:35-36`, `:239-243`) | **race-against-the-funder** — the settlement tx is valid *unless* the funder's timelocked refund confirms first |
| recovery from ambiguity | operator reconciliation, no automatic retry (`bootstrap.go:244-249`) | once the channel expires, **no reconciliation exists at all** (§5.3) |
| whose fault an ambiguity is | the mint's liveness | the chain's liveness **or** the redeemer's own delay |

**Is the increment zero? No — but it is not the increment the spike brief hoped
for.** #239 moves the round-trip *away from the customer* (the reseller's
problem) but keeps it *at the upstream* (`bootstrap.go:22`). A channel removes
it entirely. That is a real difference.

**Is the increment worth paying for? No, on this repository's evidence.** The
increment is purchased with an ambiguity class strictly **worse** than the one
#239 was engineered to survive, and the repo already treats #239's class as
unacceptable enough to be operator-hand-work-only (`bootstrap.go:244-249`; and
the operator guide still says "Nothing credits or refunds this automatically
today" at `docs/operator-guide.md:966`). Trading a *reconcilable* ambiguity for
an *unreconcilable* one is a regression, not an increment. See §5.3 and §5.6.

**Stated as one sentence to paste into a PR body:**

> The channel's only true increment over #239 is that it deletes the mint
> round-trip at *validation* time; it pays for that by replacing an ambiguity
> the operator can settle (`src/upstream_session_manager/bootstrap.go:244-249`)
> with one that expires into nothing.

## 5. What BREAKS — every existing invariant the channel idea violates

Each entry is a named, cited rule that is **load-bearing today**, followed by the
code that enforces it, followed by what the channel does to it. Doc rules live in
`AGENTS.md`; the demo rules are pinned by the 15 tests verified in
`evidence/bearer-port-demo-test.txt`.

### 5.1 "A NUT-07 answer is about consumption, NEVER existence"

- **Rule.** `docs/architecture/bearer-instrument-port.md:68-70`; pinned by
  `TestNut07AnswersConsumptionNotExistence` (15/15,
  `evidence/bearer-port-demo-test.txt:31`).
- **Code.** `Port.Decode` answers existence from the issuance log
  (`src/tollwallet`-equivalent `research/bearer-port-demo/port.go:81-124`), and
  `Port.CheckState` answers consumption (`port.go:292-324`); the split is stated
  as deliberate at `port.go:71-76`.
- **Violation.** A channel has **no separate existence oracle**. The only
  observable is the settlement race, so existence and consumption collapse into
  the same event. A `Settle` that fails on the funder's refund path proves the
  outpoint was never spendable — which is *consumption* evidence being used as
  *existence* evidence, the exact conflation the rule forbids.
- **Severity: fatal.** Fixing it requires a chain query, i.e. a new oracle, i.e.
  "the counterfactual where the payer watches the chain" — excluded by
  `SPIKE-BRIEF.md:95-96`.

### 5.2 `Acquire` must be an atomic swap, and the port's whole recovery story rests on it

- **Rule.** "One swap-based receive for every backend… the incoming instrument
  is *destroyed* by being spent, so receiving one means re-issuing it"
  (`bearer-instrument-port.md:133-135`, `:161-163`; also
  `research/bearer-port-demo/README.md:93-97`); the physics claim at `:131-135`;
  the
  atomicity contract in code at `research/bearer-port-demo/authorities.go:180-191`
  with the ordering spelled out (`:230-245`) and pinned by
  `TestAcquireIsAtomicAndDetectsDoubleRedemption` (15/15).
- **Violation.** `Settle` is **not a swap**. It is `N`-in / `0`-out: nothing is
  re-issued, and nothing replaces the consumed asset. Cashu's atomicity
  (mark-spent *and* sign-replacement, or neither) has no channel equivalent.
  Worse, the channel's consumption is *contestable*: the funder's refund branch
  may win. So `Settle`'s failure is not "nothing happened" — it is "one of two
  live branches confirmed, and the paying party cannot tell which without the
  chain."
- **Severity: fatal.** The port's crash-consistency argument
  (`bearer-instrument-port.md:145-147`) depends on a refusable, single-writer
  authority. A chain race has two writers and no refusal.

### 5.3 The port must never require the redeemer to hold chain state — and the channel does

- **Rule.** The payer-blindness requirement is the spike's own crux
  (`SPIKE-BRIEF.md:95-96`: "Any construction that quietly requires the payer to
  watch the chain has not answered the question"). The brief's permissible
  relaxation is that the **router** may have WAN
  (`SPIKE-BRIEF.md:49-53`).
- **Code / docs.** `WalletPort` has no chain-facing method at all: the full
  surface is `src/tollwallet/port.go:187-267`, and every method speaks Cashu/
  Lightning (mint URLs, NUT-04/05 quotes). There is no block-height, no outpoint,
  no script, no fee input anywhere in the interface.
- **Violation.** The relaxation is a *liveness* assumption, not a *safety* one.
  If the router is offline (or the fee market spikes) past the channel's
  **expiry**, the funder's refund path wins, and the customer's money is gone
  with **no recourse** — because the funder is the party that decided the expiry,
  and the redeemer's only remedy is the broadcast that has now become
  unbroadcastable.
- **Severity: fatal for the router's own failure budget.** The repo already
  treats "the router can lose power, the process can be killed" as the base case
  it must survive (`AGENTS.md:29-31`). A channel makes the *worst* outcome of
  that case unrefundable.
- **Note on the bootstrap comparison.** `bootstrap.go:244-249` explicitly refuses
  to re-forward an ambiguous proof and routes it to operator reconciliation.
  A channel has **no reconciliation step** after expiry. The channel's failure
  mode is strictly worse than the one #239 was written to survive.

### 5.4 NUT-09 recovery assumes server-side memory

- **Rule.** `bearer-instrument-port.md:26` (recovery replays the holder's
  derivation against *that log*), `:72-80` (sweep from index 0; the sweep
  argument); the `RecoverMode`s enumerate *where the memory lives*
  (`research/bearer-port-demo/types.go:62-78`); pinned by
  `TestRecoverReplaysAgainstAuthorityIssuanceMemory` and
  `TestRecoveryWithWrongSeedYieldsNothing` (15/15).
- **Violation.** A channel has **no server-side memory**. The durable backup is
  the `n-1`-deep commitment-tx chain, held by the **funder**. Recovery is
  *cooperative*: it happens iff the funder is honest and online. That is a
  **custody** assumption, which `SPIKE-BRIEF.md:98` demands be named exactly.
  The honest name is: **the funder is a de-facto recovery custodian.**
- **Compounding:** the `RecoveryBatch = 8` gap caveat
  (`research/bearer-port-demo/port.go:9-21`) survives into the channel as a hard
  loss rather than a truncation, because there is no `Issued()` to ask.
- **Severity: disqualifying under the brief's own framing.** The brief asks for
  *non-custodial*. A design whose loss-recovery depends on a counterparty's
  goodwill is custodial along the axis that matters most (loss, not theft).
  `RecoverModePreSignedTxSet` (§2) is added so the port can *declare* this
  instead of silently returning zero — because "a port that returns 0 turns 'you
  lost everything' into 'you own nothing'."

### 5.5 The point-of-no-return rule assumes the ambiguity is *reconcilable*

- **Rule.** `AGENTS.md:28-35` requires each money-touching change to state "the
  point of no return, what durable state is written **before** it, recovery
  behavior after a crash at each step, retry and duplicate-execution behavior,
  and the compensating action if a later step fails"; `AGENTS.md:69-73`:
  "Ambiguous network results must be reconciled, never blindly retried."
- **Code.** `AGENTS.md:57-60` — no irreversible op before local validations
  (#403, #409). Enforced at `src/merchant/merchant.go:1573` (`receiveReference`
  before anything moves), `:1584-1597` (`beginReceiveIntent`, persist-before-
  effect), `:1640` (`Receive`), `:1656-1692` (the deadline branch, with the
  explicit note that no session is granted and the customer is told not to
  resubmit); the journal's contract at `src/merchant/receive_intent.go:1-31` and
  `:283-286` (a NUT-07 all-unspent reply after the call ended is what proves
  `abandoned`); the durability hardener at
  `src/merchant/journal_durability.go:7-19` (`syncParentDir`, #502/#834).
- **Violation.** The channel *has* a point of no return, but the compensating
  action does not exist. The **only** compensating action the repo can perform is
  a NUT-07 ask proving all-unspent (`receive_intent.go:283-286`) — and that
  requires the mint that a channel removes by design. There is no `abandoned`
  state for a channel: the states are `settled`, `refunded-by-funder`, and
  `unknown-until-expiry`, and the third resolves to the second, i.e. to a
  customer funds loss.
- **Severity: fatal.** This is the invariant that decides §6, and it is why the
  answer is "nowhere in the port": the port's failure discipline assumes an
  authority that can be re-interrogated. A channel's design goal is to delete
  exactly that authority.

### 5.6 `WalletPort`'s acceptance contract, and the promise that a backend is one adapter

- **Rule.** `docs/architecture/walletport-contract.md:76-83` — a candidate is
  scored on **OpenWrt build** (`:78`, both `aarch64` **and** `mipsel` musl,
  static, `CGO_ENABLED=0` preferred), flash writes per payment (`:80`),
  reliability axes (`:81`), ownership grade (`:82`), and **Licence** (`:83`,
  GPL-3.0-compatible, "gating for reuse"). The contract's own conclusion is at
  `:119-121`: "the decisive axes are **mipsel buildability**, **security
  parity**, and **fork-ownership** — not feature breadth." The bearer-port
  doc's central claim is that a new instrument kind is
  "an adapter, not a port change" (`research/bearer-port-demo/README.md:32-34`,
  `authorities.go:16-24`).
- **The promise the port makes and a chain backend cannot keep.** The port's
  premise is "no dependencies, no network, no Cashu or fedimint code — the whole
  point is that the port does not need any"
  (`research/bearer-port-demo/README.md:17-18`). A channel needs a **Bitcoin
  transaction verifier and a chain source**: script construction, CSV/nSequence
  verification, sighash computation, tx relay. The repo has the EC keys for
  identity only — `src/identity/go.mod:13-17` pulls `btcec/v2`, `btcutil`,
  `chainhash`, `secp256k1/v4`, and `src/identity/identity.go:1-14` uses them for
  HKDF-derived addresses and a BIP39/NIP-06 seed. There is **no** `txscript`,
  `wire`, script-building or sighash code anywhere in the tree (grep: no
  matches). So the channel backend would breach the port's stated minimalism
  claim and add a new dependency family.
- **And the licence tripwire fires.** The upstream library for pre-signed
  Bitcoin transaction construction is `btcd`'s `txscript` (**ISC**), but the
  repo's licence gate is "GPL-3.0-compatible" (`walletport-contract.md:83`)
  tested against a manifest field per backend
  (`src/tollwallet/manifests/*.json`, e.g. `gonuts.json`:
  `"licence": "GPL-3.0-only (fork)"`). This is a **process** problem rather than
  a legal one for ISC — but it is a required manifest field, an ownership grade
  (`walletport-contract.md:82`), a build axis for a dependency family that does
  not exist in the tree, and therefore a real integration cost that the demo's
  "15 tests, no dependencies" milestone cannot express.
- **Severity: fatal to the *engineering* case.** Not because any one line
  forbids it, but because the port's own value proposition is that a new backend
  is a small adapter (`walletport-contract.md:119-121`). A chain backend is not
  feature breadth; it is a second protocol
  stack with its own liveness, fee market, and expiry semantics.

### 5.7 No `Authority.Issue` means no derivation space, hence no key management

- **Rule.** The seed "is the port's own recovery material and stays in the
  port's store; an instrument holds only derived, one-way values. There is
  deliberately no `Root()` accessor to misuse"
  (`research/bearer-port-demo/instrument.go:45-48`); pinned by
  `TestSeedNeverLeavesPortAndCounterPersistsBeforeExposure` and
  `TestDerivationIsPureAndSpentnessLivesAtTheAuthority` (15/15).
- **Violation.** A channel needs **signing keys**, not a derivation counter, and
  it needs the funder's key material to sign `n` commitments. Tooling that holds
  the funder's keys is at a different (and higher) risk class than a wallet that
  holds a seed for blinded outputs.
- **Severity: high, and it is an *operator* risk not a code risk.** The manifest
  contract already records `storage.seed_at_rest: "plaintext"` for every current
  backend (`src/tollwallet/manifests/gonuts.json`, `cdk.json`, `nucula.json`) and
  `walletport-contract.md:54-55` names plaintext-seed-at-rest as a **known gap**
  that "a replacement must not regress". Adding a second key family on a router
  that already stores one seed in plaintext widens that gap rather than closing
  it.

## 6. Recommendation

### 6.1 Does this belong in `WalletPort`, beside it, or nowhere?

**Beside it. Not in it, and not merged into the `Authority` family.**

Reasoning, shortest form:

1. `WalletPort`'s contract is Cashu-shaped and its *value* is that it is small
   and stable (`walletport-contract.md:76-83`, `:119-121`). The channel adds a second
   protocol stack (chain verification, fee market, expiry races) that no
   existing axis on that contract measures.
2. The `Authority` abstraction is defined by **memory** and says so
   (`bearer-instrument-port.md:30-31`). A channel has no memory. Admitting it as
   a fourth adapter would falsify the abstraction's own stated definition, and
   §1 Q1/Q3 show it passes only under a new field that encodes *the absence of
   the thing the port requires*.
3. The failure-mode fit is worse than what is already accepted. #239's
   ambiguous outcome is *reconciled* by an operator
   (`bootstrap.go:244-249`); a channel's ambiguous outcome is *final* (§5.3,
   §5.5).
4. The engineering cost is a new dependency family and a new licence/ownership
   manifest entry (`walletport-contract.md:78-83`) for a backend the router
   would call on its most constrained tier — while the incumbent already solves
   the *redeemer*-offline case that a channel gives up (§3).

### 6.2 What IS worth taking from this spike (and what is already planned)

- **`Adjudicator` as a third capability axis** — genuinely new versus
  `#631`'s five-item plan. `#631`'s item 4 replaces the two booleans with
  `{state_check, recover}` (`bearer-instrument-port.md:163-165`). It does
  **not** add *who decides*. That is a real gap: the field is what lets a
  backend that "cannot answer" say *why* it cannot, which is the same defect
  `bearer-instrument-port.md:49` already identified.
- **`RecoverModePreSignedTxSet`** — a new value in an existing enum
  (`types.go:62-78`). Zero cost, and it makes "you lost everything"
  distinguishable from "you own nothing". Worth landing on its own merits,
  independently of whether any channel ever ships.
- **NOT worth taking:** `SettlementPort` itself. The temptation to add richer
  per-party datapoints to the channel's instrument (a splice reference, an
  expiry height, a "we can refund" flag) should be resisted for a reason the
  port already states: such a field is honest only if the *counterparty* writes
  it. A router writing `we can refund` unilaterally is the same lie
  `bearer-instrument-port.md:30-31` refuses when it says an authority with no
  memory is a signed sheet of paper. Leave them out.

### 6.3 Honest limits of this memo

- **No channel was implemented.** This is an architecture-fit analysis; every
  claim about channels is from the prior art named in `SPIKE-BRIEF.md:63-75`
  (Spilman 2013; Poon-Dryja 2016 `OP_CSV`; Decker-Wattenhofer; Ark), not from
  code I ran. The only things I ran are the existing demo (15/15) and a
  type-check of the §2 delta (well-typed) — both captured in `evidence/`. No
  channel was built, and nothing in §3-§5 is a measurement.
- **The `CapabilityV2`/`SettlementPort` code in §2 is a design delta, and it is
  written against the *demo's* type vocabulary** (`research/bearer-port-demo/`),
  not `src/tollwallet/`, because the demo is where
  `Authority`/`Capability`/`CheckMode` actually live today.
  `src/tollwallet/port.go:187-267` is `WalletPort` proper and has no `Capability`
  type at all; the manifest booleans (`src/tollwallet/manifests/*.json`) are the
  only current encoding of capability. What is *proven* is only that the delta
  is **well-typed** against the real demo types (`evidence/archfit-delta-typecheck.txt`);
  nothing about its runtime behaviour is proven, and no channel code was written.
- **I did not disprove the channel's usefulness in general.** I disproved it for
  *this* consumer: a TollGate router that redeems, on the small tier, with an
  offline-window failure budget. A different consumer — one that can guarantee a
  redeemer online inside the expiry window — could use exactly the §2 interface.
  The `Adjudicator.VerifiableByHolder` field exists to make that distinction
  mechanical rather than rhetorical.
- **Two commands produce every number in this memo, and there are no other
  numbers in it.** The demo run (15/15) and the §2 type-check
  (`evidence/verify_delta.sh`). Nothing here is estimated, and per
  `SPIKE-BRIEF.md:79-81` that is the whole of the measurement claim.

### 6.4 The one-line answer for the PR

> A one-way channel is describable to the bearer-instrument port only by adding
> an `Adjudicator` field that records *that the port's central requirement —
> memory — is absent*; it passes the consumption question as `probe` and fails
> the existence and recovery questions outright, and it replaces #239's
> reconcilable ambiguity with an expiry that ends in customer funds loss. No.
> Beside the port, not in it.
