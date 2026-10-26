- **`make test` — a worktree- and clone-safe test runner.** Raw `go
  test ./src/<pkg>` from the repo root fails with "main module does not
  contain package" in every checkout type (fresh clones and git
  worktrees alike — the root module shares src/'s import path but
  contains no packages), and a root-level `go build ./...` silently
  exits 0 while building nothing. `make test` (scripts/go-test.sh) runs
  the same per-module race tests as the battery from any directory,
  with module selectors (`make test M=src/cli`), and a root guard
  package now makes a root-level `go test ./...` fail with the
  supported entry points instead of a no-signal "matched no packages"
  warning
  ([#989](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/989)).
