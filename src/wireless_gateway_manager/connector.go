// Package wireless_gateway_manager implements the Connector for managing OpenWRT network configurations.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
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

	// Find an available STA interface to use for the connection.
	staInterface, err := c.findAvailableSTAInterface("")
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

	// Configure the selected STA interface to use wwan network
	if _, err := c.ExecuteUCI("set", staInterface+".network=wwan"); err != nil {
		return err
	}
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

	// Wait a moment for the interface to come up
	time.Sleep(2 * time.Second)

	// Get the actual device name from wireless status and set it on wwan network
	deviceName, err := c.getDeviceNameForInterface(staInterface)
	if err != nil {
		logger.WithError(err).Warn("Failed to get device name for wwan, network may not come up properly")
	} else {
		logger.WithFields(logrus.Fields{
			"interface": staInterface,
			"device":    deviceName,
		}).Info("Setting wwan network device")

		if _, err := c.ExecuteUCI("set", "network.wwan.device="+deviceName); err != nil {
			logger.WithError(err).Warn("Failed to set wwan device")
		} else {
			if _, err := c.ExecuteUCI("commit", "network"); err != nil {
				logger.WithError(err).Warn("Failed to commit network config")
			} else {
				// Bring up the wwan interface
				cmd := exec.Command("ifup", "wwan")
				if err := cmd.Run(); err != nil {
					logger.WithError(err).Warn("Failed to bring up wwan interface")
				} else {
					logger.Info("Successfully brought up wwan interface")
				}
			}
		}
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

func (c *Connector) reloadRadio(radio string) error {
	cmd := exec.Command("wifi", "reload", radio)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logger.WithFields(logrus.Fields{
			"radio":  radio,
			"error":  err,
			"stderr": stderr.String(),
		}).Error("Failed to reload radio")
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

	// Enable by default so it can be used for scanning
	if _, err := c.ExecuteUCI("set", "wireless."+interfaceName+".disabled=0"); err != nil {
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
	if _, err := c.ExecuteUCI("set", "wireless.tollgate_sta.disabled=0"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}
	return c.reloadWifi()
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

	// Reuse any existing "TollGate-XXXX" SSID already on a radio.
	for _, radio := range []string{"default_radio0", "default_radio1"} {
		ssid, err := c.ExecuteUCI("get", "wireless."+radio+".ssid")
		if err != nil {
			continue
		}
		ssid = strings.TrimSpace(ssid)
		if strings.HasPrefix(ssid, "TollGate-") {
			baseSSIDName = ssid
			logger.WithField("base_name", baseSSIDName).Info("Found existing AP with base name")
			break
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

		// Both radios broadcast the same SSID so clients see one network and
		// band-steer. Previously we appended "-2.4GHz" / "-5GHz" here.
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceSection+".ssid="+baseSSIDName); err != nil {
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

// Disconnect disconnects from the current network.
func (c *Connector) Disconnect() error {
	logger.Info("Disconnecting from current network")

	// Find the currently active STA interface
	activeInterface, err := c.getActiveSTAInterface()
	if err != nil {
		return fmt.Errorf("failed to get active STA interface: %w", err)
	}

	if activeInterface == "" {
		logger.Info("No active STA interface found, nothing to disconnect")
		return nil
	}

	// Disable the active interface
	if _, err := c.ExecuteUCI("set", activeInterface+".disabled=1"); err != nil {
		return fmt.Errorf("failed to disable interface %s: %w", activeInterface, err)
	}

	// Commit the changes
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return fmt.Errorf("failed to commit wireless config: %w", err)
	}

	// Reload wifi to apply changes
	if err := c.reloadWifi(); err != nil {
		return fmt.Errorf("failed to reload wifi: %w", err)
	}

	logger.WithField("interface", activeInterface).Info("Successfully disconnected from network")
	return nil
}

// Reconnect attempts to reconnect to the network.
// This is a simple implementation that just reloads the wifi.
func (c *Connector) Reconnect() error {
	logger.Info("Reconnecting to network")

	// Reload wifi to apply any pending changes or reconnect
	if err := c.reloadWifi(); err != nil {
		return fmt.Errorf("failed to reload wifi: %w", err)
	}

	logger.Info("Reconnect command issued")
	return nil
}

// getActiveSTAInterface finds the currently active STA interface.
func (c *Connector) getActiveSTAInterface() (string, error) {
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		// Look for an interface that is in sta mode and not disabled
		if strings.HasSuffix(line, ".mode='sta'") {
			section := strings.TrimSuffix(line, ".mode='sta'")
			// Check if it's disabled
			disabledOutput, err := c.ExecuteUCI("get", section+".disabled")
			if err != nil {
				// If we can't get the disabled status, assume it's enabled
				return section, nil
			}
			if strings.TrimSpace(disabledOutput) != "1" {
				return section, nil
			}
		}
	}

	return "", nil // No active STA interface found
}

// getDeviceNameForInterface gets the actual device name (ifname) for a wireless interface section
// by querying the wireless status via ubus
func (c *Connector) getDeviceNameForInterface(interfaceSection string) (string, error) {
	// Extract just the section name from "wireless.tollgate_sta_2g" -> "tollgate_sta_2g"
	parts := strings.Split(interfaceSection, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid interface section format: %s", interfaceSection)
	}
	sectionName := parts[len(parts)-1]

	// Call ubus to get wireless status
	cmd := exec.Command("ubus", "call", "network.wireless", "status")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get wireless status: %w, stderr: %s", err, stderr.String())
	}

	// Parse the JSON output
	var status map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		return "", fmt.Errorf("failed to parse wireless status JSON: %w", err)
	}

	// Search through all radios for our interface section
	for radioName, radioData := range status {
		radioMap, ok := radioData.(map[string]interface{})
		if !ok {
			continue
		}

		interfaces, ok := radioMap["interfaces"].([]interface{})
		if !ok {
			continue
		}

		for _, iface := range interfaces {
			ifaceMap, ok := iface.(map[string]interface{})
			if !ok {
				continue
			}

			section, ok := ifaceMap["section"].(string)
			if !ok || section != sectionName {
				continue
			}

			// Found our interface! Get the ifname
			ifname, ok := ifaceMap["ifname"].(string)
			if !ok {
				return "", fmt.Errorf("interface %s found but has no ifname", sectionName)
			}

			logger.WithFields(logrus.Fields{
				"section": sectionName,
				"radio":   radioName,
				"ifname":  ifname,
			}).Debug("Found device name for interface")

			return ifname, nil
		}
	}

	return "", fmt.Errorf("interface section %s not found in wireless status", sectionName)
}

// Ensure Connector implements ConnectorInterface
var _ ConnectorInterface = (*Connector)(nil)

func (c *Connector) GetSTASections() ([]STASection, error) {
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	var sections []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "=wifi-iface") {
			parts := strings.SplitN(line, ".", 2)
			if len(parts) < 2 {
				continue
			}
			sectionPart := parts[1]
			eqIdx := strings.Index(sectionPart, "=")
			if eqIdx >= 0 {
				sections = append(sections, sectionPart[:eqIdx])
			}
		}
	}

	var staSections []STASection
	for _, section := range sections {
		modeOutput, err := c.ExecuteUCI("get", "wireless."+section+".mode")
		if err != nil || strings.TrimSpace(modeOutput) != "sta" {
			continue
		}

		ssid, _ := c.ExecuteUCI("get", "wireless."+section+".ssid")
		device, _ := c.ExecuteUCI("get", "wireless."+section+".device")
		encryption, _ := c.ExecuteUCI("get", "wireless."+section+".encryption")
		disabledOutput, _ := c.ExecuteUCI("get", "wireless."+section+".disabled")

		staSections = append(staSections, STASection{
			Name:       section,
			SSID:       strings.TrimSpace(ssid),
			Device:     strings.TrimSpace(device),
			Encryption: strings.TrimSpace(encryption),
			Disabled:   strings.TrimSpace(disabledOutput) == "1",
		})
	}

	return staSections, nil
}

func (c *Connector) GetActiveSTA() (*STASection, error) {
	sections, err := c.GetSTASections()
	if err != nil {
		return nil, err
	}
	for i := range sections {
		if !sections[i].Disabled {
			return &sections[i], nil
		}
	}
	return nil, nil
}

func sanitizeSSIDForUCI(ssid string) string {
	sanitized := strings.ToLower(ssid)
	sanitized = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, sanitized)
	if len(sanitized) > 40 {
		sanitized = sanitized[:40]
	}
	return "upstream_" + sanitized
}

func (c *Connector) FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio string) (string, error) {
	sections, err := c.GetSTASections()
	if err != nil {
		return "", err
	}

	for _, section := range sections {
		if section.SSID == ssid && section.Disabled {
			logger.WithFields(logrus.Fields{
				"interface": section.Name,
				"ssid":      ssid,
			}).Info("Reusing existing disabled STA interface")
			return section.Name, nil
		}
	}

	ifaceName := sanitizeSSIDForUCI(ssid)
	logger.WithFields(logrus.Fields{
		"interface": ifaceName,
		"ssid":      ssid,
	}).Info("Creating new named STA interface")

	if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+"=wifi-iface"); err != nil {
		return "", fmt.Errorf("failed to create wifi-iface section %s: %w", ifaceName, err)
	}
	if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".device="+radio); err != nil {
		return "", fmt.Errorf("failed to set device for %s: %w", ifaceName, err)
	}
	if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".mode=sta"); err != nil {
		return "", err
	}
	if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".network=wwan"); err != nil {
		return "", err
	}
	if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".ssid="+ssid); err != nil {
		return "", err
	}
	if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".encryption="+encryption); err != nil {
		return "", err
	}
	if passphrase != "" {
		if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".key="+passphrase); err != nil {
			return "", err
		}
	} else {
		c.ExecuteUCI("delete", "wireless."+ifaceName+".key")
	}
	if _, err := c.ExecuteUCI("set", "wireless."+ifaceName+".disabled=1"); err != nil {
		return "", err
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return "", err
	}

	return ifaceName, nil
}

func (c *Connector) RemoveDisabledSTA(ssid string) error {
	sections, err := c.GetSTASections()
	if err != nil {
		return err
	}

	for _, section := range sections {
		if section.SSID == ssid {
			if !section.Disabled {
				return fmt.Errorf("cannot remove active upstream '%s', switch first", ssid)
			}
			logger.WithFields(logrus.Fields{
				"interface": section.Name,
				"ssid":      ssid,
			}).Info("Removing disabled upstream STA")
			if _, err := c.ExecuteUCI("delete", "wireless."+section.Name); err != nil {
				return fmt.Errorf("failed to delete STA section %s: %w", section.Name, err)
			}
			if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
				return err
			}
			if err := c.reloadWifi(); err != nil {
				logger.WithError(err).Warn("Failed to reload wifi after removing upstream")
			}
			return nil
		}
	}

	return fmt.Errorf("no disabled upstream found with SSID '%s'", ssid)
}

func (c *Connector) SwitchUpstream(activeIface, candidateIface, candidateSSID string) error {
	candidateRadio, _ := c.ExecuteUCI("get", "wireless."+candidateIface+".device")
	candidateRadio = strings.TrimSpace(candidateRadio)
	if candidateRadio == "" {
		return fmt.Errorf("no radio found for interface %s", candidateIface)
	}

	activeRadio := ""
	if activeIface != "" {
		r, _ := c.ExecuteUCI("get", "wireless."+activeIface+".device")
		activeRadio = strings.TrimSpace(r)
	}

	logger.WithFields(logrus.Fields{
		"active":         activeIface,
		"active_radio":   activeRadio,
		"candidate":      candidateIface,
		"candidate_radio": candidateRadio,
		"ssid":           candidateSSID,
	}).Info("Switching upstream")

	crossRadio := activeRadio != "" && activeRadio != candidateRadio

	if _, err := c.ExecuteUCI("set", "wireless."+candidateIface+".disabled=0"); err != nil {
		return fmt.Errorf("failed to enable candidate upstream %s: %w", candidateIface, err)
	}

	if !crossRadio && activeIface != "" {
		if _, err := c.ExecuteUCI("set", "wireless."+activeIface+".disabled=1"); err != nil {
			return fmt.Errorf("failed to disable active upstream %s: %w", activeIface, err)
		}
	}

	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}

	reloadDone := make(chan error, 1)
	go func() {
		reloadDone <- c.reloadRadio(candidateRadio)
	}()

	staIface, err := c.waitForSTAIP(candidateRadio, 180*time.Second)
	if err == nil && staIface != "" {
		go func() { <-reloadDone }()
		logger.WithFields(logrus.Fields{
			"ssid":  candidateSSID,
			"iface": staIface,
			"radio": candidateRadio,
		}).Info("Successfully switched upstream")

		if crossRadio && activeIface != "" {
			if _, err := c.ExecuteUCI("set", "wireless."+activeIface+".disabled=1"); err != nil {
				logger.WithError(err).Warn("Failed to disable old upstream on other radio (non-fatal)")
			}
			if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
				logger.WithError(err).Warn("Failed to commit after disabling old upstream")
			}
			if err := c.reloadRadio(activeRadio); err != nil {
				logger.WithError(err).Warn("Failed to reload old radio after disabling old upstream")
			}
		}

		exec.Command("/etc/init.d/dnsmasq", "restart").Run()
		exec.Command("/etc/init.d/firewall", "restart").Run()
		return nil
	}

	logger.WithFields(logrus.Fields{
		"ssid":      candidateSSID,
		"candidate": candidateIface,
	}).Warn("Timed out waiting for DHCP, reverting to previous upstream")

	<-reloadDone

	if _, err := c.ExecuteUCI("set", "wireless."+candidateIface+".disabled=1"); err != nil {
		logger.WithError(err).Error("Failed to disable candidate during fallback")
	}

	if err := c.reloadRadio(candidateRadio); err != nil {
		logger.WithError(err).Error("Failed to reload candidate radio during fallback")
	}

	if activeIface != "" {
		if _, err := c.ExecuteUCI("set", "wireless."+activeIface+".disabled=0"); err != nil {
			logger.WithError(err).Error("Failed to re-enable previous upstream during fallback")
		}
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		logger.WithError(err).Error("Failed to commit during fallback")
	}
	if err := c.reloadRadio(candidateRadio); err != nil {
		logger.WithError(err).Error("Failed to reload radio during fallback")
	}

	return fmt.Errorf("timed out waiting for DHCP on %s, reverted to previous upstream", candidateSSID)
}

func (c *Connector) GetSTADevice(ifaceName string) (string, error) {
	output, err := c.ExecuteUCI("get", "wireless."+ifaceName+".device")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func (c *Connector) waitForSTAIP(radio string, timeout time.Duration) (string, error) {
	radioNum := strings.TrimPrefix(radio, "radio")
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		entries, err := os.ReadDir("/sys/class/net")
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		for _, entry := range entries {
			name := entry.Name()
			if !strings.Contains(name, "sta") && !strings.Contains(name, "wlan") {
				continue
			}

			phyIdx, err := os.ReadFile("/sys/class/net/" + name + "/phy80211/index")
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(phyIdx)) == radioNum {
				if ip := c.getInterfaceIP(name); ip != "" {
					return name, nil
				}
				logger.WithFields(logrus.Fields{
					"iface": name,
					"radio": radio,
					"remaining": time.Until(deadline).Truncate(time.Second),
				}).Debug("STA interface found but no IP yet")
			}
		}
		time.Sleep(2 * time.Second)
	}

	return "", fmt.Errorf("timed out waiting for STA IP on radio %s", radio)
}

func (c *Connector) getInterfaceIP(iface string) string {
	cmd := exec.Command("ip", "-o", "-4", "addr", "show", "dev", iface)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		for _, line := range strings.Split(out.String(), "\n") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "inet" && i+1 < len(fields) {
					ip := strings.SplitN(fields[i+1], "/", 2)[0]
					if ip != "" {
						return ip
					}
				}
			}
		}
	}

	cmd = exec.Command("ifconfig", iface)
	out.Reset()
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "inet addr:") {
			parts := strings.SplitN(line, "inet addr:", 2)
			if len(parts) == 2 {
				ip := strings.SplitN(parts[1], " ", 2)[0]
				if ip != "" {
					return ip
				}
			}
		}
	}
	return ""
}

func (c *Connector) EnsureWWANSetup() error {
	if _, err := c.ExecuteUCI("get", "network.wwan"); err != nil {
		logger.Info("Creating network.wwan interface (DHCP)")
		if _, err := c.ExecuteUCI("set", "network.wwan=interface"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("set", "network.wwan.proto=dhcp"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("commit", "network"); err != nil {
			return err
		}
		exec.Command("/etc/init.d/network", "reload").Run()
	}

	output, err := c.ExecuteUCI("show", "firewall")
	if err != nil {
		logger.WithError(err).Warn("Failed to query firewall config")
		return nil
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	var wanZone string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "=zone") {
			parts := strings.SplitN(line, ".", 2)
			if len(parts) < 2 {
				continue
			}
			zonePart := parts[1]
			eqIdx := strings.Index(zonePart, "=")
			if eqIdx < 0 {
				continue
			}
			zoneName := zonePart[:eqIdx]
			nameOutput, _ := c.ExecuteUCI("get", "firewall."+zoneName+".name")
			if strings.TrimSpace(nameOutput) == "wan" {
				wanZone = zoneName
				break
			}
		}
	}

	if wanZone != "" {
		networks, _ := c.ExecuteUCI("get", "firewall."+wanZone+".network")
		if !strings.Contains(networks, "wwan") {
			logger.Info("Adding wwan to wan firewall zone")
			if _, err := c.ExecuteUCI("add_list", "firewall."+wanZone+".network=wwan"); err != nil {
				return err
			}
			if _, err := c.ExecuteUCI("commit", "firewall"); err != nil {
				return err
			}
			exec.Command("/etc/init.d/firewall", "reload").Run()
		}
	}

	return nil
}

func (c *Connector) EnsureRadiosEnabled() error {
	radios, err := c.getRadiosFromConfig()
	if err != nil {
		return err
	}

	var changed bool
	for _, radio := range radios {
		disabled, _ := c.ExecuteUCI("get", "wireless."+radio+".disabled")
		if strings.TrimSpace(disabled) == "1" {
			logger.WithField("radio", radio).Info("Enabling disabled radio")
			if _, err := c.ExecuteUCI("set", "wireless."+radio+".disabled=0"); err != nil {
				return err
			}
			changed = true
		}
	}

	if changed {
		if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
			return err
		}
		exec.Command("wifi", "up").Run()
		time.Sleep(5 * time.Second)
	}

	return nil
}

func (c *Connector) getRadiosFromConfig() ([]string, error) {
	data, err := os.ReadFile("/etc/config/wireless")
	if err != nil {
		return nil, fmt.Errorf("failed to read wireless config: %w", err)
	}

	var radios []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "config wifi-device") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start >= 0 && end > start {
				radios = append(radios, line[start+1:end])
			}
		}
	}
	return radios, nil
}
