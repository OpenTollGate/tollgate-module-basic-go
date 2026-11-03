- **The nftables guards no longer hardcode the captive bridge's name — a
  renamed bridge keeps its API access instead of silently blackholing.**
  The shipped guards (20/30/31/32) matched `br-lan` by literal; an operator
  renaming the bridge lost the portal→:2121 path with nothing in the log
  naming the cause (bench-verified on rc1: the guard's own drop counter
  doing the killing). The guards now resolve `$tg_portal_if` /
  `$tg_private_if` from `00-tollgate-defs.nft`, rendered from nodogsplash's
  `gatewayinterface` and the module-owned private bridge by
  `tollgate-nft-interfaces-render` — with a shipped static default so the
  defines resolve before the first render, a service-start WARN naming the
  expected interface when it does not exist, and every fallback said aloud.
  The rendered `20-nds-enforce.nft` (the #754 rescue) carries the same
  defines: the renderer's template and the shipped file are byte-identical
  by contract test (#757, #784).
