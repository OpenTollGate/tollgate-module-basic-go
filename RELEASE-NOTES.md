# TollGate v0.6.0 — Release Notes (DRAFT for the 0.6.0 tag)

> **Draft status**: consolidates everything merged through 2026-10-27,
> including the rc4 -> final train (the pre-tag blocker set from #909 is in),
> rolled up with the rc1 notes into one final document. Remove this banner
> at the cut.

TollGate v0.6.0 turns an OpenWrt router into a Cashu-powered payment
gateway for internet access, with reseller (upstream TollGate) mode, a
branded captive portal, and Nostr-based build distribution. This final
release rolls up rc1 and rc2: the fund-safety blockers, the installer
and credential hardening series, and the build/distribution integrity
work, on top of everything in the alphas.

## Fund-safety and payment correctness

- **The business-transaction record exists: an ambiguous or interrupted
  payment converges to service or to spendable value, never to a lost
  note** — the receive-intent journal replays interrupted receives and
  the owed-grant machinery converges each outcome exactly once
  ([#502](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/502)).
- **NUT-07 evidence correlates answers by value, and journal renames are
  directory-durable** — checkstate answers match the proof they are
  about, and a journaled rename survives power loss
  ([#840](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/840)).
- **Reseller-mode renewals no longer self-deadlock on the payment lock**,
  and the bootstrap's one-shot forward contract is enforced
  ([#678](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/678)).
- **An authorisation NoDogSplash holds that this module never made is
  closed by the usage sweep** (fail-closed, per-MAC, metering-safe)
  ([#619](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/619)).
- **The wallet seam is generalised to any bearer instrument** — NUT-07
  and NUT-09 derived rather than assumed, with the port contract and
  candidate mapping (incl. Fedimint findings) written up in
  `docs/architecture/`
  ([#631](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/631)).

## Installer and credential hardening (the #884 series, resolved by #913)

- **The credential model, final shape (#913, rc4): a passwordless install
  defaults root to a fixed TollGate credential instead of refusing.**
  The admin board's login checks root's `/etc/shadow` hash, and rpcd's
  session.login accepts ANY password while root's is empty — so an empty
  root password is an open admin board. rc3 refused to install in that
  state, but a failing pre-install is non-fatal under apk-tools 3 (the
  package lands anyway) and the refusal burdened every operator. The
  package now sets root to `TOLLGATE_DEFAULT_CREDENTIAL` when no
  credential exists, sets the private (management) Wi-Fi key to the same
  value, marks the admin credential **provisional**, and serves the
  board; the admin/config UI forces a change at first login (shipping
  separately, post-release). No install-time credential input is
  accepted (`TOLLGATE_ADMIN_PASSWORD` / staged files stay ignored), an
  operator's own credential is always preserved, and the write still
  fails closed if it cannot take
  ([#913](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/913),
  [#884](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/884)).
- **The package never prints generated credentials to the console** and
  generated values are marked provisional
  ([#850](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/850),
  [#897](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/897)).
- **`upstream connect` refreshes stored credentials and clears competing
  STA sections off the target radio** — reseller reconnects work after
  credential rotation
  ([#817](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/817),
  [#848](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/848)).
- **The installer's trust model is stated and enforced** — the CA bundle
  is declared and manual-install trust is explicit
  ([#894](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/888)).

## Captive portal and network behavior

- **Captive clients can resolve hostnames again** — the nodogsplash
  pre-auth allowlist covers DNS on UDP/53 as well as TCP/53
  ([#769](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/769)).
- **The discovery advertisement answers as JSON** (`GET /` on :2121)
  ([#730](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/730)).
- **The whitelabel prefix lives in one assembled brand registry** — the
  rebrand gutter is green and the whitelabel build is a first-class
  target ([#739](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/739),
  [#722](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/722)).
- **The admin-UI URL contract is documented** (`docs/access-urls.md`)
  with the canonical access paths pinned
  ([#698](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/698)).

## Build, packaging, and distribution integrity

- **The gonuts-tollgate pin is one version, everywhere, by fence** —
  `build-inputs.json` is the single truth, carriers are discovered by
  glob, and `bump-gonuts.sh` is the only supported way to move the pin
  ([#791](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/791)).
- **The build surface is a checked contract** — the two-path build
  doctrine (SDK/ipk vs SDK-free/apk) is enforced by
  `tests/contract/check-build-surface.py`
  ([#713](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/713),
  [#708](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/708)).
- **ipk package versions use Debian pre-release ordering** (`0.6.0~rc1`
  < `0.6.0`), and **bcm2709 artifact rows build again** (arch-name fix)
  ([#738](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/738),
  [#717](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/717)).
- **Both package formats ship the GPL license text**
  ([#787](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/787)).
- **The package-build surface has a recorded decommission plan (#914)**:
  `FreedomTechFeed/packages/net/tollgate-wrt/Makefile` is the single
  canonical shipping package; this repo's `packaging/Makefile` becomes
  test/reproducibility-only, with the mechanical removal (Package/Build
  targets, build helpers, publish jobs) executing after v0.6.0-rc4
  ([#914](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/914)).
- **The VM acceptance campaign** pins the x86-64 battery lane on
  ai-legion (first campaign evidence recorded)
  ([#502](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/502)).

## Internal: tests, seams, and contributor infrastructure

- Changelog entries are per-fragment files with a checked identity
  convention; duplicates in `[Unreleased]` fail the pre-commit
  ([#679](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/679),
  [#581](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/581)).
- The Receive deadline and unmeterable-report seams are per-merchant
  (no harness-writable globals)
  ([#837](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/837));
  the first-reachable and close-gate-cleanup contracts are pinned by
  tests ([#836](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/836),
  [#842](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/842)).
- The cloud-lab client image builds from scratch again (global-ARG fix)
  ([#844](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/844)).
- AGENTS.md carries the firewall/naming rules of engagement, the
  verification-venue map, and the fragment convention
  ([#800](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/800));
  CONTRIBUTING states the branch-refresh discipline
  ([#693](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/693)).
- The late-receive outcome test no longer races its own log
  ([#718](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/718));
  the duplicate-guard harness installs its own ndsctl seam
  ([#822](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/822)).

## Since rc4 — the 0.6.0 final content

Everything below landed after the rc4 notes above and is part of the 0.6.0
tag. Internal test/CI hardening is summarized at the end rather than listed
PR by PR.

### Wallet, payments, and config safety

- **The wallet now works on NOR-flash overlays.** bbolt needs a shared mmap
  the overlay cannot provide; setup mounts a small ext4 loop image at
  `/etc/tollgate/wallet.ext4`, preserves it across sysupgrade, `fsck`s it
  before every mount, and records a loud failure instead of blaming the
  mints when it cannot ([#1079](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1079),
  [#1080](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1080)).
- **Durable config writes, and an offline wallet check that cannot hang.**
  `config.json`, `identities.json` and `install.json` persist through one
  temp+fsync+rename helper; `tollgate wallet check` reports "locked by the
  running daemon" instead of waiting forever, and a corrupt DB is a verdict
  rather than a crash ([#871](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/871)).
- **The single-writer wallet invariant is enforced** at boot and on the CLI
  socket, with an atomic cross-process claim ([#932](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/932)).
- **A degraded merchant retries wallet init** on a bounded backoff and says
  why it is degraded, instead of blaming "no reachable mints" while every
  mint answers 200 ([#1078](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1078)).
- **A present-but-unlisted client can make a first purchase.** The socket-
  resolved MAC is the presence proof; the valve's bounded auth retry and the
  durable owed-grant store cover a gate that cannot open ([#865](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/865)).
- **A config with a wrong-shaped field refuses to boot** instead of silently
  resetting to the factory mint list ([#930](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/930)).
- **The wallet fork is pinned to v0.14.1**, which adds advertised-keyset-id
  verification, the empty-swap-fee refusal and pre-persisted swap intents;
  test fakes now advertise ids their keys actually derive to ([#1063](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1063)).
- **go.sum completeness, the dependency sweep, and the go-nostr carrier fix**
  keep every module buildable on its own sums ([#985](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/985),
  [#885](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/885),
  [#1120](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1120)).

### Captive portal and network behavior

- **IPv6 is disabled device-wide** (LAN RA/DHCPv6, LAN/WAN v6, the stock
  `wan6`) until captivity is v6-aware: a v6-capable WAN bypassed the portal
  for pre-auth clients ([#874](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/874)).
- **The AP rebind is existence-checked and the SSID rewrite is one-way.**
  An operator-chosen SSID and credentials survive setup; the AP is never
  bridged to a network that does not exist ([#786](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/786)).
- **The nft guards render their interfaces from the router's own config**
  instead of hardcoding the captive bridge ([#784](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/784)).
- **An operator-pinned private address survives re-setup**, and a collision
  never silently moves it ([#935](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/935)).
- **A fresh install no longer leaves `uhttpd.main` with no listeners**, and
  `entry_ui` records the mapping the deployment can actually back ([#899](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/899),
  [#933](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/933)).
- **MPTCP bonding validation** rejects CIDR uplink addresses, flushes
  endpoints on disable, and fails loudly on endpoint-add ([#978](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/978)).
- **The upstream portal trigger fires only for validated TollGates, once per
  gateway**, and every temporary workaround is now registered and fenced
  ([#795](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/795)).

### Multi-WAN objectives (new)

- **A buyer picks an objective — `cost`, `balanced` or `speed` — not weights.**
  The config contract validates the objective/eligibility/price-provenance
  model and derives weights purely from measurements ([#1123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1123),
  [#1124](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1124)).
- **An offline sampler and an mwan3 policy renderer** produce the artifacts
  from measured rate/RTT/loss; strings only, nothing applied ([#1126](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1126)).
- **The applier installs them fail-closed**: render and validate first,
  timestamped backups, no-op when current, ordered commit/restart/fragment
  reload, and `Restore` for recovery ([#1127](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1127)).
- **Honest limits:** no client assignment, no probe execution and no
  production runner wiring ship here; a single-link objective has no
  fallback by design.

### Build, packaging, and supply chain

- **The portal is pinned to 22a04f8** (admin credential setup + the vendored
  token corpus), and the pin is audited against the manifest ([#964](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/964)).
- **The SDK package build fails closed** on missing portal products, a torn
  staging copy, or an unwritable shared portal tree ([#918](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/918)).
- **The build contract owns the binary set and the arch authority**, fenced
  against hand edits ([#1119](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1119)).
- **Shipped scripts follow OpenWrt naming** (`tollgate-first-login-setup`,
  `tollgate-check-package-path`), with the shard generator as the only writer
  ([#1061](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1061)).
- **The postinst refuses success when nodogsplash is a zero-byte husk** —
  in the module and in the shipping feed recipe ([#715](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/715),
  [feed #96](https://github.com/FreedomTechFeed/packages/pull/96)).
- **Release trust is explicit**: a trust-scoped fake-release scanner with a
  live drill fixture ([#762](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/762)),
  declared signing identities instead of a built-in maintainer key ([#956](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/956)),
  one gitleaks config on both lanes ([#759](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/759)),
  an installer-contract lane on a pristine VM ([#892](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/892)),
  and a fail-closed happy-path gate before any announcement ([#924](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/924)).
- **The release fold is a fence**: a final version must have its changelog
  section, drained fragments and a notes entry ([#919](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/919)).

### Internal: tests, CI, and contributor infrastructure

The PR lane is now a local, worktree-isolated gate with a trust boundary
([#955](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/955)); a
parallel runner with portal caching cuts queue time ([#993](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/993));
count-soak, cgroup-bound battery legs, private-tmpdir lanes and the
negative-control runner make the fences themselves testable ([#905](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/905),
[#877](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/877),
[#830](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/830)).
Merchant tests drain their async work (no stray ndsctl execs), and the ngit
lane is split so no job can be silently dropped.

## Known limitations

- Restart loses in-flight session metering (sessions survive; metering
  baselines do not) — the known gap called out in rc1, unchanged.
- 24.10.8/ipk is a frozen compatibility lane (security and stop-ship
  fixes only); 25.12/apk is the feature target.
- The management bridge (`br-mgmt`) and hostmode shims remain
  experimental and are not part of this release's supported surface.

## Upgrade notes

- Upgrading from v0.5.x: **if root has no password at install time, the
  package sets a fixed default (`TollGate`) for root and the private
  Wi-Fi key, marked provisional — change it at first login** (#913). An
  operator's own existing credential is never touched, and the installer
  accepts no credential input. (rc3 briefly refused passwordless
  installs instead; rc4 replaced the refusal with the default, because
  apk-tools 3 makes pre-install failures non-fatal and the refusal
  burdened every operator.)
- Reseller nodes: reconnect with `upstream connect` after any
  credential rotation; stored credentials refresh automatically now.
- Package versions now sort correctly against future 0.6.x releases
  (tilde pre-release ordering on the ipk lane).

- **IPv6 is off device-wide.** This is the 0.6.0 mitigation for the pre-auth
  IPv6 bypass (#783): LAN RA/DHCPv6, `lan.ipv6`, `wan.ipv6` and the stock
  `wan6` are disabled on every setup pass. Re-enabling requires a v6-aware
  captive path, which is post-0.6.0 work.
- **On jffs2/squashfs NOR targets the wallet lives in an ext4 loop image**
  at `/etc/tollgate/wallet.ext4` (mounted at `/etc/tollgate/wallet`). It is
  under `/etc/tollgate`, so `keep.d` preserves it across sysupgrade — do not
  delete it, and include it in backups. A pre-existing `wallet.db` husk is
  preserved beside it, never deleted.
- **`entry_ui` on a feed-less install records `luci`** (the mapping the
  listeners back) rather than the unbacked `board`; fully-vendored installs
  still get `board`. A value the operator set is never rewritten.
- **Setup preserves an operator-chosen AP SSID and credentials**, and binds
  the AP only to an existing network; the previous unconditional rewrite is
  gone.
- **A schema-shape error refuses boot** rather than rewriting the config
  with factory mints; fix the named field or restore from
  `/etc/tollgate/config_backups`.
- **One wallet holder at a time**: a second daemon/CLI sidecar is refused at
  boot and on the socket. `/etc/config` and the wallet must not be written
  by two processes.
- **`tollgate wallet check` reports "locked" against a live daemon** (it does
  not block); stop the service or check a copy for a verdict.

## Getting v0.6.0

Published to the Nostr release channel (kind-1063 events, package
`tollgate-wrt`, channel `stable` once tagged; `beta`/`alpha` carry the
pre-releases) and mirrored across Blossom servers. Verify the sha256 of
any download against the event's `x` tag. See the README for the `nak`
fetch recipes.
