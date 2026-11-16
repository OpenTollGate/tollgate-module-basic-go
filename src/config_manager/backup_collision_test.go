package config_manager

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestBackupAndLogPreservesSameTimestampBackups(t *testing.T) {
	root := t.TempDir()
	backups := filepath.Join(root, "backups")
	first := filepath.Join(root, "first.json")
	second := filepath.Join(root, "second.json")
	if err := os.WriteFile(first, []byte("first corrupt config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second corrupt config"), 0600); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	if err := backupAndLogAt(first, backups, "config", "v-test", at); err != nil {
		t.Fatal(err)
	}
	if err := backupAndLogAt(second, backups, "config", "v-test", at); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("same-timestamp backups = %v, want two distinct artifacts", names)
	}

	contents := map[string]bool{}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(backups, name))
		if err != nil {
			t.Fatal(err)
		}
		contents[string(b)] = true
	}
	if !contents["first corrupt config"] || !contents["second corrupt config"] {
		t.Fatalf("a corrupt-config backup was overwritten: contents=%v names=%v", contents, names)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("first source was not moved: %v", err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("second source was not moved: %v", err)
	}
}
