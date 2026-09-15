# Nostr CI (ngit-ci) for tollgate-module-basic-go

`.ngit/act/workflows/` is read by the **ngit-ci** coordinator, so the checks run
on **ngit/Nostr pushes** as well as GitHub. `go-ci.yml` mirrors the GitHub
`Test` workflow:

| Job | What | Why |
|---|---|---|
| `go-test` | `go test ./... -count=1` per module (config_manager, utils, cmd/tollgate-cli, lightning, tollgate_protocol, tollwallet, valve, wireless_gateway_manager) | unit coverage across the standalone-buildable modules |
| `main-test` | `go test -count=1 -tags testenv .` in `src/` | the root module + testenv integration surface |
| `build-purity` | `bash tests/contract/build-purity.sh` | enforces the build-purity contract |

`runs-on: ubuntu-latest`; Go is pinned from `src/go.mod`; no secrets, no
services.

The full fidelity runner is the GitHub `Test` workflow
(`.github/workflows/test.yml`), which additionally uses the repo's
`go-test-summary.sh` helper and `-race`. This ngit variant keeps the runner
dependency-free so it works inside the ngit-ci sandbox.
