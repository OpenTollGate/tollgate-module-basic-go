// Package tollgate anchors the repository-root Go module.
//
// The root module deliberately contains no product code: every real
// package lives in a nested module under src/ (see AGENTS.md and
// CONTRIBUTING.md). Before this package existed, `go test ./...` run
// from the repo root matched zero packages and still exited 0 —
// reporting success while testing nothing, which is how the breakage
// reported in issue #961 was mistaken for a worktree-only problem.
// The guard test in this package turns that silent pass into a failure
// that names the supported entry points.
//
// Run tests with `make test` (from any directory, git worktrees
// included), `scripts/go-test.sh [module...]`, or from inside a module
// directory (`cd src && go test ./...`). The full gate is
// `make go-battery`.
package tollgate
