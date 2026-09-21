# TollGate v0.6.0-alpha4 (tollgate-wrt)

**Released**: 2026-09-21
**Channel**: `alpha` — a tester-facing release candidate, not a stable
release. Expect rough edges; report them.

<!-- markdownlint-disable MD013 -->

`v0.6.0-alpha4` supersedes two never-published predecessors:
`v0.6.0-alpha2` (prepared 2026-09-13, never tagged) and
`v0.6.0-alpha3` (tagged 2026-09-21 but held back before a single
artifact shipped — the ngit mirror had been re-keyed mid-migration and
the portal pin had drifted past its manifest). It is the first TollGate
release **published from the Nostr CI lane** — GitHub Actions has been
down org-wide since 2026-08-27 — and the first whose published
artifacts are reproducible byte-for-byte (pinned toolchains plus a
commit-derived `SOURCE_DATE_EPOCH` on both the local packaging path
and the publishing lane). Publication itself changed shape: the
release matrix is sharded across budgeted workflow runs, and
**announcements are emitted only when every shard of the release
succeeded** — a partial build no longer looks like a finished one.

The theme is *funds safety and honest failure*: the wallet drain can no
longer destroy tokens when a later mint fails; payments are refused up
front when the gate provably cannot open them; an expired-keyset note
is reported as unrecoverable instead of "mint unreachable"; renewal no
longer double-charges at near-zero usage; the portal works on
nodogsplash 5.0.2; upgrades no longer abort on minimal systems without
`jq`. Alongside the fixes sits one deliberate **feature**: a
process-isolated wallet sidecar architecture (alpha quality, unit
tested, not router-validated) that lets a non-Go wallet back the
module behind the unchanged `WalletPort` contract.

## At a glance

- **Wallet drain safety**
  ([#375](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/375)):
  canonical mint identity, per-mint partial results, an fsync'd token
  journal written before the next mint is attempted, `--yes`/`-y`, and
  meaningful exit codes.
- **Payments refused when the gate provably cannot open**
  ([#412](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/412)):
  a read-only `ndsctl` probe rejects the payment up front
  (`client-not-registered`) instead of consuming an irreversible token
  for a session that can never start.
- **Degraded mode recovers on its own**
  ([#400](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/400),
  [#401](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/401)):
  the runtime downgrade wires its recovery trigger, and a mint outage
  observed by real traffic fires the downgrade the periodic probe used
  to miss.
- **Renewal no longer double-charges**
  ([#430](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/430),
  fixed): the renewal check never fires while more than half of the
  current allotment remains, and the default `bytes_renewal_offset` is
  coherent with what an advertisement can actually sell.
- **Expired keysets are reported honestly**
  ([#440](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/440)):
  a note on a retired keyset gets a dedicated
  `payment-error-keyset-expired` "cannot be recovered" code instead of
  being misclassified as mint-unreachable — and no longer poisons the
  health tracker.
- **Swap fees explained; only healthy mints advertised**
  ([#409](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/409),
  [#408](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/408)).
- **Portal on nodogsplash 5.0.2, whitelabel brand**
  ([#428](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/428)):
  `gatewaydomainname` is no longer set (and is deleted on upgrade and
  re-runs); the whitelabel hostname (`tollgate` or `net4sats`) comes
  from `/etc/tollgate/brand` and now moves with the brand on upgrade
  while custom hostnames are kept.
- **Packaging survives the real world**: upgrades no longer abort on
  `jq`-less minimal systems
  ([#407](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/407));
  the full nftables ruleset actually ships, so the backend API on
  `:2121` is LAN-protected
  ([#387](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/387));
  the whitelabel config UI has its own `uhttpd` section on `:8090`
  ([#451](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/451))
  and the admin board is reachable from captive clients
  ([#458](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/458));
  Wi-Fi scanning addresses the radio's real interfaces instead of the
  uci section name
  ([#449](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/449)).
- **The package ships the full pinned portal bundle**: the guest SPA,
  the admin board SPA and the `tollgate` rpcd plugin all build in-tree
  from the manifest pin (#466) — a missing artifact at the pin is now a
  hard build error — and apk-based OpenWrt 25.x upgrades run setup
  again: the version gate no longer self-matches (#463, fixes #459).
- **Wallet sidecar architecture (alpha feature)**
  ([#395](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/395)):
  a `WalletPort` client speaking newline-delimited JSON-RPC over an
  `AF_UNIX` socket to an out-of-process wallet daemon, per-backend
  capability manifests (`gonuts`, `cdk`, `nucula`) and a
  `wallet-policy.json` selection policy — the groundwork for backing
  the module with a non-Go wallet without linking CGO. Unit tested
  only; see the wallet-backend contract and measurement protocol docs
  ([#431](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/431)).
- **Reproducible, self-verifying, sharded releases**
  ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383),
  [#410](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/410),
  [#435](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/435),
  [#445](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/445),
  [#441](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/441)):
  pinned toolchains; `SOURCE_DATE_EPOCH` stamped end-to-end via the
  stage-1 rendezvous record; the matrix sharded into budgeted runs;
  and a post-publish gate that requires every declared `(arch,
  format)` to be announced and mirrored with a matching sha256.

## What's new

### Wallet and payment safety

`wallet drain cashu` is now safe to run on a wallet that holds funds:
mint URLs are canonicalized (scheme/host case, trailing slashes,
default ports, userinfo/query/fragment) and merged on read, so one
logical mint can no longer appear as two phantom-balanced entries;
per-mint failures produce an explicit partial result
(`success:false`, `partial:true`, `tokens`, per-mint `errors`) instead
of discarding earlier tokens; every produced token is appended to an
fsync'd `/etc/tollgate/wallet-drain-journal.jsonl` **before** the next
mint is attempted; `--yes`/`-y` and distinguishable exit codes round
it out.

The payment path refuses what it cannot deliver: a read-only
`ndsctl json` probe rejects payment up front with an actionable
`client-not-registered` notice when NDS has no client session for the
MAC — re-probing at the valve's auth-retry cadence so the reseller
flow's asynchronous registration is not refused on first sight, and
failing open on probe errors. A note on a retired keyset gets the
dedicated `payment-error-keyset-expired` code (the mint is healthy;
the note is permanently unspendable) instead of a misleading
retry-flavored error. Swap fees are reported (`WalletPort.SwapFeeSats`)
and pre-checked, and only mints serving a real NUT-01 keyset list are
advertised. The gonuts hardening wave is included (empty-proofs
panic contained, verbatim mint rejections, `ErrTokenAlreadySpent`
reachable, wallet-locked error instead of deadlock).

The sidecar architecture is the one feature: `src/tollwallet` gains
the RPC client, the three capability manifests and the policy
selector. It ships behind the unchanged `WalletPort` contract with the
in-process backend still the default; treat it as scaffolding for the
wallet migration, not a supported configuration.

### Sessions and renewal

Reseller sessions no longer pay twice at startup
([#430](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/430)):
the renewal check never fires while more than half of the current
allotment remains (with a once-per-effect warning when the configured
offset is overridden), and the default `bytes_renewal_offset` no
longer exceeds what a typical advertisement can sell after step
quantization.

### Captive portal, brand and packaging

`gatewaydomainname` is gone — set nowhere, deleted on version-changing
upgrades **and** same-version setup re-runs — fixing the NDS 5.0.2
pre-auth redirect loop
([#428](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/428)).
The whitelabel hostname is selected by `/etc/tollgate/brand`
(`tollgate` default, `net4sats`), drives hostname, DNS, SSIDs and the
NDS gateway name, and moves with the brand on upgrade while
operator-chosen hostnames are preserved
([#444](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/444)).
The whitelabel config UI is served by a dedicated `uhttpd` section on
`:8090` (no more LuCI redirect), the admin board (`:8090`/`:8443`) is
reachable from captive clients exactly like LuCI on `:8080`, and
Wi-Fi scanning resolves real radio interfaces (`phyN-apK`) instead of
uci section names, so the admin page lists networks again.

### Release engineering

The publishing lane is deterministic and self-verifying: pinned
toolchains (#383), a commit-derived `SOURCE_DATE_EPOCH` that rides the
stage-1 rendezvous record so every shard packages with the epoch the
binaries were stamped with (#441), and the post-publish gate from
#435. Stage 2 is now eleven budgeted shard workflows rendered from
`packaging/ngit-release-matrix.json` (no leg dropped), and
announcements moved to a dedicated workflow that refuses unless every
shard succeeded and every leg has a build record naming the same
release run — a failed or timed-out shard leaves the release
unannounced instead of half-announced (#445). One `make go-battery`
runs the documented Go gate across all 16 modules (#455).

## Behavior changes worth flagging

- **`gatewaydomainname` is gone** (deleted on upgrade and re-runs);
  `<hostname>.lan` keeps resolving via dnsmasq.
- **`/etc/tollgate/brand` selects the whitelabel hostname**, and on
  upgrade the hostname moves with the brand; custom hostnames are kept.
- **The admin board (`:8090`, opt-in `:8443`) is reachable from
  captive clients** like LuCI — a deliberate `users_to_router` widening.
- **Payments are refused before the token is consumed** when NDS has
  no client session for the MAC (`client-not-registered`).
- **`wallet drain cashu` has new output and exit codes**, a journal
  file (`/etc/tollgate/wallet-drain-journal.jsonl`, root-only, never
  auto-removed), and `--yes`/`-y`.
- **`preinst` is a no-op**; the binary owns `install_time`.
- **The renewal clamp may log a warning** when it overrides a
  configured `bytes_renewal_offset` larger than half the allotment.
- **No partial releases are announced anymore**: if a build shard
  fails, consumers see *no* kind-1063 events for that version at all —
  by design.

## Notable bug fixes

- A runtime downgrade recovers when a mint comes back (live case: 2 h+
  degraded with a healthy probe)
  ([#400](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/400));
  a mint outage observed by real traffic fires the transition the
  periodic probe missed
  ([#401](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/401)).
- The full nftables ruleset ships; the backend API on `:2121` is
  LAN-only on fresh installs as documented
  ([#387](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/387)).
- Package license metadata corrected to `GPL-3.0-only`
  ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383)).

## Internal changes (CI, tests, dependencies)

- `gonuts-tollgate` re-pinned to the tagged `v0.11.2`
  ([#394](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/394)).
- Wallet-backend architecture docs: contract, integration decision and
  the measurement protocol (incl. the INJ-1…INJ-8 fault-injection set)
  ([#431](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/431)).
- Merchant tests decoupled from the live wallet via centralized Cashu
  token fixtures
  ([#396](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/396)).
- Cloud-lab runs isolated per checkout
  ([#446](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/446));
  contract fixtures gained a mirror/relay-list sync check
  ([#456](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/456));
  `make go-battery` runs the whole Go gate
  ([#455](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/455)).
- A renewal-policy simulation study with measured results (the 10-s
  throttle cap, the right defaults for ≤400 Mbps uplinks), and the
  cloud-lab e2e driver honors the documented compose override
  ([#465](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/465));
  the captive-portal bundle-location decision is recorded as an ADR
  ([#462](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/462)).
- Tester guide and the single-channel tester intake with S1 stop-ship
  rules
  ([#381](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/381));
  the ngit mirror is linked from the README
  ([#390](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/390)).

## Known issues

- **nodogsplash 5.0.2 on OpenWrt 22.03**: iptables-nft translation
  strips port matchers, so pre-auth DNAT hijacks all TCP, not just :80
  ([#398](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/398),
  open).
- **Runtime-downgrade recovery is proactive-cycle-bound (~13 min)** —
  recovery waits for the periodic probe cycle; the aggressive loop is
  not armed on runtime downgrades
  ([#429](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/429),
  open).
- **Payout melts and drain ignore mint input fees** —
  `balance_tolerance_percent` acts as accidental compensation
  ([#414](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/414),
  open).
- **No refund path when gate-open fails after a successful `Receive`**
  beyond the L1 pre-check landed in #412
  ([#403](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/403),
  remainder open).
- **Config can reset to the factory-default mint** if the daemon exits
  during degraded mode
  ([#402](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/402),
  open).
- **Keyset `final_expiry` silently kills held balances**; wallets must
  rotate off inactive keysets — the payment path now at least says so
  ([#417](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/417),
  open; #440/#447 improved the reporting).
- **NDS still answers HTTP 500 for unmappable clients** (stock NDS
  5.0.2, upstream behaviour; the SPA loads from uhttpd regardless).
- **The wallet sidecar is scaffolding, not a supported backend** —
  unit tested only, no router validation, no measurement baseline
  published yet.
- **The sharded publish pipeline has never run against a real tag** —
  branch/dev runs prove the mechanics; a tag run is first-of-its-kind.
- `tollgate-clientd` is **not** part of this release (its PR remains
  open pending its own fixes).

## Verification status (read this before trusting a build)

- **Unit/race, this exact tip**: `make go-battery` green across all 16
  modules (gofmt/vet/build + race-enabled `testenv` suite), and the
  config-schema and build-purity contract checks pass on the release
  commit content.
- **Live/lab evidence** (per fix, before this tag): the degraded-mode
  pair was live-reproduced (PRTA #110); the drain regression was
  live-validated (PRTA #107); the gate-open refusal triggers were
  lab-reproduced; the NDS 5.0.2 redirect loop was observed on real
  clients before the fix; the renewal double-charge was measured on
  the reseller lab.
- **Not verified on a router from this tag**: everything — router
  validation is what this alpha cycle is for. The sidecar specifically
  has unit coverage only.

## Upgrade notes

- **Back up the wallet directory and `/etc/tollgate/config.json`
  before upgrading.**
- **`.ipk` filenames** are `tollgate-wrt_v0.6.0-alpha4_<arch>.ipk`
  (UPX variants add a `-upx-…` suffix); the control-file version is
  the tag name verbatim. `opkg install` on OpenWrt 24.10 and earlier.
- **`.apk` (OpenWrt 25.x)**: the sharded pipeline is built to let the
  SDK legs finish within their budgets; whether they do on a real tag
  is exactly what this RC establishes. Check for announcements before
  promising 25.x testers anything.
- **Do not reuse `v0.6.0-alpha1/2/3` or any `v0.7.0-alpha*` package** —
  none of them published a single artifact: alpha1's prerelease page is
  empty, and the alpha2/alpha3 tags were cut but shipped nothing
  (alpha3 was held back by release-lane incidents and is superseded by
  this release).
- **Expect a setup rerun after installing.** Existing WiFi/portal
  configuration is preserved; the hostname moves with the brand unless
  you set your own; check `/tmp/tollgate-setup.log` (root-only).

## Getting v0.6.0-alpha4

- Packages are announced as NIP-94 kind-`1063` events
  (`n=tollgate-wrt`, `v=v0.6.0-alpha4`, `c=alpha`). Filter by **both**
  publisher keys — the ngit CI key publishes now; the historical
  Actions key never will again:

  ```bash
  nak req -k 1063 \
      -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
      -a 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d \
      --tag n=tollgate-wrt --tag v=v0.6.0-alpha4 --limit 50 \
      wss://relay.damus.io wss://nos.lol wss://nostr.mom
  ```

- **No events for this version means the release is not published** —
  a failed shard suppresses all announcements by design; do not
  install stray artifacts from other versions' events.
- Download from any `url` tag (mirrors of the same blob) and **verify
  the file's sha256 against the event's `x` tag** before installing:

  ```bash
  echo "<x-tag-sha256>  tollgate-wrt_v0.6.0-alpha4_<arch>.ipk" | sha256sum -c -
  opkg install tollgate-wrt_v0.6.0-alpha4_<arch>.ipk
  ```

- **Reporting problems**:
  [docs/tester-intake.md](docs/tester-intake.md) names the single
  intake channel and the report template;
  [docs/rc-tester-guide.md](docs/rc-tester-guide.md) documents
  install/upgrade/rollback. Any wallet/funds symptom is **S1 =
  stop-ship**.

## Contributors

Commits since the never-published `v0.6.0-alpha2` preparation came
from Amperstrand, c03rad0r and Felix (via `felixfelix-bot`), on top of
the `v0.5.0`-era contributor base — c03rad0r, Amperstrand, Origami74,
Matt Van Horn and Alex Xie. The full per-PR record is
[CHANGELOG.md](CHANGELOG.md).
