package config_manager

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The #505 audit downgraded two plain os.WriteFile sites (identities.json —
// which carries the Nostr signing key and the operator's payout Lightning
// addresses — and install.json) to the same crash-consistency contract
// config.json already had after #402. These tests pin that contract for all
// three files through writeFileDurably: whole-file-or-old-file atomicity via
// temp+fsync+rename, the EBUSY/pinned-inode fallback, 0600 permissions and
// no temp-file litter.

func TestSaveIdentitiesIsAtomicAndCleansUpItsTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identities.json")

	if err := SaveIdentities(path, NewDefaultIdentitiesConfig()); err != nil {
		t.Fatalf("SaveIdentities: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat identities.json: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("identities.json perms = %o, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp file litter after successful save: %v", names)
	}
}

func TestSaveIdentitiesFallsBackToInPlaceWhenRenameIsImpossible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identities.json")
	identities := NewDefaultIdentitiesConfig()
	if err := SaveIdentities(path, identities); err != nil {
		t.Fatalf("seed identities.json: %v", err)
	}
	seed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}

	prev := renameIntoPlace
	renameIntoPlace = func(_, _ string) error { return syscall.EBUSY }
	t.Cleanup(func() { renameIntoPlace = prev })

	identities.ConfigVersion = "v0.0.2-rotated"
	if err := SaveIdentities(path, identities); err != nil {
		t.Fatalf("SaveIdentities with pinned inode (EBUSY rename): %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) == string(seed) {
		t.Error("in-place fallback did not write the new content")
	}
	if !strings.Contains(string(got), "v0.0.2-rotated") {
		t.Errorf("in-place fallback wrote unexpected content: %s", got)
	}
}

func TestSaveInstallConfigIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.json")

	if err := SaveInstallConfig(path, NewDefaultInstallConfig()); err != nil {
		t.Fatalf("SaveInstallConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat install.json: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("install.json perms = %o, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp file litter after successful save: %d entries", len(entries))
	}
}

// A failed rename (not EBUSY — a genuinely failing filesystem) must leave
// the previous file intact: the atomicity contract is "the old file or the
// new file, never a truncated half of either".
func TestWriteFileDurablyKeepsOldFileWhenEverythingFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.json")
	if err := os.WriteFile(path, []byte(`{"seed":true}`), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	prev := renameIntoPlace
	renameIntoPlace = func(_, _ string) error { return syscall.EIO }
	inPlacePrev := writeInPlaceDurably
	t.Cleanup(func() {
		renameIntoPlace = prev
		writeInPlaceDurably = inPlacePrev
	})
	writeInPlaceDurably = func(string, []byte) error { return syscall.EIO }

	if err := writeFileDurably(path, []byte(`{"seed":false}`)); err == nil {
		t.Fatal("writeFileDurably should fail when both rename and fallback fail")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != `{"seed":true}` {
		t.Errorf("old file was not preserved on total failure: %s", got)
	}
}
