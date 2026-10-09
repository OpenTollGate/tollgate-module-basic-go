# VM acceptance campaign (ai-legion QEMU lane)

The x86-64 leg of the release acceptance matrix, on the ai-legion lab
host, driven over the OpenWrt serial console (pty) of a QEMU VM booted
from the pristine `openwrt-25.12.0-x86-64-generic-ext4-combined` image
with `snapshot=on` (pristine boot per run — the conwrt-bench
`qemu-x86-64.yaml.example` pattern).

## Files

- `vm.sh` — VM lifecycle (boot with LAN+WAN NICs, kill). The second NIC
  matters: the stock image routes via `eth1`=wan, and without it the
  guest cannot reach `10.0.2.2` (the host) — every artifact download
  fails with `Network unreachable`.
- `qemu-tollgate.yaml` — the labgrid client config (kept for when the
  labgrid QEMUDriver console-socket issue on this host is fixed; the
  campaign drives the pty directly today).
- `evidence-console-*.log` — the raw serial-console transcript of a
  campaign run (36k lines: boot, install, checks).

## Running

Host side (ai-legion): `python3 -m http.server 18099 --directory
<pkg-dir>` (NOT :8000 — an existing service answers there with a JSON
404).

VM boots → console → set root password → stage the artifact + dependency
closure (`tollgate-wrt` apk + nodogsplash, jq, libmicrohttpd-no-ssl,
iptables-nft, iptables-mod-conntrack-extra/ipopt/nat-extra from the
25.12.0 target feed) → `apk add --allow-untrusted` all at once.

## 2026-10-07 result — artifact
`tollgate-wrt-0.6.0_rc1-r0.apk` sha256
`d1b1ba5e27d87914e1c28b98a26abae5c48c3a4d3ff94727771d3e9258265807`
(local `build-sdk-package.sh` build, pinned SDK x86-64-25.12.0, go1.26.8,
node v22.17.0, portal bundles staged by `portal-build.sh`):

| Check | Result |
|---|---|
| VM boots OpenWrt 25.12.0 (kernel 6.12.71) | PASS |
| Artifact installs with dependency closure (204 pkgs, 41.4 MiB) | PASS |
| Post-install: hostname minted (`tollgate-5762`), network restarted | PASS |
| `tollgate version` → `version: v0.6.0-rc1`, stamped build_time | PASS |
| Payment API `:2121` LISTENING | PASS |
| `/etc/tollgate/config.json` + `wallet.db` written | PASS |
| Captive portal at `/www/tollgate/` (index.html, assets, manifest) | PASS |
| NoDogSplash running, `gatewayport 2050` | PASS |
| NTP pre-auth rule live (nft `udp/123` present) | PASS |
| Same-version reinstall (`apk add` again) RC=0, service stays up | PASS |
| Reboot persistence | NOT RUN — `snapshot=on` discards the disk by design; needs the persistent-overlay variant (tracked as the next lane step) |

Known-local-build artifact: `install.json` records
`installed_version: "0.0.0"` (the setup script's package-manager fallback
did not resolve the apk-recorded version on this offline install); the
CLI reports `v0.6.0-rc1` correctly and the CI lane stamps this properly —
worth confirming on a CI artifact.

## v6 captive-portal regression — `v6_captive_regression.py`

Pins the IPv6 side of captivity dynamically on a pristine install of a
tollgate-wrt artifact (origin: the #783 investigation and the rel-769 lane
forensics, 2026-10-09 — see below for why this exists).

    python3 tests/vm-campaign/v6_captive_regression.py

Self-contained: boots its own snapshot-mode VM (serial console only, no
hostfwd — cannot collide with sibling lanes), stages the closure from an
in-process http.server, installs, then asserts:

| Check | Pins |
|---|---|
| rig-no-uplink-in-captive-bridge | br-lan carries no `ethX` uplink port (de-bridges + fails if one survives) |
| t1-uci-ipv6-off | `dhcp.lan.ra`/`dhcpv6` = disabled, `network.lan.ip6assign` = 0 (#148/#160) |
| t2-no-ra-on-br-lan | zero Router Advertisements on the wire in the capture window |
| t3-rs-gets-no-ra | a fresh client's Router Solicitation is not answered |
| t4-no-slaac-no-ra-route | client never gains global/site v6 or a `proto ra` route |
| t5-no-v6-escape | pre-auth external fetch over v6 fails |
| t6a/t6b v4 journey | UDP DNS answers from the router (#749/#769) and the portal fetches on tcp/2050 |

Env knobs: `V6CR_IMAGE`, `V6CR_PKGDIR`, `V6CR_APKS`, `V6CR_WORK`, `V6CR_MEM`,
`V6CR_RA_WINDOW_S` (defaults: the ai-legion shared image + the rc1 closure).

### Why the rig-validity check exists (the #783 lesson)

The stock x86 image bridges **eth0 into br-lan**. In a QEMU rig every NIC is
a slirp uplink, and slirp is itself an IPv6 router — it RAs onto the captive
bridge (source `52:56:00:00:00:02` / `fe80::2`, `fec0::/64`) and NATs guest
traffic at the hypervisor. Left in place, client v6 traffic exits
L2-bridged through eth0 and **never traverses the router's nftables at
all** — the rig then "proves" a captive bypass no router configuration could
prevent (this is exactly how the rel-769 lane produced the original #783
report; the forensics are in `~/tollgate-vm/rel-769/transcript.md` on
ai-legion). Any dynamic captive test on this rig shape MUST first prove the
captive bridge is not bridged to an uplink, or it is testing the
hypervisor, not the router. The offline state half is pinned by
`tests/uci-defaults-ipv6-lan-off_test.sh`; the post-reboot leg still needs
the persistent-overlay VM variant tracked above.
