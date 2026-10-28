- **The NDS enforcement fragment no longer tells a refuted story about why it
  exists.** Since #283, `20-nds-enforce.nft`'s header — and the renderer
  template that must stay byte-identical to it — claimed fw4's
  `inet fw4 forward` chain "accepts forwarded traffic before the `ip filter
  FORWARD` chain (same priority) ever sees it". That is false, and was measured
  false on an nftables 1.0.9 forwarding rig: an `accept` is final only for the
  chain that issued it, so a packet accepted by one base chain still traverses
  later base chains on the same hook (6/6 packets in either registration order
  and across priorities), while `drop`/`reject` do starve them. The ordering
  premise was never sound either — two base chains at the same priority have
  unspecified order. The header now states the symptom #283 observed, records
  that its cause is NOT established, keeps the unspecified-ordering hazard, and
  names the packet-path measurement that would settle it; the rig, its raw
  counters and both candidate hypotheses live in
  `docs/measurements/nft-verdict-propagation-2026-10-28.md`. It also records the
  trap the old story hid: this chain's terminal `reject` at priority −1 makes
  NDS's own counters read zero on a build where the fragment is WORKING, so that
  zero is not evidence of a fault. Comment-only — every rule line is byte-identical
  ([#1133](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1133)).
