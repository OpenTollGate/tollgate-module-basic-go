//go:build cdk_wallet && testenv

package tollwallet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #505 audit: the mnemonic is the wallet's entire key material, and its
// first-generation write must be crash-atomic (whole file or no file) —
// a torn seed bricks the wallet at every later boot. Pins the durable
// write's observable contract: content, 0600, and no temp-file litter.
func TestWriteMnemonicDurably(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mnemonicFile)
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

	if err := writeMnemonicDurably(path, mnemonic); err != nil {
		t.Fatalf("writeMnemonicDurably: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back mnemonic: %v", err)
	}
	if string(got) != mnemonic {
		t.Errorf("mnemonic content mismatch:\n got: %q\nwant: %q", got, mnemonic)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat mnemonic: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mnemonic perms = %o, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp file litter after durable write: %v", names)
	}

	// A re-write over an existing seed replaces it whole (the rename
	// contract), never truncates in place.
	if err := writeMnemonicDurably(path, "letter advice cage absurd amount doctor acoustic avoid letter advice cage above"); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back rewritten mnemonic: %v", err)
	}
	if strings.HasPrefix(string(got), "abandon") {
		t.Errorf("rewrite did not replace the seed: %q", got)
	}
}
