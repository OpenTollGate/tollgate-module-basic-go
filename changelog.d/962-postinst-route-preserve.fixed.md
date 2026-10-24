- **The installer no longer wipes manually-configured default routes on
  statically-routed seats (#962).** The postinst's
  `/etc/init.d/network restart` (run so uci-defaults apply to a running
  router) re-applies UCI-backed routes only: a default route added by hand —
  the statically-routed bench/CI seat pattern, with no DHCP WAN and no uci
  gateway — was silently wiped the moment the installer printed
  "Restarting network...", killing egress after install. Reproduced live on
  the vlab seat: runtime `default via 10.99.99.2 dev br-lan`, postinst,
  `ip route show default` empty.

  Both postinst recipes (the standalone `packaging/postinst` and the inline
  heredoc in `packaging/Makefile` that the feed's package mirrors verbatim) now
  snapshot the default routes before the first restart and re-add whatever the
  restart(s) did not bring back — idempotently, so a route netifd re-installed
  identically is not duplicated. The Makefile's restore runs after the
  recheck_private_subnet block, which may restart the network a second time.
  Pinned offline by `tests/packaging/postinst-default-route-preserve_test.sh`
  (contract greps on both recipes + a functional run of the restore against a
  mocked `ip`), and validated live on the vlab seat: the same postinst flow
  that wiped the route before the fix leaves it in place after.
