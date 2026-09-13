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
