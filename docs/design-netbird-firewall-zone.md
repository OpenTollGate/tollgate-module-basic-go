# Design: Netbird Firewall Zone for tollgate-wrt

## Problem

When Netbird creates its WireGuard tunnel interface (`wt0`) on the OpenWrt router,
no firewall zone is configured for it. The result:

- The router can SSH **out** to VPS peers (established/related return traffic is
  allowed by default).
- VPS peers **cannot** SSH into the router — incoming traffic on `wt0` is dropped
  by the default `input='REJECT'` policy.

## Root Cause: Broken fw4 Include

The original implementation tried to add firewall rules via an `include` entry
pointing to `/etc/config/firewall-tollgate`. However, **fw4's include mechanism
only supports shell scripts, not UCI config files**. Attempting to include a UCI
config file produces the error:

```
You cannot use UCI in firewall includes!
Include '/etc/config/firewall-tollgate' failed with exit code 1
```

All working firewall rules (Allow-TollGate-In, private_zone, etc.) were actually
added via UCI commands in `99-tollgate-setup` — the include was always a no-op.

## Solution

Add the netbird zone via UCI commands in `99-tollgate-setup`, following the same
pattern as `setup_private_network()`. Also disable the broken include.

### Changes Made

#### 1. `files/etc/uci-defaults/99-tollgate-setup`

**`setup_firewall_include()`** — Disabled. Added comment explaining this is a
pre-existing bug fix (fw4 includes only support scripts, not UCI config). Existing
routers should clean up manually:
```
uci delete firewall.tollgate_rules && uci commit firewall && fw4 reload
```

**`setup_netbird_zone()`** — New function, called from the driver section:

```
Zone: netbird
  device: wt0
  input: ACCEPT      (allows SSH and all traffic to the router)
  output: ACCEPT
  forward: REJECT     (default deny, explicit forwarding only)

Forwardings:
  netbird → lan      (access LAN devices)
  netbird → private  (access private/c03rad0r devices)
  netbird → wan      (NOT allowed — no internet routing)
```

#### 2. `files/etc/config/firewall-tollgate`

Reverted to original content (just `Allow-TollGate-In` rule). The netbird entries
were removed because the include mechanism doesn't parse UCI config.

#### 3. `tests/test_firewall_config.sh`

Rewritten to validate `99-tollgate-setup` instead of `firewall-tollgate`:
- Checks `setup_netbird_zone` function exists with correct UCI commands
- Checks function is called in driver section
- Checks `firewall-tollgate` does NOT contain netbird entries
- Checks `setup_firewall_include` is a no-op

#### 4. `scripts/add-netbird-zone.sh`

Standalone script for existing routers that already have the setup flag set.
Run directly on the router to add the zone manually.

### Upgrade Path

- **New installations**: `99-tollgate-setup` creates the netbird zone on first boot.
  If Netbird is not yet installed, the zone is created but inert. When Netbird
  creates `wt0`, the zone activates automatically.

- **Existing routers**: Run `scripts/add-netbird-zone.sh` manually, or reinstall
  the package and delete the setup flag (`rm /etc/tollgate-setup-done`) to
  re-trigger the setup script.

- **Stale include cleanup**: On existing routers, the broken include should be
  removed manually: `uci delete firewall.tollgate_rules && uci commit firewall`

### Target Platform

- **OpenWrt >= 23.05** (fw4 / nftables). Confirmed by `CONFIG_USE_APK` in Makefile.
- fw4 supports `list device '<name>'` directly in zone definitions, so no
  separate UCI network interface stub is needed.

### Verification

```sh
# Run static validation
make -f Makefile.test test-config

# On the router after install:
uci show firewall | grep netbird
fw4 print | grep -A5 netbird
```
