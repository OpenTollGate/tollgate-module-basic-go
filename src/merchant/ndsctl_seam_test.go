package merchant

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The package's ndsctl seam (#726).
//
// Merchant tests drive the REAL valve — src/valve execs `ndsctl` straight
// from PATH — so on a host without an ndsctl binary every gate open fails,
// a paid purchase's owed grant can never complete, and any test whose
// assertion needs a WORKING auth (not just a pending one) fails with
// payment-received-grant-pending. The valve package installs its own fake
// per test (installFakeNdsctl); merchant had no such seam, so this
// TestMain stages one for the whole package.
//
// It stages the seam the harnesses already share — tests/cloud-lab/
// fake-ndsctl.sh, the same drop-in tests/happy-path/run.sh reuses on
// purpose ("the cloud-lab seam, not a second one") — copied into a temp
// bin dir as `ndsctl` and prepended to PATH. No merchant-local fake is
// written: a second fake would drift from the one the on-target lanes
// assert against.
//
// The fake's logging contract (fake-ndsctl.sh): it appends AUTH/DEAUTH
// lines to $NDSCTL_LOG, which defaults to the SHARED /tmp/ndsctl.log.
// Like the happy-path harness, NDSCTL_LOG is pointed inside the staged
// temp dir, so this suite never writes the host's /tmp path.
//
// Tests that need a fake with different behavior (a wedged control
// socket, a scripted client list, a failing auth) still install their
// own: they PREPEND their dir via t.Setenv, which shadows this one for
// their duration only. A missing seam is fatal on purpose — the suite
// depends on it, and failing loudly here beats failing per-test on
// hosts without an ndsctl.
func TestMain(m *testing.M) {
	binDir, err := os.MkdirTemp("", "merchant-ndsctl-seam-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "stage fake ndsctl: %v\n", err)
		os.Exit(1)
	}

	// Tests run with the package directory as CWD; the shared seam lives
	// two levels up, outside this module, with the test harnesses.
	seam := filepath.Join("..", "..", "tests", "cloud-lab", "fake-ndsctl.sh")
	if err := stageFakeNdsctl(seam, filepath.Join(binDir, "ndsctl")); err != nil {
		fmt.Fprintf(os.Stderr, "stage fake ndsctl: %v — the merchant tests drive the real valve through the cloud-lab seam; run them from a full checkout\n", err)
		os.RemoveAll(binDir)
		os.Exit(1)
	}
	if err := os.Setenv("NDSCTL_LOG", filepath.Join(binDir, "ndsctl.log")); err != nil {
		fmt.Fprintf(os.Stderr, "stage fake ndsctl: %v\n", err)
		os.RemoveAll(binDir)
		os.Exit(1)
	}
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		fmt.Fprintf(os.Stderr, "stage fake ndsctl: %v\n", err)
		os.RemoveAll(binDir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(binDir)
	os.Exit(code)
}

// stageFakeNdsctl copies the cloud-lab fake into binDir/ndsctl with the
// executable bit set, the same way tests/happy-path/run.sh stages it.
func stageFakeNdsctl(src, dst string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0o755)
}
