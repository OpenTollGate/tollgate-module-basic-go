// Package crows_nest implements the Connector for managing OpenWRT network configurations.
package crows_nest

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Connector manages OpenWRT network configurations via UCI commands.
type Connector struct {
	log *log.Logger
}

// Connect configures the network to connect to the specified gateway.
func (c *Connector) Connect(gateway Gateway, password string) error {
	c.log.Printf("[crows_nest] Attempting to connect to gateway %s (%s) with encryption %s", gateway.SSID, gateway.BSSID, gateway.Encryption)

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
			c.log.Printf("[crows_nest] WARN: No password provided for encrypted network %s", gateway.SSID)
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

	// Restart network to apply changes
	if err := c.restartNetwork(); err != nil {
		return err
	}

	c.log.Printf("[crows_nest] Successfully configured connection for gateway %s", gateway.SSID)

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
		c.log.Printf("[crows_nest] INFO: Could not get managed Wi-Fi interface, probably not associated: %v", err)
		return "", nil
	}

	cmd := exec.Command("iw", "dev", interfaceName, "link")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.log.Printf("[crows_nest] WARN: Could not get connected SSID from interface %s: %v, stderr: %s", interfaceName, err, stderr.String())
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
			c.log.Printf("[crows_nest] INFO: UCI entry to delete was not found (which is okay): uci %s", strings.Join(args, " "))
			return "", nil
		}
		c.log.Printf("[crows_nest] ERROR: Failed to execute UCI command: %v, stderr: %s", err, stderr.String())
		return "", err
	}

	return stdout.String(), nil
}

func (c *Connector) restartNetwork() error {
	cmd := exec.Command("/etc/init.d/network", "restart")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.log.Printf("[crows_nest] ERROR: Failed to restart network: %v, stderr: %s", err, stderr.String())
		return err
	}

	return nil
}

// verifyConnection checks if the device is connected to the specified SSID.
func (c *Connector) verifyConnection(expectedSSID string) error {
	c.log.Printf("[crows_nest] Verifying connection to %s...", expectedSSID)
	const retries = 10
	const delay = 3 * time.Second

	for i := 0; i < retries; i++ {
		time.Sleep(delay)
		currentSSID, err := c.GetConnectedSSID()
		if err != nil {
			c.log.Printf("[crows_nest] WARN: Verification check failed: could not get current SSID: %v", err)
			continue
		}

		if currentSSID == expectedSSID {
			c.log.Printf("[crows_nest] Successfully connected to %s", expectedSSID)
			return nil
		}
		c.log.Printf("[crows_nest] INFO: Still not connected to %s, currently on %s. Retrying...", expectedSSID, currentSSID)
	}

	return fmt.Errorf("failed to verify connection to %s after %d retries", expectedSSID, retries)
}

func (c *Connector) cleanupSTAInterfaces() error {
	c.log.Println("[crows_nest] Cleaning up existing STA wifi-iface sections...")
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
		c.log.Printf("[crows_nest] Deleting old STA interface section: %s", section)
		if _, err := c.ExecuteUCI("delete", section); err != nil {
			// We log the error but continue, as a failed delete is not critical
			c.log.Printf("[crows_nest] WARN: Failed to delete section %s: %v", section, err)
		}
	}

	return nil
}
