- **The payout routine and the lightning-quote janitor now die with the
  merchant.** `StartPayoutRoutine` spawned one payout goroutine per accepted
  mint (seven on the default config) and the quote janitor one more, both on
  bare tickers with no stop channel — so a merchant that was shut down (the
  runtime downgrade path) kept attempting payouts against its dead wallet
  and cleaning quote state nobody read, one leaked goroutine set per
  downgrade/upgrade cycle, the same family as the sweep leak
  ([#733](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/733))
  ([#888](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/888)).
