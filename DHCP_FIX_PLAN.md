# DHCP Timeout Fix Plan

**DELETE BEFORE MERGING TO MAIN**

## Problem

When `tollgate upstream connect` switches the STA to a different radio (e.g., radio0 → radio1), the radio reload creates a new netdev (`phy0-sta0` → `phy1-sta0`) but OpenWrt's netifd doesn't re-evaluate the `wwan` interface. The DHCP client (`udhcpc`) either never starts or sends discovers that go unanswered.

L2 association succeeds (strong signal, wpa_supplicant reports `CTRL-EVENT-CONNECTED`) but L3 never comes up (no DHCP lease, no IP address).

## Root Cause

`wifi reload radioX` tears down the old STA and brings up the new one. Netifd sees the old wwan device disappear and marks `wwan` as down. When the new STA's netdev appears on a different radio, netifd doesn't automatically rebind `wwan` to it because it considers `wwan` still in a down/pending state from the old STA removal.

## Fix: Dual-Trigger Nudge in `waitForSTAIP`

Nudge netifd with `ifup wwan` to force it to rebind to the new netdev. Two triggers, whichever fires first:

### Trigger 1: Cross-Radio (Immediate)
If `activeRadio != candidateRadio`, nudge as soon as L2 association succeeds (STA netdev exists but has no IP). This handles the common case where we know upfront the transition is cross-radio.

### Trigger 2: Timer (15s Grace Period)
Regardless of radio match, if 15 seconds pass with no IP, nudge once. This handles edge cases like same-radio transitions that still confuse netifd, or cases where active radio info isn't available.

Both triggers set the same `nudged` flag — nudge fires at most once per call.

## Code Changes

### `waitForSTAIP` signature change
```go
// Before:
func (c *Connector) waitForSTAIP(radio, targetSSID string, timeout time.Duration) (string, error)

// After:
func (c *Connector) waitForSTAIP(radio, activeRadio, targetSSID string, timeout time.Duration) (string, error)
```

### Callers updated
- `SwitchUpstream` line 943: pass `activeRadio` as second arg

### Comment in code
```
// When switching STAs across different radios (e.g., radio0 → radio1),
// netifd may not re-evaluate the wwan interface after the radio reload.
// The new STA's netdev appears (phy1-sta0) but netifd still considers
// wwan bound to the old device (phy0-sta0, now torn down). As a result,
// udhcpc never starts or sends discovers that go unanswered.
//
// We nudge netifd with "ifup wwan" to force it to rebind to the new
// netdev. Two triggers:
//   1. Cross-radio: nudge immediately once L2 association succeeds
//      (detected by activeRadio != candidateRadio and STA netdev exists)
//   2. Timer: nudge after 15s grace period as a fallback for edge cases
//
// The nudge fires at most once per call (whichever trigger fires first).
```

## Test Plan

| # | Test | What it verifies |
|---|------|-----------------|
| 1 | `go vet` + `go test` (all packages) | Regression — unit tests still pass |
| 2 | `r-smoke-degraded` Alpha + Beta | Single-router degraded lifecycle |
| 3 | `r-smoke-degraded-upstream` | Critical: alpha→beta cross-radio DHCP + payment |
| 4 | Manual: `tollgate upstream connect TP-Link_97E6` | Same-radio switch regression |
| 5 | Manual: `tollgate upstream connect TollGate-D1C6` | Cross-radio switch |
| 6 | Update PR.md with results | Final documentation |

If test #3 still fails at DHCP, debug with manual workarounds (static IP, manual ifup) to understand the issue, then fix the code and re-test.
