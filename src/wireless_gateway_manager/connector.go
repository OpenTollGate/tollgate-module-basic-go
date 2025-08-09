// Package wireless_gateway_manager implements the Connector for managing OpenWRT network configurations.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Connect configures the network to connect to the specified gateway.
func (c *Connector) Connect(gateway Gateway, password string) error {
	logger.WithFields(logrus.Fields{
		"ssid":       gateway.SSID,
		"bssid":      gateway.BSSID,
		"encryption": gateway.Encryption,
	}).Info("Attempting to connect to gateway")

	// Ensure a STA interface exists, creating one if necessary
	if err := c.ensureSTAInterfaceExists(); err != nil {
		return fmt.Errorf("failed to ensure STA interface exists: %w", err)
	}

	// Configure network.wwan (STA interface) with DHCP
	if _, err := c.ExecuteUCI("set", "network.wwan=interface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "network.wwan.proto=dhcp"); err != nil {
		return err
	}

	// Configure wireless.tollgate_sta for STA mode
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.ssid="+gateway.SSID); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.bssid="+gateway.BSSID); err != nil {
		return err
	}

	// Set encryption based on gateway information
	if gateway.Encryption != "" && gateway.Encryption != "Open" {
		if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.encryption="+getUCIEncryptionType(gateway.Encryption)); err != nil {
			return err
		}
		if password != "" {
			if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.key="+password); err != nil {
				return err
			}
		} else {
			logger.WithField("ssid", gateway.SSID).Warn("No password provided for encrypted network")
		}
	} else {
		// For open networks, ensure no encryption or key is set
		if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.encryption=none"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("delete", "wireless.tollgate_sta.key"); err != nil {
			return err
		}
	}

	// Commit changes
	if _, err := c.ExecuteUCI("commit", "network"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}

	// Reload wifi to apply changes
	if err := c.reloadWifi(); err != nil {
		return err
	}

	logger.WithField("ssid", gateway.SSID).Info("Successfully configured connection for gateway")

	// Verify the connection
	return c.verifyConnection(gateway.SSID)
}

func getUCIEncryptionType(encryption string) string {
	switch encryption {
	case "WPA/WPA2":
		return "psk2"
	case "WPA2":
		return "psk2"
	case "WPA":
		return "psk"
	case "WEP":
		return "wep"
	default:
		return "none" // Fallback for unknown or open types
	}
}

func (c *Connector) GetConnectedSSID() (string, error) {
	interfaceName, err := getInterfaceName()
	if err != nil {
		logger.WithError(err).Info("Could not get managed Wi-Fi interface, probably not associated")
		return "", nil
	}

	cmd := exec.Command("iw", "dev", interfaceName, "link")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"error":     err,
			"stderr":    stderr.String(),
		}).Warn("Could not get connected SSID from interface")
		return "", nil // Not an error if not connected, but return empty string
	}

	output := stdout.String()
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "SSID:") {
			// Correctly parse the line, which is formatted as "\tSSID: MySSID"
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}

	return "", nil // No SSID found, likely not connected
}

// ExecuteUCI executes a UCI command.
func (c *Connector) ExecuteUCI(args ...string) (string, error) {
	cmd := exec.Command("uci", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// For 'delete', "Entry not found" is not a critical error.
		if len(args) > 0 && args[0] == "delete" && strings.Contains(stderr.String(), "Entry not found") {
			logger.WithField("command", strings.Join(args, " ")).Debug("UCI entry to delete was not found (which is okay)")
			return "", nil
		}
		logger.WithFields(logrus.Fields{
			"error":  err,
			"stderr": stderr.String(),
		}).Error("Failed to execute UCI command")
		return "", err
	}

	return stdout.String(), nil
}

func (c *Connector) reloadWifi() error {
	cmd := exec.Command("wifi", "reload")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logger.WithFields(logrus.Fields{
			"error":  err,
			"stderr": stderr.String(),
		}).Error("Failed to reload wifi")
		return err
	}

	return nil
}

// verifyConnection checks if the device is connected to the specified SSID.
func (c *Connector) verifyConnection(expectedSSID string) error {
	logger.WithField("ssid", expectedSSID).Info("Verifying connection")
	const retries = 10
	const delay = 3 * time.Second

	for i := 0; i < retries; i++ {
		time.Sleep(delay)
		currentSSID, err := c.GetConnectedSSID()
		if err != nil {
			logger.WithError(err).Warn("Verification check failed: could not get current SSID")
			continue
		}

		if currentSSID == expectedSSID {
			logger.WithField("ssid", expectedSSID).Info("Successfully connected")
			return nil
		}
		logger.WithFields(logrus.Fields{
			"expected_ssid": expectedSSID,
			"current_ssid":  currentSSID,
		}).Info("Still not connected, retrying")
	}

	return fmt.Errorf("failed to verify connection to %s after %d retries", expectedSSID, retries)
}

func (c *Connector) cleanupSTAInterfaces() error {
	logger.Info("Cleaning up existing STA wifi-iface sections")
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	sectionsToDelete := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, ".mode='sta'") {
			section := strings.TrimSuffix(line, ".mode='sta'")
			sectionsToDelete = append(sectionsToDelete, section)
		}
	}

	for _, section := range sectionsToDelete {
		logger.WithField("section", section).Debug("Deleting old STA interface section")
		if _, err := c.ExecuteUCI("delete", section); err != nil {
			// We log the error but continue, as a failed delete is not critical
			logger.WithFields(logrus.Fields{
				"section": section,
				"error":   err,
			}).Warn("Failed to delete section")
		}
	}

	return nil
}

// ensureSTAInterfaceExists checks for a STA interface and creates a default one if it doesn't exist.
func (c *Connector) ensureSTAInterfaceExists() error {
	logger.Info("Ensuring STA wifi-iface section exists")
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return err
	}

	if strings.Contains(output, ".mode='sta'") {
		logger.Info("STA interface already exists")
		return nil
	}

	logger.Info("No STA interface found, creating default")
	radioNames, err := c.GetRadioDeviceNames()
	if err != nil {
		return fmt.Errorf("failed to get radio device names: %w", err)
	}
	// Assuming the first radio is suitable for STA mode.
	primaryRadio := radioNames[0]

	logger.WithField("radio", primaryRadio).Info("Using primary radio for new STA interface")

	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta=wifi-iface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.device="+primaryRadio); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.mode=sta"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.network=wwan"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.disabled=0"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}
	return c.reloadWifi()
}

// SetAPSSIDSafeMode renames the local AP's SSID to a "SafeMode-" prefix.
func (c *Connector) SetAPSSIDSafeMode() error {
	logger.Info("Setting AP SSID to SafeMode")
	return c.updateAPSSIDWithPrefix("SafeMode-")
}

// RestoreAPSSIDFromSafeMode removes the "SafeMode-" prefix from the local AP's SSID.
func (c *Connector) RestoreAPSSIDFromSafeMode() error {
	logger.Info("Restoring AP SSID from SafeMode")
	return c.updateAPSSIDWithPrefix("") // Pass an empty prefix to restore the original name
}

func (c *Connector) updateAPSSIDWithPrefix(prefix string) error {
	radioNames, err := c.GetRadioDeviceNames()
	if err != nil {
		logger.WithError(err).Error("Failed to get radio device names for AP SSID update")
		return err
	}

	var commitNeeded bool
	for _, radioName := range radioNames {
		ifaceName := getDefaultInterfaceName(radioName)
		if _, err := c.ExecuteUCI("get", "wireless."+ifaceName); err != nil {
			logger.WithField("interface", ifaceName).Info("AP interface not found, skipping SSID update")
			continue
		}

		currentSSID, err := c.ExecuteUCI("get", "wireless."+ifaceName+".ssid")
		if err != nil {
			logger.WithFields(logrus.Fields{"interface": ifaceName, "error": err}).Warn("Could not get current SSID")
			continue
		}
		currentSSID = strings.TrimSpace(currentSSID)
		baseSSID := strings.TrimPrefix(currentSSID, "SafeMode-") // Remove prefix if it exists

		var newSSID string
		if prefix != "" {
			newSSID = prefix + baseSSID
		} else {
			newSSID = baseSSID // This is the restored SSID
		}

		if currentSSID != newSSID {
			if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".ssid="+newSSID); err != nil {
				logger.WithFields(logrus.Fields{"interface": ifaceName, "error": err}).Error("Failed to set new SSID")
				continue
			}
			logger.WithFields(logrus.Fields{"interface": ifaceName, "new_ssid": newSSID}).Info("Updated AP SSID")
			commitNeeded = true
		}
	}

	if commitNeeded {
		if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
			return fmt.Errorf("failed to commit wireless config for AP SSID update: %w", err)
		}
		logger.Info("Reloading wifi to apply new AP SSID")
		return c.reloadWifi()
	}

	return nil
}


// UpdateLocalAPSSID updates the local AP's SSID to advertise the current hop count.
func (c *Connector) UpdateLocalAPSSID(hopCount int) error {
	if err := c.ensureAPInterfacesExist(); err != nil {
		logger.WithError(err).Error("Failed to ensure AP interfaces exist")
		return err // This is a significant issue, so we return the error.
	}

	radioNames, err := c.GetRadioDeviceNames()
	if err != nil {
		logger.WithError(err).Error("Failed to get radio device names for AP SSID update")
		return err // Return the error to the caller
	}
	var commitNeeded bool
	for _, radioName := range radioNames {
		ifaceName := getDefaultInterfaceName(radioName)
		// Check if the interface section exists before trying to update it.
		if _, err := c.ExecuteUCI("get", "wireless."+ifaceName); err != nil {
			logger.WithField("interface", ifaceName).Info("AP interface not found, skipping SSID update")
			continue
		}

		baseSSID, err := c.ExecuteUCI("get", "wireless."+ifaceName+".ssid")
		if err != nil {
			logger.WithFields(logrus.Fields{
				"interface": ifaceName,
				"error":     err,
			}).Warn("Could not get current SSID")
			continue // Try the next radio
		}
		baseSSID = strings.TrimSpace(baseSSID)

		// Strip any existing hop count from the base SSID
		parts := strings.Split(baseSSID, "-")
		if len(parts) > 1 {
			lastPart := parts[len(parts)-1]
			if _, err := strconv.Atoi(lastPart); err == nil {
				// It ends with a number, so it's a hop count. Strip it.
				baseSSID = strings.Join(parts[:len(parts)-1], "-")
			}
		}

		var newSSID string
		if hopCount == math.MaxInt32 {
			newSSID = baseSSID
			logger.WithFields(logrus.Fields{
				"interface": ifaceName,
				"ssid":      newSSID,
			}).Info("Disconnected, setting AP SSID to base")
		} else {
			newSSID = fmt.Sprintf("%s-%d", baseSSID, hopCount)
			logger.WithFields(logrus.Fields{
				"interface": ifaceName,
				"ssid":      newSSID,
			}).Info("Updating local AP SSID")
		}

		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".ssid="+newSSID); err != nil {
			logger.WithFields(logrus.Fields{
				"interface": ifaceName,
				"error":     err,
			}).Error("Failed to set new SSID")
			continue
		}
		commitNeeded = true
	}

	if commitNeeded {
		if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
			return fmt.Errorf("failed to commit wireless config for AP SSID update: %w", err)
		}
		logger.Info("Reloading wifi to apply new AP SSID")
		return c.reloadWifi()
	}

	return nil
}

// ensureAPInterfacesExist checks for and creates the default TollGate AP interfaces if they don't exist.
func (c *Connector) ensureAPInterfacesExist() error {
	logger.Info("Ensuring default AP interfaces exist")
	var created bool
	var baseSSIDName string

	radioNames, err := c.GetRadioDeviceNames()
	if err != nil {
		return fmt.Errorf("failed to get radio names for ensuring AP interfaces: %w", err)
	}

	// First, try to find an existing AP to get the base SSID name
	for _, radioName := range radioNames {
		ifaceName := getDefaultInterfaceName(radioName)
		ssid, err := c.ExecuteUCI("get", "wireless."+ifaceName+".ssid")
		if err == nil {
			ssid = strings.TrimSpace(ssid)
			if strings.HasPrefix(ssid, "TollGate-") {
				parts := strings.Split(ssid, "-")
				// Extracts "TollGate-XXXX" from "TollGate-XXXX-2.4GHz" or "TollGate-XXXX-5GHz-1"
				if len(parts) >= 2 {
					baseSSIDName = strings.Join(parts[0:2], "-") // "TollGate-XXXX"
					logger.WithField("base_name", baseSSIDName).Info("Found existing AP with base name")
					break
				}
			}
		}
	}

	// If no base name was found, generate a new one
	if baseSSIDName == "" {
		randomSuffix, err := c.generateRandomSuffix(4)
		if err != nil {
			return fmt.Errorf("failed to generate random suffix for SSID: %w", err)
		}
		baseSSIDName = "TollGate-" + randomSuffix
		logger.WithField("base_name", baseSSIDName).Info("No existing AP found, generated new base name")
	}

	for i, radioName := range radioNames {
		ifaceName := getDefaultInterfaceName(radioName)

		// Check if the AP interface section already exists
		if _, err := c.ExecuteUCI("get", "wireless."+ifaceName); err == nil {
			logger.WithField("interface", ifaceName).Info("AP interface already exists")
			continue
		}

		// Interface doesn't exist, so create it based on defaults.
		logger.WithField("interface", ifaceName).Info("AP interface not found, creating with consistent naming")
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+"=wifi-iface"); err != nil {
			return fmt.Errorf("failed to create wifi-iface section %s: %w", ifaceName, err)
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".device="+radioName); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".network=lan"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".mode=ap"); err != nil {
			return err
		}

		// Basic band detection based on radio index
		band := "2.4GHz"
		if i > 0 { // Assume subsequent radios are 5GHz, a common convention
			band = "5GHz"
		}
		defaultSSID := fmt.Sprintf("%s-%s", baseSSIDName, band)
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".ssid="+defaultSSID); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".encryption=none"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".disabled=0"); err != nil {
			return err
		}
		created = true
	}

	if created {
		logger.Info("Default AP interfaces were created/updated, committing changes")
		_, err := c.ExecuteUCI("commit", "wireless")
		return err
	}

	return nil
}

// GetRadioDeviceNames dynamically discovers the names of the radio devices from the UCI configuration.
func (c *Connector) GetRadioDeviceNames() ([]string, error) {
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return nil, fmt.Errorf("failed to get wireless config: %w", err)
	}

	var radioNames []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, "='wifi-device'") {
			section := strings.TrimSuffix(line, "='wifi-device'")
			parts := strings.Split(section, ".")
			if len(parts) > 0 {
				radioNames = append(radioNames, parts[len(parts)-1])
			}
		}
	}

	if len(radioNames) == 0 {
		return nil, errors.New("no wifi-device sections found in wireless config")
	}

	logger.WithField("radios", radioNames).Info("Discovered radio devices")
	return radioNames, nil
}

func (c *Connector) generateRandomSuffix(length int) (string, error) {
	// This implementation is not cryptographically secure, but is sufficient for this purpose.
	// A more robust implementation would use crypto/rand.
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b), nil
}

func getDefaultInterfaceName(radioName string) string {
	return "tollgate_" + radioName
}
