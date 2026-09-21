# Changelog

All notable changes to the TollGate basic module are documented here.
This project loosely follows [Keep a Changelog](https://keepachangelog.com/)
and [Semantic Versioning](https://semver.org/).

> **Note:** Releases prior to `v0.4.0` predate this changelog and were not
> documented. The entries below cover everything merged into `main` since the
> `v0.4.0` tag.

## [Unreleased]

## [v0.6.0-alpha4] - 2026-09-22

Packaging-fix pre-release on the `v0.6.0-alpha3` code base, cut from the
captive-portal lane (PR #513): the module now depends on `nodogsplash`
instead of replacing it, the pre-auth allow list carries the `:443` entry the
`:8080` → `https://` redirect needs, a reinstall whose version string is
unchanged now re-asserts that list, and the management subnet steps out of the
way of a colliding upstream. Nothing in the Go module changes. The version
string moves to `v0.6.0-alpha4` (`0.6.0_alpha4-r0`) so that `apk` sees a real
upgrade over `alpha3` and takes the full setup path rather than the
same-version short branch.

### Added

- **Management subnet selection avoids upstream collisions instead of assuming
  it is safe.** The private network's /24 was derived as "the LAN's /24 with
  the third octet stepped by one" with no look at the upstream side, so a WAN
  side that is itself a private LAN in that same /24 (a hotel, a site uplink,
  another router's LAN) put the management subnet and the uplink subnet in the
  same network: the private bridge's default route then pointed at an address
  the router also owned, and traffic for the upstream's client range was
  routed back into the router instead of out of the WAN. The candidate is
  still the LAN-adjacent /24 — operators expect it and the DNS/DHCP defaults
  assume it — but it is now checked against the LAN's real mask and against
  every upstream-side network (the configured WAN address, plus every
  non-LAN address the kernel knows, which is where DHCP/STA/FIPS uplinks
  live), and a collision falls back to a random non-overlapping /24 from 10/8,
  logged so a field report shows why. The Go-side knob
  (`IPAddressRandomized`) is still only logged and stays out of scope here.
  See
  [docs/architecture/private-subnet-collision-avoidance-decision.md](docs/architecture/private-subnet-collision-avoidance-decision.md).
  ([#513](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/513)).

### Fixed

- **The captive portal no longer rejects v4 (`cashuB`) tokens that carry a
  short keyset id.** The shipped portal is built from the revision pinned in
  `packaging/build-inputs.json`, and that revision decoded the token with
  `getDecodedToken(token)` — a call that needs a `MintKeyset` list and
  therefore throws `A short keyset ID v2 was encountered, but got no keysets
  to map it to` on every token whose keyset id is a short one, which is what
  coinos/minibits hand out; the portal surfaced that to the user as `#CU102`
  and the payment could not be made. `.portal.commit` moves
  `992cf7f1` → `d699367` (upstream `main`, portal #55), which decodes through
  the keyset-agnostic `getTokenMetadata` first, and the bundle was regenerated
  from that pin with `bash packaging/portal-build.sh`, so the shipped bytes
  and the pin agree again. ([#517](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/517))

- **Runtime downgrades recover in seconds, not the next proactive
  cycle.** When all mints went unreachable under a running service, the
  downgrade path wired the recovery trigger (#400) but nothing probed
  aggressively — recovery waited for the 5-minute proactive check
  (~13 minutes stuck in degraded mode observed live after a transient
  mint blip). The aggressive 15-second probe loop that the startup path
  already used is now armed on the runtime downgrade too, and it fires
  the same first-reachable callback, so a wired recovery triggers within
  seconds of the mint returning. The aggressive timings moved from
  package constants to per-tracker fields so tests can shorten them
  without shared mutable state (which itself raced under `-race`).
  Fixes [#429](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/429).

- **The schema's config-version default caught up to `v0.0.8` — and a
  test now keeps every schema default in lockstep with the shipped
  defaults.** The schema table still declared `v0.0.7` while
  `NewDefaultConfig` and the migration stamp ship `v0.0.8` (the README
  said v0.0.7 too; aligned).
  `TestSchemaDefaultsMatchNewDefaultConfig` walks the schema and fails
  on any leaf default that disagrees with `NewDefaultConfig` — the two
  tables must change together, as the lenient-defaults change (#478)
  had to do by hand. Verified by mutation: a one-sided default change
  or a version regression reds the suite.

### Fixed
- **uhttpd contract re-asserted on every reinstall/apk upgrade.** The
  same-version branch of `99-tollgate-setup` — the branch a reinstall or an
  apk upgrade takes while `/etc/tollgate-setup-done` still matches the
  running version — re-verified the wireless APs and never re-ran
  `setup_uhttpd`, so the uhttpd settings this script owns could drift out of
  contract across upgrades. The branch now re-runs `setup_uhttpd` and
  `setup_uhttpd_portal` (alongside the existing `:8090` layout repair) and
  commits `uhttpd` only when the resulting config differs. That drift is
  what locked the operator out of LuCI on the 2026-09-21 pre13 build: a
  stale `uhttpd.main.redirect_https` sent `:8080` to a `:443` listener that
  was not running. `redirect_https` itself is now a derived value — set to
  `1` only when the cert/key pair is readable and non-empty — instead of a
  hardcoded `0`, so it always agrees with the rule the feed's
  `92-tollgate-admin-setup` evaluates for the same option. See
  [docs/architecture/uhttpd-redirect-https-ownership-decision.md](docs/architecture/uhttpd-redirect-https-ownership-decision.md).

- **Radio assignment no longer assumes `radio0` is 2.4 GHz.** OpenWrt numbers
  radio sections by detection order, not by band, so which section is the
  2.4/5 GHz radio varies between routers — the first-boot setup and the
  upstream STA interfaces were binding `tollgate_2g_open`/`private_radio0`/
  `tollgate_sta_2g` to `radio0` and their 5 GHz counterparts to `radio1` by
  name. Radios are now resolved by their `band` option (with legacy `hwmode`
  and channel fallbacks, mirroring OpenWrt's own resolution order):
  first-boot setup puts each public/private AP on the radio that actually
  owns the band, rebinds sections left on the wrong radio by older installs,
  and skips (instead of mis-labeling or creating phantoms) when a band has no
  radio; the upstream connector creates `tollgate_sta_2g`/`tollgate_sta_5g`
  on the band-matching radio so a 5 GHz upstream is never hunted with a
  2.4 GHz radio
  ([#452](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/452)).
- **Gateway detection sees explicit default routes again; inference is a
  last resort.** `netlink.RouteList` decodes default routes with `Dst`
  as the parsed `0.0.0.0/0` (or `::/0`) rather than nil on the pinned
  library version, so the detector's `Dst == nil` comparisons matched
  nothing: both route-based lookup methods were dead code for IPv4 and
  gateway selection always fell through to x.x.x.1 IP inference — which
  can point reseller mode at the wrong gateway whenever the real one is
  not the subnet's .1 (observed live in the #430 lab: an explicit
  `default via 172.29.0.10 metric 100` was ignored in favor of an
  inferred 172.29.0.1). A family-safe default-route predicate now
  accepts both encodings, and the lowest-metric default wins when
  several exist (kernel route-selection order). Root-caused with a
  netns probe reproducing both route-add forms against the exact pinned
  `vishvananda/netlink v1.3.1`. Fixes [#454](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/454).
- **Installing the module no longer leaves the router without a captive
  portal.** The SDK package definition declared no runtime dependency at all
  (`DEPENDS:=+libc`), and the `.ipk` recipes stamped `Replaces: nodogsplash`
  into the control file opkg reads. On the apk lane the shipped artifact
  carried `depends:libc` and no `replaces` field (verified from the raw
  `apk mkpkg` invocation in the build log), so nothing pulled or retained the
  daemon: after installing on a GL-MT3000 running OpenWrt 25.12.5, nodogsplash
  was gone and the portal was down until it was reinstalled by hand -- the same
  failure class `packaging/preinst` documents for an undeclared runtime
  dependency that a maintainer script needs ([#93](https://github.com/Amperstrand/tollgate-module-basic-go/issues/93)).
  On the opkg lane `Replaces` supersedes the named package outright, so those
  recipes would have removed the daemon too. The module's recipes now match the
  shipping-path feed definition (`net/tollgate-wrt/Makefile`:
  `DEPENDS:=+nodogsplash +jq`), keep the virtual `nodogsplash-files` ownership,
  and narrow `Replaces` to `base-files`, where only the payload-file ownership
  overlap is intentional.
  `tests/packaging/package-nodogsplash-dependency_test.sh` pins the contract
  across every recipe, their generated ngit shards and the built `.ipk`
  control file.
  ([#513](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/513)).
- **Pre-auth clients can reach LuCI's TLS port, so the `:8080` redirect lands.**
  `uhttpd.main.redirect_https` derives to `1` whenever a cert/key pair is
  readable, so a captive-LAN client hitting the LuCI port on `:8080` is
  answered `307 Location: https://<router>/` — but `setup_nodogsplash()`'s
  pre-auth allow list covered `:2121`, `:8080`, `:2050`, `:2051`, `:8090` and
  `:8443` and not `:443`, so nodogsplash REJECTed the redirect target and the
  operator could not reach LuCI before authenticating at all: the same lockout
  the neighbouring uhttpd ownership decision exists to prevent. The Go CLI's
  `ssl enable` path already wrote this rule (`src/cmd/tollgate-cli/ssl.go`);
  first boot — every freshly flashed router — was the only path without it.
  The rule is now written with the rest of the list and matched as a whole
  field, so `allow tcp port 8443` can never satisfy the `:443` check, whether
  uci renders the list one entry per line or space separated. See
  [docs/architecture/luci-https-pre-auth-reachability-decision.md](docs/architecture/luci-https-pre-auth-reachability-decision.md).
  ([#513](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/513)).
- **The same-version reinstall path re-asserts the captive-portal allow list.**
  The short branch `99-tollgate-setup` takes when `/etc/tollgate-setup-done`
  already equals the installed version verified the wireless APs and
  re-asserted the uhttpd contract, but never wrote the nodogsplash
  `users_to_router` allow list. An install of a build whose version string is
  unchanged (the pre15 round over pre14: both reported `v0.6.0-alpha3`, so
  `apk` saw the same package version and the flag stayed equal) therefore kept
  the previous install's list verbatim and came out without the pre-auth
  `:443` rule — nodogsplash REJECTed the `:8080` → `https://` redirect target
  and the post-install sweep scored 4/9. The writer is now one idempotent
  function (`assert_nodogsplash_allow_entries`) called by both the full-setup
  and the same-version path: it adds only what is missing (a list holding
  `:8443` but not `:443` gains `:443`, and the anchored `:443` match keeps
  `:8443` from satisfying it) and never duplicates an entry, so a
  same-version install repairs a stale or absent list instead of inheriting
  it. `tests/uci-defaults-same-version-allowlist_test.sh` drives the real
  same-version branch against a fake `uci`/`apk`.
  ([#513](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/513)).

- **The default private SSID no longer names the operator.** The private
  network's SSID is chosen in `99-tollgate-setup` when neither radio has one
  yet, and it was generated as `c08r4d0r-${RANDOM_SUFFIX}` — a maintainer's
  own handle, repeated in the AP beacon of every router that took the
  default and therefore readable by any WiFi scanner in range: flashing a
  router broadcast the operator's username. The default is now
  `TollGate-Private-${RANDOM_SUFFIX}`, a product name, and
  `tests/contract/check-identity-leak.sh` (a CI gate, also wired into
  `hooks/pre-commit`) fails when a maintainer identity literal reappears
  under `packaging/files/` or when the generated default stops being
  device-unique and product-prefixed. Routers that already have a private
  SSID keep it — the default is only generated when the option is unset —
  so this changes no deployed network.

### Changed / Internal

### Changed / Internal
- **ngit stage 1 now builds the portal from the pinned toolchain.** The
  `build-portal` job stopped overriding `PORTAL_REF` with a floating `main`
  (refused by the #466 reproducibility gate, red on every push to `main`
  since) and pins node 22.17.0 with the GitHub twin's npm verification, so
  the job matches `packaging/build-inputs.json` again.
  ([#477](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/477))

- **The committed portal bundle is now regenerated from the pin, and the
  contract is guarded.** The checked-in copy under
  `packaging/files/tollgate-captive-portal-site/` still listed
  `assets/index-DxBkINUB.js` in `asset-manifest.json`, shipped a
  `welcome.html` the pinned build no longer produces, and was missing the
  `logo192/512.png` the pinned build does produce — a build log, not a
  mirror of the pin. It now matches the pinned build output exactly, and
  `tests/packaging/assert-portal-bundle-contract.sh` fails the build when the
  pinned revision validates tokens with a keyset-requiring decode or when the
  committed copy drifts from what the pin builds. ([#517](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/517))
- **The default production mint list is guarded against test mints.**
  `defaultProductionMints()` is what a release-line configuration offers a
  paying customer, so a test mint reaching it would route real traffic at a
  throwaway mint; `config_manager_production_mints_test.go` pins both the
  list itself and the release-line default config against
  `testnut.cashu.space`, `nofee.testnut.cashu.space`,
  `nofees.testnut.cashu.space` and `testnut.cashu.exchange`, and keeps the
  `IsDevBuild()` gate that owns the one test mint the module does know
  (`testnut.cashu.exchange`) under test. ([#517](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/517))

- **The ngit release shards now carry a valid `SOURCE_DATE_EPOCH`
  expression.** The shard generator emitted the package-job env line from
  inside an f-string, collapsing `${{ … }}` to `${ … }` in all eleven
  committed workflows — the runner passes that through literally and
  `packaging/build-env.sh`'s epoch validation would have failed every
  package job on the first ngit-lane release run. The line is now
  token-emitted like every other brace-bearing template, the shards are
  regenerated, and the pipeline test pins both the `${{ }}` form's
  presence and the absence of the collapsed form. ([#491](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/491))


- **Pinned that an outage-refused payment never burns the token.** New
  cloud-lab lane (`run-rejection-safety.sh`): a payment refused while the
  mint is down must leave every proof UNSPENT at the mint (NUT-07) and
  the same token must still buy a session after recovery; a replayed
  token must never buy a second session (#423-class property).
  ([#512](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/512))


- **Dependency-sweep completion: every module resolves gonuts-tollgate
  v0.12.1.** #506's bump touched the directly-declaring go.mods but left
  `src/cli`'s indirect pin at v0.11.2, failing `make go-battery` there.
  Tidied (caught independently in the #507 and #511 verification passes).
  ([#516](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/516))

- **Release lane: the portal build no longer passes a floating ref.**
  Stage 1's build-portal step invoked `portal-build.sh` with
  `PORTAL_REF=main`, which the script's pin-hardening rejects
  outright ("does not match the pinned portal commit") — the job had
  been red on every `main` push since the enforcement landed while the
  pinned build itself was green. The step now uses the script default
  (the manifest SHA).
- **Fresh installs now size upstream prepay for a large renewal margin.**
  Defaults move to `preferred_session_increments_bytes` 2,500,000,000
  and `bytes_renewal_offset` 1,225,000,000 (49% of the increment —
  renew near half a tank, just under the #442 clamp so it stays
  dormant). Anchored to the #460/#465 simulation: the 2.5 GB tank keeps
  the 10 s renewal trigger throttle from capping sustained throughput
  below ~2 Gbps, and the offset covers multi-second payment RTTs at
  gigabit rates; the previous pair (500,000,000 / 125,000,000) capped
  gigabit uplinks at ~39% and left ~10 s of runway at 100 Mbps.
  Operators on slow or expensive links should tune down per the
  operator table in `tests/sim/RESULTS.md`. Existing saved configs are
  not rewritten. ([#478](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/478))

- **The cloud-lab's default client run is green as documented.** The
  quick start brings up the full default topology (the fee tests need
  `mint-fees`), the `requires_docker` pytest mark is registered, and
  `run-keyset-rotation.sh` honors compose project isolation plus an
  optional port-stripping override for shared hosts, layering compose
  files correctly (base + override + extra) instead of replacing the
  default set.
  ([#485](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/485))

- **Two-router autopay skips without the profile.** A default
  `docker compose run --rm client` failed two reseller tests on
  connection-refused whenever the two-router profile wasn't started;
  the module now probes the reseller at collection (5 s grace) and
  skips with the profile start command as the reason.
  ([#484](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/484))

- **Live external-mint lane in the cloud lab.** First committed coverage
  against a Cashu mint this repo does not control: a profile-gated
  `upstream-ext` service (its `accepted_mints` also lists
  `testnut.cashu.exchange` — nutshell main with a FakeWallet, free test
  ecash, active sat keyset fees `input_fee_ppk=10`) driven by
  `tests/cloud-lab/run-external-mints.sh`. `test_external_mints.py`
  pins, against the live mint: the keyset fee policy, a 1-sat token
  terminated by the #409 below-swap-fee pre-check, and a fee-deducted
  session credit. Gated on `EXTERNAL_MINTS=1`, skips with a reason when
  the mint is down (canary, not gate); the default upstream stays
  offline-only and real-money mints are never probed.
  ([#476](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/476))

- **Version-sync guard against placeholder duplication.** `check-version-sync.sh`
  now fails when `__TOLLGATE_VERSION__` appears in more than one non-comment
  line of `99-tollgate-setup`: the #459 bug was exactly such a second
  occurrence (a literal sentinel in the case pattern) being rewritten by the
  global packaging substitution. Complements the substitution-proof gate from
  #463; comment mentions stay exempt.
- **Recorded the captive-portal bundle-location decision.** The portal is
  consumed as a hash-pinned CI-built artifact rather than merged into this
  repo; the stale `portal.commit` pin and the guest-SPA-only
  `portal-build.sh` are the real defects to fix. See
  [docs/architecture/captive-portal-bundle-location-decision.md](docs/architecture/captive-portal-bundle-location-decision.md).

- **Build the full captive-portal bundle in-tree from the pinned portal pin.**
  `packaging/portal-build.sh` now stages all five portal build products into
  `packaging/files/`: the guest SPA (`tollgate-captive-portal-site`), the admin
  board SPA (`tollgate-admin/`, installed to `/www/tollgate`), the `tollgate`
  rpcd plugin and its ACL, and the `92-tollgate-admin-setup` uci-default (with
  `__ADMIN_HOME__` substituted for this build's webroot). The portal pin moves
  to `OpenTollGate/tollgate-captive-portal-site@4f74a6dd…`. This implements the
  approved bundle-location ADR above: the module builds and stages the whole
  bundle instead of a stale, hand-vendored subset. A missing source artifact at
  the pin is now a hard build error, so the bundle can no longer silently come
  from a pin that cannot produce it.

- **Go pin follows the official OpenWrt SDK feed (1.25.8 → 1.26.8).** The
  reproducibility pin moves to the golang the pinned 25.12.0 SDK's packages
  feed actually ships (`golang1.26-1.26.8-r1`), not a minor of our own
  choosing: `packaging/build-inputs.json` gains
  `.openwrt_sdk.go_per_release` — the official golang per OpenWrt release
  line (23.05 → 1.21.13, 24.10 → 1.23.12, 25.12 → 1.26.8) — audited
  against the live `openwrt/packages` branches and released feeds by the
  new `scripts/sdk-go-version.sh` (`check` fails on drift, `update`
  refreshes the map). Bumping the pin changes shipped-binary bytes once,
  as any toolchain bump does.
  ([#448](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/448))
- **CI Go versions derive from `packaging/build-inputs.json`.** Every lane
  that needs the reproducibility pin's Go (ngit `go-test`, ngit
  `repro-check`, ngit `build-package-binaries` — whose workflow-level
  `GO_VERSION` had drifted to a stale 1.25.0 after #434 closed unmerged —
  and the GitHub `build-package` lane) resolves `go-version` from the
  manifest at run time, so no lane-local literal can go stale again.
  `test.yml` deliberately keeps resolving from `src/go.mod` (the module
  minimum, unchanged). Also carries the bash-not-sh runbook note for the
  release driver scripts (dash dies on their bash arrays).
  ([#448](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/448))
- **Lane-local Go literals are refused, not just derived around.**
  `scripts/check-version-sync.sh` (pre-commit hook and release
  precondition) now fails on any literal `go-version`/`GO_VERSION` in a
  workflow — even one that matches today's manifest, since it would go
  stale on the next pin bump — and checks the 16 module `go.mod`
  directives for internal consistency. The enforcement half of the
  manifest-as-single-source design.
  ([#448](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/448))

## [v0.6.0-alpha3] - 2026-09-21

### Fixed

  [docs/architecture/uhttpd-redirect-https-ownership-decision.md](docs/architecture/uhttpd-redirect-https-ownership-decision.md)
  ([#472](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/472)).

- **Mints that recover after the tollgate booted are accepted again
  without a restart.** The wallet's accepted-mint set was frozen at the
  boot probe, so a configured mint that was unreachable at startup kept
  rejecting tokens forever — even once healthy. The health tracker now
  admits recovered mints into the running wallet
  (`WalletPort.AcceptMint`), after the same 3-probe threshold that
  governs recovery elsewhere. ([#486](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/486))

- **Full setup re-runs again on apk-based OpenWrt (25.x) upgrades.** The
  packaging's global `__TOLLGATE_VERSION__` substitution also rewrote the
  setup script's own `case` pattern, so the version gate self-matched on
  every shipped script, fell through to the opkg-only lookup, and pinned
  the marker to `unsubstituted` on apk systems (no `/usr/lib/opkg/status`)
  — after the first boot, no setup function ever ran again, silently
  dropping upgrade-time changes (e.g. #458's admin-board port allows).
  The placeholder reference is now built by string concatenation the
  substitution cannot rewrite, and the unsubstituted-run fallback also
  resolves the version from `apk list --installed` when opkg is absent.
  Fixes [#459](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/459).

- **Upgrades move the hostname with the brand, and keep custom ones.**
  The upgrade path now migrates the system hostname together with the
  whitelabel brand (`tollgate.lan` / `net4sats.lan`) instead of leaving a
  stale name behind, while a hostname the operator chose themselves is
  preserved untouched
  ([#444](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/444)).

- **A redirected config directory is now loud.** Running the CLI or
  service with `TOLLGATE_TEST_CONFIG_DIR` set — which silently redirects
  the production `/etc/tollgate` paths to a test location — prints an
  explicit warning, so a test run can no longer be mistaken for a
  production one
  ([#443](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/443)).

- **Wallet drain no longer discards tokens when a later mint fails.**
  `wallet drain cashu` drained mints one by one but aborted with a bare
  error on the first per-mint failure, silently discarding tokens already
  produced by earlier successful (and irreversible) mints — on wallets
  whose registry held two URL spellings of one mint (e.g. a trailing-slash
  variant) this could destroy real funds (#375). Mint URLs are now
  canonicalized (scheme/host case, any trailing slashes, default ports
  dropped, userinfo/query/fragment discarded) at registration and
  merged on read, so one logical mint can no longer appear as two
  phantom-balanced entries; per-mint failures are collected and reported
  as an explicit partial result (`success:false`, `partial:true`,
  `tokens`, per-mint `errors`) in both plain and JSON output; and every
  produced token is appended to an fsync'd
  `/etc/tollgate/wallet-drain-journal.jsonl` before the next mint is
  attempted, closing the crash window between the irreversible swap and
  the aggregate response.

- **UPX in the ngit release shards is pinned.** The upx legs installed
  `upx-ucl` from apt unpinned — and upx output is part of the artifact
  bytes, so those legs were only reproducible while the CI hosts' apt
  snapshots agreed. All shards now fetch the UPX pinned in
  `packaging/build-inputs.json` (5.2.1, sha256-verified) via
  `scripts/fetch-upx.sh`, matching the GitHub twin; the apk
  ultrabrute leg also provisions that binary into the SDK container,
  which it was missing entirely
  ([#474](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/474)).

### Added

- **The admin board is reachable from captive clients.** `nodogsplash`'s
  `users_to_router` now allows TCP `8090` (admin board) and `8443` (opt-in
  HTTPS) on the captive LAN, exactly like LuCI on `8080`, so the config UI
  works straight after a plain `apk`/`opkg` install
  ([#458](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/458)).

### Fixed

- **Wi-Fi scanning now addresses the radio's interfaces.** `wireless_gateway_manager`
  ran `iwinfo <radio> scan` with the uci section name (`radio0`), which is a usage
  error on modern OpenWrt where a radio's interfaces are `phy<idx>-ap<k>` — the
  scan silently returned "empty scan result" and the admin Wi-Fi page listed no
  networks. The scanner now resolves `radioN` to a scannable interface —
  preferring netifd's `ubus call network.wireless status` (and skipping radios
  that are down or disabled), falling back to the `phy<N>-ap*` mapping and
  finally the uci name ([#449](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/449)).

### Changed / Internal

- **Recorded the captive-portal bundle-location decision.** The portal is
  consumed as a hash-pinned CI-built artifact rather than merged into this
  repo; the stale `portal.commit` pin and the guest-SPA-only
  `portal-build.sh` are the real defects to fix. See
  [docs/architecture/captive-portal-bundle-location-decision.md](docs/architecture/captive-portal-bundle-location-decision.md).

- **Build the full captive-portal bundle in-tree from the pinned portal pin.**
  `packaging/portal-build.sh` now stages all five portal build products into
  `packaging/files/`: the guest SPA (`tollgate-captive-portal-site`), the admin
  board SPA (`tollgate-admin/`, installed to `/www/tollgate`), the `tollgate`
  rpcd plugin and its ACL, and the `92-tollgate-admin-setup` uci-default (with
  `__ADMIN_HOME__` substituted for this build's webroot). The portal pin moves
  to `OpenTollGate/tollgate-captive-portal-site@4f74a6dd…`. This implements the
  approved bundle-location ADR above: the module builds and stages the whole
  bundle instead of a stale, hand-vendored subset. A missing source artifact at
  the pin is now a hard build error, so the bundle can no longer silently come
  from a pin that cannot produce it.

- **Renewal-policy simulation study (#460).** `tests/sim/renewal_sim.py`
  models the upstream renewal mechanics (poll cadence, #442 clamp,
  non-blocking payment RTT, the 10 s trigger throttle, step
  quantization, cumulative allotment, stranded capital on session loss)
  and sweeps link/increment/offset/price cells; `tests/sim/RESULTS.md`
  carries the findings — the 10 s throttle caps sustained throughput at
  increment-per-10-s, the shipped #450 defaults are right for
  ≤400 Mbps uplinks, offset's real job is covering payment RTT — plus an
  operator table and the `auto`-mode spec for the fast-link quadrants.
  Anchored to the live bytes lab (`renewal_e2e.sh`, 14 PASS / 0 FAIL on
  merged main). Also: `renewal_e2e.sh` now honors the documented
  docker-compose override file (explicit `-f` flags disabled its
  auto-load — the #436 gotcha family).
  ([#465](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/465))

- **Cloud-lab isolation and docs (batched).** Cloud-lab compose runs are
  isolated per checkout, so parallel checkouts no longer share project
  state
  ([#446](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/446));
  the multi-branch lab traps (stale images, host ports) are documented
  ([#436](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/436)).

- **Keyset-rotation lane in the cloud lab.** A third cdk-mintd service
  (`mint-rotate`) with scriptable keyset rotation
  (`CDK_MINTD_FAKE_WALLET_KEYSET_ROTATIONS`), driven by
  `tests/cloud-lab/run-keyset-rotation.sh`: phase A mints proofs on a
  100-ppk keyset, phase B expires that keyset (same deterministic ID) and
  activates a zero-fee one. Discovered and pinned in the process: cdk-mintd
  0.17.6 refuses swaps on expired keysets outright, and the refusal is
  **misclassified** today — "could not swap proofs: Keyset has expired"
  matches the mint-unreachable pattern, so the customer is told the mint is
  down when it is healthy and their pre-rotation token is permanently dead
  (#447 lands the dedicated expired-keyset code, filed as #440; the lane
  pins the current
  classification so the refinement is a conscious change). The phase tests
  are gated on `ROTATION_LANE=1` (exported by the runner) so a default
  `docker compose run --rm client` no longer collects them.
  `test_keyset_rotation.py` also pins the sum-then-ceil fee boundary
  across proof counts (255/1023/2047-sat tokens).
  Depends on #409 and #413.
  ([#416](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/416))
- **Packaging artifact-contents test.** New
  `tests/packaging/assert-artifact-contents.sh` asserts a built `.ipk`/`.apk`
- **The ngit release lane stamps builds with `SOURCE_DATE_EPOCH`.** The
  publishing lane compiled with a wall-clock `BuildTime` (and Go 1.25.0
  while the #383 pin is 1.25.8 — aligned in #434), so the artifacts it
  published could never be reproduced byte-for-byte even though the local
  packaging path is reproducible. Ported onto the sharded release lane
  (#445): stage 1 derives the epoch from the
  source commit (git, else the triggering commit's timestamp, else the
  job clock — the chosen source is printed, and the value rides the
  stage-1 kind-30078 rendezvous record), stamps `BuildTime` and the
  portal from it, and each shard's resolve-inputs extracts that epoch
  from the record (same-derivation fallback for pre-epoch records) and
  exports it to the packaging jobs, so the `.ipk` mtimes agree with the
  binaries' `BuildTime` per commit. The
  `.apk` SDK-container tar stream is not yet epoch-normalized (no apk leg
  has completed under the coordinator — `.ngit/README.md` "Does the
  matrix fit?").

- **Mirror and relay lists are drift-checked.** The Blossom mirror set
  and the announce/verify relay set are carried by ~13 workflow files,
  the shard-generator template and `verify_publication.sh`'s default;
  `tests/contract/check-mirror-sync.py` (wired into the test lane next
  to the dependency-sync check) fails when any copy diverges from the
  majority, so a dying mirror or an added relay is a coordinated change
  or a red build. The GitHub twin's deliberately different mirror list
  and the coordination relays are documented exclusions.
  ([#456](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/456))


- **One Go battery, one truth.** `make go-battery` (from the repo root,
  via `scripts/go-battery.sh`) runs the full pre-PR gate — gofmt, vet,
  build, `-race -count=1 -tags testenv` tests — in **every** Go module.
  The documented `cd src && go test ./...` form covered only the root
  module of the 16-module tree and silently skipped all subpackages;
  AGENTS.md and CONTRIBUTING.md now point at the single implementation.
  ([#455](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/455))

- **Release pipeline shards stage 2, and announces only a complete
  release.** One `act` invocation is bounded by the coordinator's 1800 s
  ceiling, and the single stage-2 matrix (14 `.ipk` + 3 `.apk`) did not fit
  it: the run at `b25d8a28` timed out after announcing 5 of the 17 legs, and
  the `.apk`/SDK legs never got a slot at all. Stage 2 is now eleven workflow
  files rendered from
  [`packaging/ngit-release-matrix.json`](packaging/ngit-release-matrix.json),
  each budgeted under 1500 s and each covering part of the same matrix — no
  architecture or compression leg is dropped. A shard no longer publishes a
  kind-1063: announcements moved to
  `.ngit/act/workflows/build-package-announce.yml`, which runs
  `scripts/ngit-release-announce.sh` and refuses unless every shard of the
  same (version, channel, release run) succeeded **and** every leg has a build
  record naming that same release run — so a shard that fails or times out
  leaves the release unannounced instead of half-announced.
  `scripts/ngit-ci-release.sh` drives the ordered run. `build-portal` on the
  ngit lane was fixed in the same change: it had been red since the
  reproducible-build requirement landed, because it could not derive
  `SOURCE_DATE_EPOCH` without git history. See
  [`.ngit/README.md`](.ngit/README.md)
  ([#445](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/445)).

- **Wallet-backend documents: contract, integration decision, measurement
  protocol.** Promotes the three production-worthy documents from the
  wallet-migration research branch: `docs/architecture/walletport-contract.md`
  (what a replacement Cashu backend must satisfy),
  `docs/architecture/wallet-integration-decision.md` (in-process wallet vs CDK
  sidecar, per target tier), and
  `docs/architecture/wallet-measurement-protocol.md` (metric catalogue,
  proposed blocker thresholds, the six-fact reproducibility contract and the
  mandatory INJ-1…INJ-8 fault-injection set). The protocol document also
  records two findings from the current-wallet audit: the seed shares
  `wallet.db` with the proofs, and the documented `cdk_wallet` build command is
  a false green. Research plans, experiment sources and raw logs stay out of
  this tree (archive: `felixfelix-bot/tollgate-wallet-migration-research`).
  ([#431](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/431))


- **Release pipeline runs on Nostr CI (no GitHub dependency).** The
  `.ipk`/`.apk` → Blossom → kind-1063 release path now also runs under
  `ngit-ci`, from `.ngit/act/workflows/`, as two workflows because one
  `act` invocation is bounded by the coordinator's 30-minute job ceiling:
  `build-package-binaries.yml` (versioning, the five cross-compile
  targets, the captive portal, and the Blossom mirroring plus the
  build-id records stage 2 resolves) and the sharded stage 2 described
  above (the 14 `.ipk` and 3 `.apk` matrix, per-leg Blossom upload,
  kind-1063 announcement, the tollgate-os handoff record). The GitHub twin is
  untouched and still runs where Actions is available. `container:`
  blocks — refused with `startup_failure` on this deployment — become
  `docker run` against the same SDK image; the release signing key is
  provisioned operator-side as `NGIT_CI_SECRET_TMBG__NSEC_HEX`; the
  GitHub-only cross-repo dispatch becomes a kind-30078 handoff record
  plus a documented manual step. See [`.ngit/README.md`](.ngit/README.md)
  for the measurements, the trigger differences and the end-to-end
  verification evidence.
- **Merchant tests de-coupled from the Cashu wallet library (T16).** Token
  fixtures are centralised in `tokenfixture_test.go`, now the single place in
  the merchant tests that touches a concrete Cashu library (gonuts) — swapping
  the wallet backend is an edit to that one file plus a build-tagged sibling,
  not to every test. ([#396](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/396))

- **gonuts re-pin.** Re-pin `github.com/OpenTollGate/gonuts-tollgate`
  from the `tmp/release-integration` pseudo-version to the tagged release
  `v0.11.2` (empty-proofs guard, LoadWallet deadlock fix, hostile-token corpus).

- **`.gitignore` no longer misses the built CLI binary.** The anchoring
  done in #383 stopped the bare build-output names from shadowing source
  directories, but turned `tollgate-cli` into `src/tollgate-cli` — a path
  no build writes. The CLI module lives in `src/cmd/tollgate-cli`, and a
  binary is named after the last element of the module path, so its
  `go build .` writes `src/cmd/tollgate-cli/tollgate-cli`. That executable
  was therefore untracked *and* unignored: it showed up in `git status`
  after any local build, and `git add -A` would have committed 9.9 MB of
  binary. The entry now names the path the build actually writes; nothing
  else in the file changes.
  ([#411](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/411))

### Added

- **Process-isolated wallet sidecar + capability manifests.** `src/tollwallet`
  gains a `WalletPort` client that speaks a newline-delimited JSON RPC over an
  `AF_UNIX` socket to an out-of-process wallet daemon, so a non-Go wallet
  (e.g. CDK) can back the module without linking CGO. Per-backend capability
  manifests (`manifests/{gonuts,cdk,nucula}.json`) and a
  `wallet-policy.json` selection policy make the backend a per-target choice
  behind the unchanged `WalletPort` contract; `select.go` resolves the policy to
  a backend and `Call()` provides an explicit escape hatch for backend-specific
  methods. `SwapFeeSats` is served by a `swap_fee_sats` RPC (a daemon without
  the method answers `ok:false`, which surfaces as the error callers already
  tolerate), and the transport distinguishes safe retries from
  already-executed requests: money-moving methods (`send`, `melt`,
  `receive`, `drain`, `mint_tokens`, …) that were written but not answered
  fail with `ErrSidecarAmbiguous` instead of being re-issued — a blind
  retry could double-spend — while read-only methods reconnect and retry.
  Adds manifest/policy validation tests and a policy↔manifest
  consistency check that runs in the test matrix. ([#395](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/395))

- **Reproducible builds.** Every byte-affecting build input is now pinned
  in `packaging/build-inputs.json` and loaded through the canonical
  `packaging/build-env.sh`: exact Go (1.25.8), Node (22.17.0), npm
  (10.9.2), and UPX (5.2.1) toolchains with verified tarball hashes; the
  captive-portal source pinned to an immutable commit SHA; OpenWrt SDK
  images pinned by registry digest. `SOURCE_DATE_EPOCH` (default: the
  TollGate HEAD commit timestamp) now drives every embedded timestamp —
  `BuildTime` in the binaries, ipk archive mtimes (via `gzip -n` and
  `--mtime`), portal output files, and files staged into the apk SDK
  container. `make reproducibility-test` (and `scripts/repro-test.sh`)
  rebuilds any artifact in two independent clean roots with isolated
  caches and requires identical SHA-256s, with diffoscope diagnostics on
  mismatch. Verified byte-identical on the build host: both Go binaries,
  the portal tree, x86_64 and aarch64 ipk, a UPX-compressed ipk, and the
  x86_64 apk. CI gains a fast binary reproducibility check on every
  push plus a package check via workflow dispatch
  (`.github/workflows/repro-check.yml`); the build workflow now pins the
  Go patch release, derives `BuildTime` from the source epoch, adds
  `-buildvcs=false`, uses the portal's declared Node/npm, fetches UPX
  from the pinned manifest, and runs the apk SDK containers
  digest-pinned. See [docs/reproducible-builds.md](docs/reproducible-builds.md). ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **Hosted-runner-independent build script.**
  `scripts/shc-build-package.sh` orders a short-lived Sovereign Hybrid
  Compute VM (via the `shc` CLI), syncs the working tree, builds the
  aarch64 `.ipk` through `packaging/local-build-ipk.sh`, verifies the
  package control metadata and `--version` output of the built
  binaries, copies the artifact to `artifacts/shc/`, and always
  cancels the VM (exit trap plus reaper deadline backstop). Keeps
  package builds possible while GitHub Actions is unavailable. ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **`--version` flag on both shipped binaries.** `tollgate --version`
  (via cobra's built-in version support) and `tollgate-wrt --version`
  now print the embedded version and exit, as expected by OpenWrt
  package CI. The build now injects the CLI binary's version via
  `-X main.version` — previously the CLI build reused the service's
  `src/cli.*` ldflags, which its separate Go module never links, so
  the shipped `tollgate` binary contained no version at all. ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

### Fixed

- **Upgrades no longer abort on devices without `jq`.** The maintainer
  scripts no longer shell out to `jq`: `packaging/preinst` stops
  parsing `install.json` (the removed body ran only on upgrades and
  aborted the upgrade on jq-less devices, which then orphan-removed
  runtime dependencies), and Go now owns `install_time` end to end —
  `config_manager` writes it on install where the scripts used to
  read/patch it with `jq`. Fresh installs are untouched (they never ran
  the `jq` path). ([#407](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/407))

- **Bytes-metered upstream sessions no longer renew instantly on default
  config.** The default `bytes_renewal_offset` (131,100,000) exceeds the
  allotment actually purchasable from a typical upstream advertisement
  (5 × 22,020,096 = 110,100,480 bytes after step quantization), so every
  reseller session fired a renewal payment at near-zero usage — doubling
  the session cost on startup. The renewal check now never fires while
  more than half of the current allotment remains; a warning is logged
  (once, whenever the clamp takes effect) whenever the configured offset
  is overridden. Fixes [#430](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/430).

- **Fresh installs now ship coherent, right-sized prepay defaults for
  bytes-metered upstream sessions.** The old `bytes_renewal_offset`
  default (131,100,000 — equal to the preferred increment) exceeded any
  allotment actually purchasable after step quantization, the exact
  condition that made every bytes-metered upstream session renew at
  near-zero usage ([#430](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/430)).
  The defaults are now `preferred_session_increments_bytes`
  500,000,000 (~477 MiB prepaid per renewal) and `bytes_renewal_offset`
  125,000,000 (25% of the preferred increment — about 10 s of renewal
  runway at 100 Mbps), in both the config writer and the schema, and
  session creation warns when a persisted config's renewal offset is at
  or above the preferred increment. Existing saved configs are
  deliberately not rewritten; per-link-profile tuning is tracked in
  [#460](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/460).
  ([#450](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/450))
- **Expired-keyset payments are no longer misreported as a mint outage.**
  A token whose proofs sit on a keyset the mint has retired (NUT-02
  rotation; cdk-mintd refuses the swap with "Keyset has expired") was
  classified `payment-error-mint-unreachable` — telling the customer to
  retry or use another mint when the mint is healthy and the note is
  permanently unspendable. It now carries the dedicated
  `payment-error-keyset-expired` code with a message that says the
  e-cash note cannot be recovered, and no longer marks the healthy mint
  unreachable in the health tracker. Fixes [#440](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/440).

- **Captive portal no longer redirect-loops on nodogsplash 5.0.2.** The
  first-boot setup no longer sets (and now actively deletes)
  `nodogsplash.gatewaydomainname` — on version-changing upgrades and on
  same-version setup re-runs (postinst / boot hook) alike: with it
  configured, NDS 5.0.2 answers
  every splash request carrying a `redir` param with a redirect back to
  the splash itself, so pre-auth phone/laptop clients abort with
  `ERR_TOO_MANY_REDIRECTS` and never reach the payment page. The
  friendly `<hostname>.lan` name keeps resolving without the option —
  dnsmasq serves the system hostname in the `lan` zone. The
  whitelabel hostname is now brand-selected (`tollgate`, the default, or
  `net4sats`) from a single `/etc/tollgate/brand` file, driving the
  system hostname (and thus `tollgate.lan` / `net4sats.lan` DNS), AP
  SSIDs, and the NDS gateway name; the two brands differ only in
  naming. `tollgate ssl` no longer sets the option either and cleans
  it up on revert. ([#432](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/432), fixes [#428](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/428))



- **A runtime downgrade can recover again.** When all mints went
  unreachable under a running service, the downgrade path registered the
  onUpgrade consumer but never the tracker's first-reachable trigger that
  fires it (only the startup degraded paths did), so the service stayed
  degraded — payments returning service-unavailable-class notices — until
  manually restarted (live: 2h+ degraded with the mint probe healthy
  again). `MerchantDegraded.WireRecoveryTrigger`/`AttemptUpgrade` now wire
  the recovery cycle, used by the runtime downgrade in main
  ([#400](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/400); pair: #401/#420).

- **A payment failure no longer suppresses the degraded-mode transition.**
  `MintHealthTracker.MarkUnreachable` mutated the reachable count without
  firing the reachable-set callback, so when real traffic observed a mint
  outage before the periodic probe did, the probe path's change detection
  compared against the already-zeroed count and the downgrade never fired
  for the rest of the outage (live-reproduced in PRTA #110 Phase D). The
  callback now fires whenever a previously-reachable mint goes down
  ([#401](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/401); the companion recovery-path gap, #400, is tracked separately).

- **Captive portal no longer redirect-loops on nodogsplash 5.0.2.** The
  first-boot setup no longer sets (and now actively deletes)
  `nodogsplash.gatewaydomainname`: with it configured, NDS 5.0.2 answers
  every splash request carrying a `redir` param with a redirect back to
  the splash itself, so pre-auth phone/laptop clients abort with
  `ERR_TOO_MANY_REDIRECTS` and never reach the payment page. The
  friendly `<hostname>.lan` name keeps resolving without the option —
  dnsmasq serves the system hostname in the `lan` zone. The
  whitelabel hostname is now brand-selected (`tollgate`, the default, or
  `net4sats`) from a single `/etc/tollgate/brand` file, driving the
  system hostname (and thus `tollgate.lan` / `net4sats.lan` DNS), AP
  SSIDs, and the NDS gateway name; the two brands differ only in
  naming. `tollgate ssl` no longer sets the option either and cleans
  it up on revert. ([#432](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/432), fixes [#428](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/428))

- **Release-channel publication is now self-verifying.** A new
  `verify-publication` job runs after `publish-metadata`: every
  (architecture, format) the build matrix produced must have a kind-1063
  event on the channel relays for the published version+channel, and the
  artifacts must be servable from ≥2 mirrors with the sha256 from the `x`
  tag (sample mode: one ipk + one apk; `VERIFY_DOWNLOAD=all` for full).
  `trigger-build-os` now depends on it — OS builds fire only on verified
  publications. `BLOSSOM_MIN_SUCCESS` is raised to 3 for tag refs (stays 1
  for branch/dev builds). Converts the two known silent failure modes —
  v0.6.0-alpha1 published nowhere, and v0.5.0 mirror rot — into red builds
  ([#406](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/406)).

- **Swap fees are explained, and pre-checked.** A token whose value is
  entirely consumed by the mint's swap fee used to fail with the mint's opaque
  `no outputs provided`. The wallet now reports the fee
  (`WalletPort.SwapFeeSats`, resolving V4 short keyset IDs), the payment path
  pre-checks `amount <= fee`, and failures are coded
  `payment-error-below-swap-fee` / `payment-error-mint-unreachable` with a
  human message ([#409](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/409)).

- **Only healthy mints are advertised.** The health probe now fetches
  `/v1/keysets` and requires a non-empty NUT-01 keyset list instead of
  accepting any 2xx `/v1/info`, and `CreateAdvertisement` lists only the
  tracker's reachable set. A mint front answering `/v1/info` with an HTML
  page (observed with `mint.coinos.io`) is no longer advertised, so clients
  no longer select a mint whose swap then fails
  ([#408](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/408)).

- **Cashu wallet hardening from the gonuts bump** (pinned to the
  integration ref of gonuts #23/#24/#25, re-pinned to the tagged release
  once it exists): an empty-proofs token now fails the payment with a
  normal error instead of a contained panic (gonuts #23); mint swap
  rejections surface verbatim instead of empty `could not swap proofs:`
  errors (gonuts #25), which also makes the `ErrTokenAlreadySpent`
  sentinel actually reachable — this change broadens its match to the
  phrasings mints really use ("Token already spent" / "inputs have
  already been spent") and pins the whole chain with a two-layer test;
  a token worth less than its keyset's input fee fails fast instead of
  posting an absurd swap; and a second in-process wallet load returns a
  clear "wallet database is locked" error instead of deadlocking
  (gonuts #24).

- **nftables ruleset is packaged in full.** The OpenWrt recipe installed
  `/etc/nftables.d/` one file at a time and named only
  `20-nds-enforce.nft`, so `packaging/files/etc/nftables.d/30-backend-firewall.nft`
  never reached a built package; the inlined `.ipk` staging also copied
  `packaging/files/` by explicit path and dropped the ruleset directory, so
  the `.ipk` shipped neither ruleset file. The consequence was that the
  backend API on `:2121` (bound on all interfaces) stayed reachable from
  every non-`br-lan` interface instead of being LAN-firewall-protected. The
  recipe now glob-installs the whole ruleset directory and the `.ipk`
  staging copies its `*.nft` files, so a new `*.nft` ships without a second
  edit.
  ([#387](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/387))

- **Package license metadata corrected from `CC0-1.0` to
  `GPL-3.0-only`** in `packaging/Makefile`, `packaging/local-build-ipk.sh`,
  and the CI ipk control template, matching the repository's actual
  GPL-3.0 `LICENSE`. ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **Payments refused up front when the gate provably cannot open.**
  `PurchaseSession` consumed the customer's Cashu token (proofs swapped —
  irreversible) before attempting gate-open, so a failed `ndsctl auth` left
  the value in the operator wallet with no session and no refund path (both
  lab-reproduced triggers of [#403](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/403):
  unknown MAC, and NDS 5.0.2 exiting 1 for an already-Authenticated client).
  A new read-only `ndsctl json` probe (`valve.CheckClientState`) now refuses
  payment with an actionable `client-not-registered` notice *before*
  `Receive` — re-probing at the valve auth-retry cadence so the reseller
  flow's asynchronous NDS registration is not refused on first sight, and
  failing open on probe errors so a broken probe cannot block payments —
  while `authorizeMAC` treats an already-Authenticated client as authorized
  instead of failing the first payment after fresh daemon state.
  ([#412](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/412))

### Added

- **`--yes`/`-y` for `wallet drain cashu`.** Skips the interactive
  confirmation prompt for non-interactive callers, so automation no
  longer needs `--json` merely to bypass the prompt.

- **Meaningful exit codes.** `wallet drain cashu` now exits non-zero on
  cancellation (declined prompt or stdin at EOF) and on full or partial
  drain failure; JSON-mode commands (via `--json`) exit non-zero
  whenever the response reports `success:false` instead of exiting 0
  because serialization succeeded (#375).

- **Process-isolated wallet sidecar + capability manifests.** `src/tollwallet`
  gains a `WalletPort` client that speaks a newline-delimited JSON RPC over an
  `AF_UNIX` socket to an out-of-process wallet daemon, so a non-Go wallet
  (e.g. CDK) can back the module without linking CGO. Per-backend capability
  manifests (`manifests/{gonuts,cdk,nucula}.json`) and a
  `wallet-policy.json` selection policy make the backend a per-target choice
  behind the unchanged `WalletPort` contract; `select.go` resolves the policy to
  a backend and `Call()` provides an explicit escape hatch for backend-specific
  methods. `SwapFeeSats` is served by a `swap_fee_sats` RPC (a daemon without
  the method answers `ok:false`, which surfaces as the error callers already
  tolerate), and the transport distinguishes safe retries from
  already-executed requests: money-moving methods (`send`, `melt`,
  `receive`, `drain`, `mint_tokens`, …) that were written but not answered
  fail with `ErrSidecarAmbiguous` instead of being re-issued — a blind
  retry could double-spend — while read-only methods reconnect and retry.
  Adds manifest/policy validation tests and a policy↔manifest
  consistency check that runs in the test matrix. ([#395](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/395))

- **Reproducible builds.** Every byte-affecting build input is now pinned
  in `packaging/build-inputs.json` and loaded through the canonical
  `packaging/build-env.sh`: exact Go (1.25.8), Node (22.17.0), npm
  (10.9.2), and UPX (5.2.1) toolchains with verified tarball hashes; the
  captive-portal source pinned to an immutable commit SHA; OpenWrt SDK
  images pinned by registry digest. `SOURCE_DATE_EPOCH` (default: the
  TollGate HEAD commit timestamp) now drives every embedded timestamp —
  `BuildTime` in the binaries, ipk archive mtimes (via `gzip -n` and
  `--mtime`), portal output files, and files staged into the apk SDK
  container. `make reproducibility-test` (and `scripts/repro-test.sh`)
  rebuilds any artifact in two independent clean roots with isolated
  caches and requires identical SHA-256s, with diffoscope diagnostics on
  mismatch. Verified byte-identical on the build host: both Go binaries,
  the portal tree, x86_64 and aarch64 ipk, a UPX-compressed ipk, and the
  x86_64 apk. CI gains a fast binary reproducibility check on every
  push plus a package check via workflow dispatch
  (`.github/workflows/repro-check.yml`); the build workflow now pins the
  Go patch release, derives `BuildTime` from the source epoch, adds
  `-buildvcs=false`, uses the portal's declared Node/npm, fetches UPX
  from the pinned manifest, and runs the apk SDK containers
  digest-pinned. See [docs/reproducible-builds.md](docs/reproducible-builds.md). ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **Hosted-runner-independent build script.**
  `scripts/shc-build-package.sh` orders a short-lived Sovereign Hybrid
  Compute VM (via the `shc` CLI), syncs the working tree, builds the
  aarch64 `.ipk` through `packaging/local-build-ipk.sh`, verifies the
  package control metadata and `--version` output of the built
  binaries, copies the artifact to `artifacts/shc/`, and always
  cancels the VM (exit trap plus reaper deadline backstop). Keeps
  package builds possible while GitHub Actions is unavailable. ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **`--version` flag on both shipped binaries.** `tollgate --version`
  (via cobra's built-in version support) and `tollgate-wrt --version`
  now print the embedded version and exit, as expected by OpenWrt
  package CI. The build now injects the CLI binary's version via
  `-X main.version` — previously the CLI build reused the service's
  `src/cli.*` ldflags, which its separate Go module never links, so
  the shipped `tollgate` binary contained no version at all. ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **Whitelabel config UI on :8090 no longer degrades into a LuCI
  redirect.** The net4sats whitelabel admin UI (docroot
  `/www/net4sats`, installed by the whitelabel installer) is served by
  a dedicated `uhttpd` section on port 8090; first-boot/reinstall
  setup now re-ensures that section whenever the branded docroot is
  present, mirroring the known-good deployed layout. Repair attempts
  that instead added `:8090` to `uhttpd.main` land on LuCI's docroot
  (`/www`, whose `index.html` meta-refreshes to `cgi-bin/luci` — the
  reported ":8090 redirects to LuCI instead of the configUI" symptom);
  setup now strips such stray entries from `uhttpd.main` on every run,
  including same-version reinstalls, which previously left them in
  place. Port 8090 is deliberately not opened for pre-auth
  public-SSID clients: the config UI is owner-facing and reached over
  the private network. ([#451](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/451))

### Changed / Internal

- **Tester guide for the alpha RC.** New [docs/rc-tester-guide.md](docs/rc-tester-guide.md)
  documents the supported-matrix placeholder (honest about what is untested),
  the feed signing key and the repository line for OpenWrt 25.12, install,
  upgrade, remove and rollback — including the `/etc/apk/world` version pin a
  rollback leaves behind, and the fact that `--force-downgrade` is not an
  apk-tools 3.x option — the failure modes reproduced in practice, how to
  report a result, and what to expect from an alpha. Every command was
  executed against a real OpenWrt 25.12.5 userland; router-only steps are
  marked UNTESTED. `RELEASE-NOTES.md` no longer suggests
  `apk add --allow-untrusted`.
  ([#381](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/381))

### Changed / Internal

- **Release pipeline shards stage 2, and announces only a complete
  release.** One `act` invocation is bounded by the coordinator's 1800 s
  ceiling, and the single stage-2 matrix (14 `.ipk` + 3 `.apk`) did not fit
  it: the run at `b25d8a28` timed out after announcing 5 of the 17 legs, and
  the `.apk`/SDK legs never got a slot at all. Stage 2 is now eleven workflow
  files rendered from
  [`packaging/ngit-release-matrix.json`](packaging/ngit-release-matrix.json),
  each budgeted under 1500 s and each covering part of the same matrix — no
  architecture or compression leg is dropped. A shard no longer publishes a
  kind-1063: announcements moved to
  `.ngit/act/workflows/build-package-announce.yml`, which runs
  `scripts/ngit-release-announce.sh` and refuses unless every shard of the
  same (version, channel, release run) succeeded **and** every leg has a build
  record naming that same release run — so a shard that fails or times out
  leaves the release unannounced instead of half-announced.
  `scripts/ngit-ci-release.sh` drives the ordered run. `build-portal` on the
  ngit lane was fixed in the same change: it had been red since the
  reproducible-build requirement landed, because it could not derive
  `SOURCE_DATE_EPOCH` without git history. See
  [`.ngit/README.md`](.ngit/README.md)
  ([#445](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/445)).

- **Wallet-backend documents: contract, integration decision, measurement
  protocol.** Promotes the three production-worthy documents from the
  wallet-migration research branch: `docs/architecture/walletport-contract.md`
  (what a replacement Cashu backend must satisfy),
  `docs/architecture/wallet-integration-decision.md` (in-process wallet vs CDK
  sidecar, per target tier), and
  `docs/architecture/wallet-measurement-protocol.md` (metric catalogue,
  proposed blocker thresholds, the six-fact reproducibility contract and the
  mandatory INJ-1…INJ-8 fault-injection set). The protocol document also
  records two findings from the current-wallet audit: the seed shares
  `wallet.db` with the proofs, and the documented `cdk_wallet` build command is
  a false green. Research plans, experiment sources and raw logs stay out of
  this tree (archive: `felixfelix-bot/tollgate-wallet-migration-research`).
  ([#431](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/431))

- **Release pipeline runs on Nostr CI (no GitHub dependency).** The
  `.ipk`/`.apk` → Blossom → kind-1063 release path now also runs under
  `ngit-ci`, from `.ngit/act/workflows/`, as two workflows because one
  `act` invocation is bounded by the coordinator's 30-minute job ceiling:
  `build-package-binaries.yml` (versioning, the five cross-compile
  targets, the captive portal, and the Blossom mirroring plus the
  build-id records stage 2 resolves) and `build-package.yml` (the 14
  `.ipk` and 3 `.apk` matrix, per-artifact Blossom upload and kind-1063
  announcement, the tollgate-os handoff record). The GitHub twin is
  untouched and still runs where Actions is available. `container:`
  blocks — refused with `startup_failure` on this deployment — become
  `docker run` against the same SDK image; the release signing key is
  provisioned operator-side as `NGIT_CI_SECRET_TMBG__NSEC_HEX`; the
  GitHub-only cross-repo dispatch becomes a kind-30078 handoff record
  plus a documented manual step. See [`.ngit/README.md`](.ngit/README.md)
  for the measurements, the trigger differences and the end-to-end
  verification evidence.

- **Merchant tests de-coupled from the Cashu wallet library (T16).** The
  merchant token-flow tests no longer import the concrete wallet package; token
  fixtures are centralised in `tokenfixture_test.go`. This keeps the tests
  library-agnostic so the wallet backend can change without touching them.
  ([#396](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/396))

- **gonuts re-pin.** Re-pin `github.com/OpenTollGate/gonuts-tollgate`
  from the `tmp/release-integration` pseudo-version to the tagged release
  `v0.11.2` (empty-proofs guard, LoadWallet deadlock fix, hostile-token corpus).

- **`.gitignore` no longer misses the built CLI binary.** The anchoring
  done in #383 stopped the bare build-output names from shadowing source
  directories, but turned `tollgate-cli` into `src/tollgate-cli` — a path
  no build writes. The CLI module lives in `src/cmd/tollgate-cli`, and a
  binary is named after the last element of the module path, so its
  `go build .` writes `src/cmd/tollgate-cli/tollgate-cli`. That executable
  was therefore untracked *and* unignored: it showed up in `git status`
  after any local build, and `git add -A` would have committed 9.9 MB of
  binary. The entry now names the path the build actually writes; nothing
  else in the file changes.
  ([#411](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/411))

- **ngit mirror linked from the README.** A new "Mirror, CI and releases on
  Nostr (ngit)" section documents the mirror's `nostr://` and HTTPS clone
  URLs, the gitworkshop browser URL, the ngit-CI dashboard, and how to fetch
  build artifacts from Nostr (`kind 1063`) with sha256 verification instead
  of GitHub releases. ([#390](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/390))

- **`tar` format pinned to GNU in the SDK-free ipk lane.** The three
  `tar` invocations in `packaging/build-ipk.sh` now pass `--format=gnu`
  explicitly, matching buildroot's `ipkg-build` which pins it: the lane
  previously relied on the host tar's compile-time default format, so any
  host defaulting to pax/posix would produce opkg-readable but
  non-reproducible ipks. This was the last unpinned determinism knob on
  the ipk path. Byte-neutral on the equivalence build host (identical
  sha256 with and without the flag, aarch64 @ v0.6.0-alpha2 inputs).
  ([#405](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/405))
- **Fee-charging mint in the cloud lab.** A second cdk-mintd FakeWallet
  service (`mint-fees`) runs with `CDK_MINTD_INPUT_FEE_PPK=100`, mirroring
  real-world mints such as mint.coinos.io where a single-proof swap costs
  1 sat. New `tests/cloud-lab/test_swap_fees.py` validates end-to-end what
  the swap-fee unit tests stub: the fee is visible in the mint's keysets, a
  below-fee token is refused before the swap with
  `payment-error-below-swap-fee` and stays unspent, an above-fee payment is
  credited net of the fee, and zero-fee payments are unchanged.
  Depends on #409 for the refusal path.
  ([#413](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/413))
- **Packaging artifact-contents test.** New
  `tests/packaging/assert-artifact-contents.sh` asserts a built `.ipk`/`.apk`
  ships the runtime files under `packaging/files/`, and is wired into both
  packaging jobs in `.github/workflows/build-package.yml`. The packaged
  `etc/nftables.d/` set must equal the source set: a missing, empty, or
  truncated `*.nft` fails the build, and so does a stray extra file under
  `etc/nftables.d/` that has no source counterpart. Other pre-existing
  divergences are reported as a non-fatal warning. The `.apk` job selects the
  package artifact by name and fails loudly if it is not found, instead of
  testing whichever `.apk` happens to come first.
  ([#387](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/387))

- **Environment-variance check via `reprotest`.**
  `make reproducibility-variance` (`scripts/repro-variance.sh`) rebuilds
  the `.ipk` under hostile environment variations — umask, timezone,
  locales, file ordering — using the reproducible-builds.org `reprotest`
  engine, complementing the two-clean-roots harness: the harness proves
  independent roots agree; the variance run proves the build survives
  environments that differ from ours (the umask leak fixed in this
  release shipped precisely because nothing varied it). Test dependency
  only — installed via pip, not pinned in `build-inputs.json`. See
  [docs/reproducible-builds.md](docs/reproducible-builds.md),
  "Variance testing". ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **`.gitignore` binary patterns anchored.** The bare `tollgate-cli`
  pattern also matched the source directory `src/cmd/tollgate-cli/`,
  silently ignoring any new (untracked) files added there; patterns
  are now anchored to the repo and `src/` roots where the binaries
  actually land. ([#383](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/383))

- **Tester intake: one channel, a report template, and the stop-ship rule.**
  New [docs/tester-intake.md](docs/tester-intake.md) names the **single** intake
  channel for alpha reports — comments on one pinned issue, with no second
  place to send anything — and demands the facts that make a report
  triageable: router model, `cat /etc/openwrt_release`, `apk --print-arch`,
  the feed line used, `apk list --installed tollgate-wrt`,
  `tollgate version`, `sha256sum` of the installed binaries,
  `logread -e tollgate | tail -50`, and expected vs actual. It states the
  triage rule (a report without a package version and an architecture is
  untriaged: asked once, then closed), the severity definitions (S1 = any
  wallet/funds symptom = **stop-ship**, the index is pulled before anyone
  investigates; S2 = service broken or crash; S3 = cosmetic/docs), that every
  qualified report becomes one tracked work item tagged with severity +
  architecture, the secret-handling rules, and the honest support matrix
  (release line, which architecture was actually tested, what "best effort"
  means, and that a rollback exists). Every command in its template was
  executed in a real OpenWrt 25.12.5 userland; the two router-only behaviours
  are marked as untested. `docs/rc-tester-guide.md` §9 now names that channel
  instead of a placeholder, `RELEASE-NOTES.md` links the document, and the
  post-release step in `docs/release-process.md` names the kanban card label
  (`S1`/`S2`/`S3` + architecture) so the intake thread and the board cannot
  drift apart.
  ([#382](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/382))

## [v0.6.0-alpha2] - 2026-09-13

### Added

- **Docker-based integration test environment.** `tests/cloud-lab/`
  brings up a self-contained payment lab via docker-compose: a
  cdk-mintd FakeWallet mint, upstream and reseller TollGate containers
  (run against a fake-ndsctl shim so all payment/session logic runs
  unmodified), and a client container (cdk-cli + nak + pytest) with
  smoke-payment, mint-failure, and two-router autopay suites.
  ([#362](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/362))

- **Token recovery tool.** New offline operator tool
  (`scripts/token-recovery/`) that parses
  `/etc/tollgate/tokens-to-recover.txt`, checks each token's proof
  state at the mint via NUT-07 `/v1/checkstate`, and recovers value
  from UNSPENT proofs through the wallet. `-dry-run` reports
  recoverable/spent/pending counts without touching the wallet. Useful
  for salvaging tokens rejected by gate-side failures (e.g. the NDS
  exit-status-1 class of bugs).
  ([#354](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/354))

- **Captive-portal uhttpd instance on port 2051.** A second uhttpd
  section (`config uhttpd portal`) now serves the SPA directly on
  `0.0.0.0:2051` / `[::]:2051`, decoupling portal serving from the
  nodogsplash pre-auth path. The NDS `users_to_router` allow list
  includes port 2051 (idempotent, mirrors the existing
  2121/8080/2050 pattern).

- **Management WiFi password in setup log.** `setup_private_network()`
  now writes the generated `private_key` to `/tmp/tollgate-setup.log`
  alongside the existing SSID and IP entries.

- **Unified identity module (NIP-06 + HKDF + RevealSeed).** New
  `src/identity` module with NIP-06 12-word BIP39 mnemonic derivation,
  HKDF (RFC 5869) attribute derivation for IPv4/MAC/password (replacing
  raw SHA-256), and a loopback-only `/identity/reveal-seed` endpoint
  (1 KB body limit). 14 passing tests.
  ([#331](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/331))

- **Vendor-IE discovery + calibrated score boost.** The WGM now reads the
  vendor-specific Information Element from beacon frames to detect TollGate
  APs and calibrate how likely a discovered AP is a tollgate. Vendor IE
  encode/decode is implemented from the wire-format spec with round-trip
  tests, an encoder overflow check runs before cast, the config schema gains
  a `vendor_ie_discovery` field, and the discovered-AP score is boosted based
  on cross-platform WiFi research. Rebased from #332.
  ([#353](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/353))

### Changed

- **Setup reruns on every install and upgrade.** The setup script's version
  marker now tracks the release version — it used to be a hand-written
  literal (`v0.6.2`) that never matched a release tag — so reinstall and
  upgrade trigger a full setup rerun on already-deployed routers,
  installing the stub and portal instance alongside prior configuration.
  Existing management-WiFi credentials are preserved (see Fixed below).

- **Setup log restricted to root.** `/tmp/tollgate-setup.log`, which
  records the management-WiFi password, is now created with mode 600
  instead of the default 0644.

- **CI: `trigger-build-os` gated to the upstream repo.** The TollGate
  OS repository-dispatch requires `REPO_ACCESS_TOKEN` (upstream-only)
  and should never fire from fork branches; fork builds now skip the
  job instead of failing on the missing token.

- **CI: captive portal built once per run, not per matrix leg.** A new
  `build-portal` job compiles the portal SPA a single time and shares
  the assets to both package jobs via a `portal-assets` artifact,
  replacing the per-leg `setup-node` + `portal-build.sh` steps in
  `package-ipk` and `package-apk`. This removes ~15 redundant portal
  builds per run and fixes per-leg commit drift (each leg previously
  resolved `main` independently, so legs could package different portal
  revisions). The resolved portal commit SHA is reported in the job
  summary.
  ([#368](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/368))

### Fixed

- **Payment-goroutine panics no longer kill the process.** A panic
  inside the wallet layer during `PurchaseSession`'s `Receive` call
  (e.g. a mint returning malformed keysets) crashed the whole
  `tollgate-wrt` daemon, taking down every concurrently active session.
  The goroutine now recovers and sends an explicit
  `payment processing panicked: ...` error into the existing result
  channel, so the caller immediately gets a signed
  `payment-processing-failed` notice instead of process death or a
  misleading 30-second timeout.
  ([#360](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/360))

- **Build script no longer injects the test mint into release builds.** The
  local build script `packaging/local-build-ipk.sh` sets `cli.Version`,
  `GitCommit`, and `BuildTime` via ldflags but never set
  `config_manager.GitBranch`, leaving it `unknown`. Because `IsDevBuild()`
  treated any non-`main` branch as dev, `unknown` triggered dev mode and
  injected a test mint (with dummy invoices) into every release `.ipk`. The
  script now passes `-X ...config_manager.GitBranch=main` in `LDFLAGS`, and
  `IsDevBuild()` treats `unknown`/empty branches the same as `main`, so only
  actual non-`main` branch names enable dev mode.
  ([#359](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/359))

- **Splash stub preserves query parameters on redirect.** The
  captive-portal redirect stub now appends `location.search` to the
  `splash.html` URL so NDS-provided query params (notably
  `?clientmac=XX:XX:…`) survive the redirect to the SPA on port 2051.
  Without this, the backend's MAC-based session logic lost the real
  client MAC whenever the stub stripped the query string.
  ([#363](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/363))

- **NDS session timeout no longer overrides purchased sessions.**
  `setup_nodogsplash()` now sets `sessiontimeout='86400'` (24 h) and
  `authidletimeout='3600'` (1 h). NDS defaults to 1200 s (20 min),
  which deauthed users mid-session regardless of the Go backend's
  purchased duration. The large ceilings let the Go backend remain the
  sole authority on session lifetime.
  ([#363](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/363))

- **Accept client MAC from request body/query.** The backend now
  accepts a `mac` field in Lightning invoice requests and Cashu payment
  requests (as a query param), and a `mac` query param for invoice
  polling and `/whoami`. This allows the splash page to pass the real
  client MAC (from nodogsplash preauth redirect) instead of relying on
  IP-based ARP/DHCP lookup that fails behind the router's reverse proxy.
  The PR #6 fallback MAC (`00:00:00:00:00:00`) is kept as a safety net
  for backward compatibility.
  ([#358](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/358))

- **All HTTP handlers now handle MAC lookup failure gracefully.**
  Previously, `handleLightningInvoicePost` (POST /ln-invoice),
  `HandleRootPost` (POST /), and `handler` (/whoami) returned 400 or
  500 when `getMacAddress` failed. They now log a warning and use a
  fallback MAC (`00:00:00:00:00:00`) instead, matching the fix already
  applied to `handleLightningInvoiceGet` (GET /ln-invoice) in the
  previous commit. `HandleUsage` and `HandleBalance` were already
  non-fatal.

- **Captive portal no longer served through nodogsplash.**
  `/etc/nodogsplash/htdocs` is no longer a symlink to the SPA. A tiny
  stub page — installed as `splash.html`, the page NDS actually serves
  pre-auth — with a JS `location.replace` to port 2051 `/splash.html`
  (plus a `<noscript>` fallback link built from the LAN IP, since NDS
  redirects clients by gateway IP) is installed instead, keeping NDS
  pre-auth responses well under 1 KB. Verified on stock NDS 5.0.2:
  clients NDS cannot map to a MAC (`ip neigh` miss) receive an NDS
  error page on every request, and internal NDS errors render as
  HTTP 500 — the SPA itself now loads from uhttpd regardless.

- **No-CIDR LAN IP in the stub fallback link.** On routers where
  `network.lan.ipaddr` holds CIDR notation (`192.168.1.1/24`, legal
  UCI since OpenWrt 21.02), the stub's `<noscript>` fallback link
  embedded the raw value, producing a malformed URL
  (`http://192.168.1.1/24:2051/…`). The JS redirect path was
  unaffected (it uses `location.hostname`); the suffix is now
  stripped before the link is generated. Found during on-hardware
  validation (GL.iNet MT3000).

- **Portal install hardening: directory listings off, htdocs guard,
  anchored grep.** The portal uhttpd instance now sets `no_dirlists='1'`
  (the SPA ships no `index.html`, so `/` must not expose a file inventory
  to pre-auth clients); the stub installer removes an `htdocs` found as a
  regular file instead of failing silently; and the new port-2051
  idempotency guard greps for `port 2051$` so a hypothetical
  `port 20512` cannot false-positive.

- **Upgrade no longer rotates management-WiFi credentials.**
  `setup_private_network()` now reuses an existing private SSID/PSK
  from `wireless.private_radio0` and only generates fresh values when
  none is configured, so a version-bump-triggered setup rerun no longer
  drops every paired admin device from the management network.

- **Backend API firewall: port 2121 restricted to LAN interfaces.**
  New nftables include (`30-backend-firewall.nft`) blocks the backend
  API from non-br-lan interfaces. WiFi clients still reach the payment
  endpoint via NDS users_to_router rules. WAN-side and upstream clients
  can no longer directly probe the backend API. Defense-in-depth for
  #226 — does not change the listen address or payment flow.
- **Discovery logging: structured scan history for TollGate AP analysis.**
  Background scan cycles now log every discovered AP (BSSID, SSID, signal,
  radio, TollGate flag, price/step) to `/etc/tollgate/discovery_log.jsonl`
  (persistent across reboots; log-rotated). An in-memory registry tracks
  known TollGates across scans with signal range, sample count, and latest
  pricing. New CLI command `tollgate-cli upstream known` shows the summary.
  The `upstream scan` output now includes `is_tollgate`, `price_per_step`,
  and `step_size` fields — now populated since vendor-IE discovery landed in
  #353 (see Added below): `is_tollgate` and pricing reflect real TollGate
  APs. Foundation for Phase 2 speed probing and Phase 3 advertised pricing
  in #311.
  ([#312](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/312))

### Fixed

- **CORS: local/same-host origin echo only — wildcard removed.**
  `CorsMiddleware` no longer falls back to `Access-Control-Allow-Origin: *`;
  the origin is echoed only for local/private origins and for pages served
  by the router itself on another port (e.g. the portal on uhttpd :2051
  calling the API on :2121), with `Vary: Origin` added on echo. The backend
  API is LAN-firewall-protected rather than credential-protected, so a
  wildcard would let any website read API responses from a browser on the
  TollGate network (OWASP). POSTs with content types other than
  text/plain or application/json now return 415 instead of 400.
  ([#349](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/349))

- **Spending condition validation: reject P2PK/HTLC-locked tokens.**
  `tollwallet.Receive()` now checks each proof's secret for spending
  conditions before crediting the user. Tokens with P2PK or HTLC locks
  are rejected with `ErrLockedToken`, preventing an attacker from
  getting free internet access with tokens the gateway can never spend.
  Found during cashu-audit Layer 3 audit. Fixes #324.

- **Fund() token decode: use generic DecodeToken instead of V4-only.**
  `merchant.Fund()` called `cashu.DecodeTokenV4()` (V4-only, no V3
  fallback). Changed to `cashu.DecodeToken()` which tries V4 then V3.
  Fixes #325.

- **Mint HTTP 429 error mapping.** When a Cashu mint returns 429
  (rate limit), the error code is now `mint-rate-limited` with a
  user-friendly message instead of generic
  `payment-processing-failed`. Gonuts v0.10.0 handles retry internally
  ([#260](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/260)).

- **SSRF guard on post-payment NDS session trigger.** The new
  `triggerNdsSession()` (added for router-to-router usage tracking) now
  validates the upstream `GatewayIP` before issuing the port-80 HTTP GET,
  rejecting loopback, link-local, and unspecified addresses. Without this,
  a malicious or corrupt advertisement could coax the downstream into
  probing the local box. Also demotes the per-payment success log from
  `Info` to `Debug` (it fires on every renewal and was noisy)
  ([#88](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/88),
  [#315](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/315),
  [#347](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/347)).

- **Lightning quote persistence: data race + crash-safety fix.**
  `persistLightningQuotes` now deep-copies `lightningQuoteRecord`
  values under the `RLock` instead of sharing pointers — eliminates a
  data race where `saveQuotes` read mutable fields
  (`SessionGranted`, `Allotment`, `CompletedAt`) from shared pointers
  after the lock was released. `saveQuotes` now calls `tmp.Sync()`
  before `tmp.Close()` so the rename-atomicity guarantee holds on
  power-loss. Adds 5 concurrency tests as regression guards.

- **Cashu wallet swap-counter race (critical).** Bump `gonuts-tollgate`
  from v0.7.1 to v0.7.4 to pick up the fix for an unrecoverable
  "blinded message already signed" error (NUT-02 code 10002). In v0.7.1
  the keyset counter was incremented only after a successful swap, so
  a transient mint failure (timeout, DNS hiccup, 5xx) left the counter
  stuck — every retry reused the same counter, the mint rejected with
  10002, and the wallet bricked permanently with no self-recovery.
  v0.7.4 increments the counter before the swap call and adds a
  `swapWithRetry` path that regenerates fresh blinded messages on
  retry.

- **Mint URL fuzzy matching in `calculateAllotment()`.** The mint URL
  from Cashu tokens was compared against configured accepted mints
  using exact string equality (`==`), causing payments to fail when
  the URL differed by a trailing slash, uppercase host, or path
  normalization. `calculateAllotment()` now uses the existing
  `tollwallet.MintURLMatches()` function which tolerates these
  differences — the same function already used by the wallet layer
  during `Receive()`
  ([#250](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/250),
  [#251](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/251)).

- **HTTP response body reads now limited to 1 MB.** All `io.ReadAll`
  calls on HTTP response bodies (LNURL resolve, invoice fetch, gateway
  probes, usage tracker) now use `io.LimitReader` with a 1 MB cap,
  matching the existing limit on the main payment handler. Prevents
  OOM crashes on resource-constrained routers when a malicious or
  compromised upstream service returns an oversized response
  ([#267](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/267)).

- **Lightning quote persistence across restarts.** Lightning invoice
  quotes are now persisted to disk (`quotes.json` in the wallet
  directory) so they survive process restarts. Previously all pending
  quotes were stored in-memory only; when `tollgate-wrt` restarted
  (deploy, config change, or crash), users who had already paid saw the
  portal stuck on "Waiting for payment" because the backend returned
  `lightning quote not found`. On startup, persisted quotes are loaded,
  expired/settled ones are pruned, and monitoring goroutines are
  relaunched for unpaid quotes so access is granted if the invoice was
  settled while the process was down
  ([#248](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/248)).

- **Protocol compliance: notice event codes and tips tag.** Map
  implementation-specific notice event codes to spec-defined codes from
  TIP-01 (`session-management-failed`, `gate-open-failed`, and
  `allotment-calculation-failed` → `session-error`;
  `payment-error-token-spent` already matched). Codes with no spec
  equivalent (`payment-error-invalid-token`, `invalid-mac-address`,
  `payment-processing-timeout`, `payment-processing-failed`) are kept
  as-is with precision in the content string. Also remove non-existent
  TIP-03 and TIP-04 from the advertisement `tips` tag — only TIP-01 and
  TIP-02 are defined.

- **Wireless config missing-file guard.** `scanner.GetRadios()` and
  `connector.getRadiosFromConfig()` now return gracefully when
  `/etc/config/wireless` does not exist instead of erroring every scan
  cycle
  ([#196](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/196)).
- **Dead firewall include removed.** The `firewall-tollgate` include file
  was silently rejected by fw4 (nftables); rules now created directly via
  UCI named sections with idempotent guards
  ([#196](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/196)).
- **Makefile references to deleted firewall-tollgate.** PR #196 removed
  `files/etc/config/firewall-tollgate` but two Makefile references (install
  rule + conffiles list) were left behind, breaking all package builds
  ([#235](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/235)).
- **Upstream gateway IP validation.** Loopback, unspecified, and
  link-local addresses are now rejected in the TollGate prober to prevent
  SSRF
  ([#196](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/196)).

### Changed / Internal

- **Pre-commit actually runs in a fresh clone.** `.pre-commit-config.yaml` passed `--baseline .secrets.baseline` without that file being tracked, so the `detect-secrets` hook aborted with an invalid-path error and **every commit failed** in a fresh clone or worktree. The baseline (generated by the pinned `v1.4.0`) is committed here; it records the existing findings in test vectors and fixtures as non-secrets.

- **Nostr CI runs the documented pre-PR gate.** `.ngit/act/workflows/go-test.yml` runs `gofmt -l .`, `go vet ./...`, `go build ./...` and the race-enabled `testenv` suite from `src/` under ngit-ci, alongside the existing port of the GitHub test pipeline. GitHub Actions is unchanged. ([#379](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/379))

- **Spec-quote drift checking in CI.** greatspectations quotes are now
  verified against current cashubtc/nuts HEAD on every push (new step
  in the contract-lint job; drift fails the build). Fixed one drifted
  NUT-03 quote (spec added backticks), added a NUT-05 quote at the
  RequestMeltQuote site, and repaired two comment lines that
  accidentally parsed as malformed quote markers. Clean copy of
  Amperstrand's #357 with reviewer fixes: vacuous-pass fix in
  make/speccheck.sh (tool/config errors propagate; drift-only exits 0
  with a loud banner), NUT-05 comment moved to RequestMeltQuote,
  greatspectations invocation pinned, pip output unfiltered.
  ([#357](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/357))

- **Cross-implementation hash_to_curve vectors.** `src/tollwallet` now pins
  NUT-00 `HashToCurve` output byte-for-byte against the canonical
  cross-implementation vector set (gonuts/btcec ↔ cashu-core-lite/k256 ↔
  coincurve/Python), including the hex-looking-secret trap. Y is what
  NUT-07 checkstate keys on — divergence silently reports spent proofs as
  unspent (the bug class found twice in prta #86 review).
  ([#351](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/351))

- **Safe exec wrapper package.** New `src/sysexec/` package providing a
  testable `Runner` interface with context, timeout, structured logging,
  and retry support for `exec.Command` calls. Foundation for refactoring
  the 37 existing exec.Command call sites (#263). 13 tests, stdlib only
  ([#265](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/265)).

- **WalletPort interface + GonutsWallet adapter.** New `src/tollwallet`
  `port.go` defines the `WalletPort` interface and primitive types
  (`StatePaid`, `StateIssued`, …) abstracting away gonuts-specific types,
  with a `GonutsWallet` adapter implementing it via the existing
  gonuts-tollgate library and a build-time `cdk_wallet_stub.go` for the
  future cdk-go adapter. Foundation for decoupling the merchant from
  gonuts-tollgate; adds token-flow characterization tests.
  ([#299](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/299))

- **Supply chain: personal-fork `replace` directive dropped.** The
  `replace github.com/OpenTollGate/gonuts-tollgate => github.com/felixfelix-bot/gonuts-tollgate v0.11.1`
  directives in `src/go.mod` and `src/tollwallet/go.mod` are removed now
  that the official OpenTollGate gonuts-tollgate v0.11.1 tag is published
  (same codebase state as the fork). Both modules re-tidied against the
  official release, removing the personal-fork dependency from the
  upstream supply chain. No functional changes.
  ([#361](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/361))

- **Operator guide.** New `docs/operator-guide.md` covering every `tollgate`
  CLI subcommand (service, wallet, private network, upstream Wi-Fi, config,
  health) with example output, flags, and a troubleshooting section; README
  modules table and documentation list updated to reflect the full CLI
  surface
  ([#188](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/188)).

- **CI debt resolved: config_manager buildinfo tests + merchant in the
  go-test matrix.** The `buildinfo_test.go` expectations were stale after
  #359 added five more production mints (lnserver.com, macadamia.cash,
  westernbtc.com, kashu.me, cubabitcoin.org) and made `IsDevBuild()` treat
  `unknown`/empty branches as non-dev; tests now assert 7 production mints
  on `main`/`unknown`/empty and 8 (7 + testnut) on feature branches,
  matching the merged behavior. Separately, `src/merchant` now builds and
  tests standalone (its go.mod gained the ltcsuite/ltcd `exclude`
  directive and a full re-tidy in #361), so it is added to the go-test
  matrix. `src/cli`, `src/upstream_detector` and
  `src/upstream_session_manager` remain omitted pending the same rewrite.
  ([#365](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/365))

- **CI: `package-apk` portal build uses Node 20, not Debian Node 12.**
  The apk job installed Node via `apt-get` inside the `openwrt/sdk`
  container (Debian bullseye → Node 12.22.12), which crashed the
  captive-portal build (`vite build`) with a `module.enableCompileCache?.()`
  SyntaxError on every main push since Aug 26. Replaced with the same
  `actions/setup-node@v4` (node 20) pattern `package-ipk` already uses.
  ([#366](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/366))

- **CI: build-matrix trigger hygiene.** `push` builds are restricted to
  `main` + `v*` tags (PRs already build every branch — eliminating
  same-commit double-builds and fan-out bursts), markdown/docs-only
  changes are ignored via `paths-ignore`, and a `concurrency` group with
  `cancel-in-progress` supersedes redundant runs on the same ref. Cuts
  the workflow's 43k runner-minutes YTD waste.
  ([#369](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/369))

- **CI: apk SDK build-tree caching + job timeouts, carried onto `main`
  by the #370 follow-up.** #370 is marked merged, but its content
  never reached `main`: its base was the stacked `ci/trigger-hygiene`
  branch (#369), which landed on `main` as the squash `db8af35` 54
  seconds before #370 merged into that already-merged branch.
  `package-apk` gains an `actions/cache` step (SHA-pinned `@v5`)
  caching `/builder/dl`, `staging_dir`, and `build_dir` keyed per SDK
  target with `restore-keys` fallback, so subsequent runs skip feed
  downloads and dependency compiles. `timeout-minutes` is added to
  the four heavy jobs that ran against the 360-minute default
  (compile 30, portal 15, ipk 30, apk 90); `publish-metadata` already
  carried its 15.
  ([#370](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/370),
  [#385](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/385))

- **Release version has a single source of truth: `VERSION` at the
  repository root.** Three version literals used to disagree —
  `src/cli/version.go`'s ldflags placeholder (`v0.0.0`),
  `packaging/local-build-ipk.sh`'s `v0.7.0-alpha10`, and
  `SETUP_VERSION="v0.6.2"` in
  `packaging/files/etc/uci-defaults/99-tollgate-setup`. `VERSION` is now
  the only place the release version is written down: CI refuses a tag
  that is not byte-identical to it, the setup script ships a
  `__TOLLGATE_VERSION__` placeholder that the `.ipk` staging, the SDK
  Makefile and `local-build-ipk.sh` substitute (a copy run straight from
  a checkout falls back to the installed package version),
  `scripts/build-sdk-package.sh` derives its version from `VERSION`, and
  `src/cli/version.go` carries the non-release `dev` sentinel so a plain
  `go build` cannot pass itself off as a release.
  `scripts/check-version-sync.sh`, wired into `hooks/pre-commit`, fails
  the tree if a version literal — or a CHANGELOG section / release-notes
  title that disagrees with `VERSION` — creeps back in.

- **Release runbook and version rules documented.** New
  [docs/release-process.md](docs/release-process.md) is the maintainer
  runbook: the pre-flight gates, the exact annotated-tag commands on
  **upstream** `main` (never the fork — the trap that produced the
  orphaned `v0.7.0-alpha*` tags), the publish and verify sequence, and
  the allowed version-string shapes.
  [CONTRIBUTING.md](CONTRIBUTING.md) states the same version rules for
  contributors.

### Security

- **Exposed deployment backup purged from history.** A router
  deployment backup directory (`deploy-backup-20260730/`) containing
  the merchant private identity key, an ecash `wallet.db`, and
  spendable recovery tokens was accidentally committed in #358.
  `main` was rewritten on 2026-08-27 to remove the path from all
  commits and force-pushed; incident details and residual-exposure
  notes in `SECURITY.md`, key rotation tracked in
  [#364](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/364).

## [v0.5.0] - 2026-07-03

Everything merged into `main` since `v0.4.0` (tagged 2026-04-06),
including the `v0.5.0-alpha1` through `v0.5.0-alpha3` pre-releases.
Release notes: [RELEASE-NOTES.md](RELEASE-NOTES.md).

### Added

- **Upstream WiFi management.** New manager that detects and connects to
  upstream gateways, with a startup connectivity check, TollGate-aware probing,
  and a cross-radio DHCP nudge to recover stuck links
  ([#109](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/109),
  [#122](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/122)).
- **Mint resilience.** Per-mint health tracking, try-all-mints fallback on
  payment, and automatic recovery of mints that come back online, so a single
  failing mint no longer blocks purchases
  ([#120](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/120)),
  plus aggressive mint health-check retry on startup so a router that boots
  faster than its uplink still finds its mints.
- **SSL/HTTPS management for the captive portal**, all new in this release and
  implemented in Go, with a self-signed certificate mode, hostname setup
  (`TollGate`), and captive-portal domain configuration
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).
- **Lightning checkout and balance view** in the captive portal
  ([#107](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/107)).
- **Schema-driven configuration with a `--json` CLI.** `GetConfigSchema()` and
  dot-path get/set with validation, plus `tollgate --json config
  schema/get/set/save` (and health/wallet) commands to support admin-UI
  integration. Ships with a test workflow, schema contract lint, and a
  build-purity contract test
  ([#147](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/147)).
- **x86_64 / amd64 build target** for virtual-lab testing
  ([#80](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/80) and
  follow-ups).
- **Local OpenWrt SDK source-build helper** for reproducing package builds
  off-CI
  ([#105](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/105),
  [#79](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/79)).
- **Merchant degraded mode.** A zero-dependency `PaymentMerchant` interface
  ([#138](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/138)),
  mint health tracking with a provider and sentinel error plus USM decoupling
  ([#139](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/139)),
  and dynamic upgrade/downgrade between full and degraded operation
  ([#140](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/140)),
  surfaced through a captive-portal degraded-mode UI
  ([#141](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/141)).
- **SSL management rewritten in Go** with wrapper scripts, replacing the earlier
  shell-driven approach
  ([#142](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/142)).
- **V2 keyset ID support** for CDK 0.16.0+ compatibility
  ([#126](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/126)).

### Fixed

- **Transport reliability on OpenWrt:** force TLS 1.2 and set HTTP client
  timeouts so requests no longer hang on constrained routers
  ([#137](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/137)).
- **Security:** generate passwords with `crypto/rand` instead of time-based
  (`math/rand`) entropy
  ([#111](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/111)).
- **First-boot stability:** eliminate the reboot race, speed up `uci-defaults`,
  and unify the AP SSID
  ([#84](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/84)).
- **Install/postinst:** execute UCI defaults and reload services during
  `postinst` so a fresh install comes up correctly
  ([#90](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/90)).
- **Captive portal / HTTPS:** prevent the `uhttpd` crash loop by configuring a
  cert/key for HTTPS, keep NoDogSplash on port 80, and make the cert CN match
  the actual hostname
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).
- **Firewall:** prevent duplicate NoDogSplash firewall rules in
  `users_to_router`
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).
- **Packaging:** wrap the `.ipk` as a gzipped tar instead of an `ar` archive so
  it installs on stock OpenWrt
  ([#100](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/100)).
- **Payment correctness:** case-insensitive mint URL comparison, proper
  spent-token detection, valve re-auth without a stale in-memory cache, and
  trust `X-Forwarded-For` only from localhost, plus IP/MAC input validation and
  a 1 MB request-body cap
  ([#104](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/104)).
- **Merchant payout safety / valve timer race:** guard against `PricePerStep=0`
  division-by-zero, prevent a `uint64` underflow in payout, and stop a stale
  valve timer callback from deleting its replacement
  ([#161](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/161)).
- **Two-router autopay reliability:** retry `ndsctl auth` briefly in the valve
  so a payment's gate-open no longer fails on the first attempt when NoDogSplash
  has not yet registered the reseller client (previously failed with "failed to
  open gate" and recovered only via the token-recovery path ~60–90s later)
  ([#170](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/170)).
- **Wallet/mint registration:** register all accepted mints in the wallet at
  startup, and always open the gate for bytes (data-metered) sessions
  ([#167](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/167)).
- **Config migration:** fix the `config_version` `v0.0.7` → `v0.0.8` migration
  ([#174](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/174))
  and the `upstream_detector` `go.mod`
  ([#172](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/172))
  ([#178](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/178)).
- **BOLT11 / NoDogSplash:** make BOLT11 decode non-fatal and set the NoDogSplash
  gateway port to 2050
  ([#158](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/158)).
- **Captive-portal bypass:** disable IPv6 on the LAN during installation so
  clients cannot route around the portal
  ([#148](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/148),
  [#160](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/160)).
- **Session lifecycle:** evict expired timed sessions and start the scan loop
  ([#106](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/106)).
- **Additional security hardening and correctness guards**
  ([#163](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/163)).

### Changed / Internal

- **Default profit share:** split the 0.21 dev share across three maintainer
  identities (`c08r4d0r`, `amperstrand`, `origami74`, 0.07 each), each with its
  own Lightning address; applies to fresh default configs, existing configs are
  not rewritten
  ([#165](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/165)).
- **Release distribution:** publish redundantly to multiple relays and Blossom
  mirrors
  ([#152](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/152)),
  and list every successful Blossom mirror as a `url` tag on the NIP-94
  release events
  ([#183](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/183)).
- **CI:** split compile from package, add APK output and batched publish, native
  `.ipk` packaging with a flag-based matrix and a compression gate, and run the
  build workflow on pull requests
  ([#97](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/97),
  [#98](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/98),
  [#80](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/80)).
- Moved the `random-lan-ip` UCI default out to `tollgate-os`
  ([#96](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/96)).
- Renamed `c03rad0r` to `c08r4d0r` across the codebase
  ([#92](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/92)).
- Dead-code and docs cleanup sweep
  ([#81](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/81)).
- **CI:** replace artifact actions with Blossom + Nostr coordination
  ([#155](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/155))
  and expand the test matrix to cover standalone-buildable modules
  ([#157](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/157)).
- **CI:** skip the build/publish pipeline for fork PRs, which cannot access the
  publishing secrets
  ([#166](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/166)),
  and build an `x86_64` `.apk` variant
  ([#183](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/183)).
- **Tests:** make the root-module test hermetic via a fresh temp config dir
  (`testenv` build tag), so the suite runs off-router
  ([#169](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/169),
  [#179](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/179)).
- Add `AGENTS.md` with LLM contributor rules and tighten `.gitignore` for
  planning docs
  ([#159](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/159));
  since expanded alongside the new [CONTRIBUTING.md](CONTRIBUTING.md) and
  [PR-REVIEW.md](PR-REVIEW.md).

## [v0.4.0] - 2026-04-06

Router-to-router autopay
([#77](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/77)) and
earlier work. Not documented in this changelog.

[Unreleased]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.6.0-alpha4...main
[v0.6.0-alpha4]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.6.0-alpha1...v0.6.0-alpha4
[v0.6.0-alpha3]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.6.0-alpha1...v0.6.0-alpha3
[v0.6.0-alpha2]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.5.0...v0.6.0-alpha2
[v0.5.0]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/OpenTollGate/tollgate-module-basic-go/releases/tag/v0.4.0
