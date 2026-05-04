package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func (s *CLIServer) handleConfigCommand(args []string, flags map[string]string) CLIResponse {
	if len(args) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Config command requires a subcommand (get, set, schema, save, save-identities)",
			Timestamp: time.Now(),
		}
	}

	subcommand := args[0]
	switch subcommand {
	case "get":
		return s.handleConfigGet()
	case "set":
		if len(args) < 3 {
			return CLIResponse{
				Success:   false,
				Error:     "config set requires <key> <value>",
				Timestamp: time.Now(),
			}
		}
		return s.handleConfigSet(args[1], args[2])
	case "schema":
		return s.handleConfigSchema()
	case "save":
		if len(args) < 2 {
			return CLIResponse{
				Success:   false,
				Error:     "config save requires <json-string>",
				Timestamp: time.Now(),
			}
		}
		return s.handleConfigSave(args[1])
	case "save-identities":
		if len(args) < 2 {
			return CLIResponse{
				Success:   false,
				Error:     "config save-identities requires <json-string>",
				Timestamp: time.Now(),
			}
		}
		return s.handleIdentitiesSave(args[1])
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown config subcommand: %s (supported: get, set, schema, save, save-identities)", subcommand),
			Timestamp: time.Now(),
		}
	}
}

func (s *CLIServer) handleConfigGet() CLIResponse {
	if s.configManager == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Config manager not available",
			Timestamp: time.Now(),
		}
	}

	cfg := s.configManager.GetConfig()
	identities := s.configManager.GetIdentities()

	return CLIResponse{
		Success: true,
		Message: "Configuration retrieved",
		Data: map[string]interface{}{
			"config":     cfg,
			"identities": identities,
		},
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleConfigSet(key, value string) CLIResponse {
	if s.configManager == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Config manager not available",
			Timestamp: time.Now(),
		}
	}

	err := config_manager.SetDotPath(s.configManager, key, value)
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to set %s: %v", key, err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success: true,
		Message: fmt.Sprintf("Set %s = %s (restart tollgate-wrt to apply)", key, value),
		Data: map[string]interface{}{
			"key":   key,
			"value": value,
		},
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleConfigSchema() CLIResponse {
	return CLIResponse{
		Success: true,
		Message: "Configuration schema",
		Data: map[string]interface{}{
			"config":     config_manager.GetConfigSchema(),
			"identities": config_manager.GetIdentitiesSchema(),
		},
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleConfigSave(jsonStr string) CLIResponse {
	if s.configManager == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Config manager not available",
			Timestamp: time.Now(),
		}
	}

	var cfg config_manager.Config
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Invalid JSON: %v", err),
			Timestamp: time.Now(),
		}
	}

	requiredFields := []string{"config_version", "metric", "step_size", "accepted_mints", "profit_share"}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("JSON parse error: %v", err),
			Timestamp: time.Now(),
		}
	}
	var missing []string
	for _, f := range requiredFields {
		if _, ok := raw[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Missing required fields: %v", missing),
			Timestamp: time.Now(),
		}
	}

	if err := cfg.ValidateProfitShare(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Invalid profit_share: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := config_manager.SaveConfig(s.configManager.ConfigFilePath, &cfg); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to save config: %v", err),
			Timestamp: time.Now(),
		}
	}

	s.configManager.ReloadConfig()

	return CLIResponse{
		Success:   true,
		Message:   "Configuration saved (restart tollgate-wrt to apply)",
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleIdentitiesSave(jsonStr string) CLIResponse {
	if s.configManager == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Config manager not available",
			Timestamp: time.Now(),
		}
	}

	var identities config_manager.IdentitiesConfig
	if err := json.Unmarshal([]byte(jsonStr), &identities); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Invalid JSON: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := config_manager.SaveIdentities(s.configManager.IdentitiesFilePath, &identities); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to save identities: %v", err),
			Timestamp: time.Now(),
		}
	}

	s.configManager.ReloadIdentities()

	return CLIResponse{
		Success:   true,
		Message:   "Identities saved (restart tollgate-wrt to apply)",
		Timestamp: time.Now(),
	}
}
