# Netbird Firewall Zone — Context for Next LLM Session

## What This Is

A feature branch (`feature/netbird-in-develop`) to add Netbird firewall zone configuration to the tollgate-wrt OpenWrt package. The goal is to allow SSH (and other traffic) from Netbird VPN peers to reach the router and LAN/private networks without manual firewall setup.

## Current State

**The implementation was completed and committed once (`f17b56e`) but then reverted** to keep this branch clean for wifi testing. All the work below has been validated (20/20 static tests pass, confirmed working on a real router). It just needs to be re-applied.

## What Needs to Happen

Re-apply the netbird changes. There are 6 files to touch:

### 1. `packaging/files/etc/uci-defaults/99-tollgate-setup` — 3 edits

**Edit A: Disable broken `setup_firewall_include()`** (pre-existing bug)

Replace the function body with a no-op. The original code creates an fw4 include pointing to `/etc/config/firewall-tollgate` (a UCI config file), but fw4's include mechanism only supports shell scripts — it throws "You cannot use UCI in firewall includes!" on every firewall restart. All firewall rules are now managed via UCI commands in this script.

```sh
setup_firewall_include() {
    return
}
```

**Edit B: Add `setup_netbird_zone()` function** after `setup_private_network()`:

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

**Edit C: Call `setup_netbird_zone` in the driver section**, right after `setup_private_network`:

```sh
setup_private_network        # consumes RANDOM_SUFFIX
setup_netbird_zone
commit_all
```

### 2. `packaging/Makefile` — comment out DEPENDS (line ~36)

Change:
```
	DEPENDS:=+nodogsplash +luci +jq
```
To:
```
	# DEPENDS:=+nodogsplash +luci +jq
	# Temporarily disabled: opkg can't resolve libc virtual dependency from feeds.
	# Runtime deps are guaranteed on all target routers. Re-enable once resolved.
	DEPENDS:=
```

This is needed because `opkg install` fails with "cannot install dependency libc" — the feeds don't provide the libc virtual package that nodogsplash/luci/jq transitively require. The dependencies are already present on all target routers.

### 3. `scripts/add-netbird-zone.sh` — new file

Standalone script for manually adding the netbird zone on routers where tollgate-wrt is already installed (can't re-run 99-tollgate-setup without losing other config). Source is at `/root/tollgate-module-basic-go/scripts/add-netbird-zone.sh`.

### 4. `docs/design-netbird-firewall-zone.md` — new file

Design document with root cause analysis of the fw4 include bug. Source is at `/root/tollgate-module-basic-go/docs/design-netbird-firewall-zone.md`.

### 5. `tests/test_firewall_config.sh` — new file

Static validation script with 20 assertions. Already adapted for the `packaging/` layout (uses `packaging/files/etc/uci-defaults/99-tollgate-setup` path). Source is at `/root/tollgate-module-basic-go/tests/test_firewall_config.sh`.

### 6. No changes needed to `packaging/files/etc/config/firewall-tollgate`

It stays at its original state (just the Allow-TollGate-In rule). No netbird entries belong there.

## Key Design Decisions

- **UCI commands in 99-tollgate-setup** instead of a separate config file — because fw4 includes don't parse UCI config files, only shell scripts
- **Dedicated `netbird` zone** (not adding wt0 to an existing zone) — clearer security boundary
- **`input 'ACCEPT'`** — authenticated VPN peers are trusted for router-local services
- **`forward 'REJECT'`** with explicit forwarding to `lan` and `private` only — no netbird→wan
- **`list device 'wt0'`** — fw4 syntax; zone is inert until wt0 interface appears (safe if Netbird isn't installed yet)
- **Idempotency guard** (`uci -q get firewall.netbird_zone`) — safe to run multiple times
- **`DEPENDS:=` emptied** — Go build system and opkg both inject libc deps that can't be resolved; runtime deps are pre-installed on all target routers

## Environment

- **OpenWrt**: 24.10.4, fw4/nftables, aarch64_cortex-a53, mediatek/filogic
- **Router SSH**: via Netbird at `100.90.216.248`
- **VPS**: `100.90.149.101`
- **SCP**: use `scp -O` (router has no sftp-server); transfers are slow over Netbird tunnel
- **Setup flag**: `/etc/tollgate-setup-done` guards the script; removing it re-runs everything (not just netbird)
- **Router has no GitHub credentials** — push from another machine

## Source Repo (completed reference implementation)

`/root/tollgate-module-basic-go` on `feature/wifiscan` branch contains the fully working netbird implementation with 4 commits:
- `12c38c8` feat: add netbird firewall zone for WireGuard tunnel access
- `6a1db64` fix: use UCI commands for netbird zone instead of broken include
- `d32f937` check if this fixes the opkg libc problem
- `5a4cbca` (merge commit)

Files to copy from source:
- `scripts/add-netbird-zone.sh` → `scripts/add-netbird-zone.sh`
- `docs/design-netbird-firewall-zone.md` → `docs/design-netbird-firewall-zone.md`
- `tests/test_firewall_config.sh` → `tests/test_firewall_config.sh` (then adjust default path from `files/...` to `packaging/files/...`)

## Stale Router State

The test router still has the stale include `firewall.tollgate_rules` from the old broken setup. Clean it up with:
```
uci delete firewall.tollgate_rules && uci commit firewall && fw4 reload
```

## Verification

After re-applying changes:
1. Run `sh tests/test_firewall_config.sh` — all 20 checks should pass
2. Build IPK and install on router via `scp -O` + `opkg install --force-reinstall`
3. Remove `/etc/tollgate-setup-done` and reboot (or run the script manually)
4. Verify: `uci show firewall | grep netbird`
5. Test SSH from VPS to router at `100.90.216.248`
