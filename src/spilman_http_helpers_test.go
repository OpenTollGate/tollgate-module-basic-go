package main

// Test helpers for the Spilman HTTP surface tests.
//
// Kept separate from the assertions so the assertions read as statements about
// behaviour rather than about fixture plumbing.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// resetSpilmanRailForTest clears the lazily-built rail so a test can switch the
// config between cases. Without this the first case's rail would leak into the
// next one and "disabled" would appear to answer.
func resetSpilmanRailForTest() {
	spilmanMu.Lock()
	defer spilmanMu.Unlock()
	spilmanOnce = nil
	spilmanErr = nil
}

// withSpilman installs a config manager holding the given Spilman block, and
// restores whatever was there on cleanup.
//
// WHY THE ENV VAR IS CLEARED AND RESTORED HERE
// ---------------------------------------------
// `NewConfigManager` rewrites any path it is given into
// `$TOLLGATE_TEST_CONFIG_DIR/<basename>` (config_manager.go:26-34). The
// `testenv` build tag sets that variable once per PROCESS to a single temp dir
// (000_test_env_testenv.go:41), so every test in the package would otherwise
// share ONE config.json — last writer wins, and a test that just enabled the
// rail sees whatever a later test wrote.
//
// That is not theoretical: with the shared dir, /spilman/params answered 404
// with `spilman rail is not enabled` even after this helper set Enabled=true,
// because another test's write landed in the same file. It only reproduced
// under `-tags testenv`, which is why it survived a green plain `go test` run.
//
// Clearing the variable for the duration of the helper's own manager
// construction makes the per-test path actually mean what it says. It is
// restored immediately so nothing else in the package is affected.
func withSpilman(t *testing.T, enabled bool, dbPath string) {
	t.Helper()
	prevCM := configManager

	// t.Setenv restores the previous value automatically at test cleanup, and
	// also marks the test as non-parallel, which is correct: this mutates a
	// package-level global (configManager).
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Start from whatever EnsureDefaultConfig produces, then set only the block
	// under test — so this fixture cannot drift from the real default schema.
	base, err := config_manager.EnsureDefaultConfig(cfgPath)
	if err != nil {
		t.Fatalf("seed default config: %v", err)
	}
	base.Spilman = config_manager.SpilmanConfig{
		Enabled:       enabled,
		ChannelDBPath: dbPath,
	}
	if err := config_manager.SaveConfig(cfgPath, base); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	m, err := config_manager.NewConfigManager(cfgPath,
		filepath.Join(dir, "install.json"), filepath.Join(dir, "identities.json"))
	if err != nil {
		t.Fatalf("build config manager: %v", err)
	}
	configManager = m
	resetSpilmanRailForTest()
	t.Cleanup(func() {
		configManager = prevCM
		resetSpilmanRailForTest()
	})
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }
