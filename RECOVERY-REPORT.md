# Bounded FIPS exit route recovery

## Scope and evidence

PRTA commit `67d4468`, `docs/evidence/fips-exit-ipip6-down-up.log`, records tunnel down removing the table-200 metric-100 default, while unreachable metric 42760 survives; link up does not restore the removed default. This slice adds only a pure module planner. No host/router networking changes, PRTA edits, executor, polling loop, or autonomous runtime reconciler.

The behavioral regression was first run against existing `BuildPlan` with the owned tunnel present and unused-resource flags false:

```
GOTOOLCHAIN=go1.26.10 go test ./fips_exit -run TestRecoveryMeasuredDownUp -count=1
--- FAIL: TestRecoveryMeasuredDownUp (0.00s)
    recovery_test.go:17: owned tunnel after down/up needs only missing default restored: ops=[] err=tunnel name, table and rule priority must be verified unused
FAIL
```

This demonstrates the fresh-install API's deliberate inability to recover, not a reason to weaken it. The same expected exact restoration is now tested through the separate recovery API. An initial command from the repository root failed because the package is in the nested `src` module; all recorded behavioral/gate commands run from `src`.

## API for the next PRTA worker

`BuildRecoveryPlan(c Config, e RecoveryEvidence, g GuardReadiness) ([]Operation, error)`.

The caller supplies trusted fresh evidence under its own namespace/execution lock:

- `Verified` means complete/current inventory with unknown attributes rejected; `OwnershipVerified` means prior module ownership established independently of matching names. `OwnedConfig` must equal `c` exactly, including identities, MTU, table and priority.
- `Environment` rechecks local daemon key/address, interface MTUs, address conflicts, policy ordering and outer route. Unused-resource flags are intentionally irrelevant here.
- `Tunnel` contains exact name, ipip6 mode, FIPS device, derived local/remote addresses, derived inner /32, MTU, confirmed `Up`, and `ExactParameters` (encaplimit none, hoplimit 64, unchanged interface identity, no unsupported parameters or extra addresses).
- `GuestRule` is the exact existing single IPv4 rule: configured priority, private table, guest ingress, from all and no additional selectors (`Exact`).
- `Routes` is the complete private-table IPv4 inventory, not a filtered list. Normalize to explicit `unicast`/`unreachable`, destination `default`, numeric table and metric, protocol `static`; `Exact` excludes unknown attributes/gateways/nexthops. The mandatory unreachable route has metric 42760 and empty device. The optional tunnel route has metric 100 and configured tunnel device. Anything else, including duplicates, fails closed.
- All six `GuardReadiness` protections must remain verified for this config. Missing/uncertain guards refuse, including when a tunnel route is present.

When only the unreachable default exists and link-up is confirmed, the sole output is:

```
ip -4 route add default dev exit-test table 200 metric 100 proto static
```

The healthy state returns no operations. Missing unreachable refuses; it is never repaired here. No unreachable deletion, route replace, main-table route, tunnel creation/rebinding, link-up, rule or guard mutation is emitted.

Existing `CollectLinkRoutes` remains fresh-install-only and **does not produce recovery evidence**. The next worker must gather/validate the above observations and independent ownership in the existing RED lane; do not manufacture attestations from config. Revalidate immediately before applying under the same lock; execute argv directly, abort on error, never retry by replacing a route. Fresh-install `ValidateOperations` does not validate this recovery plan: regenerate via `BuildRecoveryPlan` from fresh evidence instead. Finally read back both routes and rerun guest traffic/guard assertions. Actual router recovery remains unexecuted in this slice.

## Verification

Pinned `go version go1.26.10 linux/amd64`:

```
GOTOOLCHAIN=go1.26.10 go test -race -count=1 -coverprofile=<scratch>/fips-recovery.cover ./fips_exit
ok github.com/OpenTollGate/tollgate-module-basic-go/fips_exit 1.767s coverage: 86.1% of statements
GOTOOLCHAIN=go1.26.10 go vet ./fips_exit
# exit 0
GOTOOLCHAIN=go1.26.10 go tool cover -func=<scratch>/fips-recovery.cover
# BuildRecoveryPlan 100.0%; package total 86.1%
```

Tests cover the measured missing-default state, healthy no-op, disabled config, invalid config, unchanged fresh-install refusal, ownership/config/identity/MTU mismatch, missing/down/rebound tunnel, policy/outer-route uncertainty, rule drift, missing safety route, foreign/duplicate/unknown routes, and each absent guard. Coverage is focused package coverage, not whole-repository coverage.
