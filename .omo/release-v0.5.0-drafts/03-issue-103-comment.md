<!--
Target: Comment on https://github.com/OpenTollGate/tollgate-module-basic-go/issues/103
Title idea: "Code-review finding: bug is worse than described, proposed fix is stale"
-->

## Code-review finding (no fix yet)

I read the code against the issue body and the proposed fix is **stale** — the
architecture it references no longer exists. The bug itself is **real and
worse** than originally described.

### What the issue body proposes

The proposed fix adds an `onConnected` callback to a `NetworkMonitor` struct
in `src/wireless_gateway_manager/types.go`, fired from
`network_monitor.go:checkConnectivity()`, registered from
`wireless_gateway_manager.go:Init()`. It also assumes a Go-side
`ensureAPInterfacesExist()` recovery path in `connector.go:558` that races
with `updatePriceAndAPSSID()`'s `IsConnected()` gate.

### What actually exists on `main @ 612860a`

- `src/wireless_gateway_manager/types.go` does **not** contain `NetworkMonitor`
  at all. Current types are `STASection`, `UpstreamManagerConfig`,
  `Connector`, `Scanner`, `NetworkInfo`, `VendorElementProcessor`, `Gateway`.
- `src/wireless_gateway_manager/network_monitor.go` does **not exist**.
- There is no `networkManager`, `IsConnected`, `updatePriceAndAPSSID`, or
  `ensureAPInterfacesExist` anywhere in `src/wireless_gateway_manager/`.
- A repo-wide search for `ensureAPInterfacesExist`, `Ensuring.*AP`,
  `default_radio`, `APInterface`, `tollgate-setup-done`, `ensure.*AP`,
  `addAPInterface`, `configureAP` returns matches in **exactly one file**:
  `packaging/files/etc/uci-defaults/99-tollgate-setup`.

So the WGM rewrite (PR #109 "Upstream WiFi Manager rewrite", in v0.5.0)
removed the Go-side recovery path entirely. AP setup now lives **only** in the
`99-tollgate-setup` uci-defaults script, which still does the early-exit dance
the issue describes:

```sh
SETUP_FLAG="/etc/tollgate-setup-done"
if [ -f "$SETUP_FLAG" ]; then
    exit 0
fi
```

### Why this is worse than the original issue

The original issue said the Go-side recovery existed but raced. Now there is
**no recovery path at all**. If a router has `/etc/tollgate-setup-done` set
but `/etc/config/wireless` is missing `default_radio0` / `default_radio1`
(reinstall, partial-config wipe, manual `uci delete`, or an image that didn't
ship the AP sections), the public `TollGate-*` APs will **never appear** until
the operator manually `rm /etc/tollgate-setup-done` and reboots.

This is the "Risk: HIGH — upgrade from v0.4.0" line item in the v0.5.0 release
plan (#154). UCI-defaults were rewritten for v0.5.0 (#84, #90, #96); a
real-world upgrade landing in this state is plausible.

### Reproduction (no fix needed to repro)

On a router with tollgate-wrt installed and `TollGate-*` APs running:

```sh
uci delete wireless.default_radio0
uci delete wireless.default_radio1
uci commit wireless
wifi reload
# Confirm TollGate APs are gone.
service tollgate-wrt restart
# Wait 5 minutes.
uci show wireless | grep default_radio     # → empty
wifi status | grep -i ssid                 # → no TollGate SSID
```

Expected post-fix: APs reappear within ~60 s of `service tollgate-wrt restart`.
Current behavior: APs never reappear.

### Three options for the real fix

#### Option A (smallest, recommended for v0.5.0): make `99-tollgate-setup` idempotent on AP sections

Drop the global `exit 0` and instead skip only the steps that have already
run. Specifically, gate the AP setup on whether the sections already exist,
not on the setup flag:

```sh
SETUP_FLAG="/etc/tollgate-setup-done"

# OLD: exit 0 if flag exists.
# NEW: still exit if flag exists AND AP sections exist; otherwise fall through
#      to repair the AP sections (and only the AP sections).
if [ -f "$SETUP_FLAG" ]; then
    if uci -q get wireless.default_radio0 >/dev/null 2>&1 && \
       uci -q get wireless.default_radio1 >/dev/null 2>&1; then
        exit 0
    fi
    log "Setup flag exists but AP sections missing — repairing AP configuration"
    setup_public_wifi
    uci commit wireless
    wifi reload
    exit 0
fi

# ...rest of original first-boot flow unchanged
```

uci-defaults run on every boot, so this self-heals on the next reboot without
a service restart. **Smallest blast radius; no Go changes; survives
reflashes.**

#### Option B (medium): Go-side idempotent AP setup at service start

Add an `EnsureAPInterfaces()` call in the service entrypoint that mirrors the
shell logic. Survives even if uci-defaults are skipped. Larger blast radius;
re-introduces a Go path that needs to stay in sync with the shell.

#### Option C (largest): TollGate LuCI replacement / SPA-driven setup

Out of scope for v0.5.0. The new admin SPA (#145) could own setup entirely
in a future release.

### Hardware test plan (separately tracked)

A separate issue on `physical-router-test-automation` will cover the
repro/regression test:

1. Use `conwrt` or the tag-readiness fleet to flash a router to a known state
   with `TollGate-*` APs running.
2. Delete `default_radio0` / `default_radio1`, commit, `wifi reload`.
3. Restart `tollgate-wrt` (and separately, reboot the router).
4. Assert within a bounded time budget that:
   - `uci show wireless.default_radio0` and `default_radio1` return data.
   - `wifi status` reports the `TollGate-*` SSID broadcasting on both radios.
   - The router is still reachable on LAN (regression-guard against the
     AP-setup clobbering STA/uplink).

This test belongs in the tag-readiness `test_tag_readiness.py` scenario as a
new method, gated on a fixture that pre-wrecks the AP sections.

### Recommendation

For v0.5.0, go with **Option A**. It's a ~10-line shell change in
`99-tollgate-setup`, ships in the existing package, and the hardware
regression test guards it for every future tag. Filing a separate design
issue for the real fix so this comment doesn't have to carry the
implementation plan.

## Related

- Design issue (link TBD): AP-setup idempotency on service start.
- Release plan #154 risk line: "Upgrade from v0.4.0 — HIGH — UCI-defaults
  completely rewritten".
- WGM rewrite PR #109 removed the Go-side recovery path.
