package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func (s *CLIServer) handleConfigCommand(args []string, flags map[string]string) CLIResponse {
	if len(args) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Config command requires a subcommand (get, set, apply, schema, save, save-identities)",
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
	case "apply":
		return s.handleConfigApply()
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
			Error:     fmt.Sprintf("Unknown config subcommand: %s (supported: get, set, apply, schema, save, save-identities)", subcommand),
			Timestamp: time.Now(),
		}
	}
}

// handleConfigGet returns the whole configuration. Schema fields marked
// `secret` are blanked and their state is reported separately: this payload is
// rendered by the board's Settings page and merged back into a wholesale
// `config save`, so it must never carry the private network's passphrase.
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
			"config":     redactSecretFields(cfg),
			"identities": identities,
			"secret_set": secretFieldState(cfg),
		},
		Timestamp: time.Now(),
	}
}

// handleConfigApply converges every declared operator setting onto this router
// on demand. The same work runs automatically after a config write and at
// daemon start; this is the explicit "make the router match the file" verb,
// for a hand-edited config.json.
func (s *CLIServer) handleConfigApply() CLIResponse {
	if s.configManager == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Config manager not available",
			Timestamp: time.Now(),
		}
	}

	results := ApplyOperatorSettings(s.configManager.GetConfig())

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Operator settings converged (%s)", applySummary(results)),
		Data:      map[string]interface{}{"applied": results},
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

	// A declared setting is worth nothing until the component that enforces it
	// has it: converge the runtime now (UCI wireless for the private-network
	// credentials, the generated admin-scope fragment for admin_access) and
	// report per setting what happened. Compare-and-converge, so a key that maps
	// to no applier costs one comparison and changes nothing.
	results := ApplyOperatorSettings(s.configManager.GetConfig())

	data := map[string]interface{}{
		"key":     key,
		"applied": results,
	}

	message := fmt.Sprintf("Set %s = %s (restart tollgate-wrt to apply)", key, value)
	if isSecretJSONKey(key) {
		// A secret is never echoed back: the response is rendered by the board
		// and kept by whatever shell ran the command.
		message = fmt.Sprintf("Set %s (value withheld)", key)
	} else {
		data["value"] = value
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("%s; %s", message, applySummary(results)),
		Data:      data,
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

	// A wholesale save replaces the whole file, so it is the one path that can
	// silently erase a setting: `config get` blanks secret fields (see
	// redactSecretFields), and the board merges that payload back here. An empty
	// incoming value therefore means "unchanged", never "clear", for every
	// operator setting whose empty value is already "keep what the router has".
	preserved := []string{}
	if stored := s.configManager.GetConfig(); stored != nil {
		if cfg.PrivateKey == "" && stored.PrivateKey != "" {
			cfg.PrivateKey = stored.PrivateKey
			preserved = append(preserved, "private_key")
		}
		if cfg.PrivateSSID == "" && stored.PrivateSSID != "" {
			cfg.PrivateSSID = stored.PrivateSSID
			preserved = append(preserved, "private_ssid")
		}
		if cfg.PrivateEncryption == "" && stored.PrivateEncryption != "" {
			cfg.PrivateEncryption = stored.PrivateEncryption
			preserved = append(preserved, "private_encryption")
		}
		if cfg.AdminAccess == "" && stored.AdminAccess != "" {
			cfg.AdminAccess = stored.AdminAccess
			preserved = append(preserved, "admin_access")
		}
	}

	// The wholesale path bypasses per-key schema validation, so the enum values
	// a wrong string can break the router with are checked here: an unknown
	// private_encryption would take the private network down, and an unknown
	// admin_access must not silently become the default scope.
	if err := config_manager.ValidateValue("private_encryption", cfg.PrivateEncryption); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Invalid private_encryption: %v", err),
			Timestamp: time.Now(),
		}
	}
	if err := config_manager.ValidateValue("admin_access", cfg.AdminAccess); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Invalid admin_access: %v", err),
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

	if err := s.configManager.ReloadConfig(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Config saved but reload failed: %v", err),
			Timestamp: time.Now(),
		}
	}

	results := ApplyOperatorSettings(s.configManager.GetConfig())

	message := fmt.Sprintf("Configuration saved; %s", applySummary(results))
	if len(preserved) > 0 {
		message = fmt.Sprintf("%s (kept unchanged: %s)", message, strings.Join(preserved, ", "))
	}

	return CLIResponse{
		Success:   true,
		Message:   message,
		Data:      map[string]interface{}{"applied": results},
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

	if err := s.configManager.ReloadIdentities(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Identities saved but reload failed: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   "Identities saved (restart tollgate-wrt to apply)",
		Timestamp: time.Now(),
	}
}

// isSecretJSONKey reports whether a dotpath names a schema field marked secret,
// so a response can withhold the value it was handed instead of echoing it.
func isSecretJSONKey(key string) bool {
	for _, secret := range secretJSONKeys() {
		if key == secret || strings.HasPrefix(key, secret+".") {
			return true
		}
	}
	return false
}
