// Package wireless_gateway_manager implements the Connector for managing OpenWRT network configurations.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"fmt"
	"math/big"
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

	// Find an available STA interface to use for the connection
	// Determine the band from the gateway's SSID
	band := determineBandFromSSID(gateway.SSID)
	staInterface, err := c.findAvailableSTAInterface(band)
	if err != nil {
		return fmt.Errorf("failed to find an available STA interface: %w", err)
	}
	logger.WithField("interface", staInterface).Info("Found available STA interface")

	// Disable other STA interfaces to prevent conflicts
	if err := c.disableOtherSTAInterfaces(staInterface); err != nil {
		logger.WithError(err).Warn("Could not disable other STA interfaces, proceeding anyway")
	}

	// Configure network.wwan (STA interface) with DHCP
	if _, err := c.ExecuteUCI("set", "network.wwan=interface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "network.wwan.proto=dhcp"); err != nil {
		return err
	}

	// Configure the selected STA interface
	if _, err := c.ExecuteUCI("set", staInterface+".ssid="+gateway.SSID); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", staInterface+".bssid="+gateway.BSSID); err != nil {
		return err
	}

	// Set encryption based on gateway information
	if gateway.Encryption != "" && gateway.Encryption != "Open" {
		if _, err := c.ExecuteUCI("set", staInterface+".encryption="+getUCIEncryptionType(gateway.Encryption)); err != nil {
			return err
		}
		if password != "" {
			if _, err := c.ExecuteUCI("set", staInterface+".key="+password); err != nil {
				return err
			}
		} else {
			logger.WithField("ssid", gateway.SSID).Warn("No password provided for encrypted network")
		}
	} else {
		// For open networks, ensure no encryption or key is set
		if _, err := c.ExecuteUCI("set", staInterface+".encryption=none"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("delete", staInterface+".key"); err != nil {
			// This might fail if the key doesn't exist, which is fine. The ExecuteUCI function handles this.
		}
	}

	// Enable the interface
	if _, err := c.ExecuteUCI("set", staInterface+".disabled=0"); err != nil {
		return err
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

// findAvailableSTAInterface scans the wireless config for a disabled STA interface and returns its name.
// In reseller mode, it looks for tollgate_sta_2g and tollgate_sta_5g interfaces.
func (c *Connector) findAvailableSTAInterface(band string) (string, error) {
	logger.Info("Searching for an available STA wifi-iface section")
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	var staInterfaces []string
	disabledSTAInterfaces := make(map[string]bool)
	tollgateSTA2GFound := false
	tollgateSTA5GFound := false

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, ".mode='sta'") {
			section := strings.TrimSuffix(line, ".mode='sta'")
			staInterfaces = append(staInterfaces, section)
			
			// Check if we have our specific TollGate interfaces
			if strings.HasSuffix(section, ".tollgate_sta_2g") {
				tollgateSTA2GFound = true
			} else if strings.HasSuffix(section, ".tollgate_sta_5g") {
				tollgateSTA5GFound = true
			}
		} else if strings.HasSuffix(line, ".disabled='1'") {
			section := strings.TrimSuffix(line, ".disabled='1'")
			disabledSTAInterfaces[section] = true
		}
	}

	// Create our specific TollGate interfaces if they don't exist
	if !tollgateSTA2GFound {
		logger.Info("Creating tollgate_sta_2g interface")
		if err := c.createTollgateSTAInterface("tollgate_sta_2g", "radio0"); err != nil {
			logger.WithError(err).Error("Failed to create tollgate_sta_2g interface")
			return "", err
		}
		staInterfaces = append(staInterfaces, "wireless.tollgate_sta_2g")
		disabledSTAInterfaces["wireless.tollgate_sta_2g"] = true
	}

	if !tollgateSTA5GFound {
		logger.Info("Creating tollgate_sta_5g interface")
		if err := c.createTollgateSTAInterface("tollgate_sta_5g", "radio1"); err != nil {
			logger.WithError(err).Error("Failed to create tollgate_sta_5g interface")
			return "", err
		}
		staInterfaces = append(staInterfaces, "wireless.tollgate_sta_5g")
		disabledSTAInterfaces["wireless.tollgate_sta_5g"] = true
	}

	// If a specific band is requested, try to find an interface for that band
	if band == "2g" || band == "5g" {
		// Prefer disabled interfaces to avoid disrupting an active connection
		// First, try to find a disabled TollGate interface for the requested band
		interfaceName := "tollgate_sta_" + band
		for _, iface := range staInterfaces {
			if disabledSTAInterfaces[iface] && strings.HasSuffix(iface, "."+interfaceName) {
				return iface, nil
			}
		}

		// If no disabled interface for the requested band is found, use any available one for that band
		for _, iface := range staInterfaces {
			if strings.HasSuffix(iface, "."+interfaceName) {
				return iface, nil
			}
		}
	}

	// If no specific band is requested or no interface for the requested band is found,
	// use the general logic
	// Prefer disabled interfaces to avoid disrupting an active connection
	// First, try to find a disabled TollGate interface
	for _, iface := range staInterfaces {
		if disabledSTAInterfaces[iface] && (strings.HasSuffix(iface, ".tollgate_sta_2g") || strings.HasSuffix(iface, ".tollgate_sta_5g")) {
			return iface, nil
		}
	}

	// If no disabled TollGate interface is found, use any disabled interface
	for _, iface := range staInterfaces {
		if disabledSTAInterfaces[iface] {
			return iface, nil
		}
	}

	// If no disabled interface is found, use the first available TollGate interface
	for _, iface := range staInterfaces {
		if strings.HasSuffix(iface, ".tollgate_sta_2g") || strings.HasSuffix(iface, ".tollgate_sta_5g") {
			return iface, nil
		}
	}

	// If no TollGate interface is found, use the first available one, if any exist.
	if len(staInterfaces) > 0 {
		return staInterfaces[0], nil
	}

	return "", fmt.Errorf("no STA interface found in wireless configuration")
}

// createTollgateSTAInterface creates a new STA interface with the specified name and device.
func (c *Connector) createTollgateSTAInterface(interfaceName, device string) error {
	logger.WithFields(logrus.Fields{
		"interface": interfaceName,
		"device":    device,
	}).Info("Creating new TollGate STA interface")
	
	// Create the interface section
	if _, err := c.ExecuteUCI("set", "wireless."+interfaceName+"=wifi-iface"); err != nil {
		return err
	}
	
	// Set the device
	if _, err := c.ExecuteUCI("set", "wireless."+interfaceName+".device="+device); err != nil {
		return err
	}
	
	// Set mode to sta
	if _, err := c.ExecuteUCI("set", "wireless."+interfaceName+".mode=sta"); err != nil {
		return err
	}
	
	// Set network to wwan
	if _, err := c.ExecuteUCI("set", "wireless."+interfaceName+".network=wwan"); err != nil {
		return err
	}
	
	// Disable by default
	if _, err := c.ExecuteUCI("set", "wireless."+interfaceName+".disabled=1"); err != nil {
		return err
	}
	
	// Commit the changes
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}
	
	return nil
}

// disableOtherSTAInterfaces disables all STA interfaces except for the one provided.
func (c *Connector) disableOtherSTAInterfaces(activeInterfaceName string) error {
	logger.WithField("active_interface", activeInterfaceName).Info("Disabling other STA interfaces")
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	var staInterfaces []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, ".mode='sta'") {
			section := strings.TrimSuffix(line, ".mode='sta'")
			staInterfaces = append(staInterfaces, section)
		}
	}

	var commitNeeded bool
	for _, iface := range staInterfaces {
		if iface != activeInterfaceName {
			logger.WithField("interface", iface).Debug("Disabling STA interface")
			if _, err := c.ExecuteUCI("set", iface+".disabled=1"); err != nil {
				logger.WithFields(logrus.Fields{
					"interface": iface,
					"error":     err,
				}).Warn("Failed to disable STA interface")
				continue // Continue trying to disable others
			}
			commitNeeded = true
		}
	}

	if commitNeeded {
		if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
			return fmt.Errorf("failed to commit wireless config after disabling STA interfaces: %w", err)
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
	// Assuming 'radio0' is the primary radio for STA mode.
	// This could be made more dynamic if needed.
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta=wifi-iface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.device=radio0"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.mode=sta"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.network=wwan"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.disabled=1"); err != nil {
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


// UpdateLocalAPSSID updates the local AP's SSID to advertise the current price.
func (c *Connector) UpdateLocalAPSSID(pricePerStep, stepSize int) error {
	if err := c.ensureAPInterfacesExist(); err != nil {
		logger.WithError(err).Error("Failed to ensure AP interfaces exist")
		return err
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

		// Strip existing pricing info to get the base SSID
		baseSSID := stripPricingFromSSID(currentSSID)

		newSSID := fmt.Sprintf("%s-%d-%d", baseSSID, pricePerStep, stepSize)
		logger.WithFields(logrus.Fields{
			"radio":    radio,
			"new_ssid": newSSID,
		}).Info("Updating local AP SSID with new pricing")

		if currentSSID != newSSID {
			if _, err := c.ExecuteUCI("set", "wireless."+radio+".ssid="+newSSID); err != nil {
				logger.WithFields(logrus.Fields{"radio": radio, "error": err}).Error("Failed to set new SSID")
				continue
			}
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
	const chars = "0123456789"
	result := make([]byte, length)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[num.Int64()]
	}
	return string(result), nil
}

func stripPricingFromSSID(ssid string) string {
	parts := strings.Split(ssid, "-")
	if len(parts) < 4 { // Expects TollGate-<ID>-<price>-<step>
		return ssid // Not a format we can parse, return original
	}

	// Check if the last two parts are numbers (price and step)
	_, err1 := strconv.Atoi(parts[len(parts)-1])
	_, err2 := strconv.Atoi(parts[len(parts)-2])

	if err1 == nil && err2 == nil {
		// Both are numbers, so strip them
		return strings.Join(parts[:len(parts)-2], "-")
	}

	return ssid // Return original if parsing fails
}

// determineBandFromSSID attempts to determine the band (2g or 5g) from the SSID
func determineBandFromSSID(ssid string) string {
	// If the SSID contains "2.4GHz" or "2G", assume it's a 2.4GHz network
	if strings.Contains(ssid, "2.4GHz") || strings.Contains(ssid, "2G") {
		return "2g"
	}
	
	// If the SSID contains "5GHz" or "5G", assume it's a 5GHz network
	if strings.Contains(ssid, "5GHz") || strings.Contains(ssid, "5G") {
		return "5g"
	}
	
	// Default to empty string if we can't determine the band
	return ""
}
