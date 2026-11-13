package config_manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Keep every config-manager unit test away from the deployment backup store,
// including when the test process happens to run as root. No permission-based
// skip or privileged /etc setup is needed to exercise corruption recovery.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tollgate-config-unit-backups-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create isolated config-test backup directory")
		os.Exit(1)
	}
	configBackupDir = dir
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "cannot remove owned config-test backup directory")
		code = 1
	}
	os.Exit(code)
}

func isolateConfigBackups(t *testing.T) string {
	t.Helper()
	previous := configBackupDir
	configBackupDir = filepath.Join(t.TempDir(), "backups")
	t.Cleanup(func() { configBackupDir = previous })
	return configBackupDir
}

func assertCorruptBackup(t *testing.T, dir, kind string, original []byte) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, kind+"_*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatal("expected exactly one isolated corruption backup")
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("corrupt input was not preserved byte-for-byte")
	}
}

func TestInvalidConfigurationBackupsStayInOwnedDirectory(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		ensure func(string) error
	}{
		{"config", func(path string) error { _, err := EnsureDefaultConfig(path); return err }},
		{"identities", func(path string) error { _, err := EnsureDefaultIdentities(path); return err }},
		{"install", func(path string) error { _, err := EnsureDefaultInstall(path); return err }},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			backupDir := isolateConfigBackups(t)
			path := filepath.Join(t.TempDir(), tc.kind+".json")
			original := []byte("{broken input")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := tc.ensure(path); err != nil {
				t.Fatal("isolated corruption recovery failed")
			}
			assertCorruptBackup(t, backupDir, tc.kind, original)
			replacement, err := os.ReadFile(path)
			if err != nil || !json.Valid(replacement) {
				t.Fatal("valid defaults were not persisted after backup")
			}
		})
	}
}
