# Baseline: gonuts-tollgate on the router (T1a)

**Status:** first measurement 2026-09-14 (whole-service baseline; isolated wallet
footprint still TODO — see "Limitations").

## What is being measured

The router's current wallet is `github.com/OpenTollGate/gonuts-tollgate` — a
**fork** of `elnosh/gonuts`, pinned as a Go module and compiled into the
`tollgate-wrt` service. It is not a separate process, so the honest baseline is
the **service** footprint with the wallet active (mints configured, wallet
initialised), not an isolated library number.

## Measured (GL-MT6000, OpenWrt 25.12.5, aarch64_cortex-a53)

```
package     : tollgate-wrt 0.6.0_alpha2_pre-r1 (feed RC, GPL-3.0-only)
/usr/bin/tollgate-wrt : 12,081,696 B   (~11.5 MiB, unstripped-ish)
/usr/bin/tollgate     :  7,146,944 B   (CLI)
running service       : VmHWM = VmRSS = 26,764 kB (~26 MiB), 8 threads
storage               : /etc/tollgate = 347 KB; wallet.db = 128 KB (sqlite)
```

Reproduce:
```
ssh root@<router> 'ls -la /usr/bin/tollgate-wrt; \
  for p in $(pidof tollgate-wrt); do awk "/VmHWM|VmRSS|Threads/{print}" /proc/$p/status; done; \
  du -sh /etc/tollgate; ls -la /etc/tollgate/wallet.db'
```

## How this compares (context, not apples-to-apples)

| | binary | RSS | threads | process model |
|---|---|---|---|---|
| gonuts (whole service) | 11.5 MiB (`tollgate-wrt`) | ~26 MiB | 8 | in-process |
| CDK sidecar (`cdk-cli balance`, unconfigured) | 19.7 MiB stripped | ~5.6 MiB | 5 | separate process |

These are **different things** (whole service vs a single CLI invocation) and must
not be read as "CDK is 5× smaller". The comparison that matters is:
in-process wallet + service vs service + wallet sidecar, under the same mint load.

## Limitations (what is NOT measured yet)

- No **isolated** gonuts wallet footprint (mint-probe-free micro-benchmark).
- No **flash bytes written per payment** for either wallet (the metric nucula
  failed on — whole-blob rewrite).
- No **peak** RSS under a live swap/melt.
- `VmHWM` equals `VmRSS` here because sampling was post-init steady state.

These belong to the shared conformance suite (`03-baseline/interchangeability.md`)
and are the next measurement step; T5c already produced the build/measure
apparatus for the CDK side.
