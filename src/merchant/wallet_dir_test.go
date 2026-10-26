package merchant

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// walletDirFor picks the loop-image wallet directory ONLY when it is a real
// mountpoint (docs/architecture/
// wallet-storage-mmap-incapable-overlay-decision.md). A plain empty
// directory next to the config must never move the wallet — and neither
// must the inode half of the mountpoint folklore: a directory and its
// parent ALWAYS differ in inode; device identity is the signal.

func TestWalletDirForPrefersBaseWithoutMount(t *testing.T) {
	cm, testDir := setupTestConfigManager(t)

	// A plain (unmounted) wallet subdir exists — must be ignored.
	if err := os.MkdirAll(filepath.Join(testDir, "wallet"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := walletDirFor(cm); got != testDir {
		t.Fatalf("an unmounted wallet/ directory must not move the wallet: got %s want %s", got, testDir)
	}
}

func TestWalletDirForWithoutSubdir(t *testing.T) {
	cm, testDir := setupTestConfigManager(t)
	if got := walletDirFor(cm); got != testDir {
		t.Fatalf("without a wallet/ directory the config dir is the wallet dir: got %s want %s", got, testDir)
	}
}

func TestIsMountpoint(t *testing.T) {
	// "/" is the degenerate case its own way: its ".." is itself, same
	// device — the dev rule answers false, which is fine because nothing
	// consumes "/" as a wallet subdir.
	if isMountpoint("/") {
		t.Fatal("root's parent is root — the dev rule must answer false, not panic on folklore")
	}
	plain := t.TempDir()
	if isMountpoint(plain) {
		t.Fatal("a plain directory is not a mountpoint — the inode half of the folklore crept back")
	}
	if isMountpoint(filepath.Join(plain, "nope")) {
		t.Fatal("a missing directory is not a mountpoint")
	}

	// The positive case needs a mount boundary with a DIFFERENT device —
	// the production shape is an ext4 filesystem on a loop device (a bind
	// mount shares the source's superblock and st_dev, invisible to this
	// heuristic; documented on isMountpoint). Only meaningful where the
	// test runner may configure a loop device; CI without it skips — the
	// shell suite and the bench QEMU lane carry the loop-image end-to-end.
	if os.Geteuid() != 0 {
		t.Skip("positive mountpoint case needs root (loop device)")
	}
	img := filepath.Join(plain, "wallet.ext4")
	if err := exec.Command("truncate", "-s", "4M", img).Run(); err != nil {
		t.Skipf("truncate unavailable: %v", err)
	}
	if err := exec.Command("mkfs.ext4", "-F", "-q", img).Run(); err != nil {
		t.Skipf("mkfs.ext4 unavailable: %v", err)
	}
	out, err := exec.Command("losetup", "-f", "--show", img).Output()
	if err != nil {
		t.Skipf("losetup unavailable: %v", err)
	}
	loop := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("losetup", "-d", loop).Run() })
	mnt := filepath.Join(plain, "wallet")
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := syscall.Mount(loop, mnt, "ext4", syscall.MS_NOATIME, ""); err != nil {
		t.Skipf("loop mount unavailable here: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Unmount(mnt, 0) })
	if !isMountpoint(mnt) {
		t.Fatal("an ext4 loop mount IS a mountpoint — the dev check missed a real boundary")
	}
}

// The seam must be the ONLY spelling: every wallet construction path derives
// its directory from walletDirFor, so a new construction site that bypasses
// it shows up here (grep discipline, not enumeration).
func TestWalletDirSingleSeam(t *testing.T) {
	for _, f := range []string{"merchant.go", "merchant_degraded.go"} {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			// The hazard pattern is a WALLET-DIR derivation outside the
			// seam; walletDirFor's own `base :=` line is the seam itself.
			if strings.Contains(line, "walletDirPath := filepath.Dir(configManager.ConfigFilePath)") {
				t.Errorf("%s:%d derives a wallet dir directly — route it through walletDirFor (one seam)", f, i+1)
			}
		}
	}
}
