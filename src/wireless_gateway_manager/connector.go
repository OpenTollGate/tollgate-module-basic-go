// Package wireless_gateway_manager implements the Connector for managing OpenWRT network configurations.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"fmt"
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
		"radio":      gateway.Radio,
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

	// Configure wireless.tollgate_sta for STA mode on the correct radio
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.device="+gateway.Radio); err != nil {
		return err
	}
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
		// Only delete the key if it exists
		if _, err := c.ExecuteUCI("get", "wireless.tollgate_sta.key"); err == nil {
			if _, err := c.ExecuteUCI("delete", "wireless.tollgate_sta.key"); err != nil {
				return err
			}
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
	interfaces, err := getSTAManagedInterfaces()
	if err != nil {
		logger.WithError(err).Info("Could not get managed Wi-Fi interfaces, probably not associated")
		return "", nil
	}

	for iface := range interfaces {
		cmd := exec.Command("iw", "dev", iface, "link")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			// This is expected if the interface is not connected, so we just log debug info
			logger.WithFields(logrus.Fields{
				"interface": iface,
				"error":     err,
				"stderr":    stderr.String(),
			}).Debug("Could not get link info from interface (likely not connected)")
			continue // Try the next interface
		}

		output := stdout.String()
		if strings.Contains(output, "Not connected") {
			continue
		}

		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "SSID:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					ssid := strings.TrimSpace(parts[1])
					if ssid != "" {
						logger.WithFields(logrus.Fields{"interface": iface, "ssid": ssid}).Info("Found connected SSID")
						return ssid, nil
					}
				}
			}
		}
	}

	return "", nil // No connected SSID found on any interface
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

// ensureSTAInterfaceExists ensures a single, consistently named STA interface ('tollgate_sta') exists.
// It cleans up old STA interfaces and creates a default one on the first available radio if none exist.
func (c *Connector) ensureSTAInterfaceExists() error {
	logger.Info("Ensuring a consistent STA wifi-iface section exists")

	// First, clean up any existing STA interfaces to start from a known state.
	// This prevents issues with multiple or misconfigured STA interfaces.
	if err := c.cleanupSTAInterfaces(); err != nil {
		return fmt.Errorf("failed during cleanup of STA interfaces: %w", err)
	}

	// After cleanup, no STA interfaces should exist. We now create a single, default one.
	radios, err := c.getAvailableRadios()
	if err != nil {
		return fmt.Errorf("failed to get available radios: %w", err)
	}
	if len(radios) == 0 {
		return fmt.Errorf("no wifi radio devices found, cannot create STA interface")
	}

	// Create the default STA interface on the first available radio.
	// The 'Connect' function will later set the correct radio device based on the selected gateway.
	defaultRadio := radios[0]
	logger.WithField("radio", defaultRadio).Info("No STA interface found, creating default 'tollgate_sta'")

	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta=wifi-iface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.device="+defaultRadio); err != nil {
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

// getAvailableRadios scans the UCI configuration to find all wifi-device sections.
func (c *Connector) getAvailableRadios() ([]string, error) {
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return nil, err
	}

	radios := []string{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, "=wifi-device") {
			radioName := strings.TrimSuffix(line, "=wifi-device")
			// The section name is in the format 'wireless.radio0', 'wireless.radio1', etc.
			parts := strings.Split(radioName, ".")
			if len(parts) == 2 {
				radios = append(radios, parts[1])
			}
		}
	}

	if len(radios) == 0 {
		logger.Warn("No wifi-device sections found in UCI config")
	}

	return radios, nil
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

// SetSafeModeSSID sets or unsets the AP SSID to SafeMode.
func (c *Connector) SetSafeModeSSID(enable bool) error {
	if enable {
		return c.SetAPSSIDSafeMode()
	}
	return c.RestoreAPSSIDFromSafeMode()
}

func (c *Connector) updateAPSSIDWithPrefix(prefix string) error {
	if err := c.ensureAPInterfacesExist(); err != nil {
		return fmt.Errorf("failed to ensure AP interfaces exist: %w", err)
	}

	radios := []string{"default_radio0", "default_radio1"}
	var commitNeeded bool
	for _, radio := range radios {
		if _, err := c.ExecuteUCI("get", "wireless."+radio); err != nil {
			logger.WithField("radio", radio).Info("AP interface not found, skipping SSID update")
			continue
		}

		currentSSID, err := c.ExecuteUCI("get", "wireless."+radio+".ssid")
		if err != nil {
			logger.WithFields(logrus.Fields{"radio": radio, "error": err}).Warn("Could not get current SSID")
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
			if _, err := c.ExecuteUCI("set", "wireless."+radio+".ssid="+newSSID); err != nil {
				logger.WithFields(logrus.Fields{"radio": radio, "error": err}).Error("Failed to set new SSID")
				continue
			}
			logger.WithFields(logrus.Fields{"radio": radio, "new_ssid": newSSID}).Info("Updated AP SSID")
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


// UpdateLocalAPSSID updates the local AP's SSID to advertise the pricing information.
func (c *Connector) UpdateLocalAPSSID(pricePerStep int, stepSize int) error {
	if err := c.ensureAPInterfacesExist(); err != nil {
		logger.WithError(err).Error("Failed to ensure AP interfaces exist")
		return err // This is a significant issue, so we return the error.
	}

	// Now that we've ensured the interfaces exist, we can proceed.
	// We update both 2.4GHz and 5GHz APs if they exist.
	radios := []string{"default_radio0", "default_radio1"}
	var commitNeeded bool
	for _, radio := range radios {
		// Check if the interface section exists before trying to update it.
		if _, err := c.ExecuteUCI("get", "wireless."+radio); err != nil {
			logger.WithField("radio", radio).Info("AP interface not found, skipping SSID update")
			continue
		}

		baseSSID, err := c.ExecuteUCI("get", "wireless."+radio+".ssid")
		if err != nil {
			logger.WithFields(logrus.Fields{
				"radio": radio,
				"error": err,
			}).Warn("Could not get current SSID")
			continue
		}
		baseSSID = strings.TrimSpace(baseSSID)

		// Strip any existing pricing from the base SSID
		parts := strings.Split(baseSSID, "-")
		if len(parts) > 2 {
			// Check if the last two parts are numbers
			if _, err1 := strconv.Atoi(parts[len(parts)-2]); err1 == nil {
				if _, err2 := strconv.Atoi(parts[len(parts)-1]); err2 == nil {
					baseSSID = strings.Join(parts[:len(parts)-2], "-")
				}
			}
		}

		newSSID := fmt.Sprintf("%s-%d-%d", baseSSID, pricePerStep, stepSize)
		logger.WithFields(logrus.Fields{
			"radio":    radio,
			"new_ssid": newSSID,
		}).Info("Updating local AP SSID with pricing information")

		if _, err := c.ExecuteUCI("set", "wireless."+radio+".ssid="+newSSID); err != nil {
			logger.WithFields(logrus.Fields{
				"radio": radio,
				"error": err,
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

	// First, try to find an existing AP to get the base SSID name
	for _, radio := range []string{"default_radio0", "default_radio1"} {
		ssid, err := c.ExecuteUCI("get", "wireless."+radio+".ssid")
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

	radios := map[string]string{
		"default_radio0": "radio0", // 2.4GHz AP iface
		"default_radio1": "radio1", // 5GHz AP iface
	}

	for ifaceSection, device := range radios {
		// Check if the physical radio device exists
		if _, err := c.ExecuteUCI("get", "wireless."+device); err != nil {
			logger.WithFields(logrus.Fields{
				"device":            device,
				"interface_section": ifaceSection,
			}).Info("Physical radio device not found, cannot create AP interface")
			continue
		}

		// Check if the AP interface section already exists
		if _, err := c.ExecuteUCI("get", "wireless."+ifaceSection); err == nil {
			logger.WithField("interface_section", ifaceSection).Info("AP interface already exists")
			continue
		}

		// Interface doesn't exist, so create it based on defaults.
		logger.WithField("interface_section", ifaceSection).Info("AP interface not found, creating with consistent naming")
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+"=wifi-iface"); err != nil {
			return fmt.Errorf("failed to create wifi-iface section %s: %w", ifaceSection, err)
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+".device="+device); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+".network=lan"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+".mode=ap"); err != nil {
			return err
		}

		band := "2.4GHz"
		if device == "radio1" {
			band = "5GHz"
		}
		defaultSSID := fmt.Sprintf("%s-%s", baseSSIDName, band)
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+".ssid="+defaultSSID); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+".encryption=none"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+".disabled=0"); err != nil {
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

func (c *Connector) generateRandomSuffix(length int) (string, error) {
	cmd := exec.Command("head", "/dev/urandom")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	cmd = exec.Command("tr", "-dc", "A-Z0-9")
	cmd.Stdin = &stdout
	var stdout2 bytes.Buffer
	cmd.Stdout = &stdout2
	if err := cmd.Run(); err != nil {
		return "", err
	}

	cmd = exec.Command("head", "-c", strconv.Itoa(length))
	cmd.Stdin = &stdout2
	var finalStdout bytes.Buffer
	cmd.Stdout = &finalStdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return strings.TrimSpace(finalStdout.String()), nil
}
