//go:build testenv

package tollwallet

// Local-fidelity bring-up validation for the ext4-loop wallet storage
// (docs/architecture/wallet-storage-mmap-incapable-overlay-decision.md,
// issue #1134's live-bring-up leg at nandsim fidelity): the SHIPPED
// wallet-storage.sh policy runs against a REAL jffs2 mount, and the REAL
// wallet must initialize inside the resulting ext4 loop image — where the
// same binary on the bare jffs2 fails with the pinned EINVAL.
//
// Gated on WALLET_JFFS2_DIR (a jffs2 mountpoint) + WALLET_JFFS2_SETUP
// (the repo's packaging/files/usr/lib/tollgate/wallet-storage.sh). Run
// from a host with nandsim/mtdblock/jffs2; skips everywhere else.

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWalletInitializesInsideExt4LoopOnJffs2(t *testing.T) {
	jffs2Dir := os.Getenv("WALLET_JFFS2_DIR")
	setupLib := os.Getenv("WALLET_JFFS2_SETUP")
	if jffs2Dir == "" || setupLib == "" {
		t.Skip("WALLET_JFFS2_DIR/WALLET_JFFS2_SETUP not set — local-fidelity jffs2 bring-up is host-gated")
	}

	// The negative control first, with the same binary: bare jffs2 must
	// fail with the pinned mmap EINVAL (not some other error).
	bareDir := jffs2Dir + "/bare"
	if err := os.MkdirAll(bareDir, 0o700); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	if _, err := New(bareDir, []string{"http://jffs2.invalid"}, false); err == nil {
		t.Fatal("bare jffs2 wallet init must fail (the pinned EINVAL class)")
	} else if !strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("bare jffs2 failure is not the pinned EINVAL class: %v", err)
	}

	// The shipped policy: run ensure_wallet_storage against the jffs2 dir.
	script := "cd / && . " + setupLib + " && WALLET_DIR=" + jffs2Dir + " ensure_wallet_storage"
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("ensure_wallet_storage failed: %v\n%s", err, out)
	}

	// The mountpoint must now exist and be a real ext4 mount.
	mp := jffs2Dir + "/wallet"
	st, err := os.Stat(mp)
	if err != nil || !st.IsDir() {
		t.Fatalf("wallet mountpoint missing: %v", err)
	}
	if fs := statFSType(t, mp); !strings.HasPrefix(fs, "ext2") { // stat -f reports ext4 as ext2/ext3 on util-linux ≥2.38
		t.Fatalf("wallet dir filesystem = %s, want the ext4 loop image (stat reports ext2*)", fs)
	}

	// The positive: the REAL wallet initializes INSIDE the image.
	w, err := New(mp, []string{"http://jffs2.invalid"}, false)
	if err != nil {
		t.Fatalf("wallet must initialize inside the ext4 loop image: %v", err)
	}
	w.Shutdown()
}

func statFSType(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("stat", "-f", "-c", "%T", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("stat -f: %v", err)
	}
	return strings.TrimSpace(string(out))
}
