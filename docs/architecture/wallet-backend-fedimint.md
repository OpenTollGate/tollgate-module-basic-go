<!-- markdownlint-disable MD013 -->

# Fedimint ("fedi") as a TollGate wallet backend — findings

**Status:** research, 2026-09-30. Extends §5 of
[`walletport-contract.md`](walletport-contract.md) and the per-target table in
[`wallet-integration-decision.md`](wallet-integration-decision.md); supersedes
neither. Judged against the contract, not against a wish list.

**Evidence base:** the `fedimint-clientd` cross-build and federation probe
measured on this fleet (see the size/RAM rows below; full artefacts in
`felixfelix-bot/soveng-archive`, reports/fedimint-clientd-openwrt/), the
`fedimint-router` spike project, and the upstream repositories
(`fedimint/fedimint` v0.12.1, `fedimint/fedimint-clientd`, `fedimint/fedimint-sdk`).

## 1. Fedimint is not a Cashu library, and that is the whole difficulty

Cashu has mints, proofs held locally, and NUT-04/05 quotes. Fedimint has
**federations** (multi-guardian, threshold custody), a client database that
tracks notes *it* issued into the federation, and Lightning through a
**gateway** registered with that federation. There is no mint URL, no NUT-04/05
quote, and no local proof set to swap. `WalletPort`
([`src/tollwallet/port.go`](../../src/tollwallet/port.go)) is explicitly a
*Cashu* seam (its own header says so), so a fedimint backend is a translation
layer, not a drop-in.

That has two consequences for this repo:

1. Several WalletPort methods have **approximations**, not equivalents (§6).
2. Custody and trust change shape: one mint operator → a guardian federation.
   Whatever ships must say so in the operator guide; that is integration cost
   too, not just code.

## 2. Form: sidecar only

| Option | Verdict |
|---|---|
| **Sidecar daemon** (Rust fedimint client speaking the AF_UNIX JSON protocol from sidecar.go) | **the only viable form**; fits the existing per-target decision |
| cgo/FFI to fedimint-client (Rust staticlib) | **rejected**: breaks the CGO_ENABLED=0 rule (AGENTS.md), needs a Rust cross toolchain inside the Go build, and cannot target mips at all (§3) |
| Reuse upstream fedimint-clientd REST + wrappers/fedimint-go | **rejected as-is**: the daemon is dead (last commit 2024-10-20) and its client protocol no longer works — measured **0 of 6** live 2026 federations accept it (list_gateways → Response deserialization error: Invalid port); the Go wrapper was last touched 2024-10-09 |
| CLI wrapper (spawn per call) | rejected for the same reasons CDK's CLI wrapper was rejected in the decision doc: cold start, no reuse, shells out on the router |

## 3. Architectures

| Target | Built? | Note |
|---|---|---|
| aarch64 (Cortex-A53, MT6000 class) | ✅ measured | static musl, static-pie, no libc dependency |
| arm / armv7 | ✅ (toolchain supports it) | not built here |
| mipsel_24kc / mips_24kc | ❌ **impossible** | the crypto stack is ring 0.17, whose complete arch set is x86 / amd64 / aarch64 / arm / wasm32 — there is **no mips backend** (build.rs on the pinned version, read directly) |

This is a harder gate than CDK's mipsel problem (a missing AtomicU64 in the
Go/FFI path, i.e. a build bug). Fedimint cannot serve the small/16 MB mipsel
tier at all — it can only ever be a **large-tier** backend, which is exactly
where the integration decision already wants a sidecar.

## 4. Size, footprint, licence, ownership

| Axis | Fedimint | Reference points already in the decision doc |
|---|---|---|
| Stripped size, aarch64 | **14,906,072 B (14.2 MiB)** measured; **5,653,872 B (5.4 MiB)** with UPX (lzma) | CDK sidecar 6.02 MiB (aarch64) / 7.51 MiB (mipsel) |
| Caveat on that number | it is the *fat* tree: fedimint 0.4.2 clientd with RocksDB, axum REST, nostr/NWC, prometheus **and** Lightning. The thin fedimint 0.12 client (redb, no REST, no nostr) is the actual target and is expected to be smaller — being measured now | — |
| RSS | **62.5 MiB peak at startup** on the fat build (RocksDB open + tokio + module init); thin build unknown | CDK sidecar ~6 MiB |
| Licence | **MIT** ✅ (GPL-3.0-compatible — the gating axis nucula failed) | nucula: no licence |
| Engine ownership | **grade A** — fedimint/fedimint v0.12.1, actively maintained (pushed 2026-09-30) | CDK: A/B |
| Adapter ownership | **grade C** — the daemon we ship is ours, because clientd is dead (§2) and fedimint-sdk targets mobile/web | gonuts: C (fork) |

**Combined shape:** *A-grade engine + C-grade adapter.* That is new — CDK is
upstream-all-the-way, gonuts is a fork we own entirely, nucula had no licence.
It means fedimint's protocol maintenance is upstream's problem, but the sidecar
protocol, the translation rules, and the router packaging are ours forever.

## 5. Flash and RAM against the tier table

The decision doc measured the installed TollGate footprint at ~18 MiB (service
11.52 + CLI 6.82) and put the sidecar threshold at **≥128 MB RAM / large tier**.
Fedimint fits the same slot as CDK with roughly the same flash cost (5.4 MiB UPX
vs 6.02 MiB), and *possibly* a much larger RAM bill (62.5 MiB fat → unknown
thin). Until the thin RSS is measured, the honest statement is: **flash —
comparable; RAM — unproven, and it is the deciding number.** A 128 MB router
running the module, the CLI, hostapd and now a 60 MiB wallet daemon is not a
configuration to promise without the measurement.

## 6. Contract mapping (what needs new rules, not just an adapter)

| WalletPort method | Fedimint equivalent | Gap |
|---|---|---|
| DecodeToken | fedimint ecash notes (its own fed1…/note encoding) | port.go + WIREFORMAT.md assume cashuA / cashuB; the decoder must become backend-scoped |
| Receive | **reissue** the notes (federation validates; double spend is detected here) | semantics match; the receiving key is a federation, not a mint |
| GetBalance, GetBalanceByMint, GetAllMintBalances | balance per **federation** | the "mint URL" axis does not exist; needs a target key (§8) |
| Send, SendWithOverpayment, Drain | spend notes | overpayment/fee semantics differ (federation fees, no NUT-08) |
| RequestMintQuote, GetMintQuoteState, MintTokens | LN **receive** via a gateway (invoice, await, reissue) | no NUT-04 quote object; needs a federation gateway to exist and be reachable |
| RequestMeltQuote, Melt | LN **pay** via a gateway with a fee cap | MeltToLightning(maxCost) maps to the gateway fee cap; no quote round-trip |
| **CheckTokenSpendable (NUT-07)** | **no token-level liveness API exists.** The safe equivalent is *attempt the reissue*: a rejected reissue means spent, and reissue is atomic per note | this is the exact requirement that disqualified nucula, so it must be pinned by a test — not assumed. It also changes the module's pre-flight order (validate → reissue) |
| **restore (NUT-09)** | **federation-side encrypted backup + restore** of the client's notes | arguably stronger than seed-only restore, but federation-dependent — a federation that does not accept backups cannot satisfy it |
| **crash-consistent storage** | fedimint client DB (redb — pure Rust — or RocksDB), versioned by the client | must be measured per the existing protocol (writes per payment, kill-mid-swap) |
| **seed at rest** | client secret + the password flag; encryption-at-rest **to verify** | the contract tracks plaintext seeds as a regression; fedimint may be better, but it is unverified |
| Shutdown | daemon lifecycle (procd respawn) | same as any sidecar |

Two of the contract's three "must add" gaps (NUT-07, NUT-09) are therefore
satisfiable **by a rule plus a test**, not by a feature that exists. That is a
material difference from "CDK has NUT-07/09 natively" and belongs in the review.

## 7. Can fedimint and CDK coexist on one router? Yes — and the design says so

The pieces are already there: the sidecar kind in
[`manifest.go`](../../src/tollwallet/manifest.go), per-target selection in
`policy.go` / `select.go`, and a socket per daemon in `wallet.backend` +
`wallet.socket`. Coexistence is normal operation, not a new mode:

- **Separate processes.** CDK in-process (or as its own daemon) and the fedimint
  daemon are independent; the Go service still links no Cashu/Rust library and
  stays CGO_ENABLED=0.
- **Separate sockets.** One sidecar socket per backend means the fedimint daemon
  cannot collide with `cdk-walletd`.
- **Manifest-gated.** `arches` is already a manifest field, so a fedimint
  manifest advertising aarch64 only while CDK advertises both tiers
  lets the *same* image behave correctly on both — no build fork.
- **Cost shape:** two daemons = two binaries and two RSS footprints. On the
  large tier that is affordable; on the small tier neither fits.

**Two real blockers before "both at once" is honest:**

1. **Target key.** Policy/selection keys on a mint URL
   (`normalizeMintURL`, `registeredMints`). A federation has an id/invite code,
   not a mint URL. Cheapest workable shape: a namespaced pseudo-target
   (fedi:<federation-id>) routed to the fedimint backend, so the existing
   canonicalisation and selection code keeps working — but it must be a
   *deliberate* contract extension, documented in `port.go`, not a string hack
   discovered later.
2. **Token decoder.** `DecodeToken`/`Token.Serialize` are `cashuA/cashuB`-shaped
   by contract, and the CLI (wallet drain cashu) and merchant pre-flight
   assumptions follow them. Fedimint notes need either a backend-scoped decoder
   or an explicit "this backend's tokens are opaque strings" rule.

A third, non-code blocker: **Lightning needs a gateway in the federation.** A
federation without a reachable gateway cannot serve `Melt`/`MintTokens`
equivalents at all, so backend *capability* depends on which federation the
operator configured — the manifest has an `extra` map for exactly this kind of
per-installation qualifier.

## 8. What integration would take (smallest first; each line ≈ one PR)

1. **Land the sidecar client.** `sidecar.go` + `manifest.go` + `policy.go` /
   `select.go` are on the `wallet-backend` PR (#395) and **not on `main`**; the
   decision doc already flags this. Fedimint cannot integrate before that
   merges — it is a prerequisite, not a parallel track.
2. **Manifest + target mapping.** `manifests/fedimint.json` (shape: copy
   `manifests/nucula.json`), the fedi:<id> target rule, and the policy entry
   that routes it. Small, testable against the existing manifest/policy tests.
3. **The daemon.** Our `fedimint-router` binary (thin, redb, static musl) plus a
   JSON-over-UNIX adapter exposing the WalletPort method names and an `info`
   manifest emitter. The adapter is small; the client is the project already
   scoped on the `fedimint-router` board (spike → core → ecash → onchain → LN).
4. **Translation rules with tests.** Reissue-as-liveness (with a double-spend
   test), restore via federation backup, fee-cap mapping for LN pay, and the
   opaque-note decoding rule.
5. **Measurement per the existing protocol.** Footprint (thin RSS is the
   deciding number), writes per payment, kill-mid-swap, mint/federation
   unreachable, clock skew, disk full — the same axes CDK and nucula were held
   to. Plus a router-visible check: the daemon under `procd` with the 0700/0600
   socket perms and `SO_PEERCRED` check the decision doc requires.
6. **Docs.** Operator guide section on federation custody (multi-guardian trust,
   invite-code provisioning, backup responsibilities) — user-visible, so it
   needs its own changelog entry.

The two load-bearing gonuts fork fixes (HTLC signature-enforcement bypass
`296c7bf`, swap proof-loss `7dc430b`) are only a **removal gate**: they matter if
fedimint *replaces* gonuts. As an additional large-tier backend it does not
trigger them.

## 9. Verifications owed before any of this is called a plan

| # | Question | How to settle it |
|---|---|---|
| V1 | Thin-client RSS and stripped size (the deciding numbers) | the fedimint-router spike/ladder cards; measured on the build node |
| V2 | Does a 2026 federation accept a 0.12 client at all? | live join against ≥3 federations from the observer feed — the spike's go/no-go |
| V3 | Is the client DB encrypted at rest? | inspect the client DB + the the password flag/secret path in fedimint 0.12; the contract's seed-at-rest axis |
| V4 | Does reissue-rejection behave as "spent" (atomic, no partial consumption)? | a double-spend experiment against devimint, pinned as a test |
| V5 | Gateway availability and fee caps in candidate federations | query the federations the operator intends to use |
| V6 | Writes per payment on flash (redb vs the gonuts bbolt baseline) | the existing measurement protocol |

## 10. Recommendation

Worth pursuing **as an additional, large-tier sidecar backend**, not as a
gonuts/CDK replacement:

- it passes the two gates that killed nucula (licence ✅, and NUT-07/09 are
  satisfiable by rule + test rather than absent);
- it is an aarch64/armv7-only story, so it can never be the answer for the small
  mipsel tier — the tiering in the decision doc stays valid;
- the deciding unknown is the **thin-client RSS**; if it lands near CDK's ~6 MiB
  it is a straightforward third backend, and if it lands near the 62.5 MiB
  measured on the fat build it is a large-router-only option that must be
  documented as such;
- conversely, do **not** reduce scope to "swap gonuts for fedimint" — the module
  is Cashu-shaped end to end, and a migration would have to re-derive NUT-07 and
  NUT-09 behaviour that is currently load-bearing.
