# Netbird Firewall Zone — LLM Handoff Document

## What We're Trying to Achieve

Add a firewall zone for the Netbird WireGuard tunnel (`wt0`) so that authenticated
Netbird peers (VPS servers, admin machines) can SSH into the OpenWrt router and
reach devices on both the LAN and private network. This should happen automatically
when the `tollgate-wrt` package is installed on a fresh router.

## Background: What Happened So Far

### Phase 1: Initial approach (broken)

We initially added netbird zone entries to `files/etc/config/firewall-tollgate`,
expecting fw4 to parse them via a firewall include entry (`firewall.tollgate_rules`).

**This didn't work.** fw4's include mechanism only supports shell scripts, not
UCI config files. The include produced `"You cannot use UCI in firewall includes!"`
on every firewall restart. The include had been a silent no-op since the project
began — all working firewall rules were actually added via UCI commands in
`99-tollgate-setup`.

### Phase 2: Working approach

We switched to adding the netbird zone via UCI commands in `99-tollgate-setup`,
following the exact same pattern as the existing `setup_private_network()` function.
This was tested on a real router and **confirmed working** — SSH from VPS to router
succeeded.

### Phase 3: opkg install failure

When trying to install the built IPK via `opkg install`, it failed because:
- The package declared `Depends: libc` (injected by the Go build system's
  `GO_ARCH_DEPENDS`)
- `libc` is a virtual package provided by `musl`, which opkg can't resolve from
  the remote feeds
- This caused a cascade failure where ALL dependencies became "incompatible"
- Even `--force-depends` and `--nodeps` didn't help

**Fix**: Set `GO_ARCH_DEPENDS:=` (empty) in the Makefile before the GoPackage call,
and comment out `DEPENDS`. Go binaries are statically linked and don't need libc.

### Phase 4: Porting to this branch

The work was originally done on `feature/wifiscan` but that branch is for WiFi
scanning, not netbird. The changes need to be ported to this branch
(`feature/netbird-in-develop`). This branch has a different directory structure
(everything under `packaging/`) and a different build system (prebuilt binaries
instead of in-SDK Go compilation).

## What Still Needs to Happen

The changes below need to be applied to this repo. After applying them, build the
IPK and test `opkg install` on the router to confirm it works end-to-end.

### 1. `packaging/files/etc/uci-defaults/99-tollgate-setup`

**Three changes needed:**

#### a) Disable `setup_firewall_include()` (replace lines 17-23)

Replace the existing function with a no-op and explanatory comment:

```sh
# NOTE (pre-existing bug fix, unrelated to netbird zone):
# The firewall-tollgate include was removed because fw4's include mechanism
# only supports shell scripts, not UCI config files. The previous code created
# an include pointing to /etc/config/firewall-tollgate (a UCI config file),
# which caused "You cannot use UCI in firewall includes!" on every firewall
# restart. All firewall rules are managed via UCI commands in this script
# (see setup_tollgate_firewall_rules, setup_private_network, setup_netbird_zone).
# On existing routers, clean up the stale include manually:
#   uci delete firewall.tollgate_rules && uci commit firewall && fw4 reload
setup_firewall_include() {
    return
}
```

This is a **pre-existing bug fix**, separate from the netbird zone addition.

#### b) Add `setup_netbird_zone()` function (after `setup_private_network()`)

Add this after the closing `}` of `setup_private_network()` (after the line
`uci set firewall.private_forwarding.dest='wan'` and its closing `}`):

```sh
setup_netbird_zone() {
    uci -q get firewall.netbird_zone >/dev/null 2>&1 && return
    uci set firewall.netbird_zone='zone'
    uci set firewall.netbird_zone.name='netbird'
    uci add_list firewall.netbird_zone.device='wt0'
    uci set firewall.netbird_zone.input='ACCEPT'
    uci set firewall.netbird_zone.output='ACCEPT'
    uci set firewall.netbird_zone.forward='REJECT'
    uci set firewall.netbird_lan_fwd='forwarding'
    uci set firewall.netbird_lan_fwd.src='netbird'
    uci set firewall.netbird_lan_fwd.dest='lan'
    uci set firewall.netbird_private_fwd='forwarding'
    uci set firewall.netbird_private_fwd.src='netbird'
    uci set firewall.netbird_private_fwd.dest='private'
}
```

Key details:
- `uci -q get ... && return` — idempotency guard, safe to re-run
- `list device 'wt0'` — fw4 binds zone to wt0 interface; zone is inert if wt0
  doesn't exist yet (Netbird not installed), activates when wt0 appears
- `input 'ACCEPT'` — allows SSH and all traffic to the router from authenticated
  Netbird peers (safe because only authenticated VPN peers can reach wt0)
- `forward 'REJECT'` — blocks all forwarding by default, only explicit entries below
- Forwardings: netbird→lan and netbird→private (NOT netbird→wan)

#### c) Add `setup_netbird_zone` call in driver section

In the driver section at the bottom of the file, add after `setup_private_network`
and before `commit_all`:

```sh
setup_netbird_zone
```

### 2. `packaging/Makefile` — Comment out DEPENDS (line 36)

Replace:
```makefile
	DEPENDS:=+nodogsplash +luci +jq
```

With:
```makefile
	# DEPENDS:=+nodogsplash +luci +jq
	# Temporarily disabled: opkg can't resolve libc virtual dependency from feeds.
	# Runtime deps are guaranteed on all target routers. Re-enable once resolved.
	DEPENDS:=
```

Note: This repo doesn't use `$(GO_ARCH_DEPENDS)` or `$(call GoPackage)` like the
other branch, so we only need to comment out the explicit DEPENDS. The libc issue
comes from transitive dependency resolution through `nodogsplash` → `libc`.

### 3. Copy `scripts/add-netbird-zone.sh` from source repo

```sh
cp /root/tollgate-module-basic-go/scripts/add-netbird-zone.sh scripts/
chmod +x scripts/add-netbird-zone.sh
```

This is a standalone script for manually adding the netbird zone on existing
routers that already have the setup flag set.

### 4. Copy `docs/design-netbird-firewall-zone.md` from source repo

```sh
cp /root/tollgate-module-basic-go/docs/design-netbird-firewall-zone.md docs/
```

### 5. Create `tests/test_firewall_config.sh`

This is a static validation script that reads the source files and verifies the
netbird zone is correctly configured. Copy from the source repo but **adjust the
default paths** to match this repo's `packaging/` structure:

```sh
SETUP_SCRIPT="${1:-packaging/files/etc/uci-defaults/99-tollgate-setup}"
FW_CONFIG="${2:-packaging/files/etc/config/firewall-tollgate}"
```

The test validates:
- `packaging/files/etc/config/firewall-tollgate` does NOT contain netbird entries
  (they go through UCI commands, not the config file)
- `setup_firewall_include` is a no-op
- `setup_netbird_zone` function exists with correct UCI commands
- Function is called in the driver section
- Function is idempotent (has guard)
- No netbird→wan forwarding

Run with: `sh tests/test_firewall_config.sh`

### 6. `packaging/files/etc/config/firewall-tollgate` — NO CHANGE

This file stays as-is. The netbird zone is NOT added here — it's added via UCI
commands in `99-tollgate-setup`.

## Verification

After making all changes:

1. Run `sh tests/test_firewall_config.sh` — all 20 checks should pass
2. Commit all changes
3. Build the IPK and test `opkg install` on the router
4. On the router, verify: `uci show firewall | grep netbird`
5. On the router, verify: `fw4 print | grep -A3 netbird`
6. From a VPS, verify: `ssh root@<router-netbird-ip>`

## Key Lessons Learned

1. **fw4 includes only support shell scripts**, not UCI config files
2. **UCI commands in `99-tollgate-setup`** are the correct way to add firewall rules
3. **`$(GO_ARCH_DEPENDS)` injects `libc`** dependency regardless of DEPENDS setting
4. **opkg can't resolve `libc`** virtual package from remote feeds, causing cascade
5. **`DEPENDS:=` (empty)** is needed for the IPK to be self-installable via opkg
6. **`setup_netbird_zone` must be idempotent** (guard with `uci -q get`) since
   `99-tollgate-setup` is a uci-defaults script that may need re-running
