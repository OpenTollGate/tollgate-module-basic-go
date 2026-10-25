# `packaging/` — what ships, and the decommission of the module's own package build

> Status: **PROPOSAL (draft)**. The mechanical removal below is deliberately NOT
> done in this change; it lands after the `v0.6.0-rc4` release so the release
> lane and the module's own CI stay untouched while an RC is being cut.

## The one canonical shipping package

`FreedomTechFeed/packages` → `net/tollgate-wrt/Makefile` is **the** shipping
package. It pins this repo by an immutable commit SHA (`PKG_SOURCE_VERSION`),
downloads the codeload tarball, verifies `PKG_HASH`, and builds the `.ipk`/`.apk`
release assets — this is what a router installs.

This repo's own `packaging/Makefile` builds a package from the working tree for
**test and reproducibility lanes** only. It is NOT a second shipping lane: a
tagged release of THIS repo must not publish the same package.

## What the feed consumes from this repo

The feed stages the tarball's `packaging/files/` tree (and substitutes
`__TOLLGATE_VERSION__` itself, plus the portal bundle it builds in CI). That tree
is the contract and MUST stay here:

- `packaging/files/etc/uci-defaults/99-tollgate-setup` (the setup driver)
- `packaging/files/etc/uci-defaults/90-tollgate-captive-portal-symlink`
- `packaging/files/etc/init.d/tollgate-wrt`
- `packaging/files/etc/nftables.d/*`
- `packaging/files/etc/crontabs`, `.../man`, `.../lib/upgrade/keep.d`, `.../usr/...`

## Decommission plan (after the release)

Remove the module's OWN package definition and its release scaffolding, keeping
`packaging/files/`:

1. `packaging/Makefile` — drop `Package/tollgate-wrt` (`preinst`/`postinst`/
   `install`) and `Build/*`. Keep `portal-build` and the reproducibility targets
   the test lanes use, OR move those to `packaging/`.
2. Delete `packaging/build-ipk.sh`, `packaging/local-build-ipk.sh`,
   `packaging/build-env.sh` once no CI job uses them.
3. `.github/workflows/build-package.yml` — remove the **publish** jobs (the
   Blossom/Nostr announce + asset upload). Keep a build-only lane if the test
   suites still need an artifact, or point them at the feed build.
4. Delete the now-dead `packaging/preinst` and `packaging/postinst` (the inline
   scripts in `packaging/Makefile` are the live ones; `local-build-ipk.sh` is the
   only consumer of the files).
5. `docs/release-process.md` / `CONTRIBUTING.md` — state that the feed Makefile
   is the single shipping package and this repo tags are source markers only.

Until step 3 lands, a `v*` tag on THIS repo still triggers the module's own
Blossom/Nostr publish. Cut the feed release first, then execute this plan.
