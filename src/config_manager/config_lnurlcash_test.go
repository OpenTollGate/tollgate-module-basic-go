package config_manager

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// LNURLcash is an OPT-IN payment method that settles through the operator's own
// mint. It must be absent from a shipped config's behaviour: a router that has
// never heard of it must not start accepting withdraw notes because it upgraded.
func TestLNURLCashShipsDisabled(t *testing.T) {
	cfg := NewDefaultConfig()
	if cfg.LNURLCash.Enabled {
		t.Fatal("lnurlcash ships ENABLED: the default config must not accept withdraw notes")
	}
	if cfg.LNURLCash.MaxNoteSats == 0 {
		t.Fatal("lnurlcash default max_note_sats is zero: an enabled router would price every note at nothing")
	}
	if cfg.LNURLCash.RedemptionTimeoutSeconds <= 0 {
		t.Fatal("lnurlcash default redemption_timeout_seconds is not positive: a redemption could hang forever")
	}
}

// The JSON shape is the operator's file and the LuCI panel's contract.
func TestLNURLCashJSONShape(t *testing.T) {
	data, err := json.Marshal(NewDefaultConfig())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The top level mixes scalars and objects, so decode to raw first and then
	// decode only the section under test. Decoding straight into
	// map[string]map[string]json.RawMessage rejects config_version (a string)
	// and would fail for a reason that has nothing to do with this feature.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("unmarshal top level: %v", err)
	}
	var section map[string]json.RawMessage
	rawSection, ok := top["lnurlcash"]
	if !ok {
		t.Fatal("default config JSON has no lnurlcash section")
	}
	if err := json.Unmarshal(rawSection, &section); err != nil {
		t.Fatalf("unmarshal lnurlcash section: %v", err)
	}
	for _, key := range []string{"enabled", "max_note_sats", "redemption_timeout_seconds", "retention_seconds"} {
		if _, ok := section[key]; !ok {
			t.Errorf("lnurlcash section has no %q field", key)
		}
	}
}

// A config written before this feature existed must load with the feature off,
// not fail and not inherit a zero timeout.
func TestLNURLCashAbsentSectionLoadsDisabled(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	if err := os.WriteFile(path, []byte(`{"config_version":"v0.0.11","log_level":"info","accepted_mints":[{"url":"https://mint.example"}]}`), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.LNURLCash.Enabled {
		t.Fatal("a config without an lnurlcash section loaded with the feature ENABLED")
	}
	if cfg.LNURLCash.RedemptionTimeoutSeconds != DefaultLNURLCashConfig().RedemptionTimeoutSeconds {
		t.Fatalf("absent section gave timeout %d, want the default %d",
			cfg.LNURLCash.RedemptionTimeoutSeconds, DefaultLNURLCashConfig().RedemptionTimeoutSeconds)
	}
}

// The operator must be able to read the trade-off where they turn it on.
func TestLNURLCashSchemaStatesTheTradeOff(t *testing.T) {
	schema := GetConfigSchema()
	var section *FieldSchema
	for i := range schema {
		if schema[i].JSONKey == "lnurlcash" {
			section = &schema[i]
			break
		}
	}
	if section == nil {
		t.Fatal("config schema has no lnurlcash section")
	}
	if section.Type != "object" {
		t.Fatalf("lnurlcash schema type = %q, want object", section.Type)
	}
	for _, child := range section.Children {
		if child.Description == "" {
			t.Errorf("lnurlcash.%s has no Description — every field must state its trade-off", child.JSONKey)
		}
	}
	var enabled *FieldSchema
	for i := range section.Children {
		if section.Children[i].JSONKey == "enabled" {
			enabled = &section.Children[i]
		}
	}
	if enabled == nil {
		t.Fatal("lnurlcash schema has no enabled field")
	}
	if enabled.Default != false {
		t.Fatalf("lnurlcash.enabled default = %v, want false", enabled.Default)
	}
	// The custody disclosure is the reason this is opt-in; it belongs in the
	// field text, not only in the docs.
	if want := "issuer"; !containsFold(enabled.Description, want) && !containsFold(section.Description, want) {
		t.Errorf("neither lnurlcash.enabled nor its section description mentions the %q (who holds the money)", want)
	}
}

func TestLNURLCashRetentionIsADuration(t *testing.T) {
	// Retention is expressed in seconds in the file so an operator cannot write
	// "7d" and get a silent zero.
	cfg := DefaultLNURLCashConfig()
	if cfg.RetentionSeconds != int((7 * 24 * time.Hour).Seconds()) {
		t.Fatalf("default retention = %d s, want one week", cfg.RetentionSeconds)
	}
}

func containsFold(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (indexFold(haystack, needle) >= 0)
}

func indexFold(s, sub string) int {
	l := len(sub)
	for i := 0; i+l <= len(s); i++ {
		match := true
		for j := 0; j < l; j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 32
			}
			if 'A' <= b && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
