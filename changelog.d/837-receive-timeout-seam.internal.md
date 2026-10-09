- **The Receive deadline seam is per merchant, not a package-level var.**
  `receiveTimeout` was a mutable package global — the same shape the
  pre-flight probe seam abandoned after its own `-race` catch — so a harness
  shrinking it raced the deadline read of a `PurchaseSession` goroutine an
  earlier test had leaked, and the race detector killed
  `TestLateReceiveOutcomeIsRecordedWhenReceiveCompletesAfterTheDeadline`
  roughly one targeted run in five (#821). The deadline is now
  `Merchant.receiveTimeout` with the old 30 s as its zero-value default;
  harnesses narrow it on their own instance, so no shared state remains to
  race. A 25-iteration targeted soak of the racing pair runs clean.
  ([#837](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/837)).
