<!--
Target: New issue on https://github.com/OpenTollGate/physical-router-test-automation
Title: "bug(deploy): scripts/deploy.sh references missing LuCI file — packaging dead reference"
Labels: bug, deploy
-->

## Problem

`scripts/deploy.sh` in this repo references a file that does not exist in
`upstream tollgate-module-basic-go`:

```sh
install -D -m 0644 packaging/files/www/luci-static/resources/view/tollgate-payments/settings.js "$PAYLOAD/www/luci-static/resources/view/tollgate-payments/settings.js"
```

Verified on `tollgate-module-basic-go@main` (`612860a`, 2026-06-19):

```sh
$ ls packaging/files/www/luci-static/resources/view/tollgate-payments/settings.js
ls: packaging/files/www/luci-static/resources/view/tollgate-payments/settings.js: No such file or directory

$ find packaging/files/www -type f 2>&1 | head
# (no output — the entire www/ tree is gone)
```

The LuCI admin UI assets were removed from `tollgate-module-basic-go` (the
project is migrating to a SPA in `tollgate-captive-portal-site` — see #145).
`scripts/deploy.sh` still tries to `install` the old LuCI `settings.js`,
which fails the deploy.

This is the "dead LuCI files" item in the v0.5.0 tag-readiness report
(tollgate-module-basic-go#169).

## Repro

```sh
./scripts/deploy.sh <some-commit-sha> <router-ip> root aarch64_cortex-a53
```

Expected: payload tree builds. Actual: `install` fails on the missing file
(unless `install -D` happens to no-op silently, depending on coreutils
version — on macOS / BSD `install` will not).

## Fix

Drop the line that references the missing LuCI file. Likely also drop any
other `packaging/files/www/...` references if there are no remaining LuCI
assets in the upstream repo. Suggested diff:

```diff
 install -D -m 0755 packaging/files/etc/hotplug.d/iface/95-tollgate-restart "$PAYLOAD/etc/hotplug.d/iface/95-tollgate-restart"
-install -D -m 0644 packaging/files/www/luci-static/resources/view/tollgate-payments/settings.js "$PAYLOAD/www/luci-static/resources/view/tollgate-payments/settings.js"
+# LuCI assets removed; admin UI is migrating to the captive-portal-site SPA (#145).
```

While in there, also worth confirming:
- The binary paths (`bin/tollgate-wrt`, `bin/tollgate-cli`) match the
  current upstream Makefile / build layout. The tag-readiness report's
  "binary paths" caveat suggests these may also need updating.
- The `90-tollgate-captive-portal-symlink` uci-default still exists upstream
  (it does, verified on `612860a`).

## Acceptance criteria

- [ ] `./scripts/deploy.sh <sha> <router-ip> root <arch>` builds a payload
      tree without error on stock `main`.
- [ ] The resulting payload installs cleanly via `opkg install` on a
      GL-MT3000 running OpenWrt 24.10.4.
- [ ] No dead references to `packaging/files/www/...` remain in the script.
- [ ] Tier 0 of the tag-readiness suite (`make tag-readiness-static`) is
      unaffected.

## Related

- Tag-readiness report (`tollgate-module-basic-go#169`) — "dead LuCI files"
  caveat.
- `tollgate-module-basic-go#145` — frontend migration to the captive-portal
  SPA (the reason the LuCI assets were removed).
- `tollgate-module-basic-go#116` — security issue around sensitive values in
  the old LuCI admin (becomes moot once the SPA migration lands).
