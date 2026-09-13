# .ngit — Nostr CI (ngit-ci)

ngit-ci reads workflows from `.ngit/act/workflows/` **only** — files under
`.github/workflows/` are detected but never executed. GitHub Actions is
untouched and keeps running; the two systems run side by side.

## What runs, and why

| File | Checks |
| --- | --- |
| `act/workflows/go-test.yml` | The pre-PR sequence documented in [AGENTS.md](../AGENTS.md), run from `src/`: `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test -race -count=1 -tags testenv ./...` (last step through `.github/scripts/go-test-summary.sh`). |
| `act/workflows/test.yml` | The existing port of `.github/workflows/test.yml`: per-module Go tests over the module matrix, the main-package `testenv` test, `js-schema-lint`, `build-purity`, and the dependency/import-path checks. |

Two files because they cover different things: `test.yml` tests the nested
modules (which `./...` from `src/` does not reach — they are separate modules)
and the `tests/contract` checks, while `go-test.yml` runs the documented gate
that `test.yml` omits (`gofmt`, `go vet`, `go build`, and a race-enabled run of
the root module). Keeping them separate keeps a failure attributable to one
suite and leaves the ported file a faithful copy of its GitHub twin.

The Go toolchain is pinned in `go-test.yml` as `go-version: "1.25.0"` — the
version `src/go.mod` declares. The explicit version is deliberate: resolving a
version *file* is what failed first inside this deployment's `act` image, and a
literal version takes a shorter path through `actions/setup-go`.

## Triggers

Both files run on **push to `main`** and on **pull requests**. `schedule` is
not supported by ngit-ci and is not used.

**This deployment runs the `request-required` policy.** Ordinary push and PR
runs do not start until a maintainer publishes a standing **Service Request
(kind 9843)** naming the coordinator and this repository; until then the
coordinator logs `Skipping push trigger until an authorized Service Request is
observed`. Manual triggers (`ngit ci trigger`, or gitworkshop's retry button)
are one-shot and bypass the gate.

```bash
nak event --sec <maintainer-nsec> -k 9843 -c "" \
  -t "a=30617:<maintainer-hex>:tollgate-module-basic-go" \
  -t "p=<coordinator-hex>" \
  wss://relay.ngit.dev wss://gitnostr.com
```

## Reading results

- `ngit ci status <commit|pr>` — job and workflow state for a commit or PR.
  (Older `ngit` builds lack the `ci` subcommand; gitworkshop.dev shows the same
  results against the commit or PR, and `nak` reads them directly.)
- Published kinds: **39842** workflow progress, **9841** job result (carries
  the job's log tail), **9842** workflow result/conclusion. Each names the
  commit, the workflow path, and the SHA-256 of the workflow file's content, and
  the 9842 event's `q` tag quotes the Service Request for a gated run.

```bash
# results from the coordinator that ran the job
nak req -k 9842 -a "$COORD_HEX" -l 5  wss://relay.ngit.dev   # conclusions
nak req -k 9841 -a "$COORD_HEX" -l 20 wss://relay.ngit.dev   # per-job + log tail
```

## State of the checks right now

`gofmt -l .` reports three files on `main` (`config_manager/config_schema.go`,
`merchant/lightning_state_test.go`, `merchant/quotes_wireformat_test.go`), so
the `gofmt` step fails until they are formatted — `cd src && gofmt -w .`. The
other three commands pass (verified locally, `go1.25`/`go1.26`).

## Not run here (deliberately)

- **Router-visible behaviour.** Nothing exercises a real router — captive
  portal, firewall, Wi-Fi, `ndsctl`. As AGENTS.md says, unit tests passing does
  not mean a router-visible change works.
- **Release/packaging.** `build-package.yml` (OpenWrt `.ipk`/`.apk`
  cross-compiles, Blossom uploads, kind-1063 announcements) is a release
  pipeline, not a test suite, and is not ported.
- **act-image caveat.** ngit-ci runs jobs in `catthehacker/ubuntu:act-*`
  images, which are lighter than GitHub runner VMs. A red run there is an
  environment gap until the job's log tail says otherwise; `go-test.yml` prints
  `go version` / `go env GOROOT GOVERSION` first so that is visible.
