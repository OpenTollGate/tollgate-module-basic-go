- **Gate cleanup clears an abandoned close streak instead of inheriting one.**
  The valve's per-gate close-streak state outlives the test that spent it: an
  abandoned streak blocks every later `CloseGate` of that MAC behind
  `ErrGateCloseAbandoned`, and plain `CloseGate` cannot clear it — so the
  unmeterable abandoned-close test passed its first `-count` iteration and
  failed every later one in milliseconds (9/10 in isolation, 2-of-3 iterations
  in full-suite runs). `closeGateCleanup` now falls back to
  `ReconcileGateClose` — the reconciliation path the design names as the only
  way to un-abandon a gate — and the test lifts its `failDeauth` marker in a
  LIFO-ordered cleanup so that reconciliation can succeed
  ([#841](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/841)).
  ([#842](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/842)).
