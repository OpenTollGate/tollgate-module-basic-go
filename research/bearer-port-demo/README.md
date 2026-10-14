# `bearerport` — a bearer-instrument port, and where NUT-07 / NUT-09 come from

**Status:** research demo, 2026-10-01. Companion to
[`docs/architecture/wallet-backend-fedimint.md`](../../docs/architecture/wallet-backend-fedimint.md)
and §5 of [`walletport-contract.md`](../../docs/architecture/walletport-contract.md);
the findings write-up is
[`docs/architecture/bearer-instrument-port.md`](../../docs/architecture/bearer-instrument-port.md).

Run it:

```bash
cd research/bearer-port-demo
go test ./...            # 15 tests, the contract as assertions
go run ./cmd/demo        # the narrated transcript (see DEMO-OUTPUT.txt)
```

No dependencies, no network, no Cashu or fedimint code — the whole point is that
the port does not need any.

## What this proves

The port is generic over bearer instruments for exactly one reason: **it does
not model any of them.** It requires of an authority two memories and a
declaration:

| Requirement | Why NUT-07 / NUT-09 both live here |
|---|---|
| What did you sign? | NUT-09 recovery: replay the holder's derivation against this log |
| What did you consume? | NUT-07 state: this is the only place spentness exists |
| How can you answer? | query / probe / none — declared, so silence is never read as "unspent" |

Three adapters (`CashuAuthority`, `FedimintAuthority`, `PowAuthority`) implement
the same `Authority` interface and differ only in those three fields. Nothing in
`port.go` knows what `cashuA`, `fed1` or `pow1` look like inside.

## The two derivations

### NUT-07 — "is this instrument spent?"

NUT-07 is not a feature you implement; it is a question you can only ask if the
authority has a memory to answer from. Three modes, all honest:

- **query** — the authority exposes a read-only state API (Cashu `/checkstate`).
- **probe** — it has none, so the only sound way to learn the state is to
  attempt the consumption (fedimint reissue). That is a mutation: probing a
  *live* note claims it. Declared, not hidden.
- **none** — no sound way. The port reports `UNKNOWN` and says why
  (a miner's payout ledger keys on the account, not the nonce).

Two rules are load-bearing, and both are tests:

1. `UNSPENT` and `SPENT` are things the authority *said*. Anything else is
   `UNKNOWN`, which fails closed. Mapping "no answer" to "unspent" is how a spent
   note gets re-sold.
2. **A NUT-07 answer is about consumption, never existence.** A plain Cashu mint
   answers `UNSPENT` for a nullifier it has never seen, because it reports
   membership in its spent set. Existence is a different question, answered from
   the issuance log — which is what `Decode` does. Conflating the two is how a
   forged note passes a naive "is it spendable?" check.

The nullifier itself is `HMAC(seed, index)` under a **per-authority** label:
deterministic, one-way, and scoped so a mint and a federation never share a
namespace. Spentness is never derived — the authority learns it at redemption,
because redemption is the only moment the secret is revealed.

### NUT-09 — "recover my funds from the seed"

NUT-09 is the same deterministic derivation, replayed against the authority's
**issuance log**:

- the sweep starts at index 0 (Cashu `/restore` does too — starting at the
  current counter silently skips live outputs below it, which always exist after
  a swap);
- an index the authority recognises but reports *spent* counts as continuity,
  not a miss;
- a wrong seed recovers nothing, because a different seed derives different
  commitments and the seed never crosses the wire.

The port never assumes one memory shape: Cashu replays a signing log, fedimint
restores the federation's backup of the note set, a miner's pool returns the
payout ledger. Same interface, `Issued(commitment) -> {amount, spent}`.

## What it would take, concretely

Not a rewrite. Five changes, each about a PR:

1. **Namespaced target key.** `cashu:<url>`, `fedi:<federation-id>`,
   `pow:<miner>`. The existing `normalizeMintURL`/`registeredMints` code keeps
   working; a federation id and a mint URL simply can never be spelled the same.
2. **Backend-scoped `Decode`.** `cashuA`/`cashuB` parsing becomes one adapter's
   job. The port hands callers `{kind, authority, unit, face, attested,
   nullifier, commitment, blob}` and nothing else.
3. **One swap-based `Acquire` for every backend.** Unavoidable physics: a bearer
   instrument is *destroyed* by being spent, so receiving one means re-issuing
   it. Modelling receive as "redeem and reissue" (N inputs, balanced M outputs,
   atomic) is what makes NUT-09 honest — after the swap the value sits at a
   *fresh* index, which is exactly what the demo shows.
4. **A declared capability matrix** replacing the `nut_07`/`nut_09` booleans in
   `src/tollwallet/manifests/*.json` — the booleans were already the right idea;
   they just need a third value ("cannot answer") and a recovery-location field.
5. **The two derivations above, each pinned by a test.**

## Files

| File | Contents |
|---|---|
| `derive.go` | the one derivation (a stripped-down NUT-13) and the commitment/seed tags |
| `types.go` | `State`, `CheckMode`, `RecoverMode`, `Capability`, the error vocabulary |
| `instrument.go` | `Instrument` — the backend-neutral decoded instrument |
| `authorities.go` | the `Authority` interface and the cashu / fedi / pow adapters |
| `port.go` | the port: decode, acquire (swap), spend, check-state, recover |
| `port_test.go`, `derivation_test.go` | the contract, as 15 assertions |
| `cmd/demo/main.go` | the narrated transcript |
| `RED.txt`, `GREEN.txt`, `DEMO-OUTPUT.txt` | captured real execution output |

## Honest limits

- The encodings (`cashuA|commit|amount`) are a demo format, not Cashu's. The
  port never reads inside them, which is the property under test.
- No fees, no denominations, no blinded signatures: a swap balances exactly.
  A real mint's fee model and MPP/denomination split are orthogonal to the two
  questions this demo is about, but they are real work.
- The `RecoveryBatch` sweep has a genuine truncation failure mode, named in the
  code: a gap of `RecoveryBatch` or more consecutive unknown indices stops the
  sweep early. Real wallets have the same property.
- The demo's cashu adapter records nullifiers at issuance (a deliberate
  extension). The `NewRawCashuAuthority` variant models the plain behaviour and
  is used to pin the consumption-vs-existence distinction.
