## Purpose

Central tracker for all known risks when upgrading a running v0.4.0 router to
v0.5.0. Derived from static analysis of `git diff v0.4.0..HEAD` across config
structs, UCI defaults, and packaging files. Each risk needs either a code fix,
a test, or a release-notes entry before stable v0.5.0 ships.

## Risk register

### R1: `config_version` not bumped despite breaking struct changes

**Severity:** CRITICAL
**Status:** Filed as separate issue (link TBD — "config_version not bumped")

`UpstreamWifi UpstreamWifiConfig` was added to the Config struct (13 fields,
all ints) but `config_version` stayed at `v0.0.7`. Migration logic is
version-gated and never fires. Old configs load with zero-value
`UpstreamWifi` → WiFi manager runs with `ScanIntervalSeconds=0`.

Also: `Relays []string` field removed. Old configs' relay data silently
dropped.

### R2: Entire `99-tollgate-setup` uci-defaults script is new

**Severity:** CRITICAL
**Status:** Needs upgrade test

v0.4.0 **did not have** `packaging/files/etc/uci-defaults/99-tollgate-setup`.
The entire file (298 lines) is new in v0.5.0. On upgrade:

- If `/etc/tollgate-setup-done` doesn't exist (it's a new flag name), the
  script runs its **full first-boot sequence** on an already-configured router.
- This clobbers: uhttpd ports, DNS, wireless AP config, hostname, nodogsplash
  config, firewall rules, IPv6 LAN setting.

**Need to verify:** does v0.4.0 create `/etc/tollgate-setup-done`? If not,
every v0.4.0 → v0.5.0 upgrade triggers the full first-boot sequence.

### R3: uhttpd port change (80 → 8080)

**Severity:** MEDIUM
**Status:** Needs release-notes entry

`99-tollgate-setup:setup_uhttpd()` moves uhttpd's HTTP listener from port 80
to port 8080:

```
uci add_list uhttpd.main.listen_http='0.0.0.0:8080'
```

**Impact:** any operator with bookmarks, monitoring, or scripts hitting
`http://<router-ip>/` on port 80 breaks after upgrade. The LuCI admin URL
changes to `http://<router-ip>:8080/`.

**Mitigation:** document in release notes; consider keeping port 80 as a
redirect to 8080 for one release cycle.

### R4: nodogsplash gatewayport change (→ 2050)

**Severity:** MEDIUM
**Status:** Needs release-notes entry

`99-tollgate-setup:setup_nodogsplash()` sets:
```
uci set nodogsplash.@nodogsplash[0].gatewayport='2050'
```

**Impact:** existing captive portal URLs that reference the old gatewayport
stop working.

### R5: IPv6 disabled on LAN

**Severity:** MEDIUM
**Status:** Needs release-notes entry + upgrade consideration

`99-tollgate-setup:setup_disable_ipv6_lan()` disables IPv6 on LAN to prevent
captive portal bypass (#148, #160):

```
uci set dhcp.lan.ra='disabled'
uci set dhcp.lan.dhcpv6='disabled'
uci set network.lan.ip6assign='0'
```

**Impact on upgrade:** clients that relied on IPv6 on the router's LAN lose
it. If the operator has IPv6-dependent services, they break.

**Consideration:** should this run on upgrade, or only on fresh install? An
operator who intentionally enabled IPv6 shouldn't have it disabled silently.

### R6: `random-lan-ip` uci-default moved to tollgate-os

**Severity:** LOW
**Status:** Needs tollgate-os coordination

The `random-lan-ip` UCI default was moved out of tollgate-module-basic-go to
tollgate-os (#96). If an operator upgrades tollgate-module but not
tollgate-os, the feature disappears.

### R7: NoDogSplash captive portal symlink is new

**Severity:** LOW
**Status:** Should be safe (idempotent)

`90-tollgate-captive-portal-symlink` creates:
```
ln -sf /etc/tollgate/tollgate-captive-portal-site /etc/nodogsplash/htdocs
```

The script checks if the symlink already exists and backs up any existing
directory. Should be safe on upgrade.

### R8: `firewall-tollgate` config is new

**Severity:** LOW
**Status:** Should be safe (additive)

New firewall rules file (`/etc/config/firewall-tollgate`) pulled in via a
named include. Additive — shouldn't break existing rules.

## Testing plan

The upgrade path needs a repeatable test using the GCP cloud lab (see
companion feature request on `physical-router-test-automation`). The test
should:

1. Install v0.4.0 on a cloud VM from a baseline snapshot.
2. Configure it with realistic settings (mints, profit-share, reseller mode).
3. Upgrade to the target v0.5.0 commit's package.
4. Reboot (triggers uci-defaults).
5. Assert each risk above is handled:
   - R1: config migrates cleanly, UpstreamWifi populated, user settings preserved.
   - R2: first-boot sequence doesn't clobber intentional configuration.
   - R3-R5: document changes in release notes; consider upgrade-aware guards.
   - R6-R8: no regression.

## Prioritization for stable v0.5.0

| Risk | Blocks stable? | Action |
|---|---|---|
| R1 (config_version) | **YES** | Fix before tag. Separate issue filed. |
| R2 (full first-boot on upgrade) | **YES** | Verify with upgrade test; add setup-flag guard if needed. |
| R3-R5 (port/IPv6 changes) | NO, but needs release notes | Document. |
| R6-R8 | NO | Verify with upgrade test. |

## Related

- Config migration bug: (link TBD)
- Release plan #154 — "Upgrade from v0.4.0 — HIGH" risk line
- Upgrade test framework: (link TBD on physical-router-test-automation)
- PRs that introduced the changes: #84, #90, #96, #109, #148, #160
