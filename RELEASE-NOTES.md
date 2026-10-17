# TollGate v0.6.0 — Release Notes (DRAFT for the 0.6.0 tag)

> **Draft status**: consolidates everything merged through `v0.6.0-rc4`
> (fragments + `[Unreleased]` + the rc3/rc4 commits), rolled up with the
> rc1 notes into one final document. Entries for the pre-tag blocker set
> (issue #909) join this document when those PRs merge. Remove this banner
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

## Getting v0.6.0

Published to the Nostr release channel (kind-1063 events, package
`tollgate-wrt`, channel `stable` once tagged; `beta`/`alpha` carry the
pre-releases) and mirrored across Blossom servers. Verify the sha256 of
any download against the event's `x` tag. See the README for the `nak`
fetch recipes.
