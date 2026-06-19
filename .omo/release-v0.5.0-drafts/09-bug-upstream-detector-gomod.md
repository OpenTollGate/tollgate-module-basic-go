<!--
Target: New issue on https://github.com/OpenTollGate/tollgate-module-basic-go
Title: "bug(build): upstream_detector/go.mod missing replace directives — go mod tidy fails, module won't build standalone"
Labels: bug, area: build
-->

## Problem

`src/upstream_detector/go.mod` is missing `replace` directives for
`merchant_types` and `utils`. Those two modules were added as transitive
dependencies when `upstream_session_manager` was refactored to depend on
them. `go mod tidy` fails, and the module can't be built standalone
(off-router, outside the workspace).

This is one of the "minor on `main`: `upstream_detector/go.mod` needs
`go mod tidy`" caveats in the tag-readiness report (#169) — but it's
actually a hard error, not a tidy-up.

## Repro

```sh
cd src/upstream_detector
go mod tidy
```

Output:

```
go: github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_detector imports
    github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager imports
    github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types: reading github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types/go.mod at revision src/merchant_types/v0.0.0: unknown revision src/merchant_types/v0.0.0
go: github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_detector imports
    github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager imports
    github.com/OpenTollGate/tollgate-module-basic-go/src/utils: reading github.com/OpenTollGate/tollgate-module-basic-go/src/utils/go.mod at revision src/utils/v0.0.0: unknown revision src/utils/v0.0.0
```

## Root cause

`src/upstream_detector/go.mod` currently has:

```
replace github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager
replace github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol => ../tollgate_protocol
replace github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager => ../upstream_session_manager
```

But `src/upstream_session_manager/go.mod` pulls in `merchant_types` and
`utils` (and has the proper `replace` directives for them):

```
require (
    github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types v0.0.0
    github.com/OpenTollGate/tollgate-module-basic-go/src/utils v0.0.0
)

replace github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types => ../merchant_types
replace github.com/OpenTollGate/tollgate-module-basic-go/src/utils => ../utils
```

`upstream_detector` transitively imports those, so its `go.mod` needs the
same `replace` directives.

## Fix

Add the two missing `replace` directives to `src/upstream_detector/go.mod`,
then run `go mod tidy`:

```diff
 replace github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager
+replace github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types => ../merchant_types
 replace github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol => ../tollgate_protocol
 replace github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager => ../upstream_session_manager
+replace github.com/OpenTollGate/tollgate-module-basic-go/src/utils => ../utils
```

After that, `go mod tidy` is a no-op (no churn) and the module builds
standalone.

## Acceptance criteria

- [ ] `cd src/upstream_detector && go mod tidy` is a clean no-op
- [ ] `cd src/upstream_detector && go build ./...` works off-router (no
      workspace)
- [ ] `cd src/upstream_detector && go test ./...` works off-router
- [ ] Tier 0 in the tag-readiness suite reports 14/14 modules instead of
      12/14

## Related

- Tag-readiness report (#169) — "Minor on `main`: `upstream_detector/go.mod`
  needs `go mod tidy`".
- The same pattern probably needs an audit across other modules that import
  `upstream_session_manager`. Quick check: at minimum
  `src/upstream_session_manager` itself has the directives (verified);
  other modules should be checked in the same PR.
- Release plan #154 — pre-release hygiene item.
