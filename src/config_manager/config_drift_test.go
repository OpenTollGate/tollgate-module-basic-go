package config_manager

import (
	"encoding/json"
	"testing"
)

// TestConfigJSONFields verifies that the Config struct produces all JSON fields
// expected by the LuCI UI (settings.js). If this test fails, either:
//   - A new field was added to Config but the UI doesn't render it (update settings.js)
//   - A field was removed from Config but the UI still references it (update settings.js)
//   - A JSON tag was renamed (update settings.js accordingly)
func TestConfigJSONFields(t *testing.T) {
	cfg := NewDefaultConfig()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Failed to marshal default config: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	topLevelFields := []string{
		"config_version",
		"log_level",
		"accepted_mints",
		"profit_share",
		"step_size",
		"margin",
		"metric",
		"show_setup",
		"reseller_mode",
		"upstream_detector",
		"upstream_session_manager",
	}
	for _, field := range topLevelFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("Config JSON missing top-level field: %s (UI may be broken)", field)
		}
	}

	mintsRaw, ok := raw["accepted_mints"].([]interface{})
	if !ok || len(mintsRaw) == 0 {
		t.Fatal("accepted_mints is missing or empty")
	}
	mintFields := []string{
		"url",
		"min_balance",
		"balance_tolerance_percent",
		"payout_interval_seconds",
		"min_payout_amount",
		"price_per_step",
		"price_unit",
		"purchase_min_steps",
	}
	mintObj, ok := mintsRaw[0].(map[string]interface{})
	if !ok {
		t.Fatal("first accepted_mints entry is not an object")
	}
	for _, field := range mintFields {
		if _, ok := mintObj[field]; !ok {
			t.Errorf("accepted_mints entry missing field: %s (UI mint card may be broken)", field)
		}
	}

	sharesRaw, ok := raw["profit_share"].([]interface{})
	if !ok || len(sharesRaw) == 0 {
		t.Fatal("profit_share is missing or empty")
	}
	shareFields := []string{"factor", "identity"}
	shareObj, ok := sharesRaw[0].(map[string]interface{})
	if !ok {
		t.Fatal("first profit_share entry is not an object")
	}
	for _, field := range shareFields {
		if _, ok := shareObj[field]; !ok {
			t.Errorf("profit_share entry missing field: %s (UI share card may be broken)", field)
		}
	}

	crowsnest, ok := raw["upstream_detector"].(map[string]interface{})
	if !ok {
		t.Fatal("upstream_detector is missing or not an object")
	}
	crowsnestFields := []string{
		"probe_timeout",
		"probe_retry_count",
		"probe_retry_delay",
		"require_valid_signature",
		"ignore_interfaces",
		"discovery_timeout",
	}
	for _, field := range crowsnestFields {
		if _, ok := crowsnest[field]; !ok {
			t.Errorf("upstream_detector missing field: %s (UI crowsnest section may be broken)", field)
		}
	}
}

// TestIdentitiesJSONFields verifies that the IdentitiesConfig struct produces
// all JSON fields expected by the LuCI UI identities tab.
func TestIdentitiesJSONFields(t *testing.T) {
	cfg := NewDefaultIdentitiesConfig()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Failed to marshal default identities: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal identities: %v", err)
	}

	topLevelFields := []string{
		"config_version",
		"owned_identities",
		"public_identities",
	}
	for _, field := range topLevelFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("Identities JSON missing top-level field: %s", field)
		}
	}

	pubIdentFields := []string{"name", "pubkey", "lightning_address"}
	fullIdent := PublicIdentity{Name: "test", PubKey: "abc123", LightningAddress: "x@y.z"}
	fullIdentData, _ := json.Marshal(fullIdent)
	var fullIdentObj map[string]interface{}
	json.Unmarshal(fullIdentData, &fullIdentObj)
	for _, field := range pubIdentFields {
		if _, ok := fullIdentObj[field]; !ok {
			t.Errorf("public_identity JSON tag missing for field: %s (UI identity card may be broken)", field)
		}
	}

	ownedIdents, ok := raw["owned_identities"].([]interface{})
	if !ok || len(ownedIdents) == 0 {
		t.Fatal("owned_identities is missing or empty")
	}
	ownedIdentObj, ok := ownedIdents[0].(map[string]interface{})
	if !ok {
		t.Fatal("first owned_identity entry is not an object")
	}
	if _, ok := ownedIdentObj["name"]; !ok {
		t.Error("owned_identity entry missing field: name (UI owned table may be broken)")
	}
}

// TestConfigRoundTrip verifies that saving and loading config preserves all fields.
func TestConfigRoundTrip(t *testing.T) {
	original := NewDefaultConfig()

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if loaded.ConfigVersion != original.ConfigVersion {
		t.Errorf("config_version mismatch: got %s, want %s", loaded.ConfigVersion, original.ConfigVersion)
	}
	if loaded.LogLevel != original.LogLevel {
		t.Errorf("log_level mismatch: got %s, want %s", loaded.LogLevel, original.LogLevel)
	}
	if loaded.Metric != original.Metric {
		t.Errorf("metric mismatch: got %s, want %s", loaded.Metric, original.Metric)
	}
	if loaded.StepSize != original.StepSize {
		t.Errorf("step_size mismatch: got %d, want %d", loaded.StepSize, original.StepSize)
	}
	if len(loaded.AcceptedMints) != len(original.AcceptedMints) {
		t.Errorf("accepted_mints length mismatch: got %d, want %d", len(loaded.AcceptedMints), len(original.AcceptedMints))
	}
	if len(loaded.ProfitShare) != len(original.ProfitShare) {
		t.Errorf("profit_share length mismatch: got %d, want %d", len(loaded.ProfitShare), len(original.ProfitShare))
	}
	for i, ps := range loaded.ProfitShare {
		if ps.Identity != original.ProfitShare[i].Identity {
			t.Errorf("profit_share[%d].identity mismatch: got %s, want %s", i, ps.Identity, original.ProfitShare[i].Identity)
		}
		if ps.Factor != original.ProfitShare[i].Factor {
			t.Errorf("profit_share[%d].factor mismatch: got %f, want %f", i, ps.Factor, original.ProfitShare[i].Factor)
		}
	}
}

// TestIdentitiesRoundTrip verifies that saving and loading identities preserves all fields.
func TestIdentitiesRoundTrip(t *testing.T) {
	original := NewDefaultIdentitiesConfig()

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var loaded IdentitiesConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if loaded.ConfigVersion != original.ConfigVersion {
		t.Errorf("config_version mismatch: got %s, want %s", loaded.ConfigVersion, original.ConfigVersion)
	}
	if len(loaded.OwnedIdentities) != len(original.OwnedIdentities) {
		t.Errorf("owned_identities length mismatch: got %d, want %d", len(loaded.OwnedIdentities), len(original.OwnedIdentities))
	}
	if len(loaded.PublicIdentities) != len(original.PublicIdentities) {
		t.Errorf("public_identities length mismatch: got %d, want %d", len(loaded.PublicIdentities), len(original.PublicIdentities))
	}
	for i, pi := range loaded.PublicIdentities {
		if pi.Name != original.PublicIdentities[i].Name {
			t.Errorf("public_identities[%d].name mismatch: got %s, want %s", i, pi.Name, original.PublicIdentities[i].Name)
		}
	}
}
