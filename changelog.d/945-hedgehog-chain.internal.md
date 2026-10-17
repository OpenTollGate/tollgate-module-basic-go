- **Add the Hedgehog chain-access layer (`src/hedgehog/chain.go`,
  `chain_core.go`).** The router has no Bitcoin node — the PR #944 research
  confirmed no `bitcoind`/`bitcoin-cli` on the device and no chain state — so
  chain access is a three-method interface rather than a node dependency. The
  router uses an Electrum/SPV endpoint; the bench and tests use bitcoind directly;
  a device with no chain at all gets `NullChainView`, which answers "unknown"
  rather than guessing.

  The CSV watcher encodes the D5 time obligation concretely: `Elapsed`,
  `Remaining` and `Expired` are the arithmetic a watchtower must get right, and a
  reorg that puts the tip behind the confirmation height clamps to zero instead of
  reporting a negative elapsed (which would make an unexpired lock look spendable).
