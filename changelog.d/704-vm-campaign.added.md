- **The x86-64 VM acceptance lane exists and its first campaign is green.**
  A QEMU-on-ai-legion lane (`tests/vm-campaign/`, the conwrt-bench
  snapshot-boot pattern) boots pristine OpenWrt 25.12.0 x86-64 and drives the
  release acceptance checks over the serial console: artifact install with its
  dependency closure, post-install hostname/network convergence,
  `tollgate version` = v0.6.0-rc1, payment API `:2121` listening, portal files
  on `/www/tollgate/`, NoDogSplash on `gatewayport 2050`, the NTP pre-auth nft
  rule, and same-version reinstall idempotence — all PASS against the local
  build-sdk-package artifact (sha256 recorded in the lane README). Reboot
  persistence and the payment-flow scenarios (real payment, concurrent
  duplicate, kill-recovery — the #502 journal's first on-target exercise) are
  the lane's documented next steps
  ([#704](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/704)).
