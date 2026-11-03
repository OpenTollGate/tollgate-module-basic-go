# Q3 (TollGate pays the bond server) — code archaeology, 2026-10-20

VERDICT: the payment chain is FULLY IMPLEMENTED in tollgate-module-basic-go but
has NEVER been demonstrated with a real ecash payment. All existing tests use
mock merchants (no mint, no tokens on the wire).

Wired chain (verified by reading code + running suites):
  main.go:747/756  detector created + USM wired
  detector main.go:293  HandleGatewayConnected(iface, mac, gwIP)
  usm:59  probe kind-10021 advertisement -> NewUpstreamSession
  session.go:364  CreatePaymentTokenWithOverpayment  <- THE PAYMENT
  session.go:541  recoverFailedPaymentToken
  renewal: usage_tracker + threshold tests (#430 clamp)

MISSING for a YES on Q3:
  1. live two-node run: gateway advertises, client pays (real mint, real token)
  2. bond-server context: paid upstream = MPTCP bond server (peering-fips.md)
  3. ShouldSteer (mptcp_bonding/config.go:286): implemented+tested, ZERO
     production callers — steering is NOT payment-gated

Suites green after fix; dep fix: OpenTollGate PR #1030 (bech32 tidy).
