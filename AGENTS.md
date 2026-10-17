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

## Topic doctrine (docs/agents/) — load before you touch

This file is the stable core every agent reads; the doctrine that
GROWS — one area per file — lives under [docs/agents/](docs/agents/) so
a lane edits its own topic file instead of appending here (#920). The
entries below are LOAD DIRECTIVES, not suggestions: open the file before
working in its area. The core summarizes; it does not replace them.

- [docs/agents/firewall-engagement.md](docs/agents/firewall-engagement.md)
  — the nftables/topology rules of engagement, every rule bench-earned.
  Load before touching `/etc/nftables.d/`, interface names, or anything
  that reloads the firewall.
- [docs/agents/labgrid-testing.md](docs/agents/labgrid-testing.md) —
  the labgrid place protocol, the conwrt-bench registry rules, flashing
  and adoption, the VM lane pattern. Load before reserving or driving
  any router or VM rig.
- [docs/agents/verification-homes.md](docs/agents/verification-homes.md)
  — where each class of verification lives (contract suites, bench QEMU
  lanes, PRTA, cloud-lab, labgrid physical tier). Load before adding a
  suite or choosing where a change is verified.

`tests/contract/check-agents-partition.py` fences this index in both
directions: every topic file is listed, every listing resolves, and
every entry carries its load directive.

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
  tagged, then bumped here. Never fix a wallet bug by patching around
  the fork locally. **The pin is ONE version, identical in every `go.mod`
  that references the fork** — `tests/contract/check-gonuts-pin.py`
  discovers carriers by glob and names
  `packaging/build-inputs.json`'s `.gonuts.version` as the single
  truth; never update a carrier count or list anywhere by hand (that is
  exactly how #791 drifted). Bumps go through
  `scripts/bump-gonuts.sh <tag>`, which rewrites the manifest and every
  carrier together; `release-check` refuses to cut a release while the
  fork has a newer stable tag than the one pinned. Pseudo-version
  integration pins are for branches awaiting their tag and must not
  outlive that tag on `main`.
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

## Environment truth and drift doctrine

Every drift incident in this repo's history has the same shape: a fact
(a pin, a port pair, a bridge name, a toolchain version) was written down
in more than one place, and the copies diverged — #791's gonuts pin
("four go.mod files today" in this file missed a fifth carrier), the
entry_ui port table declared twice (#746), and the audit in #796. The
rules below exist so agents cannot reintroduce the class; follow them
even when a local copy looks easier.

1. **One declaration per fact.** Every environment constant — bridge
   names, admin port pairs, brand prefixes, toolchain and fork pins —
   is declared in exactly one place; everything else consumes it or is
   fenced to it. Restating a value in a second file is how every drift
   started, including the ones nobody has caught yet.
2. **Discovery by glob, never enumeration.** Checks and scripts find
   their subjects at run time (`rglob("go.mod")`, `find src -name
   go.mod`). Never write a count or list of carriers, modules, or
   fragments in prose, comments, or config — "four go.mod files today"
   rots the day a module lands outside the boundary. Say what the check
   discovers, not how many it found.
3. **Manifest truth for externals.** Every external build input is
   pinned in `packaging/build-inputs.json` (Go, node, portal, both SDK
   eras, the gonuts fork) and audited from there. A new external pin
   goes into the manifest with a fence — never into a workflow literal,
   a script constant, or a README table.
4. **Behavioral fences with planted-drift verdicts.** Duplication you
   cannot remove (two languages, two packages, canonical + embedded
   copies) gets a fence that EXECUTES both sides and compares — the
   `check-entry-ui-ports.sh` pattern — plus a verdicts harness that
   plants the drift and requires the refusal. A fence that cannot fail
   is decoration.
5. **Generators write every copy.** When copies must exist, one tool
   rewrites them all in a single run (the cross-vectors generator, the
   gonuts bump script), so a half-update cannot happen. Never update
   one copy of a set by hand.

### Network and interface names

The router's bridge vocabulary is a device fact, not a code choice:
`br-lan` is the customer/guest network (never an administration path),
`br-private` carries the private SSID and the physical LAN ports (the
admin path), `br-mgmt` is the optional management bridge (refused while
absent). The vocabulary is declared once, in Go:
`src/cli/operator_settings.go`'s constants. Shell code and nft fragments
must not invent bridge names; adding one is a schema + docs + fence
change, never a local literal. Interface sets vary by model and radio —
resolve them at runtime by probing (`/sys/class/net`, `uci show
network`), never by per-model or per-target lists. Known unfenced
duplications live in the #796 fence backlog; do not add to them.

### Firewall fragments

Two mechanisms, by design: static fragments shipped under
`packaging/files/etc/nftables.d/`, and the runtime-generated
`34-admin-access-scope.nft` the applier writes from the Go constants. A
port or interface literal in a static fragment is a declaration — it
must be fenced against the same table the setup script is
(`check-entry-ui-ports.sh`'s D2 anchors; see #796 F2), and no new
fragment lands without a packaging test asserting its ports and
interfaces against that table.

### The two OpenWrt eras

Distinguish eras by package manager, never by release strings:
`command -v apk` answers the apk era (25.x), `command -v opkg` the ipk
era (≤24.10). Parsing `/etc/openwrt_release` where the probe answers is
a bug. Build truth is `build-inputs.json`'s `openwrt_sdk.releases.{apk,
ipk}` (both eras' SDK digests pinned); the pinned `.go.version` tracks
the apk era; `scripts/sdk-go-version.sh` audits `go_per_release`
against the live feeds; the ipk-era SDK stages prebuilt binaries, so
its older feed Go never compiles the tree.

Support policy (#796): **25.12/apk is the only feature target.**
24.10.8/ipk is a frozen compatibility lane for the installed base —
security and stop-ship fixes only, no features, matrix rows retained.
The lane's sunset is a deliberate release-time decision with fleet
evidence (lab registry, tester intake), never a silent drop and never
mid-freeze.

### Cross-repo halves

Some contracts span two packages (the entry-port mapping is written by
this module's `99-tollgate-setup` AND the feed's
`92-tollgate-admin-setup`). The named decision record in
`docs/architecture/` is the truth; changes ship gated on both halves
(the WARNING-named re-vendor contract in the README). A cross-repo
contract without a named decision record is a bug.

## Firewall, nftables, and topology-naming rules of engagement

The full, bench-earned rulebook — accept-is-not-final across base
chains in detail, same-priority chain ordering, the fw4-reload/NDS
state contract, the three "lan" namespaces, the Go-side literal greps —
lives in
[docs/agents/firewall-engagement.md](docs/agents/firewall-engagement.md).
LOAD IT before touching `/etc/nftables.d/`, interface names, or anything
that reloads the firewall. The three rules you must not need the file to
remember:

- Only `drop` and `reject` terminate. An accept in a different base
  chain shields nothing from a later chain's reject — a green
  render-level suite hid exactly this in PR #782's first design.
- Never install firewall rules at runtime (`nft add/insert`): `fw4
  reload` rebuilds the ruleset from files and silently wipes them.
- No interface-name literals in rule lines, shipped or generated —
  `$tg_portal_if` / `$tg_private_if` from `00-tollgate-defs.nft` are in
  scope for every later include, and the fencing contract for literals
  is the "Firewall fragments" rule above.

## Hardware, VM, and verification homes

All router- and VM-based testing is coordinated through **labgrid**
(coordinator `ai-legion:20408`): reserve through places
(`labgrid-client -p <place> acquire` … `release`), treat a place held
by someone else as theirs, and never drive lab hardware ad hoc. The
lab's single source of truth is the private `Amperstrand/conwrt-bench`
repo (ADR-0005); `conwrt-lab` is retired — do not add data there. Each
class of verification has ONE canonical home, and a change is verified
where its failure class is observable — including the rule that every
contract suite must be mutation-checked before it ships (revert the
guarded fix, confirm the suite FAILS).

LOAD [docs/agents/labgrid-testing.md](docs/agents/labgrid-testing.md)
before reserving or driving any router/VM rig (registry rules, conwrt
flashing and adoption tooling, the VM lane pattern), and LOAD
[docs/agents/verification-homes.md](docs/agents/verification-homes.md)
before adding a suite or choosing where a change is verified (contract
suites, bench QEMU lanes, PRTA, cloud-lab crash lanes, labgrid physical
tier).

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
- **Adding CI, a fence, or doctrine means adding a file — never editing
  a shared anchor (#920).** A pre-commit fence is a
  `fences.d/NN-<slug>.sh` fragment (a complete if/fi block; see
  [fences.d/README.md](fences.d/README.md)); a CI job is a
  `ci.d/<lane>/NN-<slug>.yml` fragment, followed by
  `python3 scripts/assemble-ci.py` to re-render both Test workflow twins
  (see [ci.d/README.md](ci.d/README.md)); growing doctrine goes in its
  [docs/agents/](docs/agents/) topic file, indexed in the
  "Topic doctrine" section above. `hooks/pre-commit` and the rendered
  workflows are build products — never hand-edit them.
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

  If the change touches the config schema, the captive-portal contract
  or a shipped default, also run `node tests/contract/js-schema-lint.mjs`,
  `bash tests/contract/build-purity.sh` and
  `bash tests/contract/check-ssid-format.sh` from the repo root.
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

Every user-visible change lands as a **fragment** under
[changelog.d/](changelog.d/): `changelog.d/<pr-number>-<short-slug>.<type>.md`,
where `<type>` is `added|changed|internal|deprecated|fixed|removed|security`.
The file body **is** the bullet: it starts with `- `, wraps as you want it in
the changelog, and ends with the PR link
`([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N))`.
The maintainer folds fragments into
[CHANGELOG.md](CHANGELOG.md) at release time. **Never edit CHANGELOG.md from
a PR** — five PRs editing the same `[Unreleased]` anchor conflicted a dozen
times on the 0.6.0 train (#524, #531), which is why fragments exist; a
`--ours` conflict resolution twice resurrected already-moved entries.
(`tests/contract/check-changelog-duplicates.py` guards the fold; do not
bypass it by hand-editing.)

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
