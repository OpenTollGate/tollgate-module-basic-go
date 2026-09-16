# Flash budget for the wallet backends (T5)

**Question:** on a real OpenWrt router, what does it cost to run the wallet as a
**sidecar** (CDK daemon) instead of **in-process** (gonuts)? Reproduce with
`./measure.sh` (see `output.txt`, `env.txt`).

## Measured

| Artifact | aarch64 | mipsel |
|---|---|---|
| `cdk-walletd` (sidecar daemon) | **6.02 MiB** | **7.51 MiB** |
| `tollgate-wrt` package, published `.ipk`/`.apk` | ~7.6 MiB (compressed) | ~7.8 MiB (compressed) |
| `tollgate-wrt` service binary, installed | 11.52 MiB | — |
| `tollgate` CLI binary, installed | 6.82 MiB | — |
| nucula core harness (not shippable) | 1.69 MiB | 1.87 MiB |

The in-process gonuts wallet adds **no separate artifact** — it is compiled into
`tollgate-wrt`. A sidecar adds its **whole binary** on top.

## The marginal cost, and therefore the decision

- **Coexist (gonuts + CDK sidecar):** **+6.0 MiB (aarch64) / +7.5 MiB (mipsel)** of
  flash, **+~6 MiB RSS**, **+1 supervised process**.
- **Replace (drop the in-process gonuts link):** would need a build tag that
  excludes `gonuts_wallet.go`; not implemented, so today there is **no flash
  saving** from switching — only the added daemon.

So the sidecar is **not free**: it is a second process on a device whose whole
installed TollGate footprint (service + CLI) is already ~18 MiB.

- **Small / 16 MB mipsel tier → keep gonuts in-process.** A 7.5 MiB daemon on top
  of the service is not viable; in-process is exactly why the small tier works.
- **Large / ≥128 MB aarch64 tier (MT6000-class) → CDK sidecar.** +6.0 MiB is
  affordable and buys grade-A ownership, process isolation (no CGO), and the T15
  parity already proven.

This matches `05-architecture/wallet-selection.md`'s `wallet-policy` sketch and is
the input to `05-architecture/integration-decision.md`.

## Not yet measured

- **On-device free space** (`df -h /overlay`) and installed sizes — the lab router
  was unreachable from the build host during this run. `ROUTER=root@<ip>
  ./measure.sh` captures it; this is the one open field measurement for T5.
- **Peak RSS under a live swap/melt** for the daemon (idle ~5.6 MiB measured in
  `experiments/cdk-sidecar/output.txt`).
