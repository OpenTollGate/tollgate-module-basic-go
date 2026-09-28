package config_manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureDefaultInstallStampsZeroTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.json")

	ic, err := EnsureDefaultInstall(path)
	if err != nil {
		t.Fatalf("fresh EnsureDefaultInstall: %v", err)
	}
	if ic.InstallTimestamp == 0 {
		t.Fatal("fresh install config must have install_time stamped, got 0")
	}

	// Persisted file must carry the stamp too.
	var persisted InstallConfig
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("persisted install.json unreadable: %v", err)
	}
	if persisted.InstallTimestamp == 0 {
		t.Fatal("persisted install_time is 0 despite stamping")
	}
}

func TestEnsureDefaultInstallPreservesExistingTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.json")

	seed := `{"config_version": "v0.0.2", "package_path": "false", "install_time": 1234567890}`
	if err := os.WriteFile(path, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	ic, err := EnsureDefaultInstall(path)
	if err != nil {
		t.Fatalf("reload EnsureDefaultInstall: %v", err)
	}
	if ic.InstallTimestamp != 1234567890 {
		t.Fatalf("existing install_time must be preserved, got %d", ic.InstallTimestamp)
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("install.json rewritten on a no-op load:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestEnsureDefaultInstallStampsZeroInLoadedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.json")

	seed := `{"config_version": "v0.0.2", "package_path": "false", "install_time": 0}`
	if err := os.WriteFile(path, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	ic, err := EnsureDefaultInstall(path)
	if err != nil {
		t.Fatalf("EnsureDefaultInstall with zero stamp: %v", err)
	}
	if ic.InstallTimestamp == 0 {
		t.Fatal("zero install_time must be stamped on load")
	}
	if ic.InstallTimestamp > time.Now().Unix()+5 {
		t.Fatalf("stamped install_time is in the future: %d", ic.InstallTimestamp)
	}
}
