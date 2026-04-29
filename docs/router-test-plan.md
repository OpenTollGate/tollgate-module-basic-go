# Router Test Plan: Upstream WiFi on Physical Hardware

## Overview

End-to-end test of the Go upstream WiFi management code on two GL.iNet MT3000
routers connected via NetBird. Router A deploys the new binaries and connects
upstream to Router B's private WiFi network.

## Test Environment

| Role     | Label | NetBird IP      | SSIDs broadcast                               |
|----------|-------|-----------------|-----------------------------------------------|
| Alpha    | alpha | `100.90.41.166` | `TollGate-1690` (open), `c08r4d0r-1690` (psk2) |
| Beta     | beta  | `100.90.216.248`| `TollGate-D1C6` (open), `c03rad0r-D1C6` (psk2) |

- **Architecture**: arm64 (aarch64_cortex-a53), built with `GOOS=linux GOARCH=arm64`
- **Deploy script**: `scripts/local-compile-to-router.sh <IP>`
- **Test runner**: `make -f Makefile.test r-<target> ROUTER=alpha`

## Target SSID

| Property     | Value                  |
|-------------|------------------------|
| SSID        | `c03rad0r-D1C6`        |
| Encryption  | psk2+ccmp              |
| Password    | `Papa-Juliet-Foxtrot-39` |
| Broadcast by| Router B (beta)        |
| E-cash req  | No (private network)   |

## Pre-existing State (Router A)

Router A has an active STA interface `wifinet4` connecting to `c03rad0r` with
SAE encryption. This provides its current upstream connectivity. Our code will:

1. Discover `wifinet4` as the active STA via `GetActiveSTA()`
2. Disable it when switching to the new upstream
3. Create a new STA `upstream_c03rad0r_d1c6`

## Test Steps

### Step 1: Update `routers.env`

```sh
# Edit routers.env with real NetBird IPs
ROUTER_ALPHA_HOST=100.90.41.166
ROUTER_BETA_HOST=100.90.216.248
```

### Step 2: Build & Deploy to Router A

```sh
./scripts/local-compile-to-router.sh 100.90.41.166
```

Cross-compiles `tollgate-wrt` (service) and `tollgate` (CLI) for arm64, SCPs
both to `/usr/bin/`, stops/starts the service.

**Pass**: script exits 0, no errors.

### Step 3: Verify Service Startup

```sh
make -f Makefile.test r-status ROUTER=alpha
```

**Pass criteria**:
- `tollgate version` returns version info
- Logs contain `"Upstream WiFi manager initialized"`

### Step 4: Scan for Networks

```sh
make -f Makefile.test r-scan ROUTER=alpha
```

**Pass criteria**:
- Sees `c03rad0r-D1C6` (from Router B) with signal + encryption info
- Sees `TollGate-D1C6` (from Router B) as open
- May see own APs and other nearby networks

### Step 5: Connect Upstream to Router B

```sh
make -f Makefile.test r-connect \
  SSID=c03rad0r-D1C6 \
  PASS=Papa-Juliet-Foxtrot-39 \
  ROUTER=alpha
```

**Internal flow**:
1. `EnsureRadiosEnabled` — verify radio0/radio1 enabled
2. `ScanAllRadios` — scan both radios
3. `FindBestRadioForSSID` — pick radio with best signal for `c03rad0r-D1C6`
4. `DetectEncryption` — map to `psk2`
5. `EnsureWWANSetup` — ensure `network.wwan` (proto=dhcp) exists
6. `FindOrCreateSTAForSSID` — create UCI section `upstream_c03rad0r_d1c6`
7. `GetActiveSTA` — finds `wifinet4` (active, disabled=0)
8. `SwitchUpstream` — disable `wifinet4`, enable new STA, `wifi reload`

**Pass criteria**:
- Connect command succeeds
- `ping -c 3 8.8.8.8` succeeds through Router B

### Step 6: List & Verify UCI State

```sh
make -f Makefile.test r-list ROUTER=alpha
make -f Makefile.test r-verify-uci ROUTER=alpha
```

**Pass criteria**:
- `upstream_c03rad0r_d1c6` shown as ACTIVE (disabled=0)
- `wifinet4` shown as disabled (disabled=1)
- `network.wwan` configured with proto=dhcp

### Step 7: Edge Cases & Cleanup Verification

```sh
make -f Makefile.test r-test-edge-cases ROUTER=alpha
make -f Makefile.test r-test-cleanup ROUTER=alpha
```

**Pass criteria**:
- `connect NonExistentSSID` → error (SSID not found)
- `remove UnknownSSID` → error
- `tollgate version` and `tollgate status` still work
- Old shell scripts (`wifiscan.sh`, `upstream-daemon.sh`) absent

### Step 8 (Optional): Daemon Observation

```sh
make -f Makefile.test r-wait-daemon ROUTER=alpha
```

Watch for a 5-minute scan cycle. The daemon should:
- Check connectivity via ping
- Run a scheduled scan if counter reached threshold
- Log any candidate switching decisions

### Step 9: Restore Original State

```sh
# Remove our test STA
make -f Makefile.test r-remove SSID=c03rad0r-D1C6 ROUTER=alpha

# Re-enable original upstream STA
ssh root@100.90.41.166 \
  "uci set wireless.wifinet4.disabled=0; uci commit wireless; wifi reload"
```

## Quick Smoke Test (3 commands)

```sh
./scripts/local-compile-to-router.sh 100.90.41.166

ssh root@100.90.41.166 \
  "tollgate upstream scan && tollgate upstream connect c03rad0r-D1C6 Papa-Juliet-Foxtrot-39"

ssh root@100.90.41.166 \
  "tollgate upstream list && ping -c 3 8.8.8.8"
```

## Risks & Recovery

| Risk                                         | Recovery                                                               |
|----------------------------------------------|------------------------------------------------------------------------|
| Router A loses upstream when wifinet4 disabled | `ssh root@100.90.41.166 "uci set wireless.wifinet4.disabled=0; uci commit wireless; wifi reload"` |
| DHCP timeout on wwan                         | `ssh root@100.90.41.166 "ifdown wwan; ifup wwan"`                     |
| Service crash on startup                     | procd auto-restarts (3 retries). Check: `logread -e tollgate \| tail -30` |
| psk2+ccmp not detected                       | Manual fix: `uci set wireless.upstream_c03rad0r_d1c6.encryption=psk2` |
| Total connectivity loss (can't SSH)          | Use router's LAN IP (192.168.1.1) from local network                   |
