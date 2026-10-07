# VM acceptance campaign — session log 2026-10-07

## Infrastructure debugging (all resolved, documented for the next run)

| Problem | Root cause | Fix |
|---|---|---|
| VM dies after ~5 min | 512M/1024M OOM (tollgate-wrt uses ~1.2G) | `-m 2048M` |
| Console driver loses QEMU | `-serial pty` + daemonize = QEMU exits when pty closes | `-serial unix:...` |
| Console marker regex matches echoed command | Marker appears in both echo and output | `run_to_file` pattern or `$()`-free marker |
| Artifact download fails (`Operation not permitted`) | Single-NIC VM: br-lan static IP, no default route | Two NICs: net0=br-lan, net1=wan (DHCP 10.0.2.15) |
| Port 8000 already in use | Existing JSON service on ai-legion | `python3 -m http.server 18099 --directory pkgs/` |
| SSH into VM: `Connection reset` | Dropbear on stock OpenWrt needs password first | `printf "tg\ntg\n" \| passwd root` via console |
| Dockerfile.client build fails | `ARG GO_VERSION` after first FROM = not in scope | `sed 's/FROM golang:${GO_VERSION}/FROM golang:1.25.8/'` |
| cdk-cli `send` says "Insufficient funds" | cdk-cli default wallet ≠ volume mount path | Mount at `/w` and pass `-w /w` |
| labgrid QEMUDriver TypeError | labgrid 26.0 qemudriver bug on this host | Drive QEMU directly via unix socket |
| Payment refused: "no MAC for 10.0.2.15" | VM pays from its own IP; self is not in ARP/DHCP | Add `echo ... >> /tmp/dhcp.leases` |
| Payment refused: "no MAC for 10.0.2.15" (2nd) | dnsmasq overwrote manual lease entries | Also add `ip neigh add` on `dev lo` |
| Payment still refused after identity resolves | NDS pre-flight: client must be portal-connected | `ndsctl auth <mac>` (returns empty on VM — no wireless) |

## What passed (10/10 software checks — unchanged from #704)

## Payment scenarios — blocked, with the exact reason

The three on-target payment scenarios (real payment, concurrent duplicate,
kill-recovery) require a client the daemon's NDS pre-flight recognizes.
On a QEMU VM without wireless:
- `ndsctl auth` returns empty (no wireless client to authenticate)
- The pre-flight `clientRegisteredForGate` refuses the payment with
  `client-not-registered` — which is the correct behavior on a real
  router (the client IS behind the captive portal) but makes a VM-only
  lane impossible without either:
  a. a fake `ndsctl` on PATH (the unit-test approach — valid for logic,
     not for integration), or
  b. an actual wireless interface in the VM (mac80211_hwsim), or
  c. the mipsel router lane (the real hardware target).

**Recommendation**: the payment scenarios belong on the x1860 router lane
(mipsel), which is the actual supported-hardware representative. The VM
lane's value is install/upgrade/reinstall/reboot/idempotence — all green.

## The mipsel artifact

Build script fix: `SDK_TAG=ramips-mt7621-25.12.0` must be passed as a
command-scoped env var (`SDK_TAG=... bash script.sh`), not `export` in a
subshell that the script's `set -x` doesn't inherit.
