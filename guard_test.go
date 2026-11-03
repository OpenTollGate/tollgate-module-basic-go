package tollgate

import "testing"

// TestRepoRootRunsNoTests must FAIL: it is the fence that keeps
// `go test ./...` at the repo root from reporting a false green.
//
// The repository is a multi-module tree (a go.mod in src/ and in every
// src/ subpackage); the repo root's own module contains only this
// guard. A root-level `go test ./...` therefore matches this package
// and nothing else. If this test ever passes — or vanishes together
// with the root package — the root false green of issue #961 is back:
// a root-level test run would again report success while testing
// nothing.
func TestRepoRootRunsNoTests(t *testing.T) {
	t.Fatalf(`go test at the repo root runs NO tests: src/ is a multi-module
tree, so "./..." here matches only this guard package (#961).

Run tests instead:
  make test                   all modules, from any directory, any checkout
  make test M=src/cli         one module
  scripts/go-test.sh src/cli  the same, without make
  cd src && go test ./...     the src root module directly

The full gate (gofmt+vet+build+race tests in every module):
  make go-battery`)
}
