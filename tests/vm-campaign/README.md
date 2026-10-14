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
