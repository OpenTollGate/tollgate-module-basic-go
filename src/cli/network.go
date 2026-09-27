package cli

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os/exec"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
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
	case "set-encryption":
		if len(args) < 2 {
			return CLIResponse{
				Success:   false,
				Error:     fmt.Sprintf("set-encryption requires an encryption mode (%s)", strings.Join(privateEncryptionAlgorithms, ", ")),
				Timestamp: time.Now(),
			}
		}
		return s.handlePrivateNetworkSetEncryption(args[1])
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown private network action: %s (supported: status, enable, disable, rename, set-password, set-encryption)", action),
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

	// Get password - it might not be set yet
	password, err := getUCIValue("wireless.private_radio0.key")
	if err != nil {
		password = "(not set)"
	}

	// Get disabled status
	disabled, _ := getUCIValue("wireless.private_radio0.disabled")
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
	if err := validatePrivateCredential("private_ssid", newSSID); err != nil {
		return CLIResponse{Success: false, Error: err.Error(), Timestamp: time.Now()}
	}

	changed, err := setPrivateRadioOption("ssid", newSSID)
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to rename the private network: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := finishPrivateRadioWrite(changed); err != nil {
		return CLIResponse{Success: false, Error: err.Error(), Timestamp: time.Now()}
	}

	// The same value goes into config.json. Both surfaces are now writers of the
	// private network (this command and the config file / the board), and
	// without this the applier would treat a stale config.json value as the
	// operator's intent and put the old SSID back at the next daemon start.
	warning := s.recordPrivateSetting("private_ssid", newSSID)

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Private network renamed to '%s' successfully%s", newSSID, warning),
		Timestamp: time.Now(),
	}
}

// handlePrivateNetworkSetPassword changes the private network password
func (s *CLIServer) handlePrivateNetworkSetPassword(newPassword string) CLIResponse {
	// If no password provided, generate a random one
	generated := newPassword == ""
	if generated {
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

	changed, err := setPrivateRadioOption("key", newPassword)
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to change the private network password: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := finishPrivateRadioWrite(changed); err != nil {
		return CLIResponse{Success: false, Error: err.Error(), Timestamp: time.Now()}
	}

	// Recorded so the applier (config apply / daemon start) converges onto this
	// passphrase instead of restoring the one config.json still had. A failure
	// to record is a warning: the wireless is already serving the new password.
	warning := s.recordPrivateSetting("private_key", newPassword)

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Private network password changed successfully%s", warning),
		Data:      privateNetworkPasswordData(generated, newPassword),
		Timestamp: time.Now(),
	}
}

// privateNetworkPasswordData is the body of a successful `private-net
// set-password`. The passphrase is echoed back ONLY when this call minted it:
// a random passphrase has no other way of reaching the operator, while a value
// the caller supplied is already known to the caller and is therefore never
// echoed. This verb is a root-console command, not a request path, and it is
// deliberately not a read path for a passphrase the operator already had to
// know — see docs/architecture/lan-port-management-bridge-decision.md (D11).
func privateNetworkPasswordData(generated bool, newPassword string) map[string]interface{} {
	if !generated {
		return nil
	}
	return map[string]interface{}{"new_password": newPassword}
}

// handlePrivateNetworkSetEncryption sets the encryption mode of the private
// network. Until this existed the mode was a literal in 99-tollgate-setup
// (psk2+ccmp at both radios) that every full setup pass rewrote, so an operator
// had no way to change it and no way to keep a change.
func (s *CLIServer) handlePrivateNetworkSetEncryption(encryption string) CLIResponse {
	if err := validatePrivateCredential("private_encryption", encryption); err != nil {
		return CLIResponse{Success: false, Error: err.Error(), Timestamp: time.Now()}
	}

	changed, err := setPrivateRadioOption("encryption", encryption)
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to set the private network encryption: %v", err),
			Timestamp: time.Now(),
		}
	}

	if err := finishPrivateRadioWrite(changed); err != nil {
		return CLIResponse{Success: false, Error: err.Error(), Timestamp: time.Now()}
	}

	warning := s.recordPrivateSetting("private_encryption", encryption)

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Private network encryption set to '%s'%s", encryption, warning),
		Timestamp: time.Now(),
	}
}

// setPrivateRadioOption writes one option on every private radio that exists,
// and returns the sections it changed.
//
// It exists because the three private-network commands used to write
// wireless.private_radio0 and wireless.private_radio1 independently, each
// ignoring a failure on radio1 — which is how the two radios drift apart — and
// because `uci set` on a section that does not exist CREATES it, so a
// single-radio router ended up with a typeless wifi-iface section. Writing only
// existing sections and reporting "nothing to write" fixes both.
func setPrivateRadioOption(option, value string) ([]string, error) {
	sections := existingPrivateRadioSections()
	if len(sections) == 0 {
		return nil, fmt.Errorf("this router has no private network (no wireless.private_radio0 section); run the TollGate setup first")
	}

	var changed []string
	for _, section := range sections {
		current, err := getUCIValue(section + "." + option)
		if err == nil && current == value {
			continue
		}
		if err := setUCIValue(section+"."+option, value); err != nil {
			return changed, fmt.Errorf("%s.%s: %w", section, option, err)
		}
		changed = append(changed, section)
	}
	return changed, nil
}

// finishPrivateRadioWrite commits and reloads. A no-op write (every radio
// already carried the value) still reports success but does not bounce the
// wireless.
func finishPrivateRadioWrite(changed []string) error {
	if len(changed) == 0 {
		return nil
	}
	if err := commitUCI("wireless"); err != nil {
		return fmt.Errorf("failed to commit wireless changes: %w", err)
	}
	if err := reloadWireless(); err != nil {
		return fmt.Errorf("failed to reload wireless: %w", err)
	}
	return nil
}

// recordPrivateSetting mirrors a private-network change into config.json, which
// is the file the applier treats as the operator's declared intent. It returns
// a suffix for the success message (empty on success) rather than failing the
// command: the radio already carries the new value.
func (s *CLIServer) recordPrivateSetting(key, value string) string {
	if s.configManager == nil {
		return " (not recorded in config.json: no config manager; the next `config apply` would use the value already in the file)"
	}
	if err := config_manager.SetDotPath(s.configManager, key, value); err != nil {
		cliLogger.WithError(err).WithField("key", key).Warn("Failed to record the private-network change in config.json")
		return fmt.Sprintf(" (warning: could not record %s in config.json: %v)", key, err)
	}
	return ""
}

// generateRandomPassword generates a human-readable random password
func generateRandomPassword() (string, error) {
	words := []string{
		"alpha", "bravo", "charlie", "delta", "echo", "foxtrot",
		"golf", "hotel", "india", "juliet", "kilo", "lima",
		"mike", "november", "oscar", "papa", "quebec", "romeo",
		"sierra", "tango", "uniform", "victor", "whiskey", "xray",
		"yankee", "zulu",
	}

	word1, err := randomWord(words)
	if err != nil {
		return "", err
	}
	word2, err := randomWord(words)
	if err != nil {
		return "", err
	}
	word3, err := randomWord(words)
	if err != nil {
		return "", err
	}
	num, err := rand.Int(rand.Reader, big.NewInt(100))
	if err != nil {
		return "", err
	}

	capitalize := func(s string) string {
		if len(s) == 0 {
			return s
		}
		return strings.ToUpper(string(s[0])) + s[1:]
	}

	return fmt.Sprintf("%s-%s-%s-%02d", capitalize(word1), capitalize(word2), capitalize(word3), num.Int64()), nil
}

func randomWord(words []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return "", err
	}
	return words[n.Int64()], nil
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
	if strings.ContainsAny(key, "\n\r\x00") {
		return fmt.Errorf("invalid UCI key: contains control characters")
	}
	if strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("invalid UCI value: contains control characters")
	}
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
