# StarGate uplink lost — measurement blocked, with product finding

Date: 2026-10-18
State: **the router has ONE working uplink, not two.** The aggregation
measurement needs two live paths and cannot be taken.

## Measured state

```
phy0-sta0        DOWN      fe80::9083:c4ff:fed1:5759/64     (no IPv4, no route)
ip route show default  ->  (empty)
lan1@eth0        UP        192.168.2.15/24, 2a01:599:11d:2324::1/128
ip route get 23.182.128.51 from 192.168.2.15
  -> via 192.168.2.1 dev lan1 table 201        (wired works)
ping -I 192.168.2.15 8.8.8.8  -> 25.0/26.2/27.4 ms, 0% loss
```

So the **wired uplink is healthy** (policy routing from the previous iteration
survived), and the **wifi STA uplink is gone**.

## Why StarGate did not come back by itself (product finding)

`tollgate-wrt`'s `wireless_gateway_manager` is in a losing loop:

```
WARN  Startup check: active STA has no internet, triggering emergency scan
      module=wireless_gateway_manager ssid=StarGate
      phy0-sta0: CTRL-EVENT-SCAN-FAILED ret=-16 retry=1     (x20 in the ring)
INFO  Startup check: scanning for alternative upstream attempt=3 remaining=0
INFO  Startup check: no candidate found in scan
WARN  Startup check: no working upstream found after all scan retries,
      deferring to main loop
INFO  Startup grace period active  grace_seconds=90
```

Two distinct problems, and they compound:

1. **`CTRL-EVENT-SCAN-FAILED ret=-16`** — `-16` is `EBUSY`. On MT7986 the radios
   are shared AP+STA, so a scan conflicts with the live AP interfaces. The scan
   that is supposed to *find* an upstream is the thing that cannot run.
2. **The recovery logic gives up.** After 3 attempts it logs "no candidate
   found", defers, waits 90 s, and repeats — but it has already lost the
   association, so there is nothing to recover from. It never re-associates the
   STA that was previously working.

**Consequence worth fixing upstream:** an active STA that drops does not get
re-established by the manager. The "emergency scan" path is unusable on shared
AP+STA radios (EBUSY), so the fallback must be *re-associate the known AP*
before it is *scan for a different one*.

## Why this blocks the goal

Aggregation requires two genuinely disjoint live paths. With one uplink there is
no second path to bond, and any number produced now would be a single-path number
mislabelled as a bond — exactly the fabrication the constraints forbid.

Note also that **there is no default route at all**; the wired path only works
because of the per-uplink policy rule installed in the previous iteration
(`from 192.168.2.15 lookup 201`). That is worth keeping in mind: the box is
currently functioning only because of that explicit rule, not because of main.

## What is ready for when a second path exists

- `scripts/uplink-policy-routing.sh` — per-uplink rule+table (idempotent)
- `scripts/agg-run.sh` — Rule-7 runner, 3 configs x 3 runs, subflow monitor
- Endpoints already in the `subflow`-only (proxy-terminated) shape
- Peer verified MPTCP-capable (`mptcp LISTEN`, `add_addr_accepted 4`)

## Options to restore a second path

1. **Re-associate StarGate manually** (`uci` + `wifi reload` — never `iw`
   surgery on this SoC, it tears down the APs). Blocked in practice by the
   EBUSY scan failure and, per the operator, by current signal conditions.
2. **Second wired uplink** — the lab note's preferred setup: router `lan1`
   (house) vs `br-private` toward CobradorWave. This is the configuration that
   was recommended originally precisely to avoid the radio.
3. Wait for StarGate signal to return and let the manager retry.

Option 2 is the one that does not depend on RF conditions.
