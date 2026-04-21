## fix: execute UCI defaults in postinst and preserve STA connections on upgrade

### Summary

- **postinst now applies full UCI configuration without a reboot.** The old postinst only restarted `tollgate-wrt` and manually managed the NoDogSplash symlink. UCI defaults scripts (`90-tollgate-captive-portal-symlink`, `95-random-lan-ip`, `99-tollgate-setup`) were never executed during `opkg install`, so wireless, network, firewall, and DNS settings were only applied on first boot via `/etc/init.d/boot`. Installing on an already-running router left it unconfigured until a reboot.
- **`network restart` replaces `network reload` with bridge-readiness polling.** `network reload` does not create new bridge devices (e.g. `br-private`) — it only re-applies config to existing interfaces. A full `network restart` tears down and recreates the stack. A `wait_for_iface` loop polls `/sys/class/net` for each bridge (up to 15 s) before running `wifi reload`, since wifi-ifaces that reference a non-existent bridge will silently fail to attach.
- **`99-tollgate-setup` preserves active STA connections on upgrade.** Three-layer defense: (1) a self-guard flag (`/etc/tollgate-setup-done`) prevents re-execution after first successful run; (2) `configure_radio_ap()` skips any `default_radioX` interface that is currently `mode=sta`, preserving the upstream connection; (3) `mode=ap` and `network=lan` are set explicitly so the interface can never be left in an indeterminate state.
- **`tollgate-wrt` uses `stop` + `start` instead of `restart` on first install.** On a fresh install the service was never running, so `stop` is a safe no-op and `start` ensures a clean first launch.

### Network blip during install

`/etc/init.d/network restart` tears down and rebuilds the entire network stack, which briefly drops the active STA upstream connection — even on the happy path where the STA is preserved post-restart. Operators upgrading live routers should expect a short connectivity blip (typically 2-5 s) rather than a seamless swap. The STA reconnects automatically via `wifi reload`.

### Changes

| File | Description |
|---|---|
| `Makefile` (postinst) | Run UCI defaults in order; `network restart` + `wait_for_iface` bridges before `wifi reload`; `stop`+`start` for tollgate-wrt; log hint when `br-private` times out |
| `files/etc/uci-defaults/99-tollgate-setup` | Add self-guard flag; extract `configure_radio_ap()` that skips STA interfaces; set `mode=ap` and `network=lan` explicitly; create flag on success |
| `tests/docs/throughput-measurement.md` | Removed — stale working notes, not documentation |

### Tested on

- Hardware: GL.iNet GL-MT3000 (MT7981, Filogic 830)
- OpenWrt: snapshot with mt76 wireless drivers
- Install path: `opkg install` on a running router (no reboot required)
- Upgrade path: `opkg install` over an existing installation with active STA upstream connection — STA preserved, internet remained available after install
