# Research tasks

Owner column is the agent/consultant expected to close it. Statuses: TODO /
IN PROGRESS / DONE. Every task finishes by committing its artifact to this branch.

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T1a | Baseline: measure gonuts footprint + storage + latency on the router | worker (firmware) | TODO | `03-baseline/gonuts.md`, `experiments/` |
| T1b | Enumerate the `WalletPort` surface and the NUTs it implies (the acceptance contract) | worker (code) | TODO | `00-context/walletport-contract.md` |
| T2 | nucula deep dive: NUT coverage from code, licence intent, flash storage, key handling, real footprint, logic-vs-firmware portability | consultant A | TODO | `01-candidates/nucula.md` |
| T3 | CDK deep dive: licence terms, wallet API vs CLI, minimal feature set, musl cross-compile for router arches, RSS/threads, storage backends, NUT diff, churn/governance | consultant B | TODO | `01-candidates/cdk.md` |
| T4 | Method + overlooked metrics: finalise the metric set and the measurement protocol; add fault-injection scenarios | consultant C | TODO | `02-method/*` |
| T5 | Integration-architecture study: cgo+`cdk-ffi` static link vs sidecar process vs CLI wrapper — build cost, failure modes, debuggability on router | consultant B + worker | TODO | `experiments/integration/` |
| T6 | Migration path: existing router proof/key store → new wallet, including rollback | worker | TODO | `03-baseline/migration-path.md` |
| T7 | Synthesis + recommendation, with dissenting opinions preserved | manager + all consultants | TODO | `04-reports/RECOMMENDATION.md` |
| T8 | Explicit consideration and rejection (or adoption) of the stay-on-gonuts option | manager | TODO | `04-reports/RECOMMENDATION.md` |

## Definition of done for the branch

A reader can rebuild every number, see the licence findings, reproduce the
cross-compiles, and read a recommendation that names the trade-offs and the
dissent — and can check all of it out from this branch alone.

## Added after prior-art discovery (2026-09-13)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T5a | Audit the existing `cdk_wallet.go` adapter: completeness, tests that actually run, build-tag wiring, mnemonic handling | worker (code) | TODO | `01-candidates/cdk.md`, `experiments/cdk-adapter/` |
| T5b | Verify `github.com/cashubtc/cdk-go`: published? maintained? pinned how? what does `go.mod` say — real version or `replace` to a local path? | consultant B | TODO | `01-candidates/cdk.md` |
| T5c | Cross-compile test: cgo + cdk-go static lib for `aarch64`/`mipsel` musl on OpenWrt — patch count and resulting size | worker (firmware) | TODO | `experiments/cdk-cross/` |

## Added after the scope change: nucula → OpenWrt port (2026-09-13)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T2b | **Port spike:** compile nucula's wallet core on native Linux (x86_64), then cross-compile for a router arch (aarch64 musl) with the OpenWrt SDK. Measure size/RSS/threads. Exclude peripherals. | worker (firmware) | TODO | `experiments/nucula-port/` |
| T2c | **Licence ask:** approach the author for an explicit licence (MIT/Apache-2.0/GPL-3.0 choice). Nothing ships before this. | operator/manager | TODO | `01-candidates/nucula.md` |
| T2d | ESP-IDF API-boundary inventory: every header/API the core files use, with the Linux replacement, and the portable-vs-entangled ratio | worker | TODO | `01-candidates/nucula-port-map.md` |
| T2e | Behavioural parity: run the same mint-quote/mint/swap/melt flows against a local mint on Linux and compare results with gonuts for the same inputs | worker | TODO | `experiments/nucula-parity/` |

**Legal hygiene for this branch:** nucula's source is *not* committed here. The
branch carries our own port shims, diffs, measurement logs, inventory and plans
— never their unlicensed code. Anyone reproducing the spike fetches nucula at the
recorded revision themselves.

## Added 2026-09-13 (operator constraints)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T9 | Fork-ownership grading (A/B/C) for every option, with the measurement (our commits to the dependency, unadopted upstream releases, upstream CI coverage of musl/our arches) | worker | TODO | `03-baseline/fork-ownership.md` |
| T10 | "Un-fork gonuts" option study: could we contribute the fork's content upstream, or vendor a pinned upstream release? What does the fork add, commit by commit? | worker | TODO | `03-baseline/unfork-gonuts.md` |
| T11 | Upstream-acceptance enquiry for a nucula Linux port: is the author willing to host/maintain a Linux target and grant a licence? Blocks C1 entirely. | operator | TODO | `01-candidates/nucula.md` |

## Added 2026-09-13 (operator: flexibility > diversification)

| # | Task | Owner | Status | Artifact |
|---|---|---|---|---|
| T12 | **Interchangeability design:** define the single conformance suite both candidates must pass, the selection mechanism (build tag vs config vs sidecar), and whether gonuts/CDK/nucula can coexist in one module tree. Reuse the existing vectors rather than writing new ones. | worker (code) | TODO | `03-baseline/interchangeability.md` |
| T13 | **Objective scorecard:** fill the axis table in `04-reports/RECOMMENDATION.md` with measured values only, weights stated explicitly, and trade-offs written where each candidate wins and loses. Includes the un-forked gonuts comparator. | manager + workers | TODO | `04-reports/RECOMMENDATION.md` |
| T14 | Adapter effort estimate per candidate: how many lines/hours to implement the full `WalletPort` (gonuts exists; CDK exists but is unverified for musl; nucula needs a port). | worker (code) | TODO | `03-baseline/adapter-effort.md` |

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
| T15 | Parity cases for the fork's security/funds-safety fixes (HTLC signature enforcement; swap proof-loss) — a failing test per case, run against the candidate wallet | worker | TODO | `experiments/parity/` |
| T16 | **Optionality work (1–2 days, recommended regardless of the final choice):** de-couple the 9 test files from the concrete wallet library so the three un-fork paths become a *choice* rather than a fate. Only 2 non-test files import the library (`tollwallet/gonuts_wallet.go`, `tollwallet/tollwallet.go`). | worker (code) | TODO | `03-baseline/interchangeability.md` |

**Measured blast radius for any module-path change:** 327 branches carry a `replace`
from earlier module renames, and **50 branches already point at our own fork of the
fork** — so renaming or re-pointing the dependency is not a small edit across the
estate.

**Also corrected by T10:** the un-fork comparator is *not* "cheap upstreaming".
Path (i) has no counterparty, and path (ii) — dropping the patches for root v0.4.2 —
costs 4–8 weeks and reverts the signature fix, the proof-loss fix, **all** V2 /
short-keyset-ID support and reseller overpayment. The honest comparator is therefore
"keep the fork at ~1–2 eng-days/month" versus "replace it".
