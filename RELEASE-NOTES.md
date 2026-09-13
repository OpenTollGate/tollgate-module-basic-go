# TollGate v0.6.0-alpha2 (tollgate-wrt)

**Released**: 2026-09-13
**Channel**: `alpha` — a tester-facing release candidate, not a stable
release. Expect rough edges; report them.

<!-- markdownlint-disable MD013 -->

`v0.6.0-alpha2` is the first TollGate release cut from **upstream** `main`
since `v0.5.0` — the `v0.6.0-alpha1` tag and the `v0.7.0-alpha*` tags that
preceded it exist only on a fork and point at branches that were never
merged, so nothing was ever published from them. If you chased one of those
versions, this is the one that replaces them.

The theme is *the router stops lying to the client*: the captive portal is
served by its own uhttpd instance instead of through NoDogSplash, the client
MAC now travels with the request instead of being guessed from ARP, the
NoDogSplash session ceiling no longer expires a session the customer paid
for, and a mint that answers badly — or crashes the wallet goroutine —
produces a signed failure notice instead of a dead daemon or a 30-second
stall. Alongside that: vendor-IE discovery for router-to-router scanning,
a NIP-06/HKDF identity module, a token-recovery tool for funds stranded by
gate-side failures, and a packaging/release-engineering cleanup that gives
the project one version number instead of three.

## At a glance

- **Captive portal served directly.** A dedicated uhttpd instance on
  `0.0.0.0:2051` serves the SPA; NoDogSplash keeps a sub-1 KB stub page
  pre-auth and no longer proxies the portal.
- **Payment-path hardening.** Panics in the payment goroutine are contained
  and reported, locked (P2PK/HTLC) tokens are rejected, mint rate limiting
  is reported as such, HTTP response bodies are capped at 1 MB, and the
  cashu wallet's swap-counter race — which bricked a wallet permanently on
  a transient mint failure — is fixed.
- **Sessions survive.** Quote persistence across restarts and a 24 h
  NoDogSplash ceiling mean a purchased session is no longer cut short by a
  service restart or by NDS's 20-minute default.
- **Discovery: vendor IE + scan history.** The wireless gateway manager
  decodes the TollGate vendor IE from beacon frames, and every scan cycle is
  logged to `/etc/tollgate/discovery_log.jsonl` with pricing and signal, with
  `tollgate-cli upstream known` to read the accumulated view.
- **Identity module.** NIP-06 12-word mnemonic derivation, HKDF-derived
  IPv4/MAC/password attributes, and a loopback-only seed-reveal endpoint.
- **Operator tooling.** `scripts/token-recovery/` salvages tokens rejected by
  gate-side failures; `docs/operator-guide.md` documents the whole CLI.
- **One version number.** `VERSION` at the repository root is now the single
  source of truth for the release version; CI refuses a tag that disagrees
  with it, and the maintainer tag/publish runbook is documented.

## What's new

### Captive portal

The portal is no longer served through NoDogSplash. `/etc/nodogsplash/htdocs`
is not a symlink to the SPA any more; NDS serves a tiny stub page with a JS
redirect to port 2051, plus a `<noscript>` fallback that is built from the LAN
IP (with CIDR suffixes stripped — `192.168.1.1/24` used to produce a malformed
URL on routers that store the address in CIDR form; found on a GL.iNet MT3000).
The stub also preserves the query string, so the `?clientmac=…` parameter NDS
hands out survives the redirect.

A second uhttpd instance (`config uhttpd portal`) serves the SPA on
`0.0.0.0:2051`, with directory listings disabled, the NDS `users_to_router`
allow list extended to 2051, and the installer hardened (an `htdocs` that
exists as a regular file is removed rather than silently ignored).

Clients now come with a MAC attached: the backend accepts a `mac` field in
Lightning invoice and Cashu payment requests, and a `mac` query parameter for
invoice polling and `/whoami`, so the splash page passes the MAC it already
knows instead of the backend falling back to ARP/DHCP lookup behind a reverse
proxy. Handlers that used to return 400/500 when no MAC could be resolved now
log and fall back.

### Payments and wallet safety

A panic inside the wallet layer during `PurchaseSession`'s `Receive` call
(a mint returning malformed keysets) used to take the whole daemon down with
every active session on it. The goroutine now recovers and pushes an explicit
`payment processing panicked: …` error into the existing result channel, so
the caller gets a signed `payment-processing-failed` notice immediately
([#360](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/360)).

`tollwallet.Receive()` checks each proof's secret for spending conditions
before crediting a user: P2PK and HTLC-locked tokens are rejected with
`ErrLockedToken`, closing a "free internet with tokens the gateway can never
spend" path found in the Layer 3 cashu audit (fixes
[#324](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/324)).
Mint HTTP 429 now maps to `mint-rate-limited` with a human-readable message
instead of a generic payment failure, and `merchant.Fund()` decodes V3 as well
as V4 tokens (fixes
[#325](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/325)).

Mint URLs are matched with `tollwallet.MintURLMatches()` in
`calculateAllotment()`, so a trailing slash, an upper-case host or a
normalized path no longer fails an otherwise valid payment
([#250](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/250),
[#251](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/251)).

### Sessions and quotes

Lightning quotes are persisted to `quotes.json` in the wallet directory and
reloaded on startup, and monitoring is relaunched for unpaid quotes, so a
restart no longer leaves a paying customer staring at "Waiting for payment"
([#248](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/248)).
Persistence is also race-free and crash-safe now: records are deep-copied
under the lock and the temp file is `fsync`ed before the rename, with five
concurrency tests as regression guards.

NoDogSplash's `sessiontimeout` is set to 86400 (24 h) and
`authidletimeout` to 3600, so NDS's 1200-second default can no longer deauth
a client mid-session; the Go backend stays the sole authority on purchased
session lifetime.

### Discovery and upstream

The wireless gateway manager reads the TollGate vendor-specific Information
Element from beacon frames — implemented from the wire-format spec with
round-trip tests and an overflow check before the cast — and boosts a
discovered AP's score accordingly; the config schema gains an optional
`vendor_ie_discovery` boolean (default `false`).

Every scan cycle now records each discovered AP (BSSID, SSID, signal, radio,
TollGate flag, price/step) to `/etc/tollgate/discovery_log.jsonl`, log-rotated
and persistent across reboots, with an in-memory registry tracking signal
range, sample count and latest pricing across scans;
`tollgate-cli upstream known` prints that view, and `upstream scan` reports
`is_tollgate`, `price_per_step` and `step_size` for real TollGate APs
([#312](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/312)).

### Identity

New `src/identity` module: NIP-06 12-word BIP39 mnemonic derivation, HKDF
(RFC 5869) attribute derivation for IPv4/MAC/password replacing raw SHA-256,
and a loopback-only `/identity/reveal-seed` endpoint with a 1 KB body limit.
14 tests
([#331](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/331)).

### Operator tooling and docs

`scripts/token-recovery/` parses `/etc/tollgate/tokens-to-recover.txt`, checks
each proof's state at the mint via NUT-07 `/v1/checkstate`, and recovers
UNSPENT value through the wallet; `-dry-run` reports recoverable/spent/pending
counts without touching the wallet — for salvaging tokens rejected by the
gate-side failures of the exit-status-1 class
([#354](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/354)).

`docs/operator-guide.md` documents every `tollgate` CLI subcommand (service,
wallet, private network, upstream Wi-Fi, config, health) with flags, example
output and troubleshooting
([#188](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/188)).

A Docker-based integration lab (`tests/cloud-lab/`) brings up a cdk-mintd
FakeWallet mint, upstream and reseller TollGate containers against a
fake-ndsctl shim, and a client container with smoke-payment, mint-failure and
two-router autopay suites
([#362](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/362)).

### Release engineering

The release version now has a single source of truth: `VERSION` at the
repository root. Three literals used to disagree — `v0.0.0` in
`src/cli/version.go`, `v0.7.0-alpha10` in `packaging/local-build-ipk.sh` and
`v0.6.2` in the setup script. CI refuses a tag that is not byte-identical to
`VERSION`, every packaging path substitutes a `__TOLLGATE_VERSION__`
placeholder in the setup script, `scripts/build-sdk-package.sh` derives its
version from `VERSION`, and `scripts/check-version-sync.sh` (wired into
`hooks/pre-commit`) fails the tree if a version literal creeps back in. The
maintainer runbook — pre-flight gates, the annotated-tag commands on upstream
`main`, and the publish/verify sequence — is
[docs/release-process.md](docs/release-process.md).

## Behavior changes worth flagging

- **The portal lives on port 2051.** NoDogSplash still answers pre-auth on
  port 80 with a stub page; the SPA itself is served by uhttpd on 2051. If
  you allow-listed or firewalled portal ports, allow 2051.
- **The backend API on port 2121 is LAN-only.** A new nftables include
  (`30-backend-firewall.nft`) blocks the API on non-LAN interfaces. WiFi
  clients keep reaching the payment endpoint through the NDS
  `users_to_router` rules; WAN-side and upstream clients can no longer probe
  it. Defense in depth for the API's trust model.
- **CORS no longer echoes a wildcard.** `Access-Control-Allow-Origin: *` is
  gone; the origin is echoed only for local/private origins and for pages the
  router itself serves on another port, with `Vary: Origin` added. POSTs
  with content types other than `text/plain` or `application/json` now return
  415 ([#349](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/349)).
- **NDS timeouts are 24 h / 1 h** (session / idle), by design — the Go
  backend decides session lifetime. Adjust in `setup_nodogsplash()` if your
  deployment needs a shorter ceiling.
- **Setup rerun on upgrade.** The setup script's version marker now carries
  the real release version (it used to be a hand-written `v0.6.2`, which
  never matched a release), so upgrading re-runs setup. Existing management
  WiFi credentials are preserved: the private SSID/PSK is reused from
  `wireless.private_radio0` and only generated when none is configured.
- **`/tmp/tollgate-setup.log` is root-only (0600).** It contains the
  generated management-WiFi password.
- **Config schema version is unchanged (`v0.0.8`).** No migration is needed;
  `vendor_ie_discovery` is optional and defaults to `false`.

## Notable bug fixes

- Payment-goroutine panics no longer kill the daemon
  ([#360](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/360)).
- The local build script no longer injects the test mint into release builds:
  `GitBranch` was left `unknown`, which `IsDevBuild()` treated as a feature
  branch, adding a dummy-invoice test mint to every locally built `.ipk`
  ([#359](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/359)).
- Splash redirect preserves query parameters, so MAC-based session logic sees
  the real client MAC ([#363](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/363)).
- NoDogSplash no longer overrides purchased session duration
  ([#363](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/363)).
- **Critical:** the cashu wallet's swap-counter race is fixed by
  `gonuts-tollgate` v0.7.4. In v0.7.1 the counter advanced only after a
  successful swap, so a transient mint failure left it stuck, every retry
  reused it, the mint answered NUT-02 code 10002 and the wallet was bricked
  with no self-recovery. v0.7.4 advances the counter before the swap and
  regenerates blinded messages on retry.
- SSRF guard on the post-payment NDS session trigger: loopback, link-local
  and unspecified upstream addresses are rejected
  ([#315](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/315),
  [#347](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/347)).
- HTTP response bodies are limited to 1 MB across LNURL resolve, invoice
  fetch, gateway probes and the usage tracker, preventing OOM on
  resource-constrained routers ([#267](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/267)).
- Notice event codes and the advertisement `tips` tag now match TIP-01/02
  instead of implementation-specific codes and non-existent TIP numbers.
- Wireless config is read gracefully when `/etc/config/wireless` is absent;
  the dead `firewall-tollgate` include (silently rejected by fw4) is gone,
  along with two Makefile references to it that broke package builds
  ([#196](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/196),
  [#235](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/235)).

### Security

- **Exposed deployment backup purged from history.** A router deployment
  backup committed by accident in #358 (`deploy-backup-20260730/`) contained
  the merchant private identity key, an ecash `wallet.db` and spendable
  recovery tokens. `main` was rewritten on 2026-08-27 and force-pushed;
  `SECURITY.md` records the incident and the residual exposure, and key
  rotation is tracked in
  [#364](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/364).
  If you ran any build between those dates, treat the affected key material
  as compromised.
- Wildcard CORS removed, backend API restricted to LAN interfaces,
  P2PK/HTLC-locked tokens rejected, 1 MB response-body cap
  (see above).

## Internal changes (CI, tests, dependencies)

- Spec-quote drift is checked in CI against current cashubtc/nuts HEAD; one
  drifted NUT-03 quote was fixed and a NUT-05 quote added at the
  `RequestMeltQuote` site
  ([#357](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/357)).
- `src/tollwallet` pins NUT-00 `HashToCurve` output against the canonical
  cross-implementation vectors (gonuts/btcec ↔ cashu-core-lite/k256 ↔
  Python coincurve), because divergence there makes NUT-07 checkstate report
  spent proofs as unspent
  ([#351](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/351)).
- New `src/sysexec/` safe-exec wrapper (`Runner` interface, context,
  timeout, retry) — foundation for the 37 `exec.Command` call sites
  ([#265](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/265)).
- New `src/tollwallet` `WalletPort` interface with a `GonutsWallet` adapter,
  decoupling the merchant from gonuts types
  ([#299](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/299)).
- The personal-fork `replace` directive on `gonuts-tollgate` is gone now that
  the official v0.11.1 tag exists; both modules re-tidied
  ([#361](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/361)).
- CI: `src/merchant` joined the test matrix, `config_manager` buildinfo tests
  were updated for the five extra production mints
  ([#365](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/365));
  `package-apk` uses Node 20 instead of the SDK container's Node 12, which
  had been failing the portal build on every push since Aug 26
  ([#366](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/366));
  build triggers are restricted to `main` + `v*` tags with docs-only paths
  ignored and redundant runs cancelled
  ([#369](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/369));
  the SDK build tree is cached and every heavy job has a timeout
  ([#370](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/370));
  the portal is built once per run instead of per matrix leg, which also
  removed per-leg commit drift
  ([#368](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/368)).
- The tree is `gofmt`- and `goimports`-clean, so the documented pre-PR gate
  prints nothing
  ([#352](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/352)).

## Known issues

- **`tollgate wallet drain cashu` can lose funds
  ([#375](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/375),
  open).** Reproduced on a GL.iNet MT3000 over SSH without a PTY: the command
  prints `Operation cancelled.` and exits 0 without draining, and `--json`
  can destroy funds on a wallet whose per-mint registry holds a duplicate or
  stale entry. This is the CLI path, not the payment path, and the fix is
  tracked for this release cycle — check the issue before draining a wallet
  that holds funds. If you drain manually, work from the non-JSON output and
  verify the mint's balance afterwards.
- **NoDogSplash pre-auth is still fragile for unmappable clients.** Stock NDS
  5.0.2 answers HTTP 500 (its own error page) on every request from a client
  it cannot map to a MAC (`ip neigh` miss). The SPA itself now loads from
  uhttpd regardless, which is the mitigation; the NDS-side behaviour is
  upstream and unaddressed.
- **`src/cli`, `src/upstream_detector` and `src/upstream_session_manager`
  are still outside the standalone go-test matrix** pending the same module
  rewrite `src/merchant` received
  ([#365](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/365)).

## Verification status (read this before trusting a build)

Be sceptical of "the tests pass" for router-visible behavior — several
changes in this release only exist on a real device.

- **Validated on hardware**: the captive-portal stub/redirect path and the
  no-CIDR LAN IP case (GL.iNet MT3000), and NDS behavior on stock NDS 5.0.2.
- **Covered by unit/race tests only**: the payment-goroutine panic
  containment, quote persistence and crash safety, spending-condition
  rejection, mint error mapping, the discovery logger, the identity module,
  the `sysexec` and `WalletPort` foundations, and the CI/release-engineering
  changes.
- **Not verified at all on a router for this release**: the version
  single-source change was exercised in a checkout and by simulating the CI
  steps, not by installing a package built from this tag and watching
  `99-tollgate-setup` re-run. Treat the setup-rerun behavior as untested
  until a tester confirms it on hardware.

## Upgrade notes

- **Back up the wallet directory and `/etc/config/tollgate` before
  upgrading.** The deployment backup that leaked in August is a reminder of
  what that data is worth.
- **Package filenames are `tollgate-wrt_v0.6.0-alpha2_<arch>.ipk` and
  `…_<arch>.apk`** (the tag name verbatim; UPX variants add a `-upx-…`
  suffix before the extension).
- **`.apk` (OpenWrt 25.x)**: `apk add --allow-untrusted
  tollgate-wrt_v0.6.0-alpha2_<arch>.apk`. Inside the package apk-tools sees
  the normalised version `0.6.0_alpha2-r0`.
- **`.ipk` (OpenWrt 24.10 and earlier)**: `opkg install
  tollgate-wrt_v0.6.0-alpha2_<arch>.ipk`; the control-file version is the
  tag name, `v0.6.0-alpha2`.
- **Do not reuse `v0.6.0-alpha1` or any `v0.7.0-alpha*` package.** Those
  tags are fork-only and point at unmerged branches; `v0.6.0-alpha1` has a
  0-asset prerelease page and no usable artifacts.
- **Expect a setup rerun after installing.** Existing WiFi/portal
  configuration is preserved, including management-WiFi credentials; check
  `/tmp/tollgate-setup.log` (root-only) afterwards.
- **If you reached the backend API from outside the LAN, that path is
  closed** (port 2121 is LAN-only now). Use the portal path WiFi clients use,
  or reach the router's shell.

## Getting v0.6.0-alpha2

- **Packages**: CI builds `.ipk` and `.apk` per architecture on the tag and
  uploads each artifact to multiple Blossom mirrors; every artifact is
  announced as a NIP-94 `1063` event (`n=tollgate-wrt`,
  `v=v0.6.0-alpha2`, `c=alpha`) on the project relays. Download from any
  `url` tag and **verify the sha256 against the event's `x` tag** before
  installing:

  ```bash
  nak req -k 1063 -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
      --tag n=tollgate-wrt --tag v=v0.6.0-alpha2 --limit 50 \
      wss://relay.damus.io wss://nos.lol wss://nostr.mom
  ```

- **From source**: [scripts/build-sdk-package.sh](scripts/build-sdk-package.sh)
  cross-compiles the binaries and stages the canonical
  [packaging/](packaging/) recipe into the OpenWrt SDK, producing either
  format. It takes `PACKAGE_VERSION` from the environment and otherwise reads
  `VERSION`, so a checkout at this tag builds `v0.6.0-alpha2` by default.
- **Announcements**: `RELEASE-NOTES.md` (this document) and the
  `v0.6.0-alpha2` section of [CHANGELOG.md](CHANGELOG.md) are the
  authoritative description of the release; the per-PR detail lives in the
  changelog.
- **Reporting problems**: open an issue at
  [github.com/OpenTollGate/tollgate-module-basic-go](https://github.com/OpenTollGate/tollgate-module-basic-go)
  with the router model, the package version, and `/tmp/tollgate-setup.log`
  if setup is involved. Read the known-issues section above first.

## Contributors

Commits since `v0.5.0` came from Amperstrand, c03rad0r, Felix and Matt Van
Horn, on top of the review, hardware-testing and bug-reporting work that
made this release possible. The v0.5.0 contributor list —
[@c03rad0r](https://github.com/c03rad0r),
[@Amperstrand](https://github.com/Amperstrand),
[@Origami74](https://github.com/Origami74) and Alex Xie — remains part of
what this release is built on.

The full per-PR record is [CHANGELOG.md](CHANGELOG.md).
