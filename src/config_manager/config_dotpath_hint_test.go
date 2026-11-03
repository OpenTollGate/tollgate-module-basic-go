package config_manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE DEFECT THIS PINS (#1002 C3): `config set accepted_mints ...` failed with
// a bare "unsupported type slice for set" while the README documents `set
// <key> <value>` generically. The error now carries the remediation —
// array/object keys go through `config save`.
func TestSetDotPath_ArrayKeyErrorNamesTheRemediation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := `{"config_version":"v0.0.10","accepted_mints":[{"url":"http://m"}],"log_level":"info"}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	identPath := filepath.Join(dir, "identities.json")
	if err := os.WriteFile(identPath, []byte(`{"config_version":"v0.0.1","owned_identities":[],"public_identities":[]}`), 0o600); err != nil {
		t.Fatalf("seed identities: %v", err)
	}

	cm, err := NewConfigManager(cfgPath, filepath.Join(dir, "install.json"), identPath)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}

	err = SetDotPath(cm, "accepted_mints", "x")
	if err == nil {
		t.Fatal("array key via set must fail")
	}
	for _, want := range []string{"unsupported type slice", "config save"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
