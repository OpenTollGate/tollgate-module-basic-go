- **The NDS enforcement fragment no longer explains itself with a refuted
  mechanism.** `20-nds-enforce.nft`'s header asserted that fw4's
  `inet fw4 forward` (priority 0) "accepts forwarded traffic before the
  `ip filter FORWARD` chain (same priority) ever sees it — leaving NDS's
  enforcement chain dead (0 packets)", and the story had been repeated as fact
  in code comments, reviews and docs. An `accept` verdict is final only for the
  base chain that issued it, so that causal step does not hold: measured on a
  three-namespace forwarding rig (nftables 1.0.9, kernel 7.0.0-38), an accept at
  priority 0 and at priority -1 each left a later same-hook chain counting 6/6
  forwarded packets, in both registration orders, while a `drop` at -1 starved
  it to 0. The observed symptom (NDS's chains read 0 packets, marks were set,
  clients ungated) stands; its cause is now recorded as **not established**,
  beside the standing hazard that same-priority base chains order
  unspecifiedly and the packet-path measurement that would settle it. The
  header also names the trap the old story created: because this chain's
  terminal `reject` at -1 runs ahead of NDS's priority-0 chain, NDS counters at
  0 are consistent with the fix *working*. Comment-only — the rule lines are
  byte-identical, and the packaging test's pinned digest is re-pinned with that
  proof. ([#1130](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1130)).
