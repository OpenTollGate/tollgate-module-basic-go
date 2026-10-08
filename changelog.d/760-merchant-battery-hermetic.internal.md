- **The Go battery is green on hosts without an `ndsctl`.** Two merchant-suite
  defects made `make go-battery` fail off-router and flake under load: the
  late-outcome test's record picker raced the grant-bookkeeping log lines (it
  read the last line naming the reference, but the late-success path follows
  its outcome record with bookkeeping that names the reference too), and the
  duplicate-guard test asserted a real session grant without installing the
  suite's fake `ndsctl`, so it needed a host binary. Both are test-only fixes;
  `go-battery: 16 modules green` on a machine with no `ndsctl` anywhere on
  `PATH`
  ([#760](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/760)).
