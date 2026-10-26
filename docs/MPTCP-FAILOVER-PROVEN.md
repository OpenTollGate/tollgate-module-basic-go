# Mid-transfer failover: the connection SURVIVED losing its dominant path

Date: 2026-10-19
The last unmeasured claim in this work queue, and it **passed**.

## The test

A 20-second `mptcpize` transfer, with the **dominant** subflow killed mid-flight.

That choice matters: the wired leg carried **91 %** of the bytes in the magnitude
measurement, so killing it is the worst case — not a graceful removal of the
already-idle path. At t≈8 s:

```
ip link set mptcp-wan1 down
```

(That takes the tunnel — and therefore that subflow — down without disturbing the
shared AP+STA radio, which is the correct way to impair a path on this SoC.)

## Result: the connection survived and recovered

```
0.00-1.00   sec   1.00 MBytes   8.38 Mbits/sec
1.00-2.00   sec   2.12 MBytes  17.8  Mbits/sec
2.00-3.00   sec   2.88 MBytes  24.1  Mbits/sec
3.00-4.00   sec   2.38 MBytes  19.9  Mbits/sec
4.00-5.00   sec   2.12 MBytes  17.8  Mbits/sec
5.00-6.00   sec   1.75 MBytes  14.7  Mbits/sec
6.00-7.00   sec   2.00 MBytes  16.8  Mbits/sec
7.00-8.00   sec   1.75 MBytes  14.7  Mbits/sec
8.00-9.00   sec  896 KBytes    7.34 Mbits/sec     <- path goes down
9.00-10.00  sec   1.00 MBytes   8.39 Mbits/sec
10.00-11.00 sec   0.00 Bytes    0.00 bits/sec     <- full stall, one second
11.00-12.00 sec   (stall continues)
12.00-13.00 sec   0.00 Bytes    0.00 bits/sec
13.00-14.00 sec   1.00 MBytes   8.39 Mbits/sec     <- RECOVERED on the other path
14.00-15.00 sec   1.00 MBytes   8.39 Mbits/sec
15.00-16.00 sec   1.00 MBytes   8.39 Mbits/sec
16.00-17.00 sec   2.62 MBytes  22.0  Mbits/sec
17.00-18.00 sec   1.38 MBytes  11.5  Mbits/sec
18.00-19.00 sec   2.00 MBytes  16.8  Mbits/sec
19.00-20.00 sec   1.62 MBytes  13.6  Mbits/sec

0.00-20.00 sec  28.5 MBytes  12.0 Mbits/sec  sender
0.00-20.20 sec  25.4 MBytes  10.5 Mbits/sec  receiver
```

**The transfer completed.** `iperf Done.` — no connection reset, no error. Only 84
retransmits across the whole 20 s, and the run finished on the surviving StarGate
tunnel at 8–22 Mbit/s.

## What this establishes

- **Session continuity across uplink loss: PROVEN.** A single MPTCP connection
  survived the loss of the subflow that was carrying 91 % of its bytes, and
  resumed on the other path.
- **Recovery cost: ~3 seconds of stall.** Measured gap is t≈10–12 s with some
  partial traffic either side. That is the price of losing the dominant leg with
  no scheduler to spread load: the path manager has to detect the failure and
  re-queue onto the survivor.
- **This is the real value proposition of the bond**, and it is exactly the
  "resilient multi-path, not 2×" framing the project has used throughout. The
  feature earns its keep by not dropping the session, not by adding bandwidth.

## Honest limits of this test

- **One run.** Failover timing is not characterised; 3 s is a single observation,
  not a distribution. Repeated runs would be needed to quote a recovery time.
- **Tunnel impairment, not physical uplink loss.** Taking `mptcp-wan1` down kills
  the subflow's path in a way the kernel sees promptly. A *physical* loss
  (cable pulled, satellite fade) presents differently — silent blackholing rather
  than an interface going down — and detection there depends on retransmit
  timeouts, so recovery would likely be **slower**. Untested.
- **The surviving path was the weaker one.** So the run proves continuity, not
  that performance is preserved: throughput fell from ~15–20 Mbit/s to ~8–14
  Mbit/s after the switch.
- **`--bind-dev`-style path control was not used** (it segfaults on this build);
  the impairment was done by interface state, which is a coarse but unambiguous
  signal.

## Where this leaves the goal

| claim | status |
|---|---|
| two subflows on one connection, both carrying payload | **PROVEN** (91 %/9 % measured split) |
| session survives loss of the dominant path | **PROVEN** (this test) |
| mid-transfer failover works | **PROVEN** (one run, ~3 s stall) |
| bandwidth multiplier | **NOT demonstrated** — bonded sits inside single-path variance |
| 1.2–1.3× ceiling | **unverified** on this hardware; an upper bound, not a guarantee |

Reproduce: `scripts/uplink-tunnels.sh` + `scripts/vps-tunnels.sh`, register the
tunnel addresses `signal subflow`, start a 20 s `mptcpize iperf3`, and
`ip link set mptcp-wan1 down` mid-flight.
