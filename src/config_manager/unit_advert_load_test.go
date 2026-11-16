package config_manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The advert/unit check must actually REFUSE at config load, not merely log.
//
// A helper that returns an error nobody acts on is decoration: the router would
// boot, publish `price_per_step ... "sat"` for a mint that only issues "mb", and
// a paying client would hand over value the gate then refuses. This drives the
// real loader entry point with an on-disk config so the refusal is what the
// router experiences, not what the helper returns in isolation.
func TestLoadConfig_RefusesCustomUnitMintAdvertisingAnotherUnit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"config_version": "v0.0.10",
		"metric":         "bytes",
		"step_size":      1048576, // valid for "mb", so the step_size check stays quiet
		"accepted_mints": []map[string]any{{
			"url":            "http://mint.example/mb",
			"unit":           "mb",
			"price_unit":     "sat", // the mismatch under test
			"price_per_step": 1,
		}},
	}
	blob, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := LoadConfig(path)
	if err == nil {
		t.Fatalf("LoadConfig accepted a config whose mint issues \"mb\" but advertises \"sat\"; "+
			"the router would publish one unit and accept another (returned %+v)", got)
	}
}

// A pre-existing satoshi config must keep loading: both unit fields empty.
func TestLoadConfig_LegacySatMintStillLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"config_version": "v0.0.10",
		"metric":         "bytes",
		"step_size":      22020096,
		"accepted_mints": []map[string]any{{
			"url":            "http://mint.example/sat",
			"price_per_step": 1,
		}},
	}
	blob, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("a legacy satoshi config must keep loading, got: %v", err)
	}
}
