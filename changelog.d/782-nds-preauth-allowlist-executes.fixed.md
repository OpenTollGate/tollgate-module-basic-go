- **The NDS pre-auth allowlist actually executes now — rendered into the
  enforcement chain, ahead of its first rule.** nodogsplash compiles
  `preauthenticated_users` into priority-0 chains, but the package's own
  `nds_enforce_forward` chain drops/rejects at priority −1 — the compiled
  accepts never ran, so every allowlisted pre-auth flow (the
  top-up-before-pay mint access) died (bench-verified on rc1 with
  packet-level attribution). `tollgate-nds-preauth-render` now emits
  `20-nds-enforce.nft` with the allowlist's accepts as the chain's FIRST
  rules — the position the kernel honours, because a base chain's `accept`
  verdict ends evaluation in that chain only (a separate earlier-priority
  allow chain, this fix's first shape, was refuted live on an nft 1.1.7 rig
  and removed). Unparseable, non-tcp/udp, DNS-destination and out-of-range
  entries are refused loudly rather than guessed at; the shipped default
  file is byte-identical to the renderer's empty-allowlist output by
  contract test; convergence runs at install/upgrade/boot and on
  nodogsplash config commits (#754, #782).
