//go:build testenv

package main

import (
	"os"
	"testing"
)

// TestMain removes the hermetic temp config dir provisioned by
// 000_test_env_testenv.go's init() after the test run completes, so repeated
// `go test -tags testenv` invocations do not accumulate stale config dirs
// under the system temp dir. If the caller supplied TOLLGATE_TEST_CONFIG_DIR
// explicitly, testEnvConfigDir is empty and nothing is removed.
//
// Only present under the `testenv` tag (paired with 000_test_env_testenv.go);
// plain `go test` uses the default TestMain.
func TestMain(m *testing.M) {
	code := m.Run()
	if testEnvConfigDir != "" {
		_ = os.RemoveAll(testEnvConfigDir)
	}
	os.Exit(code)
}
