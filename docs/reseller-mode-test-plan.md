# Reseller Mode: Test Plan

## 1. Shared Router Mutex

Multiple LLM sessions operate on the same physical routers (alpha, beta). All sessions
MUST coordinate via a single shared lock file to avoid deploying different binaries,
modifying configs, or restarting services simultaneously.

### Lock file location

```
/root/routers.lock
```

This file lives **outside** any repo tree so all sessions check the same file. It is
never committed to git (both repos' `.gitignore` should list `routers.lock`).

### Lock file format

```
locked: true
branch: feature/wifiscan
session: LLM session — upstream WiFi reseller mode testing
timestamp: 2026-04-30T06:00:00Z
phase: Phase R3: enabling reseller mode on Router B
```

### Protocol

1. **Before any router modification** (deploy, restart, config edit, iptables):
   ```
   cat /root/routers.lock
   ```
   If the file exists and `locked: true`, **stop**. Do not proceed. Wait for the
   other session to finish, or ask the user to coordinate.

2. **When starting router work**, create the lock:
   ```
   echo "locked: true" > /root/routers.lock
   echo "branch: $(git -C /root/tollgate-module-basic-go branch --show-current)" >> /root/routers.lock
   echo "session: LLM session — <description>" >> /root/routers.lock
   echo "timestamp: $(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> /root/routers.lock
   echo "phase: <what you're about to do>" >> /root/routers.lock
   ```

3. **Update the phase** as you progress through test steps:
   ```
   sed -i 's/^phase:.*/phase: Phase R5: verifying daemon connects to TollGate/' /root/routers.lock
   ```

4. **When done**, release the lock:
   ```
   rm /root/routers.lock
   ```

5. **Stale locks**: If a lock is older than 2 hours and the session is unreachable,
   it is reasonable to remove it:
   ```
   cat /root/routers.lock  # check timestamp
   rm /root/routers.lock   # force-release
   ```

---

## 2. Code Fix: Reseller Mode Candidate Fallback

### Problem

`findResellerCandidates` only considers `TollGate-*` SSIDs. If the daemon connects to
a TollGate that provides no internet (blacklisted), there is no fallback — the daemon
cannot switch back to a pre-existing STA like `c03rad0r2` because it's not a TollGate
SSID. The router would be stranded.

### Desired behavior

Reseller mode candidate pool = **superset** of non-reseller mode:

- **Existing disabled STAs** in `/etc/config/wireless` (same as non-reseller mode — safe fallbacks)
- **PLUS** open `TollGate-*` SSIDs discovered by scan (created on-the-fly — new candidates)

Signal strength is the only ranking criterion. No preference between TollGate vs
non-TollGate. If all TollGate SSIDs are blacklisted, the daemon falls back to any
available disabled STA with internet.

### Implementation

Modify `findResellerCandidates` in `upstream_manager.go`:

1. Collect existing disabled STA sections (reuse logic from `findKnownCandidates`)
2. Scan for open `TollGate-*` SSIDs, creating STAs on-the-fly for new ones (existing logic)
3. Merge both pools, exclude blacklisted SSIDs
4. Return the strongest signal candidate

Also add a unit test: reseller mode falls back to non-TollGate disabled STA when all
TollGate SSIDs are blacklisted.

---

## 3. Test Plan: Reseller Mode on Router B (beta)

### Topology

```
Router A (alpha, 100.90.41.166)       Router B (beta, 100.90.216.248)
  AP: TollGate-1690 (open)              AP: TollGate-D1C6 (open)
  STA: c03rad0r (ACTIVE)                STA: c03rad0r2 (ACTIVE)
```

- Router B sees `TollGate-1690` from Router A (open, no internet — requires e-cash)
- Router A stays in normal mode as safety net
- If Router B gets stranded, reach it via Router A: `ssh root@100.90.41.166` then `ssh 192.168.1.1`

### Phase R1: Pre-test verification

Verify both routers are healthy before starting.

```sh
ssh root@100.90.41.166 'tollgate upstream list && ping -c 2 -W 3 9.9.9.9'
ssh root@100.90.216.248 'tollgate upstream list && ping -c 2 -W 3 9.9.9.9'
```

**Pass criteria**:
- Router A: c03rad0r ACTIVE, ping works
- Router B: c03rad0r2 ACTIVE, ping works

### Phase R2: Build + deploy to Router B

```sh
cd /root/tollgate-module-basic-go/src
go build ./... && go vet ./... && go test \
  github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager \
  -run "TestConnector_|TestSanitize|TestScanner_|TestUpstreamManager_" -count=1

cd /root/tollgate-module-basic-go
bash scripts/local-compile-to-router.sh 100.90.216.248
```

**Pass criteria**:
- All unit tests pass (38 existing + 1 new reseller fallback test)
- Binary deployed to Router B, service restarted

### Phase R3: Enable reseller mode on Router B

```sh
ssh root@100.90.216.248 'cp /etc/tollgate/config.json /etc/tollgate/config.json.bak'
ssh root@100.90.216.248 'cat /etc/tollgate/config.json | jq ".reseller_mode = true" > /tmp/config.json && mv /tmp/config.json /etc/tollgate/config.json'
ssh root@100.90.216.248 '/etc/init.d/tollgate-wrt restart'
```

### Phase R4: Verify daemon discovers TollGate-1690

Wait ~30s for first scan cycle, then check:

```sh
ssh root@100.90.216.248 'logread | grep -iE "scan cycle|TollGate|Created STA|candidate|reseller"'
ssh root@100.90.216.248 'tollgate upstream list'
```

**Pass criteria**:
- `Created STA for TollGate candidate` with ssid=TollGate-1690 in logs
- `tollgate upstream list` shows new `upstream_tollgate_1690` STA (disabled)
- Daemon ran a scan cycle in reseller mode

### Phase R5: Verify daemon connects to TollGate-1690

Wait for daemon to switch (next scan cycle, ~5 min):

```sh
ssh root@100.90.216.248 'logread | grep -iE "Switching upstream|Successfully switched" | tail -5'
ssh root@100.90.216.248 'iwinfo phy0-sta0 info | head -3'
```

**Pass criteria**:
- `Switching upstream` ssid=TollGate-1690
- `iwinfo` shows ESSID: "TollGate-1690"

### Phase R6: Verify fallback to c03rad0r2 + blacklist

TollGate-1690 provides no internet (auth timeout or captive portal). Wait ~2-3 min
for daemon to detect connectivity loss and switch back:

```sh
sleep 180
ssh root@100.90.216.248 'logread | grep -iE "Connectivity lost|blacklist|Switching upstream|Successfully switched" | tail -10'
ssh root@100.90.216.248 'tollgate upstream list && ping -c 2 -W 3 9.9.9.9'
```

**Pass criteria**:
- `Connectivity lost` logged (after 120s pause expires)
- `Blacklisted SSID (no internet)` ssid=TollGate-1690
- Daemon switches back to c03rad0r2 — **validates the fallback fix**
- `ping -c 2 -W 3 9.9.9.9` works again

### Phase R7: Verify daemon doesn't retry blacklisted SSID

Wait for next scheduled scan cycle (~5 min):

```sh
sleep 300
ssh root@100.90.216.248 'logread | grep -iE "scan cycle|candidate|No.*candidate" | tail -5'
```

**Pass criteria**:
- Scheduled scan runs but skips TollGate-1690 (blacklisted)
- Router stays on c03rad0r2 with internet

### Phase R8: Restore normal mode

```sh
ssh root@100.90.216.248 'mv /etc/tollgate/config.json.bak /etc/tollgate/config.json'
ssh root@100.90.216.248 'tollgate upstream remove TollGate-1690 2>/dev/null; true'
ssh root@100.90.216.248 '/etc/init.d/tollgate-wrt restart'
```

**Pass criteria**:
- Router B back to normal mode
- c03rad0r2 ACTIVE, ping works

---

## 4. Recovery Procedures

| Scenario | Recovery |
|----------|----------|
| Router B stranded, no SSH via NetBird | SSH to Router A (100.90.41.166), then `ssh 192.168.1.1` (Router B LAN IP) |
| Router B in boot loop | Via Router A: `ssh root@B-LAN '/etc/init.d/tollgate-wrt stop'` then restore config |
| Config corrupted | `ssh root@B 'mv /etc/tollgate/config.json.bak /etc/tollgate/config.json && /etc/init.d/tollgate-wrt restart'` |
| Both routers stranded | Physical reboot required (user action) |

---

## 5. Execution Order

1. **Wait for `/root/routers.lock` to be released** by the other session
2. **Fix `findResellerCandidates`** to include disabled non-TollGate STAs as fallback
3. **Add unit test** for reseller mode fallback behavior
4. **Build and test locally** (38 existing + 1 new test)
5. **Acquire lock**: create `/root/routers.lock`
6. **Deploy to Router B**
7. **Run phases R1–R8**
8. **Release lock**: `rm /root/routers.lock`
9. **Update `docs/router-test-results.md`** with results
