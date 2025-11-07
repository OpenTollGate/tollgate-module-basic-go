package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// handleNetworkCommand processes network-related commands
func (s *CLIServer) handleNetworkCommand(args []string, flags map[string]string) CLIResponse {
	if len(args) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Network command requires a subcommand (private)",
			Timestamp: time.Now(),
		}
	}

	subcommand := args[0]
	switch subcommand {
	case "private":
		return s.handlePrivateNetworkCommand(args[1:], flags)
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown network subcommand: %s (supported: private)", subcommand),
			Timestamp: time.Now(),
		}
	}
}

// handlePrivateNetworkCommand processes private network commands
func (s *CLIServer) handlePrivateNetworkCommand(args []string, flags map[string]string) CLIResponse {
	if len(args) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Private network command requires an action (status, enable, disable, rename, password)",
			Timestamp: time.Now(),
		}
	}

	action := args[0]
	switch action {
	case "status":
		return s.handlePrivateNetworkStatus()
	case "enable":
		return s.handlePrivateNetworkEnable()
	case "disable":
		return s.handlePrivateNetworkDisable()
	case "rename":
		if len(args) < 2 {
			return CLIResponse{
				Success:   false,
				Error:     "Rename command requires a new SSID name",
				Timestamp: time.Now(),
			}
		}
		return s.handlePrivateNetworkRename(args[1])
	case "set-password":
		if len(args) < 2 {
			// Generate new random password
			return s.handlePrivateNetworkSetPassword("")
		}
		return s.handlePrivateNetworkSetPassword(args[1])
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown private network action: %s (supported: status, enable, disable, rename, set-password)", action),
			Timestamp: time.Now(),
		}
	}
}

// handlePrivateNetworkStatus returns the current private network configuration
func (s *CLIServer) handlePrivateNetworkStatus() CLIResponse {
	ssid, err := getUCIValue("wireless.private_radio0.ssid")
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get private network SSID: %v", err),
			Timestamp: time.Now(),
		}
	}

	password, err := getUCIValue("wireless.private_radio0.key")
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get private network password: %v", err),
			Timestamp: time.Now(),
		}
	}

	disabled, err := getUCIValue("wireless.private_radio0.disabled")
	enabled := disabled != "1" // If disabled is not "1", it's enabled

	info := PrivateNetworkInfo{
		SSID:     ssid,
		Password: password,
		Enabled:  enabled,
	}

	return CLIResponse{
		Success:   true,
		Message:   "", // No message, just show the formatted data
		Data:      info,
		Timestamp: time.Now(),
	}
}

// handlePrivateNetworkEnable enables the private network
func (s *CLIServer) handlePrivateNetworkEnable() CLIResponse {
	// Enable both 2.4GHz and 5GHz private interfaces
	if err := setUCIValue("wireless.private_radio0.disabled", "0"); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to enable 2.4GHz private network: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := setUCIValue("wireless.private_radio1.disabled", "0"); err != nil {
		cliLogger.WithError(err).Warn("Failed to enable 5GHz private network (may not exist or be in client mode)")
	}

	if err := commitUCI("wireless"); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to commit wireless changes: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := reloadWireless(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to reload wireless: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   "Private network enabled successfully",
		Timestamp: time.Now(),
	}
}

// handlePrivateNetworkDisable disables the private network
func (s *CLIServer) handlePrivateNetworkDisable() CLIResponse {
	// Disable both 2.4GHz and 5GHz private interfaces
	if err := setUCIValue("wireless.private_radio0.disabled", "1"); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to disable 2.4GHz private network: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := setUCIValue("wireless.private_radio1.disabled", "1"); err != nil {
		cliLogger.WithError(err).Warn("Failed to disable 5GHz private network (may not exist)")
	}

	if err := commitUCI("wireless"); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to commit wireless changes: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := reloadWireless(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to reload wireless: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   "Private network disabled successfully",
		Timestamp: time.Now(),
	}
}

// handlePrivateNetworkRename renames the private network SSID
func (s *CLIServer) handlePrivateNetworkRename(newSSID string) CLIResponse {
	if newSSID == "" {
		return CLIResponse{
			Success:   false,
			Error:     "SSID cannot be empty",
			Timestamp: time.Now(),
		}
	}

	// Update SSID for both radios
	if err := setUCIValue("wireless.private_radio0.ssid", newSSID); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to rename 2.4GHz private network: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := setUCIValue("wireless.private_radio1.ssid", newSSID); err != nil {
		cliLogger.WithError(err).Warn("Failed to rename 5GHz private network (may not exist)")
	}

	if err := commitUCI("wireless"); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to commit wireless changes: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := reloadWireless(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to reload wireless: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Private network renamed to '%s' successfully", newSSID),
		Timestamp: time.Now(),
	}
}

// handlePrivateNetworkSetPassword changes the private network password
func (s *CLIServer) handlePrivateNetworkSetPassword(newPassword string) CLIResponse {
	// If no password provided, generate a random one
	if newPassword == "" {
		var err error
		newPassword, err = generateRandomPassword()
		if err != nil {
			return CLIResponse{
				Success:   false,
				Error:     fmt.Sprintf("Failed to generate random password: %v", err),
				Timestamp: time.Now(),
			}
		}
	}

	// Validate password length (WPA2 requires 8-63 characters)
	if len(newPassword) < 8 || len(newPassword) > 63 {
		return CLIResponse{
			Success:   false,
			Error:     "Password must be between 8 and 63 characters",
			Timestamp: time.Now(),
		}
	}

	// Update password for both radios
	if err := setUCIValue("wireless.private_radio0.key", newPassword); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to change 2.4GHz private network password: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := setUCIValue("wireless.private_radio1.key", newPassword); err != nil {
		cliLogger.WithError(err).Warn("Failed to change 5GHz private network password (may not exist)")
	}

	if err := commitUCI("wireless"); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to commit wireless changes: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := reloadWireless(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to reload wireless: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success: true,
		Message: "Private network password changed successfully",
		Data: map[string]interface{}{
			"new_password": newPassword,
		},
		Timestamp: time.Now(),
	}
}

// generateRandomPassword generates a human-readable random password
func generateRandomPassword() (string, error) {
	// Use the same word list as the shell script
	words := []string{
		"alpha", "bravo", "charlie", "delta", "echo", "foxtrot",
		"golf", "hotel", "india", "juliet", "kilo", "lima",
		"mike", "november", "oscar", "papa", "quebec", "romeo",
		"sierra", "tango", "uniform", "victor", "whiskey", "xray",
		"yankee", "zulu",
	}

	// Generate 3 random words using time-based randomness
	word1 := words[time.Now().UnixNano()%int64(len(words))]
	time.Sleep(1 * time.Millisecond)
	word2 := words[time.Now().UnixNano()%int64(len(words))]
	time.Sleep(1 * time.Millisecond)
	word3 := words[time.Now().UnixNano()%int64(len(words))]

	// Add a random 2-digit number
	num := time.Now().UnixNano() % 100

	// Capitalize first letter of each word
	capitalize := func(s string) string {
		if len(s) == 0 {
			return s
		}
		return strings.ToUpper(string(s[0])) + s[1:]
	}

	return fmt.Sprintf("%s-%s-%s-%02d", capitalize(word1), capitalize(word2), capitalize(word3), num), nil
}

// getUCIValue retrieves a UCI configuration value
func getUCIValue(key string) (string, error) {
	cmd := exec.Command("uci", "-q", "get", key)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get UCI value: %v", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// setUCIValue sets a UCI configuration value
func setUCIValue(key, value string) error {
	cmd := exec.Command("uci", "set", fmt.Sprintf("%s=%s", key, value))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set UCI value: %v", err)
	}
	return nil
}

// commitUCI commits UCI changes for a specific config
func commitUCI(config string) error {
	cmd := exec.Command("uci", "commit", config)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to commit UCI changes: %v", err)
	}
	return nil
}

// reloadWireless reloads the wireless configuration
func reloadWireless() error {
	cmd := exec.Command("wifi", "reload")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reload wireless: %v", err)
	}
	return nil
}
