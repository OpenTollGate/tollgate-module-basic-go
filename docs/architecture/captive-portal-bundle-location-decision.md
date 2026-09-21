# Captive Portal Bundle Location — Architecture Decision

## Status: Decided (2026-09-21)

The captive-portal build bundle stays **out of** `tollgate-module-basic-go`
as *source*, and is consumed as a **hash-pinned build artifact**. The portal
repo `tollgate-captive-portal-site` is **not** merged into the module repo.

The real defect is not repo topology — it is a **stale `portal.commit` pin**
plus a **truncated `packaging/portal-build.sh`**. Fix those.

## Background

The release chain today is:

```
portal-site main ──(hand-vendored)──> FreedomTechFeed/packages
                                      net/tollgate-wrt/files/  ──> per-arch .ipk
module tarball  ─────(PKG_SOURCE_VERSION pin)───────────────────┘
```

The feed hand-vendors **five** portal-side artifacts and carries a bespoke
drift guard to keep them honest:

1. guest captive-portal SPA → `/etc/tollgate/tollgate-captive-portal-site`
2. admin board (Preact SPA) → `/www/tollgate`
3. rpcd plugin → `/usr/libexec/rpcd/tollgate` + ACL
4. `92-tollgate-admin-setup` uci-default (dedicated `:8090` uhttpd section)

Meanwhile `packaging/build-inputs.json` pins
`portal.commit = 86ac5fc` (**2026-09-11**) — a commit authored *before* the
portal repo grew its `admin/`, `openwrt/rpcd/` and `packaging/` trees. The
pinned tarball therefore **cannot regenerate the bundle it is supposed to
source**: "derive the assets from the pin" would ship files that do not
exist at that SHA.

Evidence of the cost of this arrangement: **6+ re-vendor commits in 5 days**
(`523a6f34`, `397e7ae8`, `20645a7f`, `fe770e45`, `94e8d2de`, `1f6ac1f0`,
`e86630e0`), including one **shipped regression** — a stale copy shipped the
pre10 admin regression — which is why the drift guard exists at all.

## Why not merge the repos

1. **Cadence coupling.** The portal repo is fast-moving (4+ merges/day on
   2026-09-21 alone: PRs #45–#49); the module is a 57 MB repo with a
   reproducible-build gate and immutable-SHA invariants. Merging couples a
   high-churn frontend tree to a release-critical, byte-audited backend.

2. **It relocates, not solves, the commit-vs-build question.** The portal
   maintainers deliberately **stopped committing minified JS** (upstream
   issue #335). Merged or not, the built `assets/` cannot be committed — so
   you still build in CI and still ship an artifact. The pin dies; the
   question does not.

3. **Blast radius.** A merge restructures exactly the files the release
   depends on: `packaging/build-inputs.json`, the feed `Makefile` install
   recipe, the vendored bundle dir, and the drift guard. Doing it in parallel
   with an active release **invalidates the release tag before its gate
   runs**.

4. **Permissions.** The automation bot is `push:false` on both upstream
   repos. A repo merge/transfer is an org-level action for the maintainers,
   not a bot-side refactor.

## Decided path (Option B — build in CI, ship an artifact)

1. **Repin** `packaging/build-inputs.json` `portal.commit` to a portal SHA
   that actually owns `admin/`, `openwrt/rpcd/` and `packaging/`.
2. **Extend** `packaging/portal-build.sh` to emit *all five* artifacts
   (guest SPA + admin SPA + rpcd plugin + ACL + `92`-setup), not just the
   guest SPA.
3. **Publish** the built bundle as a **hash-pinned release artifact** from
   the module's own CI. Build-in-CI-with-artifact — **never**
   commit-the-bundle (respects #335).
4. **Consume** that artifact in the feed by pinned URL + hash, exactly as
   the feed already consumes the module tarball, instead of hand-vendoring
   files into `net/tollgate-wrt/files/`.
5. **Delete** the feed's vendoring exception and drift guard **last**, only
   after the CI-built bundle is **byte-proven** against the vendored copy.

## Sequencing

After the current release. Land it as a **dedicated pre14-or-later effort**
with its own tag cycle — never in parallel with a release that is being
built or tested. The interim state (feed vendoring + drift guard + the
`vendor.lock.json` / `check-vendor-drift.sh` work merged in feed PR #20)
is safe *because* the lock covers both bundle directories.

## Consequences

- The feed stops carrying 5+ hand-vendored portal files; a single pinned
  artifact URL + hash replaces them.
- `portal.commit` becomes load-bearing again (it must point at a SHA whose
  tree can build the bundle) — worth an explicit CI assertion.
- The module CI gains a Node/npm step before the OpenWrt SDK step. This is
  structurally fine: the feed's `release-publish.yml` build job is
  `runs-on: ubuntu-latest` and only *steps into* `openwrt/gh-action-sdk@v11`;
  upstream `build-package.yml` already does a pre-SDK `setup-node@v4` +
  `portal-build.sh` today, so "the SDK cannot run npm" is **not** a
  constraint.
- Until step 5 lands, the drift guard stays — it is guardrail, not legacy.
