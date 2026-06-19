<!--
Target: New issue on https://github.com/OpenTollGate/tollgate-module-basic-go
Title: "AP setup is non-recoverable if sections are missing on reinstall/upgrade (real fix for #103)"
Labels: bug, area: packaging
-->

## Problem

#103 reported that TollGate AP interfaces are not created when
`/etc/tollgate-setup-done` already exists. The originally-proposed fix is
**stale** — the `NetworkMonitor`-based Go recovery path it describes was
removed in PR #109 (WGM rewrite) and no longer exists on `main`.

**Current behavior:** if `/etc/tollgate-setup-done` exists and
`/etc/config/wireless` is missing `default_radio0` / `default_radio1`, there
is **no code path** — shell or Go — that recreates them. The `TollGate-*`
APs never appear until an operator manually removes the setup flag and
reboots.

This is the HIGH-risk upgrade scenario called out in the v0.5.0 release plan
(#154): UCI-defaults were rewritten in #84 / #90 / #96, so a real upgrade
landing in this state is plausible.

## Repro

```sh
# Router with TollGate APs running and setup flag present.
uci delete wireless.default_radio0
uci delete wireless.default_radio1
uci commit wireless
wifi reload
service tollgate-wrt restart      # or just reboot
# Wait 5 minutes.
uci show wireless | grep default_radio   # → empty (BUG)
wifi status | grep -i ssid              # → no TollGate SSID (BUG)
```

## Root cause

`packaging/files/etc/uci-defaults/99-tollgate-setup` does:

```sh
if [ -f "$SETUP_FLAG" ]; then
    exit 0
fi
```

…before any AP setup. The only place AP sections are created is inside
`setup_public_wifi()` → `configure_radio_ap()` after that early-exit gate. So
when the flag exists, AP setup is unreachable.

## Proposed fix: Option A (idempotent uci-defaults, recommended for v0.5.0)

Smallest possible change. The script already runs on every boot; just let it
repair the AP sections when they're missing, without re-running the
first-boot-only steps.

```sh
SETUP_FLAG="/etc/tollgate-setup-done"

if [ -f "$SETUP_FLAG" ]; then
    # Setup has run, but AP sections might be missing (reinstall, partial
    # wipe, manual `uci delete`). Repair them and bail — don't re-run the
    # one-shot steps that the flag guards.
    if ! uci -q get wireless.default_radio0 >/dev/null 2>&1 || \
       ! uci -q get wireless.default_radio1 >/dev/null 2>&1; then
        log "Setup flag exists but AP sections missing — repairing"
        RANDOM_SUFFIX=$(hexdump -n 3 -e '4/1 "%02X"' /dev/urandom | cut -c1-4)
        GATEWAY_NAME="TollGate-${RANDOM_SUFFIX}"
        configure_radio_ap 'default_radio0' 'tollgate_2g_open'
        configure_radio_ap 'default_radio1' 'tollgate_5g_open'
        uci -q get wireless.radio0 >/dev/null && uci set wireless.radio0.disabled='0'
        uci -q get wireless.radio1 >/dev/null && uci set wireless.radio1.disabled='0'
        uci commit wireless
        wifi reload
    fi
    exit 0
fi

# ...rest of first-boot flow unchanged
```

### Why not regenerate the same SSID?

The original SSID had a random suffix (`TollGate-${RANDOM_SUFFIX}`). Clients
that saved it won't auto-reconnect to a new suffix. We could persist the
suffix to a file (e.g. `/etc/tollgate/gateway-name`) and re-use it on repair;
that's a follow-up nicety, not required for v0.5.0.

### Considerations / edge cases

- STA-mode `default_radio*` sections must not be clobbered. The existing
  `configure_radio_ap()` already guards with `current_mode == "sta"` → early
  return, so this is safe.
- If only one of the two sections is missing, only that one is repaired — the
  guard checks each independently.
- `wifi reload` after `uci commit wireless` is what makes this heal on the
  next reboot without a separate service restart.

## Alternative considered: Go-side idempotent setup at service start (Option B)

A second option would add `EnsureAPInterfaces()` to the Go service entrypoint
that mirrors the shell logic. Pros: survives even if uci-defaults are
skipped. Cons: re-introduces a Go path that has to stay in sync with the
shell, and the uci-defaults path already runs on every boot. Not recommended
for v0.5.0 — revisit only if Option A turns out to be insufficient in
practice.

## Acceptance criteria

- [ ] On a router with `setup-done` flag set and `default_radio0/1` missing,
      restarting the service **or** rebooting causes the APs to reappear
      within ~60 s.
- [ ] STA-mode sections are preserved (existing regression guard).
- [ ] Hardware regression test on `physical-router-test-automation` passes
      (new method in `test_tag_readiness.py`).
- [ ] No regression in the first-boot flow (existing tag-readiness preflight
      still passes — both `default_radio0` and `default_radio1` broadcast).

## Related

- #103 — original bug report. The proposed fix there is stale; this issue
  replaces it with the actual current-architecture fix.
- #154 — release plan, "Upgrade from v0.4.0 — HIGH" risk line.
- PR #109 — WGM rewrite that removed the Go-side recovery path.
- Companion hardware test (link TBD on `physical-router-test-automation`).
