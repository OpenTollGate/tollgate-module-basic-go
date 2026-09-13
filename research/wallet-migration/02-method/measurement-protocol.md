# Measurement protocol

Every number in `03-baseline/` must be reproducible from `experiments/` and must
record all five of: **arch/hardware**, **OS image + version**, **build commands**,
**candidate revision (SHA)**, **raw output**.

## Environment matrix

| Environment | Why |
|---|---|
| OpenWrt on the target router (25.12.x, apk) | the real deployment surface |
| Same OpenWrt image under QEMU for the same arch | cheap iteration; must be labelled as emulated, never presented as router numbers |
| x86_64 Linux (build host) | only ever for build/binary-size comparisons, clearly labelled |

## Metrics

**Footprint**
- binary size on target arch, stripped and unstripped
- static vs dynamic linking, and which libc
- RSS at idle, RSS at peak during mint/melt, PSS if available
- thread count at idle and during operations
- peak open file descriptors, sockets held open

**Storage**
- on-disk proof/quote store: format, size per token, growth per operation
- bytes written per mint/melt (write amplification — flash wear on a router)
- crash consistency: kill -9 mid-operation, then verify proof integrity and no double-spend state
- recovery behaviour on unclean shutdown and on power loss

**Behaviour**
- latency: mint quote → mint, melt, receive (p50/p95), against a real mint
- startup time to wallet-ready
- behaviour when the mint is unreachable / returns 5xx / times out (does it corrupt state?)
- NUT-07 state check / NUT-09 restore coverage
- concurrency: two operations in flight (does it serialise? deadlock? corrupt?)

**Engineering**
- dependency count and licence of each dependency (supply-chain surface)
- build reproducibility of the candidate itself
- cross-compile effort for the router arches (patch count, CI cost)
- API churn risk: breaking changes in the last N releases
- bus factor / governance: who merges, how many maintainers, release cadence
- licence compatibility with GPL-3.0 (TollGate's licence)

## Rule

A metric that cannot be reproduced from `experiments/` does not go in the report.
Anything estimated rather than measured is labelled **ESTIMATE** with its basis.
