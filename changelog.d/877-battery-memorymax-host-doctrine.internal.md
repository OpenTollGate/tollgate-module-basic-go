- **The go-battery bounds each module's test leg in its own cgroup, and the
  host-resource doctrine is written down.** On 2026-10-11 the bench host's
  consumers (nine agent sessions, an observability stack, VMs, a six-agent
  fan-out) legitimately summed past all 24 GiB of swap; earlyoom killed an
  unrelated session mid-review. `go-battery.sh` now wraps its `go test` legs
  in `systemd-run --scope -p MemoryMax=8G -p MemorySwapMax=2G` when systemd
  is present (CI unchanged), so a runaway soak dies alone with an obvious
  OOM line instead of choosing victims host-wide; AGENTS.md's new
  "Host resource discipline (ai-legion)" section pins the rest — sequential
  agent fan-outs with pre-flight checks, on-demand fixtures via
  avd-acquire/avd-release with the avd-reaper behind them, and the
  host-mem-alert banner treated as a load-shed order
  ([#877](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/877)).
