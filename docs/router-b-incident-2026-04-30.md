# Router B Incident: 2026-04-30 Stranded During Reseller Mode Test

## What Happened

Router B (beta, 100.90.216.248) became unreachable after enabling `reseller_mode=true`
and restarting the tollgate service. The router required physical recovery.

## Timeline

| Time | Event |
|------|-------|
| ~05:47 | Deployed binary with reseller fallback fix to Router B |
| ~05:48 | Set `reseller_mode: true` in `/etc/tollgate/config.json` |
| ~05:48 | Restarted `tollgate-wrt` service |
| ~05:48 | Router B NetBird went offline (100.90.216.248 unreachable) |
| ~05:50 | First SSH retry failed — "No route to host" |
| ~05:53 | Second SSH retry failed (90s wait) |
| ~05:55 | Third SSH retry failed (180s total wait) |
| ~05:55 | Router A (100.90.41.166) confirmed still reachable with internet |
| ~05:56 | Router B LAN IP (192.168.1.1) unreachable from Router A (different subnet) |
| ~06:00+ | User attempted physical recovery (uboot mode) — router did not recover |

## Root Cause Analysis

### The failure sequence

1. **Service restart** with `reseller_mode=true`. The daemon starts fresh.

2. **`EnsureRadiosEnabled()`** calls `wifi up` during startup, which triggers a
   full radio reconfiguration. This disrupts the existing c03rad0r2 STA
   connection for 30-120 seconds on MT3000 hardware.

3. **First daemon tick (30s after start)**: The daemon checks connectivity.
   The radio may still be reconfiguring from `wifi up`, so `checkConnectivity`
   (ping 9.9.9.9) fails. `lostCount` increments.

4. **Second daemon tick (60s after start)**: `lostCount` reaches threshold (2).
   Emergency scan triggers.

5. **`findResellerCandidates()`** finds two candidates:
   - `TollGate-1690` from Router A (open, -30 dBm) — TollGate candidate
   - `c03rad0r2` (disabled STA, ~-40 dBm) — fallback candidate
   - **Picks TollGate-1690** because it has the stronger signal (-30 > -40)

6. **`SwitchUpstream()`** disables c03rad0r2, enables TollGate-1690,
   `wifi reload radio0`. This tears down all radio0 interfaces.

7. **TollGate-1690 doesn't provide internet** (requires e-cash payment).
   NetBird tunnel goes down. SSH via NetBird lost.

8. **Daemon detects no internet** → lostCount reaches threshold → emergency scan
   → finds c03rad0r2 as fallback → tries to switch back.

9. **`SwitchUpstream()` back to c03rad0r2**: Another `wifi reload radio0`.
   If this succeeds, NetBird would eventually reconnect (30-120s for radio
   reconfiguration + NetBird tunnel re-establishment).

10. **BUT**: If the switch-back fails (e.g., the STA doesn't reconnect within
    the 180s timeout), the fallback restores TollGate-1690. The cycle repeats
    every ~60-90s (2 × 30s check + switch time), continuously disrupting the
    radio and preventing NetBird from ever reconnecting.

### Three contributing bugs

#### Bug 11: No startup grace period (FIXED)

The daemon starts checking connectivity 30s after service start. During this
time, `EnsureRadiosEnabled()` may have triggered a radio reconfiguration that
disrupts the existing STA. The daemon interprets this as connectivity loss and
triggers an emergency scan.

**Fix applied**: 90-second startup grace period. During grace, the daemon skips
all connectivity checks entirely, giving the radio time to fully reconfigure
after `EnsureRadiosEnabled()`. Verified on Router B: daemon waited 90s, then
started normal scanning without triggering emergency switches.

#### Bug 12: Emergency scan prefers unknown TollGate over known fallback (FIXED)

When doing an emergency switch (current SSID has no internet), the daemon picks
the strongest signal among all candidates. If a TollGate SSID has stronger signal
than the known fallback, it picks the TollGate — even though we just learned that
the current upstream has no internet and TollGates are likely in the same boat.

**Fix applied**: During emergency scans in reseller mode, TollGate SSIDs receive
a 20 dB signal penalty. A known fallback with -45 dBm will beat a TollGate at
-30 dBm (penalized to -50). TollGate still wins if it's much stronger (e.g.,
-20 dBm penalized to -40 vs fallback at -60). Unit tests verify both cases.

#### Bug 13: Repeated switch failures create a radio disruption loop (FIXED)

If SwitchUpstream fails and falls back to the same non-internet upstream, the
daemon's next cycle tries again. Each cycle disrupts the radio for 60-180s,
preventing NetBird (or any stable connection) from re-establishing. There is no
circuit breaker or maximum retry limit.

**Fix applied**: Consecutive switch failures are tracked. After 3 consecutive
failures, a 10-minute cooldown is triggered during which all scan cycles are
skipped. Failure counter resets on any successful switch. Unit tests verify
cooldown triggers at 3 failures, blocks scan cycles, and resets on success.

## What Would Have Prevented This

1. **Startup grace period** (Bug 11 fix): The daemon would have waited 60s
   before its first check, giving c03rad0r2 time to reconnect after
   `EnsureRadiosEnabled()`. The first check would have seen connectivity as OK,
   and no emergency scan would have triggered.

2. **Emergency fallback preference** (Bug 12 fix): The emergency scan would
   have preferred c03rad0r2 (known internet upstream) over TollGate-1690
   (unknown, stronger signal but likely no internet).

3. **Testing on Router A first**: Router A was the safer test target because
   we could reach Router B (100.90.216.248) via Router A as a fallback path.
   Testing on Router B first left no recovery path.

4. **Pre-deploy STA for fallback**: Before enabling reseller mode, we could
   have ensured c03rad0r2 had a very strong signal or manually set it as
   preferred.

## Lessons Learned

1. **Always have a recovery path**: Never test on the only router you can
   reach. Keep one router (Router A) as a stable relay to reach the other.

2. **Reseller mode is dangerous on startup**: The daemon's first scan cycle
   after restart can switch to a non-internet TollGate before the existing
   STA reconnects. This is especially dangerous because:
   - `EnsureRadiosEnabled()` disrupts the radio during startup
   - The 30s first-tick is too early for MT3000 hardware
   - Emergency scan can pick TollGate over known fallback

3. **Test incrementally**: We should have tested the daemon's behavior with
   `reseller_mode=true` but with NO visible TollGate SSIDs first (to verify
   the daemon stays on c03rad0r2), then introduced TollGate SSIDs.

## Recovery

Router B requires physical access to recover. Options:

1. **Serial console**: Connect via UART/serial to the MT3000, restore config
2. **uboot recovery**: Hold reset button during boot to enter failsafe mode
3. **Factory reset**: Hold reset button for 10+ seconds to reset to defaults

After recovery, restore with:
```sh
# Remove any TollGate STA sections
uci delete wireless.upstream_tollgate_1690
uci commit wireless

# Restore config
cp /etc/tollgate/config.json.bak /etc/tollgate/config.json
/etc/init.d/tollgate-wrt restart
```
