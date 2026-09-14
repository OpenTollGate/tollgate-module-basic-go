//go:build testenv

package main

import "os"

// testEnvConfigDir holds the hermetic temp config dir auto-provisioned for
// this test run (empty when the caller supplied TOLLGATE_TEST_CONFIG_DIR
// explicitly). testenv_cleanup_test.go's TestMain removes it after the run.
var testEnvConfigDir string

// init provisions a fresh, hermetic temp config dir before main.go's init()
// runs, so `go test -tags testenv` passes off-router (CI, dev machines)
// without depending on /etc/tollgate/config.json existing on the router.
//
// A fresh directory per invocation avoids the stale-state and concurrent-run
// collisions that the previous fixed path ($TMPDIR/tollgate-main-test)
// suffered from — that is what made the root-module test non-hermetic.
//
// If TOLLGATE_TEST_CONFIG_DIR is already exported (e.g. CI pointing at a
// pinned dir), it is respected unchanged and no temp dir is created.
//
// This file is guarded by the `testenv` build tag so it is never compiled
// into the production binary (enforced by tests/contract/build-purity.sh).
func init() {
	if os.Getenv("TOLLGATE_TEST_CONFIG_DIR") != "" {
		return
	}
	dir, err := os.MkdirTemp("", "tollgate-testenv-")
	if err != nil {
		panic("testenv: failed to create temp config dir: " + err.Error())
	}
	testEnvConfigDir = dir
	os.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
}
