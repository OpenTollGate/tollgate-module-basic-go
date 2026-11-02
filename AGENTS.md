# AGENTS.md

Instructions for AI coding agents.

> **Committed and shared** — every contributor's agent reads it; change
> it only for guidance applying to everyone (`CLAUDE.md` is a local,
> gitignored symlink).

## Orientation

TollGate turns an OpenWrt router into a Cashu-powered payment gateway
for internet access; the same binary buys access from upstream TollGates
(reseller mode). [README.md](README.md) has the module map and config
reference.

- Go code lives under [src/](src/); run Go tooling from there, not the
  repo root (`make test` is the root-safe runner, #961).
- Much of the behavior only exists on a real router (captive portal,
  firewall, Wi-Fi, `ndsctl`); unit tests passing does not mean a
  router-visible change works — say so in PR descriptions.

## Cross-repo ownership

**This repo owns the Go runtime, unit tests, source-level contracts
(config schema, packaging offline suites, happy-path/installer lanes)
and docs — not:**

- **Shipping packages** (recipes, pins, feed publication):
  `FreedomTechFeed/packages` (`build-sdk-package.sh` here is a dev path,
  not a competing authority).
- **Router integration / deployed-artifact acceptance**: OpenTollGate/
  PRTA — evidence binds to artifact sha256 + source SHA (hardware-fleet
  tests retired pending migration).
- Captive portal `OpenTollGate/tollgate-captive-portal-site`, pinned in
  `build-inputs.json` (never vendored divergently); wire protocol
  `OpenTollGate/tollgate`; wallet layer `OpenTollGate/gonuts-tollgate`
  (fixes land there first, then bump).

Canonical policy: PRTA's `docs/engineering-policy.md`. Identify the
owner, search open PRs for the same change, use the canonical runner; a
skipped check is never reported PASS; never duplicate another repo's
declarations.

## Fund safety and transaction invariants

Changes touching payments, wallets, sessions, gates, mints, payouts,
retries or persistent identifiers are **distributed state-machine
changes**, not local edits — power loss, process kill, mint 429/timeout/
5xx/restart, an accepted-but-lost response can strike anywhere. Each
states: the point of no return, durable state written **before** it,
per-step crash recovery, retry/duplicate behavior, and the compensating
action on later-step failure. Hard rules (issues = origin):

- **Never reuse deterministic derivation outputs.** A range `[counter,
  counter+n)` exposed to a mint is never derived again — mint 10002
  "Duplicate outputs" has repeatedly bricked wallets (#257, #266, #480).
- **Persistent keyset counters are monotonic.** Persist the range
  *before* the exposing call (#266); never let a fresh keyset record
  (counter 0) overwrite a higher persisted counter (#480; `SaveKeyset`'s
  guard + `mergeMintURLAliases`).
- **Canonicalize mint identities at every layer** via
  `normalizeMintURL`: wallet DB, `registeredMints`, accepted-mint
  comparison and config must agree, or one mint becomes two
  keyset/counter/balance copies (#375, #480); new mint-URL-keyed
  persistence goes through it.
- **No irreversible op (swap/mint/melt/LN) before locally-checkable
  validations** — decode, spending-condition, swap-fee, MAC/NDS
  pre-flight all run before `Receive` on purpose (#403, #409).
- **Every irreversible op followed by fallible work needs durable
  forward recovery or a compensating action.** `Receive → session →
  gate-open → response` spans wallet/memory/NDS/HTTP — wallet atomicity
  does NOT make it atomic. Gate failure after a successful Receive:
  decide refund vs late-grant, never silently drop (#258, #403).
- **Ambiguous results are reconciled, never blindly retried.** A
  swap/melt timeout is not failure: query proof/quote state before
  regenerating; on 10002 regenerate from a *freshly incremented* counter
  (advanced for the retry range).
- **Retries are idempotent or use fresh state.** Concurrent duplicates
  of one token never double-count; retrying a melt with the *same*
  derivation range is a brick.
- **Partial successes are never discarded.** Drain-produced tokens
  before a failing mint survive (#375); a payout that paid the owner but
  failed a maintainer records what it paid.
- **Process-memory state is not authoritative.** Sessions, deauth
  timers, baselines are memory-only; Lightning quotes persist
  deliberately (`quote_store.go`) so payment recognition survives
  restart. Anything affecting money/access/recovery persists or
  reconciles from an external authority (NDS state) on startup —
  restart loses session metering today (known gap).
- **Migrations preserve value when items fail** — failed items are
  retained (old DB kept or journaled), never dropped silently.

Research before touching wallet/payment logic: the relevant Cashu NUTs
(02/03/04/05/07/19), upstream `gonuts-tollgate`/`cashubtc/cdk` behavior
(CDK's `crates/cdk/src/wallet/*/saga/` is the reference crash-safety
model), issues #257/#258/#266/#375/#403/#417/#480/#481, Nutshell/
cashu-ts where the spec is ambiguous — never guess protocol behavior
from wrappers. Tests kill/restart at transaction boundaries (counter↔swap, swap↔proof
save, receive↔grant, grant↔gate, gate↔response) exercising network
failure, 429, timeout, mint/router restart, URL aliases, keyset
rotation, partial failure — extend `tests/cloud-lab/`'s lanes, don't
invent harnesses.

### Implementation-specific (gonuts-tollgate)

- **gonuts-tollgate is our fork** (upstream `elnosh/gonuts` is dead):
  wallet fixes land there first, tagged, then bumped here — never patch
  around the fork locally. **The pin is ONE version, identical in
  every `go.mod` referencing the fork**; `check-gonuts-pin.py` finds
  carriers by glob; `build-inputs.json`'s `.gonuts.version` is the single
  truth — never update a carrier count/list by hand (#791). Bumps via
  `scripts/bump-gonuts.sh`; `release-check` refuses a release while the
  fork's stable tag is newer than the pin.
- **bbolt persistence:** keyset records (owning derivation counters) sit
  under mint-URL buckets; no transaction spans "fetch keysets + swap +
  save proofs" — counter discipline is manual (CDK's atomic saga row is
  why we migrate). Every keyset-metadata writer (`SaveKeyset`, `AddMint`,
  refresh, restore) is a counter writer — audit as such.
- **Single writer per wallet DB (#504).** One process at a time writes
  `wallet.db`; the bbolt flock enforces it (`InitBolt`: 5 s →
  `ErrDBLocked`/`ErrWalletLocked`) and nothing routes around it: the CLI
  is socket-routed (`/var/run/tollgate.sock` → the daemon's CLIServer),
  a boot finding the lock held fails fast, `CLIServer.Start()` never
  replaces a live daemon's socket (a stale crash file is reclaimed, a
  live one is not). New wallet-touching entry points route through the
  daemon or own the DB exclusively.
- **WalletPort / sidecar:** `src/tollwallet/port.go` is the seam —
  gonuts in-process (default), cdk-go behind a build tag, or a CDK
  sidecar over AF_UNIX (`sidecar.go`); money-moving requests surface
  `ErrSidecarAmbiguous` — reconcile, never blind-retry. The binary stays
  `CGO_ENABLED=0` (mips/mipsel/arm/arm64/x86); breaking dependencies
  belong behind the sidecar.

## Environment truth and drift

Drift incidents share one shape — a fact written in two places diverged
(#791, #746, #796). Rules:

1. **One declaration per fact.** Environment constants (bridge names,
   port pairs, brand prefixes, toolchain/fork pins) declare once;
   everything else consumes or is fenced to it.
2. **Discovery by glob, never enumeration.** Checks find subjects at run
   time (`rglob("go.mod")`); a carriers count/list in prose, comments or
   config rots when a module lands outside the boundary.
3. **Manifest truth for externals.** External build inputs pin in
   `packaging/build-inputs.json` (Go, node, portal, SDKs, gonuts), never
   a workflow literal, script constant, or README table.
4. **Behavioral fences with planted-drift verdicts.** Irremovable
   duplication gets a fence that EXECUTES both sides and compares (the
   `check-entry-ui-ports.sh` pattern) plus a harness planting the drift
   and requiring the refusal; a fence that cannot fail is decoration.
5. **Generators write every copy.** When copies must exist, one tool
   rewrites them all in one run — never update one copy by hand.

### Interface names

Bridge vocabulary is a device fact declared once in Go
(`src/cli/operator_settings.go`): `br-lan` = customer/guest (never an
admin path), `br-private` = private SSID + LAN ports (admin path),
`br-mgmt` = optional management bridge (refused while absent). Shell/nft
never invent bridge names (adding one = schema + docs + fence change);
resolve interfaces at runtime (`/sys/class/net`, `uci show network`).
Grep `grep -rnE '"br-lan"|"br-private"|"br-mgmt"' src/` first — unfenced
hits are #796 F1; Go-side literals are the same bug class outside .nft
files.

### Firewall fragments

Two mechanisms: static fragments in `packaging/files/etc/nftables.d/`
and the runtime-generated `34-admin-access-scope.nft` from Go constants.
A port/interface literal in a fragment is a declaration, fenced against
the same table the setup script is; new fragments need a packaging test
asserting ports/interfaces against that table.

### The two OpenWrt eras

Distinguish by package manager, not release strings: `command -v apk` =
apk era (25.x), `command -v opkg` = ipk (≤24.10); parsing
`/etc/openwrt_release` where the probe answers is a bug. Build truth:
`build-inputs.json`'s `openwrt_sdk.releases.{apk,ipk}` (audited by
`sdk-go-version.sh`). Policy (#796): **25.12/apk is the only feature
target**; 24.10.8/ipk frozen (security + stop-ship); sunset is a
deliberate release-time decision with fleet evidence, never silent.

### Cross-repo halves & the feed

Contracts spanning two packages (the entry-port mapping: this module's
`99-tollgate-setup` AND the feed's `92-tollgate-admin-setup`) have a
named decision record in `docs/architecture/` as truth, gated on both
halves — without one it's a bug. Feed routing: this module owns the Go
source + generated **build contract** (`build-contract.json`, fenced by
`check-build-contract.py`); `FreedomTechFeed/packages` `net/tollgate-wrt`
owns the canonical recipe; `FreedomTechFeed/feed` is a wrapper mirror
(`sync-from-canonical.sh` — never hand-edit). Go payloads → here;
recipe/pins/portal → canonical feed; wrapper finds → re-sync. A changed
contract-governed fact goes in the PR body — the feed's fences going red
until it catches up is the intended signal.

### The two lanes and the contract

- **Fast lane = release pipeline** (manifest-pinned host compile staged
  into the digest-pinned SDK; reproduces months later). The OpenWrt-SDK
  build is the **compatibility canary** — it notices toolchain drift, it
  does not produce artifacts; a red canary is cured by re-tracking the
  pin, NEVER by unpinning the fast lane.
- **The build contract is generated and owns the shipped set.** Ship a
  binary: `PREBUILT_*` line in `packaging/Makefile`, run
  `scripts/gen-build-contract.sh`, let `check-build-contract.py` go red
  until every carrier catches up — that red is the fence (#1020/#1021).
  Never hand-edit `build-contract.json`; a Go-building non-carrier
  harness carries a reasoned `# contract-exempt:` line.
- **Toolchains from `TG_TOOLS`, not PATH**: manifest Go in
  `~/.cache/tollgate-tools`, sha256-verified; `portal-build.sh` /
  `repro-test.sh` default it — no third way.
- **CI economy:** fences/host cross-compiles are seconds (per-push OK);
  SDK builds are half-hours (tags/nightly/labels; never for build-
  irrelevant diffs). No lane-local toolchain literals
  (`check-version-sync.sh` refuses).
- **The pre-commit hook was once dead** (#1018) — when a hook-dependent
  check surprises you, verify the hook runs (`bash -n hooks/pre-commit`;
  `git config core.hooksPath`) before debugging it.

## Firewall and nftables rules of engagement

Bench-verified lessons (#754–#757, PR #782) for changes touching
`/etc/nftables.d/`, interface names, or firewall reloads.

- **Accept is not final across base chains.** Only `drop`/`reject`
  terminate; an earlier-priority chain's accept never shields a flow
  from a later chain's reject (PR #782 was render-green-but-dead; only a
  live dataplane rig caught it). Exempt a flow: accepts in the SAME chain
  ahead of the terminal rule, or a mark an earlier rule honors.
- **Never install rules at runtime** (`nft add/insert`): `fw4 reload`
  rebuilds from files and silently wipes them; state lives only in
  `/etc/nftables.d/*.nft` (renderer-rewritten fragments sanctioned).
- **No interface-name literals in rule lines — shipped or GENERATED.**
  fw4 includes `/etc/nftables.d/*.nft` lexically, so
  `$tg_portal_if`/`$tg_private_if` from `00-tollgate-defs.nft`
  (re-rendered from the router's config, #757) scope every later
  include; generated fragments use the defines; both are asserted
  against the one port/interface table (#796 F2 = the open instance).
- **Same-priority base chains have unspecified order.** NDS's
  `ip filter FORWARD` and fw4's `inet fw4 forward` both sit at priority
  `filter` (`20-nds-enforce.nft`) — never add a base chain where that
  ordering matters.
- **`fw4 reload` procd-restarts nodogsplash**, wiping runtime trust/auth
  state. A widening reload trigger states what NDS state dies, what
  restores it, and how long customers are interrupted.
- **"lan" is three namespaces** — bridge DEVICE (`br-lan`), network
  SECTION (`lan`), firewall ZONE (`lan`), plus `dhcp.lan` and dnsmasq's
  `local=/lan/`: independent uci objects sharing a string; renaming one
  renames nothing else (fw4 skips a renamed zone ref with exit 0 and no
  syslog #755; setup rebinds APs to deleted sections #756; netifd
  regenerates wireless bound to `lan` after deletion). Topology code
  resolves names from the router's config (`resolve_*` in
  `99-tollgate-setup`), warns on non-stock decisions, never silently
  skips.

## Hardware/VM testing (labgrid)

All router/VM testing goes through **labgrid** (coordinator
`ai-legion:20408`; use the hostname form — some `LG_COORDINATOR`s are
stale): reserve via places (`labgrid-client -p <place> acquire` …
`release`); a held place is someone else's. Lab truth: the private
**`Amperstrand/conwrt-bench`** repo (ADR-0005; `conwrt-lab` retired).
Never hardcode IPs/MACs/serial paths (resolve from the registry); access
via places (`ssh|console|power`) with acquire/release exclusivity;
flashing/adoption via conwrt tooling (`dut_recover.py --from-lab`,
`bench_net.py`, which updates the registry); a state change ends with a
registry commit; when surprised, reconcile first
(`lab_registry.py reconcile`).

VM lane: `labgrid/qemu-x86-64.yaml.example` is the pattern (client on
ai-legion; pristine boots via `snapshot=on`). On-target harness:
`tests/happy-path/run.sh --artifact <pkg>` (kind-`1063` bytes pinned via
`x`, or a local build with recorded sha256).

### Host resource discipline

The bench host is shared; on 2026-10-11 legitimate consumers filled all
swap and earlyoom killed an unrelated session. (1) **Bound big
invocations**: soaks/builds/qemu get cgroup ceilings (`systemd-run
--scope -p MemoryMax=8G -p MemorySwapMax=2G -- go test …`) so a runaway
dies alone; go-battery.sh does this under systemd. (2) **Fan out
sequentially**: check `free -h` / the earlyoom journal first; cap
concurrent opencode (~10); launch via `opencode-bounded` (3G/1G).
(3) **Fixtures on-demand**: AVDs via `avd-acquire`/`avd-release`;
idle-for-days goes in a place or shuts down; docker stacks get
`mem_limit`s. (4) **Alerts are load-shed orders**: `host-mem-alert`
precedes earlyoom ~10 min — stop soaks/fan-outs.

### Verification classes

- **Contract suites** (`tests/contract/`, offline): render-level checks —
  CANNOT catch dataplane semantics (#782). Mutation-check every suite:
  revert the guarded fix, confirm it FAILS; a cited gate must test the
  thing cited (#778).
- **Bench QEMU lanes** (per era — both when the compat lane is touched):
  dataplane ordering + counters (#754/#755/#757 legs are the model);
  images pass image-doctor (depmod, kmods, wpad/veth/ip-full).
- **PRTA**: deployed-lab/campaign harness (`extensive-test.sh`, wifi
  suites, `0.6.0-validation/`) — new topology/journey scenarios are
  suite cases THERE, not scratch scripts.
- **cloud-lab** (`tests/cloud-lab/`): fund-safety kill/restart lanes
  (any docker host). **Labgrid physical**: real RF/clients, power-pulls,
  era kernels.

VM-lane pitfalls: guest `10.0.2.2` IS the host loopback (never assume
rig isolation or touch a host service a probe reaches); a radio-less VM
exercises NDS via a synthetic pre-auth client (veth into the captive
bridge; BusyBox nc lacks `-w`/`-z` — probe with wget; "Operation not
permitted" = the nft reject signature); `fw4 reload` wipes NDS runtime
state — re-trust inside dependent tests.

## Contributing process

Per [CONTRIBUTING.md](CONTRIBUTING.md); the parts agents get wrong:

- **One logical change per PR**, targeting `main`; no drive-by
  reformatting, unrelated cleanups, or scope creep.
- **No planning documents** (`*-plan.md`, `PLAN-*.md`, `TODO-*.md`,
  `MOCK-*.md`, scratch/agent files) — `.gitignore` them; only production
  docs commit. **No coding-assistant attribution** in commits/PRs.
- Before a PR, run `make go-battery` from the repo root: gofmt (silent),
  `go vet`, `go build`, `go test -race -count=1 -tags testenv` in
  **every** Go module ([src/](src/) is a multi-module tree; running from
  `src/` alone silently skips subpackages —
  [scripts/go-battery.sh](scripts/go-battery.sh)). Config schema / portal
  contract / shipped-default changes also run `js-schema-lint.mjs`,
  `build-purity.sh`, `check-ssid-format.sh`.
- `src/merchant` payment-timing tests are the known flake class —
  reproduce on pristine `origin/main` in a throwaway worktree before
  assuming you broke one. PRs are squash-merged; the maintainer rewrites
  the final message.

## PR review requirements

The 13-criteria checklist is [PR-REVIEW.md](PR-REVIEW.md) — review
your branch against it pre-PR; use it as the rubric when asked to
review a PR.

## Changelog and registered debt

Every user-visible change lands as a **fragment** under
[changelog.d/](changelog.d/): `<pr-number>-<short-slug>.<type>.md`
(added/changed/internal/deprecated/fixed/removed/security); the body
**is** the bullet (starts `- `, ends with the PR link); the maintainer
folds fragments into [CHANGELOG.md](CHANGELOG.md) at release — **never
edit CHANGELOG.md from a PR** (#524, #531; `check-changelog-duplicates.py`
guards the fold).

- **Temporary workarounds are registered debt.** Any `TEMPORARY
  WORKAROUND` comment in `src/` carries a row in
  [docs/temporary-workarounds.md](docs/temporary-workarounds.md) or
  `temporary-workarounds_test.sh` fails the build — including when a
  workaround grows call sites outside its contract (#768: the port-80
  poke shipped ungated through a release train).
- **Probe/detect code stays side-effect-free.** Out-of-protocol requests
  to third parties fire only from session adoption after validation,
  never per poll tick — a probe hit is not evidence of a TollGate;
  per-probe side effects on a ticker become a poke loop against
  already-rejected gateways (#768).

## Builds and releases on Nostr

CI cross-compiles every push, packages `.ipk`/`.apk` per arch, uploads
to Blossom mirrors, announces a Nostr kind-`1063` event (tags: `url` per
mirror, `x`/`ox` sha256, `n`/`v`/`c`/`A`, `format`, `compression`;
kind `30078` is transient). Fetch with `nak`, filtering `-a` on BOTH
keys (GitHub Actions historical `5075e61f…f8416a`; Nostr CI/ngit #410+
`6cfc53c0…208ac0d`) or the publisher-independent tags; multi-letter
tags filter locally.
Relays: `wss://relay.damus.io`, `wss://nos.lol`, `wss://nostr.mom`,
`wss://relay{1,2}.orangesync.tech`. Verify sha256 against `x`.
