- **Unit-aware pricing for operator-issued ecash**: a mint config may declare
  `unit` (empty means `sat`), and the gateway now prices a token by its own unit,
  refusing a unit mismatch instead of mispricing it — a 64 `mb` proof used to be
  priced as 64 sats. `wallet issue <amount> <unit> [mint-url]` issues
  operator-run custom-unit ecash on request, over the existing daemon socket
  (no new network listener). A token whose unit cannot be priced is refused with
  the reason, never accepted at a guessed scale.
  ([#1040](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1040))
