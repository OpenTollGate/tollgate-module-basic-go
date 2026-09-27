package merchant

import (
	"os"
	"strings"
	"testing"
	"unicode"
)

// TestNoMerchantTestFileIsGatedOnTestenv keeps this package's tests inside the
// lane that actually runs them.
//
// .github/workflows/test.yml's `go-test` matrix runs each module with
// `./... -v -count=1 -race` and **no tags**, so a test file whose build
// constraint requires `testenv` is never compiled there: its tests are absent
// from the run, the lane reports green, and the coverage everyone assumes
// exists does not. That is exactly what had happened to the notice, token-flow
// and log-hygiene tests in this package (and to the token fixtures they need),
// and several PRs had been adding untagged duplicates to get coverage — which
// leaves the tagged originals dead and the suite slowly duplicated.
//
// The gate is banned rather than repaired because nothing in this package needs
// it: the `testenv` tag exists for the root package, whose `init()` reads
// /etc/tollgate/config.json, and it is a no-op for the subpackages
// (CONTRIBUTING.md). Where a build constraint is genuinely load-bearing here it
// is about the wallet (`!cdk_wallet`), not about the environment — and a
// wallet-parity gate is a lane someone has to add, not a tag to sprinkle. The
// `integration` tag stays allowed: tests/... lanes opt into it explicitly.
//
// A comment cannot enforce this; a future contributor re-adding the tag would
// be re-creating the dead lane silently, which is what this test fails on.
func TestNoMerchantTestFileIsGatedOnTestenv(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	scanned := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}

		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++

		// A build constraint must precede the package clause, so stop there.
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(line, "package ") {
				break
			}
			if !strings.HasPrefix(line, "//go:build") {
				continue
			}
			expr := strings.TrimSpace(strings.TrimPrefix(line, "//go:build"))
			for _, term := range strings.FieldsFunc(expr, func(r rune) bool {
				return !(r == '!' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r))
			}) {
				if term == "testenv" {
					offenders = append(offenders, name+": //go:build "+expr)
				}
			}
			break
		}
	}

	// Guard the guard: if the working directory is not the package directory,
	// the scan finds nothing and the test would pass vacuously.
	if scanned < 10 {
		t.Fatalf("scanned only %d *_test.go files — not running in the merchant package directory?", scanned)
	}

	if len(offenders) > 0 {
		t.Errorf("%d test file(s) in this package require the `testenv` build tag, so CI's no-tags go-test lane never compiles or runs them:\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
