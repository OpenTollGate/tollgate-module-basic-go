- **Gate the exit's test-only `?bind=` hook behind `-allow-test-bind`.** The
  exitserver read the buyer's claimed peer address from a query parameter, so any
  caller could name any peer's tunnel and be admitted free — the exact
  impersonation the claim signature exists to prevent. It is now off unless an
  operator turns it on, logs a warning when it is, and the real path refuses a
  loopback peer because a listener that cannot see who is calling must not guess.
  ([#1266](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1266))
