# MPTCP scheduler: why only one uplink carries traffic

Date: 2026-10-16 (Ralph loop iteration 14)
Question chased: with two subflows formed and both uplinks healthy, why did the
scheduler keep all payload on the wired uplink while the StarGate subflow sat
idle?

## Answer: the kernel exposes only the `default` scheduler. It is a build limit.

Evidence, read from the router itself:

```
$ cat /proc/sys/net/mptcp/scheduler
default
$ cat /proc/sys/net/mptcp/available_schedulers
cat: can't open '/proc/sys/net/mptcp/available_schedulers': No such file or directory
$ echo roundrobin > /proc/sys/net/mptcp/scheduler
REJECTED (only default compiled in)

$ lsmod | grep mptcp
mptcp_diag   12288  0        <- diagnostics only
$ modprobe mptcp_sched_roundrobin
unavailable
$ modprobe mptcp_sched_redundant
unavailable
$ modprobe mptcp_sched_bpf
unavailable
$ find /lib/modules/$(uname -r) -name '*mptcp*'
/lib/modules/6.6.93/mptcp_diag.ko     <- the ONLY mptcp module shipped
```

Confirming the feature is absent rather than merely unconfigured:

- `available_schedulers` **does not exist** — the multi-scheduler sysctl API is
  not compiled in.
- `ip mptcp` exposes only `endpoint` and `limits`; there is **no scheduler verb**
  and no netlink path to select one.
- The kernel's `MPTcpExt` counter block contains `MPJoinSynRx` / `MPJoinAckRx`
  (so subflow creation works) but **no `MP*Sched*` counters** at all — the
  pluggable-scheduler accounting is not built.

## Consequence

On this build there is **no way to ask for a spreading scheduler**, so the
latency-preferring `default` cannot be swapped for round-robin/redundant. The
"only one subflow carries data" behaviour is therefore expected here and is not
a misconfiguration: with a large latency gap between paths (wired LAN ~0.5 ms vs
StarGate ~30 ms) the default scheduler is behaving as designed by concentrating
on the better path.

**This is a property of the OpenWrt kernel build, not of the TollGate config or
of the MPTCP endpoints**, which are provably correct: both uplink addresses are
registered as `signal subflow`, and subflows demonstrably form.

## What would change the outcome

1. **A kernel built with the pluggable scheduler** (`CONFIG_MPTCP_SCHED*`, plus
   `mptcp_sched_roundrobin`/`_redundant`). OpenWrt would need those in the
   package build; stock 24.10.2 for this target does not ship them.
2. **Paths of similar latency.** With two comparable-RTT uplinks the default
   scheduler has less reason to concentrate on one, and the imbalance should
   shrink. This is testable with the current kernel — it needs a second uplink
   whose RTT resembles the wired one, which the lab does not have (Starlink is
   inherently high-latency).

The first is a packaging change; the second is a topology change. Neither is a
code change in this PR.

## Correction recorded

The router reports **kernel 6.6.93** (`uname -r`, `/proc/version`), not 6.12.94
as earlier notes in this work stated. OpenWrt 24.10.2 is correct. Any reasoning
that depended on the newer version should be re-read against 6.6.93; the
scheduler conclusion above was derived from the running system, not from the
version number.
