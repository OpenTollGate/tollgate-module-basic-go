package config_manager

import (
	"encoding/json"
	"testing"
)

func TestSchemaConfigFields(t *testing.T) {
	schema := GetConfigSchema()

	schemaMap := make(map[string]*FieldSchema)
	for i := range schema {
		schemaMap[schema[i].JSONKey] = &schema[i]
	}

	requiredKeys := []string{
		"config_version", "log_level", "metric", "step_size",
		"accepted_mints", "profit_share", "show_setup", "reseller_mode",
		"upstream_detector", "upstream_session_manager",
	}
	for _, key := range requiredKeys {
		if _, ok := schemaMap[key]; !ok {
			t.Errorf("Config schema missing field: %s", key)
		}
	}

	mintSchema := schemaMap["accepted_mints"]
	if mintSchema == nil {
		t.Fatal("accepted_mints schema missing")
	}
	if len(mintSchema.Children) == 0 {
		t.Fatal("accepted_mints schema has no children")
	}
	mintChildMap := make(map[string]bool)
	for _, c := range mintSchema.Children {
		mintChildMap[c.JSONKey] = true
	}
	for _, f := range []string{"url", "min_balance", "price_per_step", "price_unit", "purchase_min_steps"} {
		if !mintChildMap[f] {
			t.Errorf("accepted_mints child schema missing: %s", f)
		}
	}

	psSchema := schemaMap["profit_share"]
	if psSchema == nil {
		t.Fatal("profit_share schema missing")
	}
	if len(psSchema.Children) == 0 {
		t.Fatal("profit_share schema has no children")
	}
	psChildMap := make(map[string]bool)
	for _, c := range psSchema.Children {
		psChildMap[c.JSONKey] = true
	}
	for _, f := range []string{"factor", "identity"} {
		if !psChildMap[f] {
			t.Errorf("profit_share child schema missing: %s", f)
		}
	}
}

func TestSchemaIdentitiesFields(t *testing.T) {
	schema := GetIdentitiesSchema()

	schemaMap := make(map[string]*FieldSchema)
	for i := range schema {
		schemaMap[schema[i].JSONKey] = &schema[i]
	}

	requiredKeys := []string{"config_version", "owned_identities", "public_identities"}
	for _, key := range requiredKeys {
		if _, ok := schemaMap[key]; !ok {
			t.Errorf("Identities schema missing field: %s", key)
		}
	}

	piSchema := schemaMap["public_identities"]
	if piSchema == nil {
		t.Fatal("public_identities schema missing")
	}
	if len(piSchema.Children) == 0 {
		t.Fatal("public_identities schema has no children")
	}
	piChildMap := make(map[string]bool)
	for _, c := range piSchema.Children {
		piChildMap[c.JSONKey] = true
	}
	for _, f := range []string{"name", "pubkey", "lightning_address"} {
		if !piChildMap[f] {
			t.Errorf("public_identities child schema missing: %s", f)
		}
	}
}

func TestSchemaMatchesConfigJSON(t *testing.T) {
	schema := GetConfigSchema()
	cfg := NewDefaultConfig()

	cfgData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}
	var cfgMap map[string]interface{}
	if err := json.Unmarshal(cfgData, &cfgMap); err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	for _, field := range schema {
		if field.Required && field.JSONKey != "margin" {
			if _, ok := cfgMap[field.JSONKey]; !ok {
				t.Errorf("Schema declares %s as required but Config JSON doesn't produce it", field.JSONKey)
			}
		}
	}
}

func TestSchemaJSONSerialization(t *testing.T) {
	schema := GetConfigSchema()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Failed to marshal config schema: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal config schema: %v", err)
	}

	for i, field := range parsed {
		for _, required := range []string{"name", "type", "json_key", "required", "editable"} {
			if _, ok := field[required]; !ok {
				t.Errorf("Schema field %d missing key: %s", i, required)
			}
		}
	}

	identSchema := GetIdentitiesSchema()
	identData, err := json.Marshal(identSchema)
	if err != nil {
		t.Fatalf("Failed to marshal identities schema: %v", err)
	}
	var identParsed []map[string]interface{}
	if err := json.Unmarshal(identData, &identParsed); err != nil {
		t.Fatalf("Failed to unmarshal identities schema: %v", err)
	}
	if len(identParsed) == 0 {
		t.Error("Identities schema is empty")
	}
}

func TestSchemaEditableFieldsHaveDescriptions(t *testing.T) {
	schema := GetConfigSchema()
	for _, field := range schema {
		if field.Editable && field.Description == "" {
			t.Errorf("Editable field %s (%s) has no description", field.Name, field.JSONKey)
		}
	}
	identSchema := GetIdentitiesSchema()
	for _, field := range identSchema {
		if field.Editable && field.Description == "" {
			t.Errorf("Editable identities field %s (%s) has no description", field.Name, field.JSONKey)
		}
	}
}

func TestSetDotPathSimpleFields(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	err = SetDotPath(cm, "metric", "milliseconds")
	if err != nil {
		t.Fatalf("Failed to set metric: %v", err)
	}

	loaded, err := LoadConfig(tempDir + "/config.json")
	if err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}
	if loaded.Metric != "milliseconds" {
		t.Errorf("metric mismatch: got %s, want milliseconds", loaded.Metric)
	}

	err = SetDotPath(cm, "step_size", "44040192")
	if err != nil {
		t.Fatalf("Failed to set step_size: %v", err)
	}

	loaded, _ = LoadConfig(tempDir + "/config.json")
	if loaded.StepSize != 44040192 {
		t.Errorf("step_size mismatch: got %d, want 44040192", loaded.StepSize)
	}

	err = SetDotPath(cm, "show_setup", "false")
	if err != nil {
		t.Fatalf("Failed to set show_setup: %v", err)
	}

	loaded, _ = LoadConfig(tempDir + "/config.json")
	if loaded.ShowSetup != false {
		t.Errorf("show_setup mismatch: got %v, want false", loaded.ShowSetup)
	}
}

func TestSetDotPathMintField(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	err = SetDotPath(cm, "accepted_mints.0.price_per_step", "5")
	if err != nil {
		t.Fatalf("Failed to set mint price: %v", err)
	}

	loaded, _ := LoadConfig(tempDir + "/config.json")
	if loaded.AcceptedMints[0].PricePerStep != 5 {
		t.Errorf("price_per_step mismatch: got %d, want 5", loaded.AcceptedMints[0].PricePerStep)
	}
}

func TestSetDotPathInvalidKey(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	err = SetDotPath(cm, "nonexistent_field", "value")
	if err == nil {
		t.Error("Expected error for nonexistent field, got nil")
	}
}

func TestSetDotPathInvalidValue(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	err = SetDotPath(cm, "step_size", "not-a-number")
	if err == nil {
		t.Error("Expected error for non-numeric step_size, got nil")
	}

	err = SetDotPath(cm, "show_setup", "not-a-bool")
	if err == nil {
		t.Error("Expected error for non-boolean show_setup, got nil")
	}
}

func TestSetDotPathSchemaEnumValidation(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	err = SetDotPath(cm, "log_level", "INVALID")
	if err == nil {
		t.Error("Expected error for invalid log_level enum value, got nil")
	}

	err = SetDotPath(cm, "metric", "INVALID")
	if err == nil {
		t.Error("Expected error for invalid metric enum value, got nil")
	}

	err = SetDotPath(cm, "log_level", "debug")
	if err != nil {
		t.Errorf("Valid enum value 'debug' should be accepted, got: %v", err)
	}

	err = SetDotPath(cm, "metric", "milliseconds")
	if err != nil {
		t.Errorf("Valid enum value 'milliseconds' should be accepted, got: %v", err)
	}
}

func TestSetDotPathSchemaMinMaxValidation(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	err = SetDotPath(cm, "margin", "5.0")
	if err == nil {
		t.Error("Expected error for margin > 1.0 (max), got nil")
	}

	err = SetDotPath(cm, "margin", "-0.5")
	if err == nil {
		t.Error("Expected error for margin < 0.0 (min), got nil")
	}

	err = SetDotPath(cm, "margin", "0.5")
	if err != nil {
		t.Errorf("Valid margin 0.5 should be accepted, got: %v", err)
	}

	err = SetDotPath(cm, "profit_share.0.factor", "5.0")
	if err == nil {
		t.Error("Expected error for profit_share factor > 1.0 (max), got nil")
	}

	err = SetDotPath(cm, "profit_share.0.factor", "-0.1")
	if err == nil {
		t.Error("Expected error for profit_share factor < 0.0 (min), got nil")
	}
}

func TestSetDotPathSchemaUpstreamWifiValidation(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	err = SetDotPath(cm, "upstream_wifi.scan_interval_seconds", "5")
	if err == nil {
		t.Error("Expected error for scan_interval_seconds < 10 (min), got nil")
	}

	err = SetDotPath(cm, "upstream_wifi.scan_interval_seconds", "5000")
	if err == nil {
		t.Error("Expected error for scan_interval_seconds > 3600 (max), got nil")
	}

	err = SetDotPath(cm, "upstream_wifi.scan_interval_seconds", "600")
	if err != nil {
		t.Errorf("Valid scan_interval_seconds 600 should be accepted, got: %v", err)
	}

	err = SetDotPath(cm, "upstream_wifi.signal_floor", "-20")
	if err == nil {
		t.Error("Expected error for signal_floor > -30 (max), got nil")
	}

	err = SetDotPath(cm, "upstream_wifi.signal_floor", "-70")
	if err != nil {
		t.Errorf("Valid signal_floor -70 should be accepted, got: %v", err)
	}
}
