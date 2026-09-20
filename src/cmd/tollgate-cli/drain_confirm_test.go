package main

import (
	"os"
	"testing"
)

// Regression tests for issue #375's CLI automation defects.
//
//   1. Plain mode with stdin at EOF (`tollgate wallet drain cashu
//      </dev/null` over ssh) currently behaves like cancellation and the
//      process exits 0 — indistinguishable from success.
//   2. JSON mode reports operational failures as {"success": false} on
//      stdout while the process still exits 0, because printing the JSON
//      is treated as success.

// TestDrainCashuCmd_PlainMode_StdinEOF_ReturnsError runs the drain
// command the way an orchestrator would over ssh with no stdin: the
// confirmation prompt reads EOF. Whatever the decision is internally
// (cancellation), the command must NOT exit 0, otherwise callers cannot
// distinguish "cancelled" from "drained".
func TestDrainCashuCmd_PlainMode_StdinEOF_ReturnsError(t *testing.T) {
	origStdin := os.Stdin
	jsonOutput = false
	defer func() {
		os.Stdin = origStdin
		jsonOutput = false
	}()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	os.Stdin = devNull

	if err := drainCashuCmd.RunE(drainCashuCmd, []string{}); err == nil {
		t.Fatal("drain cashu with stdin EOF returned nil error (exit 0); non-interactive invocation must exit non-zero (issue #375 bug 1)")
	}
}

// TestDrainCashuCmd_JSONMode_OperationalFailure_ReturnsError exercises
// `tollgate --json wallet drain cashu` against an unreachable service
// (no socket). The client prints {"success": false, ...} — an
// operational failure — and MUST propagate a non-nil error so the
// process exits non-zero instead of 0.
func TestDrainCashuCmd_JSONMode_OperationalFailure_ReturnsError(t *testing.T) {
	jsonOutput = false
	defer func() { jsonOutput = false }()

	// The client honors TOLLGATE_TEST_CONFIG_DIR for its socket path
	// (mirroring the service side); a fresh temp dir guarantees no
	// socket exists, so the command deterministically observes an
	// operational failure.
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())

	jsonOutput = true
	if err := drainCashuCmd.RunE(drainCashuCmd, []string{}); err == nil {
		t.Fatal("--json drain with unreachable service returned nil error (exit 0) despite reporting success:false (issue #375 bug 2)")
	}
}
