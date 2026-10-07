- **The Go battery no longer needs an `ndsctl` on PATH (#726).** Merchant
  tests that reach the real valve — `src/valve` execes `ndsctl` straight from
  PATH — failed on hosts without the binary: every gate open failed, the owed
  grant could never complete, and the third-submission-must-grant assertion of
  `TestPurchaseSessionGuardHoldsThroughTheOutcomeUnknownWindow` came back
  `payment-received-grant-pending`. A `TestMain` in `src/merchant` now stages
  the seam the harnesses already share — `tests/cloud-lab/fake-ndsctl.sh`,
  the drop-in `tests/happy-path/run.sh` reuses on purpose — into a package
  temp bin dir prepended to PATH, with `NDSCTL_LOG` pointed inside that dir
  per the fake's logging contract. No second fake is written, and tests that
  script their own ndsctl behavior keep shadowing it via their per-test
  `t.Setenv` prepend.
  ([#729](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/729)).
