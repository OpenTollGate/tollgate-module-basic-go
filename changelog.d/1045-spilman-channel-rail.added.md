- **Spilman channel payment rail.** Adds a channel rail so a router can settle
  many small increments against an upstream without a mint round trip per
  purchase: the mint is touched at channel open and settle only. The rail is a
  nested module (`src/spilman`) behind a `//go:build spilman` tag pair, so the
  default `CGO_ENABLED=0` static build is unchanged. Billing uses the exact
  parity rate `22021` (at `22020` the exact ceil is 1001 msat per 21 MiB step,
  which rounds up to 2 sat instead of 1 — a 100% overcharge). Payment acceptance
  uses CAS against the exact previous balance rather than the monotonic max in
  the upstream Go host, which `INTEGRATION.md` warns double-credits on a retry
  carrying the same balance. The buyer path is deliberately not implemented:
  the Go client binding exports no recovery, refund, restore or unilateral-exit
  function, so buyer methods return a typed error naming the gap instead of
  succeeding silently
  ([#1045](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1045)).
