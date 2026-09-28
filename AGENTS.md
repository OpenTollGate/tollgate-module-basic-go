# AGENTS.md

<!-- markdownlint-disable MD013 -->

Instructions for AI coding agents working in this repository.

> **This file is committed and shared.** Every contributor's agent
> reads it. Do not edit it for personal, machine-local, or
> session-specific preferences — only change it when the guidance is
> meant to apply to everyone working on this repo. (`CLAUDE.md` is a
> local, gitignored symlink to this file.)

## Orientation

TollGate turns an OpenWrt router into a Cashu-powered payment gateway
for internet access; the same binary also buys access from upstream
TollGates (reseller mode). Read [README.md](README.md) for the module
map and configuration reference.

- Go code lives under [src/](src/); run all Go tooling from there,
  not the repo root.
- Much of the behavior only exists on a real router (captive portal,
  firewall, Wi-Fi, `ndsctl`). Unit tests passing does not mean a
  router-visible change works — say so honestly in PR descriptions.

## Fund safety, crash consistency, and distributed transaction invariants

Any change touching payments, wallets, sessions, gates, mints, payouts,
retries, or persistent identifiers is a **distributed state-machine
change**, not a local edit. The router can lose power, the process can be
killed, the mint can rate-limit (429), time out, return 5xx, restart, or
accept a request and lose the response. Every such change must state:
the point of no return, what durable state is written **before** it,
recovery behavior after a crash at each step, retry and duplicate-
execution behavior, and the compensating action if a later step fails.

Hard rules, each earned from a real incident (issue numbers in
parentheses):

- **Never reuse deterministic Cashu derivation outputs.** Once a
  derivation range `[counter, counter+n)` has been exposed to a mint —
  sent in a swap/mint/melt request — it must never be derived again.
  Re-derivation triggers mint error 10002 / "Duplicate outputs" and has
  repeatedly bricked wallets (#257, #266, #480).
- **Persistent keyset counters are monotonic.** Reserve/persist the
  counter range *before* the network call that exposes it (#266), and
  never let a freshly-fetched keyset record (counter 0) overwrite a
  persisted record with a higher counter — that exact overwrite is how
  #480 bricked swaps after restart. See `SaveKeyset`'s monotonic guard
  and `mergeMintURLAliases` in gonuts-tollgate.
- **Canonicalize persistent mint identities at every layer.** The
  wallet DB, `registeredMints`, accepted-mint comparison, and config
  must all agree via `normalizeMintURL` (scheme/host case, default
  ports, trailing slash). Two spellings of one mint used to create two
  keyset/counter/balance copies (#375, #480). New persistence keyed by
  a mint URL must go through the same function.
- **Do not perform irreversible monetary operations (swap, mint, melt,
  LN settlement) before validations that can be done locally** — token
  decode, spending-condition check, swap-fee pre-check, MAC/NDS
  pre-flight all run before `Receive` on purpose (#403, #409).
- **Every irreversible operation followed by fallible work needs either
  durable forward recovery or a compensating action.** `Receive →
  session → gate-open → response` is a business transaction spanning
  the wallet, memory, NDS, and the HTTP client. Wallet-internal
  atomicity does NOT make this chain atomic. When the gate fails after
  a successful Receive, the value is in the operator wallet and the
  customer has nothing (#258, #403 — decide refund vs late-grant
  explicitly; do not silently drop it).
- **Ambiguous network results must be reconciled, never blindly
  retried.** A swap/melt timeout does not mean failure — the mint may
  have processed it. On timeout, query proof/quote state before
  regenerating or retrying; on error 10002 regenerate outputs from a
  *freshly incremented* counter (and note the counter must then also
  advance for the retry range).
- **Retries must be idempotent or use fresh state.** Concurrent
  duplicate submissions of one token must not double-count; retrying a
  melt with the *same* derivation range is a brick.
- **Partial successes must never be discarded.** Drain produced tokens
  before the failing mint must survive the failure (#375); a payout
  that paid the owner but failed a maintainer must record what was
  paid.
- **Process-memory state is not authoritative.** `customerSessions`,
  gate deauth timers, and data baselines live only in memory; Lightning
  quotes are persisted deliberately (`quote_store.go`) because payment
  recognition must survive restart. Anything that affects money, access
  or recovery must either be persisted or reconciled from an external
  authority (NDS client state) on startup — and today it is not, which
  is a known gap: restart loses session metering.
- **Migration paths must preserve value even when individual items
  fail.** A failed item in a batch migration is retained (old DB kept
  or item journaled), never dropped silently.

Before modifying wallet/payment logic, research first — in this order:
the relevant Cashu NUTs (NUT-02 keysets/fees, NUT-03 swap, NUT-04 mint,
NUT-05 melt, NUT-07 checkstate, NUT-19 error semantics), current
upstream `gonuts-tollgate` / `cashubtc/cdk` behavior (CDK's wallet saga
in `crates/cdk/src/wallet/{swap,send,receive,melt}/saga/` is the
reference model for crash-safe Cashu operations), existing TollGate
issues (#257, #258, #266, #375, #403, #417, #480, #481), and Nutshell /
cashu-ts behavior where the spec is ambiguous.

> Do not guess about Cashu or Lightning protocol behavior from local
> wrappers alone. Use web research, z.ai/zread, upstream source,
> specifications, and existing issue history before making
> protocol-sensitive changes.

Tests for payment/wallet changes should kill/restart the process at
transaction boundaries (between counter increment and swap, between
swap and proof save, between receive and session grant, between session
grant and gate open, between gate open and HTTP response) and exercise:
network failure, 429, timeout, mint restart, router restart, mint URL
aliases, keyset rotation, and partial failure. `tests/cloud-lab/` has
lanes for fees, keyset rotation and mint failure — extend it rather
than inventing new harnesses.

### Implementation-specific (Go + gonuts-tollgate)

- **gonuts-tollgate is our fork to maintain.** Upstream `elnosh/gonuts`
  is dead (last release v0.4.2, 2025); we carry ~40 patches. Every
  wallet-level fix lands in `OpenTollGate/gonuts-tollgate` first, is
  tagged, then bumped here via the `replace` directive (three `go.mod`
  files). Never fix a wallet bug by patching around the fork locally.
- **bbolt persistence.** Keyset records (which own derivation counters)
  are nested under mint-URL-named buckets; the DB has no transactions
  spanning "fetch keysets + swap + save proofs". This is why counter
  discipline is manual here — CDK gets it from a single-transaction
  saga record, we get it only from the rules above.
- **Counter ownership.** In gonuts, the derivation counter is *keyset
  state* stored inside the keyset record — every writer of keyset
  metadata (`SaveKeyset`, `AddMint`, keyset refresh, restore) is a
  counter writer and must be audited as such. In CDK the counter is a
  standalone atomic row — the structural difference motivating the
  long-term migration.
- **WalletPort / sidecar.** `src/tollwallet/port.go` is the seam:
  gonuts in-process (default), cdk-go behind a build tag, or a CDK/
  nucula sidecar daemon over AF_UNIX (`sidecar.go`). Money-moving
  sidecar requests surface `ErrSidecarAmbiguous` — reconcile, never
  blind-retry.
- **Pure-Go/OpenWrt constraint.** The binary must stay `CGO_ENABLED=0`
  and build for mips/mipsel/arm/arm64/x86. Any wallet dependency that
  breaks that (e.g. cdk-go FFI on MIPS) belongs behind the sidecar, not
  in-process.

## Contributing process

Follow [CONTRIBUTING.md](CONTRIBUTING.md). The parts agents most often
get wrong:

- **One logical change per PR**, targeting `main`. No drive-by
  reformatting, no unrelated cleanups, no scope creep.
- **Do NOT commit planning documents** (`*-plan.md`, `PLAN-*.md`,
  `TODO-*.md`, `MOCK-*.md`, scratch notes, agent working files). Add
  them to `.gitignore` instead. Only production documentation is
  committed: `README.md`, `CHANGELOG.md`, protocol specs, module docs.
- **No coding-assistant attribution** in commits or PR bodies — no
  `Co-Authored-By: Claude`, no `Generated with ...` footers.
- Before opening a PR, run the Go battery **from the repo root**:

  ```bash
  make go-battery
  ```

  It runs `gofmt` (must print nothing), `go vet`, `go build` and
  `go test -race -count=1 -tags testenv` in **every** Go module —
  [src/](src/) is a multi-module tree (16 nested `go.mod` files), so
  running those commands from `src/` alone covers only the root module
  and silently skips all subpackages. One implementation:
  [scripts/go-battery.sh](scripts/go-battery.sh).

  If the change touches the config schema or captive-portal contract,
  also run `node tests/contract/js-schema-lint.mjs` and
  `bash tests/contract/build-purity.sh` from the repo root.
- PRs are squash-merged; the maintainer rewrites the final commit
  message.

## PR review requirements

The 13-criteria checklist maintainers run on every incoming PR is
[PR-REVIEW.md](PR-REVIEW.md). Before opening a PR (or after pushing a
substantial revision), review the branch against that checklist and
fix or pre-empt what it surfaces. When asked to "review a PR" in this
repo, use PR-REVIEW.md as the rubric — it specifies the context to
gather, the criteria, the report shape, and the citation format.

## Changelog requirements

Every user-visible change lands with an entry in
[CHANGELOG.md](CHANGELOG.md) under `[Unreleased]`:

- Categories: `Added`, `Fixed`, `Changed / Internal` (CI, tests,
  refactors, and docs go in the last one).
- One bullet per change, bold lead-in phrase, linking the PR as
  `([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N))`.
  Match the existing entries' wrapped style.
- Doc-only or purely internal one-liners may be batched, but don't
  skip the entry — the changelog is finalized into release notes at
  release time (`[Unreleased]` becomes `[vX.Y.Z] - date`, and
  [RELEASE-NOTES.md](RELEASE-NOTES.md) is rewritten per release).

## Builds and releases on Nostr

CI ([.github/workflows/build-package.yml](.github/workflows/build-package.yml))
cross-compiles every push, packages `.ipk`/`.apk` per architecture,
uploads each artifact to multiple Blossom servers, and announces it as
a Nostr event. Agents can fetch builds without GitHub access using
`nak`.

**Publisher pubkeys** (release events are signed by CI; two keys are
live depending on which pipeline ran):

- GitHub Actions (historical, through 2026-08-27):
  hex `5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a`,
  npub `npub12p67v8ctqjq53dspqhqa6u4matse2uek4evzgzr72th6xa8cg94qxks7ks`.
  The secret is unrecoverable from GitHub (write-only), so nothing new
  will ever publish under it.
- Nostr CI / ngit (from #410 onward):
  hex `6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d`.
  A dedicated CI release key, deliberately not the maintainer key.
  Filter by `-a` on **both** keys (or rely on the `n`/`v`/`c`/`A` tags,
  which are publisher-independent) to see the full release history.

**Relays**: `wss://relay.damus.io`, `wss://nos.lol`,
`wss://nostr.mom`, `wss://relay1.orangesync.tech`,
`wss://relay2.orangesync.tech`

**Event kinds**:

- **`1063`** (NIP-94 file metadata) — one per published package.
  Tags: `url` (one per Blossom mirror holding the file), `x`/`ox`
  (sha256), `filename`, `n` (package name, `tollgate-wrt`), `v`
  (version: git tag like `v0.5.0`, or `<branch>.<height>.<sha>` for
  branch builds), `c` (release channel: `stable`, `beta`, `alpha`,
  `dev`), `A` (architecture, e.g. `aarch64_cortex-a53`, `mips_24kc`,
  `x86_64`), `format` (`ipk` or `apk`), `compression` (`none` or a
  `upx-*` variant).
- **`30078`** — transient per-arch build coordination between CI jobs;
  deleted with a kind `5` after the release events publish. Not useful
  to consumers.

**Fetching with nak** — single-letter tags (`n`, `v`, `c`, `A`) are
relay-filterable; multi-letter tags (`format`, `compression`) must be
filtered client-side with `jq`:

```bash
# Latest stable builds for one architecture (both publisher keys)
nak req -k 1063 \
  -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
  -a 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d \
  --tag n=tollgate-wrt --tag c=stable --tag A=aarch64_cortex-a53 --limit 10 \
  wss://relay.damus.io wss://nos.lol

# All artifacts for a specific version
nak req -k 1063 \
  -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
  -a 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d \
  --tag v=v0.5.0 --limit 50 wss://relay.damus.io wss://nos.lol
```

Download from any `url` tag (they're mirrors of the same blob) and
verify the file's sha256 against the `x` tag before using it.
