// Package wireless_gateway_manager implements the Connector for managing OpenWRT network configurations.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Connector manages OpenWRT network configurations via UCI commands.
type Connector struct {
	log *log.Logger
}

// Connect configures the network to connect to the specified gateway.
func (c *Connector) Connect(gateway Gateway, password string) error {
	c.log.Printf("[wireless_gateway_manager] Attempting to connect to gateway %s (%s) with encryption %s", gateway.SSID, gateway.BSSID, gateway.Encryption)

	// Clean up existing STA interfaces to avoid conflicts
	if err := c.cleanupSTAInterfaces(); err != nil {
		return fmt.Errorf("failed to cleanup existing STA interfaces: %w", err)
	}

	// Configure network.wwan (STA interface) with DHCP
	if _, err := c.ExecuteUCI("set", "network.wwan=interface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "network.wwan.proto=dhcp"); err != nil {
		return err
	}

	// Configure wireless.wifinet0 for STA mode
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0=wifi-iface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.device=radio0"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.mode=sta"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.network=wwan"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.ssid="+gateway.SSID); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.bssid="+gateway.BSSID); err != nil {
		return err
	}

	// Set encryption based on gateway information
	if gateway.Encryption != "" && gateway.Encryption != "Open" {
		if _, err := c.ExecuteUCI("set", "wireless.wifinet0.encryption="+getUCIEncryptionType(gateway.Encryption)); err != nil {
			return err
		}
		if password != "" {
			if _, err := c.ExecuteUCI("set", "wireless.wifinet0.key="+password); err != nil {
				return err
			}
		} else {
			c.log.Printf("[wireless_gateway_manager] WARN: No password provided for encrypted network %s", gateway.SSID)
		}
	} else {
		// For open networks, ensure no encryption or key is set
		if _, err := c.ExecuteUCI("delete", "wireless.wifinet0.encryption"); err != nil {
			return err
		}
		if _, err := c.ExecuteUCI("delete", "wireless.wifinet0.key"); err != nil {
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

	c.log.Printf("[wireless_gateway_manager] Successfully configured connection for gateway %s", gateway.SSID)

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
		c.log.Printf("[wireless_gateway_manager] INFO: Could not get managed Wi-Fi interface, probably not associated: %v", err)
		return "", nil
	}

	cmd := exec.Command("iw", "dev", interfaceName, "link")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.log.Printf("[wireless_gateway_manager] WARN: Could not get connected SSID from interface %s: %v, stderr: %s", interfaceName, err, stderr.String())
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
			c.log.Printf("[wireless_gateway_manager] INFO: UCI entry to delete was not found (which is okay): uci %s", strings.Join(args, " "))
			return "", nil
		}
		c.log.Printf("[wireless_gateway_manager] ERROR: Failed to execute UCI command: %v, stderr: %s", err, stderr.String())
		return "", err
	}

	return stdout.String(), nil
}

func (c *Connector) reloadWifi() error {
	cmd := exec.Command("wifi", "reload")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.log.Printf("[wireless_gateway_manager] ERROR: Failed to reload wifi: %v, stderr: %s", err, stderr.String())
		return err
	}

	return nil
}

// verifyConnection checks if the device is connected to the specified SSID.
func (c *Connector) verifyConnection(expectedSSID string) error {
	c.log.Printf("[wireless_gateway_manager] Verifying connection to %s...", expectedSSID)
	const retries = 10
	const delay = 3 * time.Second

	for i := 0; i < retries; i++ {
		time.Sleep(delay)
		currentSSID, err := c.GetConnectedSSID()
		if err != nil {
			c.log.Printf("[wireless_gateway_manager] WARN: Verification check failed: could not get current SSID: %v", err)
			continue
		}

		if currentSSID == expectedSSID {
			c.log.Printf("[wireless_gateway_manager] Successfully connected to %s", expectedSSID)
			return nil
		}
		c.log.Printf("[wireless_gateway_manager] INFO: Still not connected to %s, currently on %s. Retrying...", expectedSSID, currentSSID)
	}

	return fmt.Errorf("failed to verify connection to %s after %d retries", expectedSSID, retries)
}

func (c *Connector) cleanupSTAInterfaces() error {
	c.log.Println("[wireless_gateway_manager] Cleaning up existing STA wifi-iface sections...")
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
		c.log.Printf("[wireless_gateway_manager] Deleting old STA interface section: %s", section)
		if _, err := c.ExecuteUCI("delete", section); err != nil {
			// We log the error but continue, as a failed delete is not critical
			c.log.Printf("[wireless_gateway_manager] WARN: Failed to delete section %s: %v", section, err)
		}
	}

	return nil
}

// EnableLocalAP enables the local Wi-Fi access point.
func (c *Connector) EnableLocalAP() error {
	c.log.Println("[wireless_gateway_manager] Enabling local APs")
	if _, err := c.ExecuteUCI("set", "wireless.default_radio0.disabled=0"); err != nil {
		c.log.Printf("[wireless_gateway_manager] WARN: Failed to enable default_radio0: %v", err)
	}
	if _, err := c.ExecuteUCI("set", "wireless.default_radio1.disabled=0"); err != nil {
		c.log.Printf("[wireless_gateway_manager] WARN: Failed to enable default_radio1: %v", err)
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}
	return c.reloadWifi()
}

// DisableLocalAP disables the local Wi-Fi access point.
func (c *Connector) DisableLocalAP() error {
	c.log.Println("[wireless_gateway_manager] Disabling local APs")
	if _, err := c.ExecuteUCI("set", "wireless.default_radio0.disabled=1"); err != nil {
		c.log.Printf("[wireless_gateway_manager] WARN: Failed to disable default_radio0: %v", err)
	}
	if _, err := c.ExecuteUCI("set", "wireless.default_radio1.disabled=1"); err != nil {
		c.log.Printf("[wireless_gateway_manager] WARN: Failed to disable default_radio1: %v", err)
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}
	return c.reloadWifi()
}

// UpdateLocalAPSSID updates the local AP's SSID to advertise the current hop count.
func (c *Connector) UpdateLocalAPSSID(hopCount int) error {
	if err := c.ensureAPInterfacesExist(); err != nil {
		c.log.Printf("[wireless_gateway_manager] ERROR: Failed to ensure AP interfaces exist: %v", err)
		return err // This is a significant issue, so we return the error.
	}

	// Now that we've ensured the interfaces exist, we can proceed.
	// We update both 2.4GHz and 5GHz APs if they exist.
	radios := []string{"default_radio0", "default_radio1"}
	var commitNeeded bool
	for _, radio := range radios {
		// Check if the interface section exists before trying to update it.
		if _, err := c.ExecuteUCI("get", "wireless."+radio); err != nil {
			c.log.Printf("[wireless_gateway_manager] INFO: AP interface %s not found, skipping SSID update for it.", radio)
			continue
		}

		baseSSID, err := c.ExecuteUCI("get", "wireless."+radio+".ssid")
		if err != nil {
			c.log.Printf("[wireless_gateway_manager] WARN: Could not get current SSID for %s: %v", radio, err)
			continue // Try the next radio
		}
		baseSSID = strings.TrimSpace(baseSSID)

		// Strip any existing hop count from the base SSID
		parts := strings.Split(baseSSID, "-")
		if len(parts) > 2 { // TollGate-XXXX-2.4GHz -> TollGate-XXXX
			if _, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				// It ends with a number, so it's a hop count. Strip it.
				baseSSID = strings.Join(parts[:len(parts)-1], "-")
			}
		}

		var newSSID string
		if hopCount == math.MaxInt32 {
			newSSID = baseSSID
			c.log.Printf("[wireless_gateway_manager] Disconnected, setting AP SSID for %s to base: %s", radio, newSSID)
		} else {
			newSSID = fmt.Sprintf("%s-%d", baseSSID, hopCount)
			c.log.Printf("[wireless_gateway_manager] Updating local AP SSID for %s to: %s", radio, newSSID)
		}

		if _, err := c.ExecuteUCI("set", "wireless."+radio+".ssid="+newSSID); err != nil {
			c.log.Printf("[wireless_gateway_manager] ERROR: Failed to set new SSID for %s: %v", radio, err)
			continue
		}
		commitNeeded = true
	}

	if commitNeeded {
		if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
			return fmt.Errorf("failed to commit wireless config for AP SSID update: %w", err)
		}
		c.log.Println("[wireless_gateway_manager] Reloading wifi to apply new AP SSID")
		return c.reloadWifi()
	}

	return nil
}

// ensureAPInterfacesExist checks for and creates the default TollGate AP interfaces if they don't exist.
func (c *Connector) ensureAPInterfacesExist() error {
	c.log.Println("[wireless_gateway_manager] Ensuring default AP interfaces exist...")
	var created bool
	var baseSSIDName string

	// First, try to find an existing AP to get the base SSID name
	for _, radio := range []string{"default_radio0", "default_radio1"} {
		ssid, err := c.ExecuteUCI("get", "wireless."+radio+".ssid")
		if err == nil {
			ssid = strings.TrimSpace(ssid)
			if strings.HasPrefix(ssid, "TollGate-") {
				parts := strings.Split(ssid, "-")
				// TollGate-XXXX-2.4GHz or TollGate-XXXX-5GHz
				if len(parts) >= 3 {
					baseSSIDName = strings.Join(parts[0:2], "-") // "TollGate-XXXX"
					c.log.Printf("[wireless_gateway_manager] Found existing AP with base name: %s", baseSSIDName)
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
		c.log.Printf("[wireless_gateway_manager] No existing AP found. Generated new base name: %s", baseSSIDName)
	}

	radios := map[string]string{
		"default_radio0": "radio0", // 2.4GHz AP iface
		"default_radio1": "radio1", // 5GHz AP iface
	}

	for ifaceSection, device := range radios {
		// Check if the physical radio device exists
		if _, err := c.ExecuteUCI("get", "wireless."+device); err != nil {
			c.log.Printf("[wireless_gateway_manager] INFO: Physical radio device %s not found, cannot create AP interface %s.", device, ifaceSection)
			continue
		}

		// Check if the AP interface section already exists
		if _, err := c.ExecuteUCI("get", "wireless."+ifaceSection); err == nil {
			c.log.Printf("[wireless_gateway_manager] INFO: AP interface %s already exists.", ifaceSection)
			continue
		}

		// Interface doesn't exist, so create it based on defaults.
		c.log.Printf("[wireless_gateway_manager] INFO: AP interface %s not found. Creating it now with consistent naming...", ifaceSection)
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
		c.log.Println("[wireless_gateway_manager] Default AP interfaces were created/updated, committing changes.")
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
