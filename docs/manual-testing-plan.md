# Manual Testing Plan — Upstream WiFi Management

Happy-path validation to run on a physical OpenWRT device before marking the PR ready for review.

## Prerequisites

- A physical OpenWRT device running this build, or a VM with `uci`, `iwinfo`, `wifi`, `ip` commands available
- At least one WiFi radio configured (`radio0`, optionally `radio1`)
- A known upstream WiFi network (SSID + passphrase) reachable from the device
- SSH or serial console access to the device
- The `tollgate-wrt` service running (which embeds the CLI server and upstream manager)
- `make` installed on the device (`opkg install make`)

## Quick Reference

```sh
# Automated targets (run from the project root on the router)
make -f Makefile.test smoke                          # 5-minute smoke test
make -f Makefile.test smoke SSID=MyNet PASS=secret   # smoke test with credentials
make -f Makefile.test full SSID=MyNet PASS=secret    # full test suite
make -f Makefile.test status                          # verify service is up
make -f Makefile.test logs                            # tail upstream logs
make -f Makefile.test scan                            # scan for upstream networks
make -f Makefile.test list                            # list configured upstreams
```

---

## Phase 1: Verify Service Startup

### 1.1 — Confirm the service starts without crashes

```sh
logread -e tollgate | tail -30
```

**Expected:** You should see these log lines:

- `"Global logger initialized"`
- `"UpstreamDetector module initialized ..."`
- `"CLI server initialized and listening on Unix socket"`
- `"Upstream WiFi manager initialized"`
- `"Starting upstream manager"` (from the UpstreamManager goroutine)
- Possibly `"Failed to ensure radios enabled"` or `"Failed to ensure wwan setup"` warnings if radios/network are already configured — these are non-fatal

### 1.2 — Confirm the CLI binary exists and is executable

```sh
which tollgate
tollgate version
```

**Expected:** Version info printed. No errors.

---

## Phase 2: CLI `upstream scan`

### 2.1 — Scan for available upstream networks

```sh
tollgate upstream scan
```

**Expected:**

- A table with columns: `SSID`, `Signal`, `Ch`, `Encryption`, `Radio`
- Your known upstream SSID appears in the list with signal strength (e.g. `-45 dBm`), encryption type (e.g. `psk2`), and radio name (e.g. `radio0`)
- Hidden SSIDs should NOT appear
- The message `"Found N network(s)"` at the bottom

**What it exercises:**

- `Scanner.ScanAllRadios()` — calls `iwinfo <radio> scan` on each radio
- `Scanner.ParseIwinfoOutput()` — parses tab-delimited scan output
- `Scanner.DetectEncryption()` — maps `WPA2 PSK (CCMP)` → `psk2`
- `Scanner.GetRadios()` — reads `/etc/config/wireless` for radio sections
- `handleUpstreamScan()` in CLI server
- `displayUpstreamScanResults()` in CLI client

### 2.2 — Verify logs show the scan

```sh
logread -e tollgate | grep -i scan
```

**Expected:** No errors related to scanning.

---

## Phase 3: CLI `upstream connect`

### 3.1 — Connect to an open (unencrypted) upstream

```sh
tollgate upstream connect OpenNetworkName
```

**Expected:**

- `"Connected to 'OpenNetworkName'"`
- No passphrase prompt (open network)

### 3.2 — Connect to an encrypted upstream

```sh
tollgate upstream connect MySecureSSID MyPassphrase
```

**Expected:**

- `"Connected to 'MySecureSSID'"`

**What it exercises (full connect flow):**

1. `Connector.EnsureRadiosEnabled()` — ensures radios aren't disabled
2. `Scanner.ScanAllRadios()` — scans for the target SSID
3. `Scanner.FindBestRadioForSSID()` — picks best radio for the SSID
4. `Scanner.DetectEncryption()` — determines UCI encryption string
5. `Connector.EnsureWWANSetup()` — creates `network.wwan` interface with DHCP
6. `Connector.FindOrCreateSTAForSSID()` — creates/reuses a `upstream_<ssid>` STA section
7. `Connector.GetActiveSTA()` — finds currently active STA (if any)
8. `Connector.SwitchUpstream()` — disables old STA, enables new STA, reloads wifi, waits for IP

### 3.3 — Verify connectivity after connect

```sh
ping -c 3 8.8.8.8
```

**Expected:** Ping succeeds (device is now online via upstream WiFi).

### 3.4 — Verify UCI state

```sh
uci show wireless | grep upstream_
uci show network.wwan
```

**Expected:**

- An `upstream_<ssid>` STA section exists with `disabled=0` (active)
- `network.wwan` is configured with `proto=dhcp`
- Any previously active STA is now `disabled=1`

---

## Phase 4: CLI `upstream list`

### 4.1 — List configured upstream STAs

```sh
tollgate upstream list
```

**Expected:**

- A table with columns: `SSID`, `STATUS`, `RADIO`, `ENCRYPTION`
- The SSID you just connected to shows `ACTIVE`
- Any previous upstreams show `disabled`
- Message: `"N upstream STA(s) configured"`

**What it exercises:**

- `Connector.GetSTASections()` — reads all STA sections from `/etc/config/wireless`
- `handleUpstreamList()` in CLI server
- `displayUpstreamSTAList()` in CLI client

---

## Phase 5: Automatic Upstream Switching (Daemon)

This phase validates the `UpstreamManager` daemon loop.

### 5.1 — Observe daemon startup

```sh
logread -e tollgate | grep "upstream manager"
```

**Expected:** `"Starting upstream manager"` logged at service start.

### 5.2 — Wait for a scheduled scan cycle (every 5 minutes by default)

Watch the logs in real time:

```sh
logread -e tollgate -f | grep -i upstream
```

**Expected (within 5 minutes):**

- `"Running upstream scan cycle"` with `reason=scheduled`
- Either `"Best candidate found"` or `"No known upstream candidates available"`
- If a significantly stronger candidate exists (>12 dB), you'll see `"Candidate significantly stronger"` followed by a switch

### 5.3 — Simulate connectivity loss

```sh
# Block connectivity through the upstream interface temporarily
# Replace <wwan-iface> with the actual interface name (e.g. wlan1)
iptables -A OUTPUT -o <wwan-iface> -j DROP
```

Then watch logs for ~60 seconds (2 consecutive failed fast-checks at 30s each):

**Expected:**

- `"Connectivity lost"` with incrementing `lost_count`
- After 2 failures: `"Running upstream scan cycle"` with `reason=emergency`
- If a known alternative upstream exists and is reachable, the daemon switches to it

Cleanup:

```sh
iptables -D OUTPUT -o <wwan-iface> -j DROP
```

---

## Phase 6: CLI `upstream remove`

### 6.1 — Attempt to remove the active upstream (should fail)

```sh
tollgate upstream remove MySecureSSID
```

**Expected:**

- Error: `"Failed to remove upstream: ..."` — active upstreams cannot be removed

### 6.2 — Connect to a different upstream first, then remove the old one

```sh
tollgate upstream connect OtherSSID OtherPassphrase
tollgate upstream remove MySecureSSID
```

**Expected:**

- `"Removed upstream 'MySecureSSID'"`
- The STA section for `MySecureSSID` is deleted from UCI config

### 6.3 — Verify removal

```sh
tollgate upstream list
```

**Expected:** `MySecureSSID` no longer appears. Only `OtherSSID` is listed as `ACTIVE`.

---

## Phase 7: Reseller Mode Guard

### 7.1 — Enable reseller mode

```sh
uci set tollgate.config.reseller_mode=1
uci commit tollgate
```

Then restart the service or wait for config reload.

### 7.2 — Observe daemon behavior

```sh
logread -e tollgate -f | grep -i upstream
```

**Expected:** The upstream manager daemon should NOT perform any scan cycles or switch upstreams while reseller mode is active. No `"Running upstream scan cycle"` logs should appear.

### 7.3 — Verify CLI commands still work manually

```sh
tollgate upstream scan
tollgate upstream list
```

**Expected:** Manual CLI commands should still work. Only the automatic daemon loop is suppressed.

**Cleanup:**

```sh
uci set tollgate.config.reseller_mode=0
uci commit tollgate
```

---

## Phase 8: Edge Cases / Regression

### 8.1 — Run `upstream connect` with an unknown SSID

```sh
tollgate upstream connect NonExistentSSID
```

**Expected:** Error: `"SSID 'NonExistentSSID' not found in scan"`

### 8.2 — Run `upstream connect` with encrypted SSID but no passphrase

```sh
tollgate upstream connect MySecureSSID
```

**Expected:** Error: `"Passphrase required for encrypted network 'MySecureSSID'"`

### 8.3 — Run `upstream remove` with unknown SSID

```sh
tollgate upstream remove UnknownSSID
```

**Expected:** Error indicating STA not found.

### 8.4 — Verify existing `tollgate` CLI commands still work

```sh
tollgate status
tollgate version
tollgate wallet balance
tollgate network private status
```

**Expected:** All pre-existing commands work as before.

---

## Phase 9: Cleanup Verification

### 9.1 — Confirm shell scripts are gone

```sh
ls /usr/bin/wifiscan.sh
ls /usr/bin/upstream-daemon.sh
ls /etc/init.d/tollgate-upstream
```

**Expected:** All three return "No such file or directory".

### 9.2 — Confirm the Go binary provides all upstream functionality

```sh
tollgate --help | grep upstream
```

**Expected:** `upstream` command listed with subcommands.

---

## Quick Smoke Test (5-minute version)

If you only have 5 minutes, run this condensed sequence:

```sh
# 1. Verify service is up
logread -e tollgate | grep "Upstream WiFi manager initialized"

# 2. Scan
tollgate upstream scan

# 3. Connect (replace with your SSID/passphrase)
tollgate upstream connect MyNet MyPassword

# 4. Verify online
ping -c 1 8.8.8.8

# 5. List
tollgate upstream list

# 6. Verify old scripts gone
ls /usr/bin/wifiscan.sh /usr/bin/upstream-daemon.sh 2>&1
```

Or use the Makefile:

```sh
make -f Makefile.test smoke SSID=MyNet PASS=MyPassword
```
