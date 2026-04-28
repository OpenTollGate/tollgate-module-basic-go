# Design: Netbird Firewall Zone for tollgate-wrt

## Problem

When Netbird creates its WireGuard tunnel interface (`wt0`) on the OpenWrt router,
no firewall zone is configured for it. The result:

- The router can SSH **out** to VPS peers (established/related return traffic is
  allowed by default).
- VPS peers **cannot** SSH into the router — incoming traffic on `wt0` is dropped
  because the interface is not assigned to any firewall zone.

## Solution

Add a dedicated firewall zone called `netbird` that:

1. Accepts all input traffic to the router itself (SSH, HTTP, etc.)
2. Allows forwarding to both `lan` and `private` zones (so Netbird peers can
   reach devices on both networks)
3. Rejects all other forwarding by default (no `netbird → wan`)
4. Activates automatically when the `wt0` interface appears (Netbird installed)

## Target Platform

- **OpenWrt >= 23.05** (fw4 / nftables). The `CONFIG_USE_APK` check in the
  Makefile confirms this target.
- fw4 supports `list device '<name>'` directly in zone definitions, so no
  separate UCI network interface stub is needed.

## Files to Change

### 1. `files/etc/config/firewall-tollgate`

This file is included in the firewall config by the `tollgate_rules` include
entry created in `99-tollgate-setup` → `setup_firewall_include()`. fw4 reads it
as additional UCI firewall configuration.

**Current contents (DO NOT REMOVE the existing rule):**

```
config rule
	option name 'Allow-TollGate-In'
	option src 'lan'
	option proto 'tcp'
	option dest_port '2121'
	option target 'ACCEPT'
```

**Append the following blocks AFTER the existing rule:**

```
config zone
	option name 'netbird'
	list device 'wt0'
	option input 'ACCEPT'
	option output 'ACCEPT'
	option forward 'REJECT'

config forwarding
	option src 'netbird'
	option dest 'lan'

config forwarding
	option src 'netbird'
	option dest 'private'
```

**Key details:**

- `list device 'wt0'` — fw4 binds this zone to the `wt0` interface. If `wt0`
  doesn't exist yet (Netbird not installed), the zone is created but matches
  no traffic. When Netbird starts and creates `wt0`, the zone activates
  automatically. No separate UCI network interface definition is required.
- `input 'ACCEPT'` — allows all traffic destined for the router itself (SSH on
  port 22, etc.) from authenticated Netbird peers. This is safe because only
  peers authenticated via the Netbird control plane can reach this interface.
- `forward 'REJECT'` — blocks all forwarding by default. Only the explicit
  forwarding entries below are permitted.
- `forwarding src='netbird' dest='lan'` — allows Netbird peers to reach devices
  on the LAN (br-lan).
- `forwarding src='netbird' dest='private'` — allows Netbird peers to reach
  devices on the private network (br-private, the c03rad0r encrypted WiFi).
- **No** `netbird → wan` forwarding — Netbird peers should NOT route internet
  traffic through the router.

**IMPORTANT formatting notes:**

- Use **tabs** for indentation, matching the existing file and OpenWrt UCI
  convention.
- Each `config` block must be separated by a blank line.
- Do NOT add any comments to the file.
- The file must end with a trailing newline.

### 2. `tests/test_firewall_config.sh` (NEW FILE)

A static validation script that parses `files/etc/config/firewall-tollgate` and
asserts the expected structure. Runs on the **development machine** (not on the
router). No special dependencies — only grep and standard POSIX shell.

**The script must:**

1. Accept an optional argument for the config file path (default:
   `files/etc/config/firewall-tollgate` relative to repo root).
2. Implement a `parse_uci_blocks()` function that reads the file and identifies
   each `config <type> [name]` block along with its options.
3. Validate the following (exit 1 with descriptive error on any failure):

   **Existing rule (regression guard):**
   - A `config rule` block exists with:
     - `option name 'Allow-TollGate-In'`
     - `option src 'lan'`
     - `option proto 'tcp'`
     - `option dest_port '2121'`
     - `option target 'ACCEPT'`

   **Netbird zone:**
   - A `config zone` block exists with:
     - `option name 'netbird'`
     - `list device 'wt0'`
     - `option input 'ACCEPT'`
     - `option output 'ACCEPT'`
     - `option forward 'REJECT'`

   **Netbird → lan forwarding:**
   - A `config forwarding` block exists with:
     - `option src 'netbird'`
     - `option dest 'lan'`

   **Netbird → private forwarding:**
   - A `config forwarding` block exists with:
     - `option src 'netbird'`
     - `option dest 'private'`

   **No netbird → wan forwarding:**
   - No `config forwarding` block has BOTH `option src 'netbird'` AND
    `option dest 'wan'`

4. Print `[PASS]` for each check and a summary at the end.
5. Exit 0 if all checks pass, exit 1 on any failure.
6. Be executable (`chmod +x`).
7. Start with `#!/bin/sh` shebang (POSIX compatible, no bashisms).
8. Do NOT add any comments to the script.

**Script structure outline:**

```
#!/bin/sh
FILE="${1:-files/etc/config/firewall-tollgate}"
FAILURES=0

assert_option() { ... }   # check that a specific option exists in a config block
assert_no_block() { ... }  # check that a config block does NOT exist with given options
check_existing_rule() { ... }
check_netbird_zone() { ... }
check_netbird_lan_forwarding() { ... }
check_netbird_private_forwarding() { ... }
check_no_netbird_wan_forwarding() { ... }

# Run all checks
check_existing_rule
check_netbird_zone
check_netbird_lan_forwarding
check_netbird_private_forwarding
check_no_netbird_wan_forwarding

# Summary
if [ "$FAILURES" -eq 0 ]; then
    echo "All checks passed."
    exit 0
else
    echo "$FAILURES check(s) failed."
    exit 1
fi
```

**Implementation approach for parsing:**

The simplest robust approach is to use `awk` to split the file into blocks.
Each block starts with `config <type>` at the beginning of a line (no leading
whitespace). All subsequent lines starting with whitespace (tab or space)
belong to that block. A blank line or a new `config` line terminates the block.

A helper function like `get_block <type> <name>` can extract all options for a
specific config block, then `assert_option` checks individual key-value pairs
within that block.

### 3. `Makefile` — Add `test-config` target

Add a `test-config` target to the main `Makefile` that runs the validation
script. This target should work in the local development context (when `TOPDIR`
is empty, i.e. not inside the OpenWrt build system).

**Add these lines at the END of the Makefile (before the final `PKG_FINISH`
line at line 279):**

```makefile
test-config:
	@./tests/test_firewall_config.sh
```

**IMPORTANT:** This must be placed BEFORE the `$(eval $(call BuildPackage,...))`
line? No — actually it should be placed AFTER the `$(eval ...)` line and the
`PKG_FINISH` line, at the very end of the file, because OpenWrt's build system
may not handle unknown targets well if they appear inside the package definition.
Place it after line 280 (after the `PKG_FINISH` line).

Actually, looking at the Makefile structure more carefully: the `PKG_FINISH`
assignment on line 279 uses `$(shell ...)` which runs at parse time. The
`$(eval $(call BuildPackage,...))` on line 277 is what defines the package. A
plain makefile target at the end should be fine since `make test-config` won't
trigger the OpenWrt build system — it will just run the target directly.

Place the target after line 280, at the very end of the file.

## What NOT to Change

- **`files/etc/uci-defaults/99-tollgate-setup`** — No changes needed. The
  `setup_firewall_include()` function already creates the include entry that
  reads `firewall-tollgate`. The netbird zone entries in `firewall-tollgate`
  are picked up automatically by fw4.
- **`Makefile` package install section** — `firewall-tollgate` is already
  installed at line 230 and listed in FILES at line 264. No new install lines
  needed.
- **`files/lib/upgrade/keep.d/tollgate`** — `firewall-tollgate` is already
  listed. No change needed.

## Upgrade Path (for existing routers)

When the updated `tollgate-wrt` package is installed on a router that already
has the package:

1. `PKG_FLAGS:=overwrite` (line 15) ensures `firewall-tollgate` is replaced
   with the new version containing the netbird zone.
2. The postinst script (line 150) runs `/etc/init.d/firewall reload`, which
   causes fw4 to re-read all includes including `firewall-tollgate`.
3. The `netbird` zone is created immediately. If `wt0` doesn't exist yet, the
   zone is inert. When Netbird is installed later and `wt0` appears, the zone
   activates automatically.

## Verification

After implementation, run:

```sh
make test-config
```

Expected output: all checks pass, exit 0.

To verify on the router after installing the package:

```sh
uci show firewall | grep netbird
# Should show:
#   firewall.cfgXXXX=zone
#   firewall.cfgXXXX.name='netbird'
#   firewall.cfgXXXX.device='wt0'
#   etc.

fw4 print | grep -A5 "netbird"
# Should show the netbird zone in the generated nftables ruleset
```
