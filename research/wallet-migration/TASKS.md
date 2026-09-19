# Research tasks

Owner column is the agent/consultant expected to close it. Statuses: TODO /
IN PROGRESS / DONE. Every task finishes by committing its artifact to this branch.

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T1a | Baseline: measure gonuts footprint + storage + latency on the router | worker (firmware) | **DONE** (on-device 2026-09-18/19: flash writes per payment, latency, CPU, fault set; supersedes the 2026-09-14 first pass) | `03-baseline/gonuts.md`, `experiments/baseline-gonuts/` (scripts + raw) |
| T1b | Enumerate the `WalletPort` surface and the NUTs it implies (the acceptance contract) | worker (code) | **DONE** | `00-context/walletport-contract.md` |
| T2 | nucula deep dive: NUT coverage from code, licence intent, flash storage, key handling, real footprint, logic-vs-firmware portability | consultant A | **DONE** | `01-candidates/nucula.md` |
| T3 | CDK deep dive: licence terms, wallet API vs CLI, minimal feature set, musl cross-compile for router arches, RSS/threads, storage backends, NUT diff, churn/governance | consultant B | **DONE** | `01-candidates/cdk.md` |
| T4 | Method + overlooked metrics: finalise the metric set and the measurement protocol; add fault-injection scenarios | consultant C | **DONE** | `02-method/*` |
| T5 | Integration-architecture study: cgo+`cdk-ffi` static link vs sidecar process vs CLI wrapper — build cost, failure modes, debuggability on router | consultant B + worker | **DONE** (per-target decision: in-process gonuts on small/16 MB mipsel; CDK sidecar on large/aarch64; +6.0/+7.5 MiB measured) | `05-architecture/integration-decision.md`, `experiments/flash-budget/`, `experiments/cdk-sidecar/init/` |
| T6 | Migration path: existing router proof/key store → new wallet, including rollback | worker | **DONE** (token-transfer mechanism rehearsed green: migrate gonuts→CDK and rollback, no value lost; same-seed NUT-09 documented as alternative) | `03-baseline/migration-path.md`, `experiments/migration/` |
| T7 | Synthesis + recommendation, with dissenting opinions preserved | manager + all consultants | **DONE** | `04-reports/RECOMMENDATION.md` |
| T8 | Explicit consideration and rejection (or adoption) of the stay-on-gonuts option | manager | **DONE** | `04-reports/RECOMMENDATION.md` |

## Definition of done for the branch

A reader can rebuild every number, see the licence findings, reproduce the
cross-compiles, and read a recommendation that names the trade-offs and the
dissent — and can check all of it out from this branch alone.

## Added after prior-art discovery (2026-09-13)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T5a | Audit the existing `cdk_wallet.go` adapter: completeness, tests that actually run, build-tag wiring, mnemonic handling | worker (code) | **DONE** | `01-candidates/cdk.md`, `experiments/cdk-adapter/` |
| T5b | Verify `github.com/cashubtc/cdk-go`: published? maintained? pinned how? what does `go.mod` say — real version or `replace` to a local path? | consultant B | **DONE** | `01-candidates/cdk.md` |
| T5c | Cross-compile test: cgo + cdk-go static lib for `aarch64`/`mipsel` musl on OpenWrt — patch count and resulting size | worker (firmware) | **DONE** | `experiments/cdk-cross/`, `experiments/cdk-sidecar/`, `01-candidates/cdk.md` §T5c |

> **T5c result (2026-09-14):** cgo/`cdk-go` is **not buildable on OpenWrt musl**
> (glibc-only `.so`; source `cdk-ffi` drops `cdylib`, yields a 133 MiB `.a`).
> The **sidecar `cdk-cli` builds clean on aarch64** (19.7 MiB stripped, static)
> but **fails on mipsel** (`std::sync::atomic::AtomicU64` absent; `nostr-relay-pool`).
> Runtime RSS deferred to the physical aarch64 router.

## Added after the scope change: nucula → OpenWrt port (2026-09-13)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T2b | **Port spike:** compile nucula's wallet core on native Linux (x86_64), then cross-compile for a router arch (aarch64 musl) with the OpenWrt SDK. Measure size/RSS/threads. Exclude peripherals. | worker (firmware) | **DONE** (core 20/20 both arches; linked + **RUN**: aarch64 on the physical MT6000, mipsel under qemu-user with all self-tests passing; 1.77 MiB / 1.87 MiB) | `experiments/nucula-port/` |
| T2c | **Licence ask:** approach the author for an explicit licence (MIT/Apache-2.0/GPL-3.0 choice). Nothing ships before this. | operator/manager | **SENT 2026-09-14 (nucula#8)** | `01-candidates/nucula-licence-request.md` |
| T2d | ESP-IDF API-boundary inventory: every header/API the core files use, with the Linux replacement, and the portable-vs-entangled ratio | worker | **DONE** | `01-candidates/nucula-port-map.md` |
| T2e | Behavioural parity: run the same mint-quote/mint/swap/melt flows against a local mint on Linux and compare results with gonuts for the same inputs | worker | **DONE** (mint/send/decode/cross-receive identical; melt-quote N/A through the port -> finding) | `experiments/parity/` (`gonutsinterop/`, `behavioural_parity.py`, `raw/behavioural_parity-t2e.txt`) |

**Legal hygiene for this branch:** nucula's source is *not* committed here. The
branch carries our own port shims, diffs, measurement logs, inventory and plans
— never their unlicensed code. Anyone reproducing the spike fetches nucula at the
recorded revision themselves.

## Added 2026-09-13 (operator constraints)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T9 | Fork-ownership grading (A/B/C) for every option, with the measurement (our commits to the dependency, unadopted upstream releases, upstream CI coverage of musl/our arches) | worker | **DONE** | `03-baseline/fork-ownership.md` |
| T10 | "Un-fork gonuts" option study: could we contribute the fork's content upstream, or vendor a pinned upstream release? What does the fork add, commit by commit? | worker | **DONE** | `03-baseline/unfork-gonuts.md` |
| T11 | Upstream-acceptance enquiry for a nucula Linux port: is the author willing to host/maintain a Linux target and grant a licence? Blocks C1 entirely. | operator | **SENT 2026-09-14 (nucula#8)** | `01-candidates/nucula-licence-request.md` |

## Added 2026-09-13 (operator: flexibility > diversification)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T12 | **Interchangeability design:** define the single conformance suite both candidates must pass, the selection mechanism (build tag vs config vs sidecar), and whether gonuts/CDK/nucula can coexist in one module tree. Reuse the existing vectors rather than writing new ones. | worker (code) | **DONE** | `03-baseline/interchangeability.md` |
| T13 | **Objective scorecard:** fill the axis table in `04-reports/RECOMMENDATION.md` with measured values only, weights stated explicitly, and trade-offs written where each candidate wins and loses. Includes the un-forked gonuts comparator. | manager + workers | **DONE** | `04-reports/RECOMMENDATION.md` |
| T14 | Adapter effort estimate per candidate: how many lines/hours to implement the full `WalletPort` (gonuts exists; CDK exists but is unverified for musl; nucula needs a port). | worker (code) | **DONE** | `03-baseline/adapter-effort.md` |

**Note:** diversification is no longer a scoring axis (operator demoted it to an
observation). The fork-ownership grade (T9) survives because it measures *cost*,
not preference.

## Added 2026-09-13 (T10 findings — these are now ACCEPTANCE CRITERIA, not notes)

The fork carries **load-bearing fixes that upstream can never absorb** (it is dead:
zero commits since 2025-09-13, PR #149 blocked 12 months, its own steward's issues
unanswered). Two of them are security/funds-safety:

- `296c7bf` — **HTLC signature-enforcement bypass**
- `7dc430b` — **proof loss in swap**

Therefore: **any replacement adapter must demonstrate equivalent behaviour for both
cases before the old wallet is removed.** A wallet swap that silently reverts a
signature-enforcement fix is a security regression, not a migration. Add to the
parity work (T2e) and to the interchangeability suite (T12):

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T15 | Parity cases for the fork's security/funds-safety fixes (HTLC signature enforcement; swap proof-loss) — a failing test per case, run against the candidate wallet | worker | **DONE** (cross-mint + double-spend passing on MT6000 -> PR #117; **swap-proof-loss conclusive** -> `raw/swap_proof_loss-cdk.txt`; **HTLC signature enforcement both legs** -> `raw/htlc-nsigs-parity-cdk.txt`) | `experiments/parity/` |
| T16 | **Optionality work (1–2 days, recommended regardless of the final choice):** de-couple the 9 test files from the concrete wallet library so the three un-fork paths become a *choice* rather than a fate. Only 2 non-test files import the library (`tollwallet/gonuts_wallet.go`, `tollwallet/tollwallet.go`). | worker (code) | **DONE** (merchant tests de-coupled -> PR #396; gonuts-adapter tests intentionally remain)| `03-baseline/t16-decoupling.md` |

**Measured blast radius for any module-path change:** 327 branches carry a `replace`
from earlier module renames, and **50 branches already point at our own fork of the
fork** — so renaming or re-pointing the dependency is not a small edit across the
estate.

**Also corrected by T10:** the un-fork comparator is *not* "cheap upstreaming".
Path (i) has no counterparty, and path (ii) — dropping the patches for root v0.4.2 —
costs 4–8 weeks and reverts the signature fix, the proof-loss fix, **all** V2 /
short-keyset-ID support and reseller overpayment. The honest comparator is therefore
"keep the fork at ~1–2 eng-days/month" versus "replace it".

## Close-out plan (2026-09-16, approved)

Order: **P1 → T2e → T5 (parallel) → T6 → wrap-up**. Concrete deliverables, so a
fresh context can pick up mid-flight.

| # | Task | Deliverable | Evidence | Effort |
|---|---|---|---|---|
| P1 | Publish a NIP-23 (`kind 30023`) long-form post summarising the findings + tradeoffs (gonuts vs CDK vs nucula) to Nostr, signed via the operator's NIP-46 bunker; commit the source | `04-reports/nostr-post.md` (+ `## Published` event id) | returned event id + per-relay acceptance | 0.5 d |
| P2 | Reliable signer fallback: validate the operator's own **nosigner** (NIP-46 daemon + one-shot `sign`) with an ephemeral identity, and document how to run it with the Amber identity on another machine | `04-reports/nosigner-setup.md` | ephemeral test event signed + published + fetched back; daemon-path findings | 0.5–1 d |
| T2e | **Behavioural parity** gonuts vs CDK on identical fixed inputs against the local fakewallet mint | `experiments/parity/gonutsinterop/` (Go driver), `experiments/parity/behavioural_parity.py`, `raw/behavioural_parity-{gonuts,cdk}.txt` | normalized results equal (amounts, states, fee reserves) | 2–4 d |
| T5 | **Integration decision record**: sidecar-vs-embed per target, flash budget, supervision, socket auth, ALPHA-pin policy | `05-architecture/integration-decision.md`, `experiments/flash-budget/`, `experiments/cdk-sidecar/init/cdk-walletd.init` | measured `df`/sizes/RSS on both tiers; procd sketch | 1–2 d |
| T6 | **Migration path + rollback**: move a router's gonuts wallet (proofs+seed) to CDK, reversibly | `03-baseline/migration-path.md`, `experiments/migration/` | local dry run: gonuts → migrate → NUT-07 verify → switch → rollback → verify | 2–4 d |

**Open questions resolved:** signing = operator NIP-46 bunker; relays = damus/nos.lol/
nostr.mom/relay1-2.orangesync.tech; T2e driver = new `gonutsinterop` (mirrors
`cdkinterop`), not the service socket. If T5's decision changes
`src/tollwallet/manifests/wallet-policy.json`, that lands as its own commit on the
module PR branch (`pr/wallet-sidecar`), not here.

### Status (2026-09-16, end of session)

| # | Status | Commit / artifact |
|---|---|---|
| P1 | **BLOCKED (signer)** — source + metadata committed; the NIP-46 bunker accepts the connect but returns no signature (3 attempts, ≤150 s each). Publish once the signer app is online | `04-reports/nostr-post.md`, `d3ccf28` |
| P2 | **DONE (fallback)** — nosigner validated with an ephemeral identity (one-shot `sign` + publish **PASS**); Amber-identity setup documented; daemon path has a known key bug + nak interop gap | `04-reports/nosigner-setup.md` |
| T2e | **DONE** — PASS (identical observable results; cross-receive both ways) | `3708637` |
| T5 | **DONE** — per-target decision recorded; +6.0/+7.5 MiB sidecar cost | `10396a9` |
| T6 | **DONE** — migrate + rollback green, no value lost | `e2ae4ff` |

**To finish P1:** bring the signer online, then run the command in
`04-reports/nostr-post.meta.md` (the `d` tag makes it addressable, so a partial
attempt is superseded) and record the returned event id there.
