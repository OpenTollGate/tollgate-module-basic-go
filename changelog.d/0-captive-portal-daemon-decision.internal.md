- **The captive-portal daemon is nodogsplash, and that is now a decision with reasons.**
  `docs/architecture/captive-portal-daemon-decision.md` records the build/adopt/contribute
  question the other NoDogSplash records assume: do not write our own daemon (30–90
  engineer-days for a component whose observed defects are overwhelmingly our own
  bookkeeping), do not migrate to openNDS yet (10–30 engineer-days against three verified
  blockers — two writers of the NDS config, and a convergence step that parses the running
  `iptables -S ndsRTR` while openNDS has been natively nftables since 10.1.0), contribute
  fixes upstream rather than fork (nodogsplash merged external PR #669 on 2026-09-25), and
  pin the shipped version explicitly (its own card, `t_4bd613b1` — `packaging/Makefile:82`
  names no version and `packaging/build-inputs.json` has no key for it). The record also
  states that the shipped 5.0.2 carries no state file, correcting the citation the startup
  record's second amendment door rests on (fixed separately in `t_f53c13c1`).
  ([#0](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/0))
