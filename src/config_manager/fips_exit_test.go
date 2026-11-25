package config_manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFIPSExitManagerRefusesBeforeIdentityCreation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(`{"fips_exit":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(config, filepath.Join(dir, "install.json"), filepath.Join(dir, "identities.json"))
	if cm != nil || err == nil || !strings.Contains(err.Error(), "runtime activation is not supported") {
		t.Fatalf("cm=%v err=%v", cm, err)
	}
	for _, name := range []string{"install.json", "identities.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("startup wrote %s: %v", name, err)
		}
	}
}

func TestFIPSExitAmbiguousSection(t *testing.T) {
	for _, raw := range []string{
		`{"enabled":true,"enabled":false}`,
		`{"enabled":false,"Enabled":false}`,
		`{"enabled":null}`,
		`{"enable":true}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var c FIPSExitConfig
			if err := json.Unmarshal([]byte(raw), &c); err == nil {
				t.Fatal("ambiguous section accepted")
			}
		})
	}
}

func TestFIPSExitSetterCannotActivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"fips_exit":{"enabled":false}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cm := &ConfigManager{ConfigFilePath: path, config: &Config{FIPSExit: &FIPSExitConfig{}}}
	if err := SetDotPath(cm, "fips_exit.enabled", "true"); err == nil {
		t.Error("activation setter accepted")
	}
	if cm.config.FIPSExit.Enabled {
		t.Error("live config mutated")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Error("config file mutated")
	}
	if err := ValidateValue("fips_exit.enabled", "true"); err == nil {
		t.Error("validation bypass")
	}
}

func TestFIPSExitSaveRefusesActivation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	before := []byte(`{"fips_exit":{"enabled":false}}`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{FIPSExit: &FIPSExitConfig{Enabled: true}}
	if err := SaveConfig(path, cfg); err == nil {
		t.Error("SaveConfig persisted unsupported activation")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Error("SaveConfig changed file")
	}
}

func TestFIPSExitSchema(t *testing.T) {
	for _, f := range GetConfigSchema() {
		if f.JSONKey == "fips_exit" {
			if len(f.Children) != 1 || f.Children[0].JSONKey != "enabled" || f.Children[0].Editable {
				t.Fatal("activation must not be advertised as editable")
			}
			return
		}
	}
	t.Fatal("fips_exit schema missing")
}

func TestFIPSExitRefusalPreservesFile(t *testing.T) {
	raw := []byte(`{"fips_exit":{"enabled":true}}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, load := range []func(string) (*Config, error){LoadConfig, EnsureDefaultConfig} {
		cfg, err := load(path)
		if err == nil || !strings.Contains(err.Error(), "runtime activation is not supported") || cfg != nil {
			t.Fatalf("wanted explicit refusal, got cfg=%v err=%v", cfg, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(raw) {
			t.Fatalf("operator file changed: %s, %v", after, err)
		}
	}
}

func TestFIPSExitConfigDecodeSafety(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		reject bool
	}{
		{`{}`, false},
		{`{"fips_exit":{"enabled":false}}`, false},
		{`{"fips_exit":{"enabled":true}}`, true},
		{`{"fips_exit":{"enabled":"true"}}`, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var c Config
			err := json.Unmarshal([]byte(tc.raw), &c)
			if (err != nil) != tc.reject {
				t.Fatalf("decode error=%v, reject=%v", err, tc.reject)
			}
		})
	}
}
