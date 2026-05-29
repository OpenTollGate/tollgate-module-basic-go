package config_manager

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func collectJSONTags(t reflect.Type, prefix string) map[string]string {
	tags := make(map[string]string)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return tags
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		parts := strings.Split(jsonTag, ",")
		name := parts[0]
		if name == "" {
			name = field.Name
		}
		omitempty := len(parts) > 1 && parts[1] == "omitempty"
		fullPath := name
		if prefix != "" {
			fullPath = prefix + "." + name
		}

		ft := field.Type
		if ft.Kind() == reflect.Slice {
			elemType := ft.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}
			if elemType.Kind() == reflect.Struct {
				for k, v := range collectJSONTags(elemType, fullPath+"[]") {
					tags[k] = v
				}
			}
			tags[fullPath] = omitemptyTag(omitempty)
		} else if ft.Kind() == reflect.Struct {
			tags[fullPath] = omitemptyTag(omitempty)
			for k, v := range collectJSONTags(ft, fullPath) {
				tags[k] = v
			}
		} else if ft.Kind() == reflect.Ptr && ft.Elem().Kind() == reflect.Struct {
			tags[fullPath] = omitemptyTag(omitempty)
			for k, v := range collectJSONTags(ft.Elem(), fullPath) {
				tags[k] = v
			}
		} else {
			tags[fullPath] = omitemptyTag(omitempty)
		}
	}
	return tags
}

func omitemptyTag(oe bool) string {
	if oe {
		return "omitempty"
	}
	return "required"
}

func collectSchemaKeys(schema []FieldSchema, prefix string) map[string]string {
	keys := make(map[string]string)
	for _, field := range schema {
		fullPath := field.JSONKey
		if prefix != "" {
			fullPath = prefix + "." + field.JSONKey
		}
		keys[fullPath] = field.Type
		if len(field.Children) > 0 && (field.Type == "array" || field.Type == "object") {
			hasNamedChildren := false
			for _, child := range field.Children {
				if child.JSONKey != "" {
					hasNamedChildren = true
					break
				}
			}
			if !hasNamedChildren {
				continue
			}
			childPrefix := fullPath
			if field.Type == "array" {
				childPrefix = fullPath + "[]"
			}
			for k, v := range collectSchemaKeys(field.Children, childPrefix) {
				keys[k] = v
			}
		}
	}
	return keys
}

func TestSchemaCoversAllConfigStructFields(t *testing.T) {
	configTags := collectJSONTags(reflect.TypeOf(Config{}), "")
	schemaKeys := collectSchemaKeys(GetConfigSchema(), "")

	for tagPath := range configTags {
		if _, ok := schemaKeys[tagPath]; !ok {
			t.Errorf("Config struct has json tag %q but schema has no matching entry — UI won't show this field", tagPath)
		}
	}
}

func TestSchemaHasNoOrphanEntries(t *testing.T) {
	configTags := collectJSONTags(reflect.TypeOf(Config{}), "")
	schemaKeys := collectSchemaKeys(GetConfigSchema(), "")

	for schemaPath := range schemaKeys {
		if _, ok := configTags[schemaPath]; !ok {
			t.Errorf("Schema declares %q but Config struct has no matching json tag — orphan schema entry", schemaPath)
		}
	}
}

func TestIdentitiesSchemaCoversAllStructFields(t *testing.T) {
	configTags := collectJSONTags(reflect.TypeOf(IdentitiesConfig{}), "")
	schemaKeys := collectSchemaKeys(GetIdentitiesSchema(), "")

	for tagPath := range configTags {
		if _, ok := schemaKeys[tagPath]; !ok {
			t.Errorf("IdentitiesConfig struct has json tag %q but schema has no matching entry", tagPath)
		}
	}
}

func TestIdentitiesSchemaHasNoOrphans(t *testing.T) {
	configTags := collectJSONTags(reflect.TypeOf(IdentitiesConfig{}), "")
	schemaKeys := collectSchemaKeys(GetIdentitiesSchema(), "")

	for schemaPath := range schemaKeys {
		if _, ok := configTags[schemaPath]; !ok {
			t.Errorf("Identities schema declares %q but IdentitiesConfig struct has no matching json tag", schemaPath)
		}
	}
}

func TestSchemaDotPathRoundTrip(t *testing.T) {
	schema := GetConfigSchema()
	for _, field := range schema {
		if !field.Editable || field.Type == "array" || field.Type == "object" {
			continue
		}
		t.Run("dotpath."+field.JSONKey, func(t *testing.T) {
			tmp := t.TempDir()
			cm, err := NewConfigManager(
				tmp+"/config.json",
				tmp+"/install.json",
				tmp+"/identities.json",
			)
			if err != nil {
				t.Fatalf("Failed to create ConfigManager: %v", err)
			}

			testValue := getTestValueForType(field)
			if testValue == "" {
				return
			}

			err = SetDotPath(cm, field.JSONKey, testValue)
			if err != nil {
				t.Fatalf("SetDotPath(%s, %s) failed: %v", field.JSONKey, testValue, err)
			}

			loaded, loadErr := LoadConfig(tmp + "/config.json")
			if loadErr != nil {
				t.Fatalf("Failed to reload: %v", loadErr)
			}

			assertFieldValue(t, loaded, field.JSONKey, testValue, field.Type)
		})
	}
}

func TestSchemaDotPathMintChildren(t *testing.T) {
	schema := GetConfigSchema()
	var mintSchema *FieldSchema
	for i := range schema {
		if schema[i].JSONKey == "accepted_mints" {
			mintSchema = &schema[i]
			break
		}
	}
	if mintSchema == nil {
		t.Fatal("accepted_mints schema not found")
	}

	for _, child := range mintSchema.Children {
		if !child.Editable {
			continue
		}
		t.Run("mint.0."+child.JSONKey, func(t *testing.T) {
			tmp := t.TempDir()
			cm, err := NewConfigManager(
				tmp+"/config.json",
				tmp+"/install.json",
				tmp+"/identities.json",
			)
			if err != nil {
				t.Fatalf("Failed to create ConfigManager: %v", err)
			}

			testValue := getTestValueForType(child)
			if testValue == "" {
				return
			}

			dotPath := "accepted_mints.0." + child.JSONKey
			err = SetDotPath(cm, dotPath, testValue)
			if err != nil {
				t.Fatalf("SetDotPath(%s, %s) failed: %v", dotPath, testValue, err)
			}

			loaded, _ := LoadConfig(tmp + "/config.json")
			if len(loaded.AcceptedMints) == 0 {
				t.Fatal("No mints loaded")
			}
			assertMintFieldValue(t, loaded.AcceptedMints[0], child.JSONKey, testValue, child.Type)
		})
	}
}

func TestSchemaDotPathProfitShareChildren(t *testing.T) {
	schema := GetConfigSchema()
	var psSchema *FieldSchema
	for i := range schema {
		if schema[i].JSONKey == "profit_share" {
			psSchema = &schema[i]
			break
		}
	}
	if psSchema == nil {
		t.Fatal("profit_share schema not found")
	}

	for _, child := range psSchema.Children {
		if !child.Editable {
			continue
		}
		t.Run("profit_share.0."+child.JSONKey, func(t *testing.T) {
			tmp := t.TempDir()
			cm, err := NewConfigManager(
				tmp+"/config.json",
				tmp+"/install.json",
				tmp+"/identities.json",
			)
			if err != nil {
				t.Fatalf("Failed to create ConfigManager: %v", err)
			}

			testValue := getTestValueForType(child)
			if testValue == "" {
				return
			}

			dotPath := "profit_share.0." + child.JSONKey
			err = SetDotPath(cm, dotPath, testValue)
			if err != nil {
				t.Fatalf("SetDotPath(%s, %s) failed: %v", dotPath, testValue, err)
			}

			loaded, _ := LoadConfig(tmp + "/config.json")
			if len(loaded.ProfitShare) == 0 {
				t.Fatal("No profit shares loaded")
			}
			assertPSFieldValue(t, loaded.ProfitShare[0], child.JSONKey, testValue, child.Type)
		})
	}
}

func TestConfigGetSchemaConsistency(t *testing.T) {
	cfg := NewDefaultConfig()
	cfgJSON, _ := json.Marshal(cfg)
	var cfgMap map[string]interface{}
	json.Unmarshal(cfgJSON, &cfgMap)

	schema := GetConfigSchema()
	for _, field := range schema {
		if field.Required && field.Type != "array" && field.Type != "object" {
			if _, ok := cfgMap[field.JSONKey]; !ok {
				t.Errorf("Required schema field %s not in default config JSON", field.JSONKey)
			}
		}
	}

	identCfg := NewDefaultIdentitiesConfig()
	identJSON, _ := json.Marshal(identCfg)
	var identMap map[string]interface{}
	json.Unmarshal(identJSON, &identMap)

	identSchema := GetIdentitiesSchema()
	for _, field := range identSchema {
		if field.Required && field.Type != "array" && field.Type != "object" {
			if _, ok := identMap[field.JSONKey]; !ok {
				t.Errorf("Required identities schema field %s not in default identities JSON", field.JSONKey)
			}
		}
	}
}

func getTestValueForType(field FieldSchema) string {
	if len(field.Enum) > 0 {
		for _, e := range field.Enum {
			defStr, _ := field.Default.(string)
			if e != defStr {
				return e
			}
		}
		return field.Enum[0]
	}
	switch field.Type {
	case "string":
		return "test_value"
	case "uint64":
		return "99999"
	case "int":
		return "7"
	case "float64":
		return "0.5"
	case "bool":
		if def, ok := field.Default.(bool); ok && def {
			return "false"
		}
		return "true"
	case "duration":
		return "15s"
	default:
		return ""
	}
}

func assertFieldValue(t *testing.T, cfg *Config, jsonKey, value, typ string) {
	t.Helper()
	switch jsonKey {
	case "log_level":
		if cfg.LogLevel != value {
			t.Errorf("log_level: got %s, want %s", cfg.LogLevel, value)
		}
	case "metric":
		if cfg.Metric != value {
			t.Errorf("metric: got %s, want %s", cfg.Metric, value)
		}
	case "step_size":
		if typ == "uint64" && cfg.StepSize != 99999 {
			t.Errorf("step_size: got %d, want 99999", cfg.StepSize)
		}
	case "show_setup":
		if cfg.ShowSetup != false {
			t.Error("show_setup: got true, want false")
		}
	case "reseller_mode":
		if cfg.ResellerMode != true {
			t.Error("reseller_mode: got false, want true")
		}
	case "margin":
		if cfg.Margin != 0.5 {
			t.Errorf("margin: got %f, want 0.5", cfg.Margin)
		}
	}
}

func assertMintFieldValue(t *testing.T, mint MintConfig, jsonKey, value, typ string) {
	t.Helper()
	switch jsonKey {
	case "url":
		if mint.URL != value {
			t.Errorf("url: got %s, want %s", mint.URL, value)
		}
	case "min_balance":
		if mint.MinBalance != 99999 {
			t.Errorf("min_balance: got %d, want 99999", mint.MinBalance)
		}
	case "balance_tolerance_percent":
		if mint.BalanceTolerancePercent != 99999 {
			t.Errorf("balance_tolerance_percent: got %d", mint.BalanceTolerancePercent)
		}
	case "payout_interval_seconds":
		if mint.PayoutIntervalSeconds != 99999 {
			t.Errorf("payout_interval_seconds: got %d", mint.PayoutIntervalSeconds)
		}
	case "min_payout_amount":
		if mint.MinPayoutAmount != 99999 {
			t.Errorf("min_payout_amount: got %d", mint.MinPayoutAmount)
		}
	case "price_per_step":
		if mint.PricePerStep != 99999 {
			t.Errorf("price_per_step: got %d, want 99999", mint.PricePerStep)
		}
	case "price_unit":
		if mint.PriceUnit != value {
			t.Errorf("price_unit: got %s, want %s", mint.PriceUnit, value)
		}
	case "purchase_min_steps":
		if mint.MinPurchaseSteps != 99999 {
			t.Errorf("purchase_min_steps: got %d", mint.MinPurchaseSteps)
		}
	}
}

func assertPSFieldValue(t *testing.T, ps ProfitShareConfig, jsonKey, value, typ string) {
	t.Helper()
	switch jsonKey {
	case "factor":
		if ps.Factor != 0.5 {
			t.Errorf("factor: got %f, want 0.5", ps.Factor)
		}
	case "identity":
		if ps.Identity != value {
			t.Errorf("identity: got %s, want %s", ps.Identity, value)
		}
	}
}
