<!-- markdownlint-disable MD013 -->

# A bearer-instrument port: how NUT-07 and NUT-09 are derived, not invented

**Status:** research, 2026-10-01. Companion to
[`wallet-backend-fedimint.md`](wallet-backend-fedimint.md) and §5 of
[`walletport-contract.md`](walletport-contract.md); neither is superseded.
Working code and a narrated demo:
[`research/bearer-port-demo/`](../../research/bearer-port-demo/) (15 tests,
`go test ./...`; transcript in `DEMO-OUTPUT.txt`).

## 1. The claim, stated narrowly

The fedimint study concluded that the port is "explicitly a *Cashu* seam" and
that NUT-07/NUT-09 are "satisfiable **by a rule plus a test**, not by a feature
that exists". That was honest but left the rule unspecified. This document
specifies it, implements it, and finds that the conclusion was too pessimistic
about how much already works and too optimistic about how little would change.

The port is generic over bearer instruments for exactly one reason: **it does
not model any of them.** It asks an authority three things and requires nothing
else:

| Required of an authority | Why this is where both NUTs come from |
|---|---|
| What did you **sign**? | NUT-09 recovery: replay the holder's derivation against that log |
| What did you **consume**? | NUT-07 state: spentness exists nowhere else |
| How can you **answer**? | declared — so silence is never read as "unspent" |

An "authority" with neither memory is not an authority; it is a signed sheet of
paper, and no port can use it. That is the whole abstraction.

## 2. Where this leaves the existing implementation

TollGate is closer to this than it looks, and the gaps are smaller than
"rewrite the wallet":

| Already generic | Evidence in this tree |
|---|---|
| The seam exists | `src/tollwallet/port.go` — `WalletPort` is already the seam |
| Backend selection | `policy.go`, `select.go`, `manifests/*.json` (kind, arches, socket) |
| Declared capability | `manifest.go`'s `NUT07`/`NUT09`/`Restore` booleans |

| Still Cashu-shaped | Where it bites |
|---|---|
| `DecodeToken` is `cashuA`/`cashuB` | `port.go`, and the CLI's `wallet drain cashu` |
| Target key is a mint URL | `normalizeMintURL`, `registeredMints`, policy matching |
| Spent-detection is error-string matching | `tollwallet.go` `isAlreadySpentError` (`"already spent"`) |
| Capability is two booleans | a backend that *cannot* answer has no way to say so |

## 3. The NUT-07 derivation

NUT-07 is not a feature. It is the question *"have you consumed this?"*, and it
is only askable when the authority holds a memory to answer from. Three modes,
each honest about itself:

| Mode | What it is | Cost |
|---|---|---|
| `query` | a read-only state API (Cashu `/checkstate`) | free, side-effect-free |
| `probe` | no such API — the state is learned by attempting the consumption | **it is a mutation**: probing a live note claims it |
| `none` | no sound way to learn the state | the port answers `UNKNOWN` and says why |

Two rules are load-bearing:

1. **`SPENT` and `UNSPENT` are things the authority *said*.** Anything else is
   `UNKNOWN`, and `UNKNOWN` fails closed. Mapping "no answer" onto "unspent" is
   how a spent note gets re-sold.
2. **A NUT-07 answer is about consumption, never about existence.** This is the
   sharp edge and the reason `Decode` and `CheckState` must stay two questions.
   A plain Cashu mint answers `UNSPENT` for a nullifier it has never seen in its
   life, because `/checkstate` reports membership in the *spent set*. So
   `UNSPENT` means "not consumed", not "real". Existence is answered from the
   signing log — and conflating the two is exactly how a forged note passes a
   naive "is it spendable?" check. The demo pins both behaviours
   (`TestNut07AnswersConsumptionNotExistence`).

**Where the nullifier comes from.** It is `HMAC(seed, index)` under a
**per-authority** label: deterministic, one-way, and scoped so a mint and a
federation never share a namespace (otherwise accepting a note at one could make
the port refuse an unrelated note at the other, and one holder's activity would
be correlatable across institutions). It is not carried on the instrument and
cannot be read back into the seed.

**When the authority learns it.** At *redemption* — that is the only moment a
secret is revealed. This is the fact that makes NUT-07 hard: at issuance a mint
sees a blinded message, so it knows the commitment and not the tag. A mint that
answers `query` soundly must have chosen to learn the tag earlier, which is a
deliberate extension, not standard Cashu.

**What this changes in existing code.** `isAlreadySpentError`'s string matching
(`tollwallet.go`) becomes one adapter's job: the port consumes a `State`, not a
message. And the mode matters for ordering. `AGENTS.md` already requires local
validations before irreversible operations (#403, #409). NUT-07 adds a
*remote* read — and for a `probe` authority it is itself irreversible, so probe
mode **cannot be a pre-flight at all**; it *is* the receive. The safe order
becomes: `Decode` (existence, local + issuance log) → `CheckState` (consumption,
only where the mode makes it a read) → the swap.

## 4. The NUT-09 derivation

NUT-09 is the *same* deterministic derivation, replayed against the authority's
signing log. It is not a backup format; it is a question the holder asks about
its own derivation space.

Rules, each earned:

- **Sweep from index 0.** Cashu's `/restore` does, and the reason is a
  funds-loss bug if you "optimise" it: starting at the current counter silently
  skips every live output *below* it, and after one swap-and-reissue cycle there
  always is one. The demo reproduces that exact shape — acquire, then recover,
  and the live output sits at a *fresh* index.
- **A spent index counts as continuity, not a miss.** Knowing index `i` was used
  is what proves index `i+1` is worth asking about.
- **A wrong seed recovers nothing.** A different seed derives different
  commitments, and the seed never crosses the wire, so there is nothing to
  attribute.
- **The counter is persisted before exposure.** Otherwise a crash between "the
  authority signed index N" and "we recorded that we own index N" leaves
  recovery with no durable index — the same rule `AGENTS.md` states for #266.

One honest caveat, named in the code: a gap of `RecoveryBatch` or more
consecutive unknown indices truncates the sweep early. Real wallets have the
same property; the mitigation is the persisted counter above.

**The port does not assume one memory shape.** Cashu replays a signing log,
fedimint restores the federation's backup of the note set, a miner's pool
returns the payout ledger. One interface —
`Issued(commitment) -> {amount, spent}` — and the port never learns which it is
talking to.

## 5. Why `Acquire` has to be a swap

This is the finding that changed the demo's design, so it is worth stating
plainly: **a bearer instrument is destroyed by being spent, so receiving one
necessarily means re-issuing it.** A wallet that "receives" is not adding value
to a pile; it is presenting the instrument to the authority (which kills it) and
getting a fresh one back in its own derivation space. Cashu NUT-03 is N inputs
to M balanced outputs, and that is not an implementation detail — it is why
recovery can be safe: after the swap, the value sits at a *fresh* index, and a
lost device cannot resurrect the spent one.

Two consequences:

1. **Atomicity is the contract, not a nicety.** Either every input is marked
   spent *and* every replacement is signed, or nothing happened. The demo
   refuses a swap whose outputs do not balance, before mutating anything.
2. **`Acquire` and `Spend` are the same primitive in opposite directions**, so
   they cannot diverge in crash behaviour. That is a structural answer to the
   `Receive → session → gate-open` chain in `AGENTS.md` (#258, #403): the wallet
   half is now one atomic step with one recovery story.

## 6. What it would take, concretely (smallest first; each ≈ one PR)

1. **Namespaced target key.** `cashu:<url>`, `fedi:<federation-id>`,
   `pow:<miner>`. Existing canonicalisation and selection keep working; a
   federation id and a mint URL simply cannot collide. Documented in `port.go`
   as a deliberate extension, not a string hack found later.
2. **Backend-scoped `Decode`.** `cashuA`/`cashuB` parsing becomes one adapter's
   job. Callers receive `{kind, authority, unit, face, attested, nullifier,
   commitment, blob}` and nothing else — no proof, no signature, no keyset.
3. **One swap-based receive for every backend** (§5). This is the largest of the
   five and the one that touches money paths; it lands behind the existing port.
4. **The capability matrix** replacing `nut_07`/`nut_09`/`restore` with
   `{state_check: query|probe|none, recover: issuance-log|authority-backup|
   account-ledger|none}`. A backend that cannot answer must be able to say so.
5. **The two derivations above, each pinned by a test**, plus the
   consumption-vs-existence test. These are the rules the fedimint study said
   were owed; they now exist at
   [`research/bearer-port-demo/`](../../research/bearer-port-demo/).

## 7. What is still owed

- **Existence for an unknown instrument.** `Decode` refuses anything no
  registered authority recognises, which is sound but means a federation whose
  log is unavailable is indistinguishable from a forged note. The error should
  distinguish "unreachable" from "not mine" at every call site.
- **Fees and denominations.** The demo balances exactly. A real mint's input
  fees (NUT-02) and denomination split are orthogonal to the two questions here,
  but they are real work and they interact with `SwapFeeSats`.
- **Probe-mode ordering in the merchant path.** §3 says probe cannot be a
  pre-flight. That needs to be enforced where `Receive` is called, not just
  documented.
- **The thin fedimint RSS** — unchanged by this work, still the deciding number
  for whether fedi is a large-tier sidecar at all
  ([`wallet-backend-fedimint.md`](wallet-backend-fedimint.md) §5).
- **A NUT-07 answer for the *incoming* note at the mint.** The port asks about
  its own outputs. When receiving, the incoming instrument's state is learned
  only by attempting the swap — which is why `ErrTokenAlreadySpent` still has to
  exist, but as a *consequence* of an atomic operation rather than a string
  match.

## 8. Recommendation

Proceed with the five items above, in that order, as a **generic-port** track
rather than a fedimint track. The demo shows fedi needs no special treatment: it
is one adapter differing in three declared fields, and the Cashu adapter is not
privileged anywhere.

The counter-recommendation from the fedimint study stands and is reinforced: do
**not** reduce scope to "swap gonuts for fedimint" — that would mean re-deriving
NUT-07 and NUT-09 behaviour that is currently load-bearing, on a backend that
cannot serve the small tier at all. Adding a backend and generalising the seam
are the same job, and the seam is the cheaper half.
