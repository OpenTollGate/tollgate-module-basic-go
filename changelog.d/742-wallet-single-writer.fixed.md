- **The wallet single-writer invariant is enforced at boot and on the CLI
  socket.** The #504 audit found the CLI already socket-routed (it never opens
  `wallet.db`), but two real windows on the daemon side:
  `CLIServer.Start()` removed a live daemon's socket unconditionally — with
  the startup gate binding the socket before merchant construction, a
  double-start stole the CLI surface and then degraded wallet-less, so every
  `tollgate` command executed against the wallet-less daemon — and
  `newFullMerchant` treated a held bbolt lock like any wallet failure,
  degrading with a misleading "first boot" log and a mint-recovery trigger
  that could never fix it. Now the socket start probes before removing (a
  daemon that answers owns it; stale crash debris is reclaimed;
  undetermined failures refuse), `tollwallet.ErrWalletLocked` distinguishes
  the single-writer refusal through the wrap chain, a held lock fails the
  boot with an operator-actionable message, and a pre-existing data race in
  `CLIServer` (accept loop vs `Stop`) exposed by the new tests is fixed with
  an atomic. The sidecar dual-open guard stays latent (backend selection has
  no production caller yet) and flock reliability on NAND/overlayfs remains
  the documented lab item
  ([#742](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/742)).
