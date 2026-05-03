package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func newTestServerWithConfig(t *testing.T) (*CLIServer, func()) {
	t.Helper()
	tempDir := t.TempDir()
	cm, err := config_manager.NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}
	return NewCLIServer(cm, nil), func() {}
}

func TestHandleConfigCommandNoArgs(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigCommand([]string{}, nil)
	if resp.Success {
		t.Error("expected failure with no args")
	}
	if !strings.Contains(resp.Error, "requires a subcommand") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleConfigCommandUnknownSubcommand(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigCommand([]string{"delete"}, nil)
	if resp.Success {
		t.Error("expected failure for unknown subcommand")
	}
	if !strings.Contains(resp.Error, "Unknown config subcommand") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleConfigSetMissingArgs(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigCommand([]string{"set", "metric"}, nil)
	if resp.Success {
		t.Error("expected failure with missing value")
	}
	if !strings.Contains(resp.Error, "requires <key> <value>") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleConfigSaveMissingJSON(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigCommand([]string{"save"}, nil)
	if resp.Success {
		t.Error("expected failure with missing JSON")
	}
}

func TestHandleConfigSaveIdentitiesMissingJSON(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigCommand([]string{"save-identities"}, nil)
	if resp.Success {
		t.Error("expected failure with missing JSON")
	}
}

func TestHandleConfigGetNilConfigManager(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleConfigGet()
	if resp.Success {
		t.Error("expected failure with nil config manager")
	}
	if !strings.Contains(resp.Error, "Config manager not available") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleConfigGetSuccess(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigGet()
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("data is not a map")
	}
	if _, ok := data["config"]; !ok {
		t.Error("missing 'config' in response data")
	}
	if _, ok := data["identities"]; !ok {
		t.Error("missing 'identities' in response data")
	}
}

func TestHandleConfigGetContainsRealData(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigGet()
	data := resp.Data.(map[string]interface{})
	cfg, ok := data["config"].(*config_manager.Config)
	if !ok {
		t.Fatal("config is not a *Config")
	}
	if cfg.Metric == "" {
		t.Error("config metric is empty")
	}
	if cfg.StepSize == 0 {
		t.Error("config step_size is 0")
	}
}

func TestHandleConfigSetNilConfigManager(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleConfigSet("metric", "bytes")
	if resp.Success {
		t.Error("expected failure with nil config manager")
	}
}

func TestHandleConfigSetSuccess(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigSet("metric", "milliseconds")
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("data is not a map")
	}
	if data["key"] != "metric" {
		t.Errorf("key: got %v, want 'metric'", data["key"])
	}
	if data["value"] != "milliseconds" {
		t.Errorf("value: got %v, want 'milliseconds'", data["value"])
	}
}

func TestHandleConfigSetInvalidKey(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigSet("nonexistent_field_xyz", "value")
	if resp.Success {
		t.Error("expected failure for nonexistent field")
	}
}

func TestHandleConfigSetInvalidValue(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigSet("step_size", "not-a-number")
	if resp.Success {
		t.Error("expected failure for non-numeric step_size")
	}
}

func TestHandleConfigSchemaAlwaysSucceeds(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleConfigSchema()
	if !resp.Success {
		t.Errorf("schema should succeed even with nil config manager: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("data is not a map")
	}
	configSchema, ok := data["config"].([]config_manager.FieldSchema)
	if !ok {
		t.Fatal("config schema is not []FieldSchema")
	}
	if len(configSchema) == 0 {
		t.Error("config schema is empty")
	}
	identSchema, ok := data["identities"].([]config_manager.FieldSchema)
	if !ok {
		t.Fatal("identities schema is not []FieldSchema")
	}
	if len(identSchema) == 0 {
		t.Error("identities schema is empty")
	}
}

func TestHandleConfigSchemaHasRequiredFields(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleConfigSchema()
	data := resp.Data.(map[string]interface{})
	configSchema := data["config"].([]config_manager.FieldSchema)
	keys := make(map[string]bool)
	for _, f := range configSchema {
		keys[f.JSONKey] = true
	}
	for _, key := range []string{"metric", "step_size", "accepted_mints", "profit_share", "show_setup", "reseller_mode"} {
		if !keys[key] {
			t.Errorf("config schema missing field: %s", key)
		}
	}
}

func TestHandleConfigSaveNilConfigManager(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleConfigSave(`{"metric":"bytes"}`)
	if resp.Success {
		t.Error("expected failure with nil config manager")
	}
}

func TestHandleConfigSaveInvalidJSON(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigSave("not-json")
	if resp.Success {
		t.Error("expected failure for invalid JSON")
	}
	if !strings.Contains(resp.Error, "Invalid JSON") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleConfigSaveMissingRequiredFields(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleConfigSave(`{"metric":"bytes"}`)
	if resp.Success {
		t.Error("expected failure for missing required fields")
	}
	if !strings.Contains(resp.Error, "Missing required fields") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
	if !strings.Contains(resp.Error, "config_version") {
		t.Errorf("error should mention config_version: %s", resp.Error)
	}
}

func TestHandleConfigSaveSuccess(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	cfg := config_manager.NewDefaultConfig()
	jsonBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Failed to marshal default config: %v", err)
	}
	resp := s.handleConfigSave(string(jsonBytes))
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	if !strings.Contains(resp.Message, "saved") {
		t.Errorf("unexpected message: %s", resp.Message)
	}
}

func TestHandleConfigSavePersistsToDisk(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	cfg := config_manager.NewDefaultConfig()
	cfg.Metric = "milliseconds"
	cfg.StepSize = 999999
	jsonBytes, _ := json.Marshal(cfg)
	resp := s.handleConfigSave(string(jsonBytes))
	if !resp.Success {
		t.Fatalf("save failed: %s", resp.Error)
	}
	loaded, err := config_manager.LoadConfig(s.configManager.ConfigFilePath)
	if err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}
	if loaded.Metric != "milliseconds" {
		t.Errorf("metric not persisted: got %s", loaded.Metric)
	}
	if loaded.StepSize != 999999 {
		t.Errorf("step_size not persisted: got %d", loaded.StepSize)
	}
}

func TestHandleIdentitiesSaveNilConfigManager(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleIdentitiesSave(`{}`)
	if resp.Success {
		t.Error("expected failure with nil config manager")
	}
}

func TestHandleIdentitiesSaveInvalidJSON(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	resp := s.handleIdentitiesSave("not-json")
	if resp.Success {
		t.Error("expected failure for invalid JSON")
	}
	if !strings.Contains(resp.Error, "Invalid JSON") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleIdentitiesSaveSuccess(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	identities := config_manager.NewDefaultIdentitiesConfig()
	jsonBytes, err := json.Marshal(identities)
	if err != nil {
		t.Fatalf("Failed to marshal identities: %v", err)
	}
	resp := s.handleIdentitiesSave(string(jsonBytes))
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	if !strings.Contains(resp.Message, "Identities saved") {
		t.Errorf("unexpected message: %s", resp.Message)
	}
}

func TestHandleConfigSetAndSaveRoundTrip(t *testing.T) {
	s, cleanup := newTestServerWithConfig(t)
	defer cleanup()
	setResp := s.handleConfigSet("metric", "milliseconds")
	if !setResp.Success {
		t.Fatalf("set failed: %s", setResp.Error)
	}
	getResp := s.handleConfigGet()
	if !getResp.Success {
		t.Fatalf("get failed: %s", getResp.Error)
	}
	data := getResp.Data.(map[string]interface{})
	cfg := data["config"].(*config_manager.Config)
	if cfg.Metric != "milliseconds" {
		t.Errorf("metric not updated: got %s", cfg.Metric)
	}
}
