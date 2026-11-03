- **Peering support for the admin board.** Upstream scans now carry the
  TollGate peer metadata (reseller, internet, open network, mint URL, pubkey);
  `tollgate upstream known` exposes peer history; `tollgate upstream status`
  reports the live upstream session, allotment, usage, spend, price, mint and
  bootstrap phase; and `tollgate upstream disconnect` stops peering by
  switching to a verified known-good non-TollGate upstream, restoring the peer
  if every fallback fails.
