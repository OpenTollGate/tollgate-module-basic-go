package config_manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The entry_ui default population contract (#928): whatever writes an
// entry_ui the operator never chose must record the value this deployment
// can actually back — board only when the D4 marker announces the mode-aware
// portal writer, the legacy luci mapping otherwise — so config.json agrees
// with the port table 99-tollgate-setup converged from the first boot, the
// property PRTA's test_entry_ui_port_mapping_contract checks on a real
// router. The operator-declared value is never rewritten (ADR D4).

// redirectEntryUIMarker points the population's marker seam at a throw-away
// path, optionally seeded with content, restoring the deployment's real path
// afterwards. Empty content means the marker file is absent (the ipk-only
// install of #928).
func redirectEntryUIMarker(t *testing.T, content string) {
	t.Helper()
	old := entryUIMarkerPath
	entryUIMarkerPath = filepath.Join(t.TempDir(), "entry-ui-mapping")
	if content != "" {
		if err := os.WriteFile(entryUIMarkerPath, []byte(content), 0644); err != nil {
			t.Fatalf("write marker fixture: %v", err)
		}
	}
	t.Cleanup(func() { entryUIMarkerPath = old })
}

// savedEntryUI reads the entry_ui value EnsureDefaultConfig persisted.
func savedEntryUI(t *testing.T, configPath string) string {
	t.Helper()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("saved config is invalid JSON: %v", err)
	}
	return saved.EntryUI
}

// oldConfigJSON is a pre-entry_ui config (v0.0.7): upgrading it is the exact
// lane of the #928 report, where the population ran after the port table had
// already converged.
const oldConfigJSON = `{
	"config_version": "v0.0.7",
	"accepted_mints": [],
	"profit_share": [{"factor": 1.0, "identity": "owner"}]
}`

// TestEntryUIDefaultFreshInstallBackedByMarker covers the fresh-install write
// (config.json does not exist yet) for every marker state the D4 gate knows.
func TestEntryUIDefaultFreshInstallBackedByMarker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker string
		want   string
	}{
		{"mode-aware feed applied board", "board", "board"},
		{"marker absent (ipk-only, #928)", "", "luci"},
		{"marker content luci", "luci", "luci"},
		{"marker content garbage", "not-a-mode", "luci"},
		{"marker content board with trailing newline", "board\n", "board"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirectEntryUIMarker(t, tc.marker)
			configPath := filepath.Join(t.TempDir(), "config.json")

			cfg, err := EnsureDefaultConfig(configPath)
			if err != nil {
				t.Fatalf("EnsureDefaultConfig failed: %v", err)
			}
			if cfg.EntryUI != tc.want {
				t.Errorf("returned config entry_ui = %q, want %q", cfg.EntryUI, tc.want)
			}
			if got := savedEntryUI(t, configPath); got != tc.want {
				t.Errorf("saved config entry_ui = %q, want %q (config must record the mapping the deployment backs, the agreement test_entry_ui_port_mapping_contract demands)", got, tc.want)
			}
		})
	}
}

// TestEntryUIDefaultMigrationPopulatesBackedValue covers the upgrade lane of
// #928: a pre-entry_ui config migrated after the port table converged.
func TestEntryUIDefaultMigrationPopulatesBackedValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker string
		want   string
	}{
		{"mode-aware feed applied board", "board", "board"},
		{"marker absent (ipk-only, #928)", "", "luci"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirectEntryUIMarker(t, tc.marker)
			configPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(configPath, []byte(oldConfigJSON), 0644); err != nil {
				t.Fatalf("write old config: %v", err)
			}

			migrated, err := EnsureDefaultConfig(configPath)
			if err != nil {
				t.Fatalf("EnsureDefaultConfig failed: %v", err)
			}
			if migrated.EntryUI != tc.want {
				t.Errorf("migrated entry_ui = %q, want %q", migrated.EntryUI, tc.want)
			}
			if got := savedEntryUI(t, configPath); got != tc.want {
				t.Errorf("saved entry_ui = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEntryUIDefaultInvalidConfigRewriteBacked covers the third population
// path: an unparseable config.json is recreated from defaults, which must
// carry the backed value too.
func TestEntryUIDefaultInvalidConfigRewriteBacked(t *testing.T) {
	redirectEntryUIMarker(t, "") // ipk-only: no marker
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}

	cfg, err := EnsureDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("EnsureDefaultConfig failed: %v", err)
	}
	if cfg.EntryUI != "luci" {
		t.Errorf("rewritten config entry_ui = %q, want luci (the backed mapping)", cfg.EntryUI)
	}
	if got := savedEntryUI(t, configPath); got != "luci" {
		t.Errorf("saved entry_ui = %q, want luci", got)
	}
}

// TestEntryUIDefaultNeverOverwritesOperatorValue pins the other half of the
// contract: a value the operator declared survives migration unchanged, in
// both directions, whatever the marker says. D4 leaves the operator's switch
// value recorded for the release that carries both halves; the population
// rule applies to defaults only.
func TestEntryUIDefaultNeverOverwritesOperatorValue(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared string
		marker   string
	}{
		{"operator chose board, feed absent (D4 keeps it recorded)", "board", ""},
		{"operator chose luci, feed applied board", "luci", "board"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirectEntryUIMarker(t, tc.marker)
			configPath := filepath.Join(t.TempDir(), "config.json")
			body := `{
				"config_version": "v0.0.7",
				"entry_ui": ` + jsonStringForTest(t, tc.declared) + `,
				"accepted_mints": [],
				"profit_share": [{"factor": 1.0, "identity": "owner"}]
			}`
			if err := os.WriteFile(configPath, []byte(body), 0644); err != nil {
				t.Fatalf("write old config: %v", err)
			}

			migrated, err := EnsureDefaultConfig(configPath)
			if err != nil {
				t.Fatalf("EnsureDefaultConfig failed: %v", err)
			}
			if migrated.EntryUI != tc.declared {
				t.Errorf("migrated entry_ui = %q, want the declared %q (never rewritten)", migrated.EntryUI, tc.declared)
			}
		})
	}
}

// TestEntryUIDefaultSteadyStateUntouched pins that a current-version config
// is not rewritten and logs nothing about entry_ui: the population runs only
// when a value is actually missing, so a converged router never sees the
// WARNING on every daemon start.
func TestEntryUIDefaultSteadyStateUntouched(t *testing.T) {
	redirectEntryUIMarker(t, "") // ipk-only steady state: no marker
	configPath := filepath.Join(t.TempDir(), "config.json")
	current := NewDefaultConfig()
	current.EntryUI = "luci" // the backed value a previous population wrote
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("marshal current config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("write current config: %v", err)
	}

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	loaded, err := EnsureDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("EnsureDefaultConfig failed: %v", err)
	}
	if loaded.EntryUI != "luci" {
		t.Errorf("entry_ui = %q, want luci unchanged", loaded.EntryUI)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("config.json was rewritten on a steady-state load — population must be write-path only")
	}
}

func jsonStringForTest(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}
	return string(b)
}
