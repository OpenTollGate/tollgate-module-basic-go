// Package crows_nest implements the Connector for managing OpenWRT network configurations.
package crows_nest

import (
	"bytes"
	"log"
	"os/exec"
	"strings"
)

// Connector manages OpenWRT network configurations via UCI commands.
type Connector struct {
	log *log.Logger
}

// Connect configures the network to connect to the specified gateway.
func (c *Connector) Connect(gateway Gateway, password string) error {
	c.log.Printf("[crows_nest] Attempting to connect to gateway %s (%s) with encryption %s", gateway.SSID, gateway.BSSID, gateway.Encryption)

	// Configure network.wwan (STA interface) with DHCP
	if _, err := c.ExecuteUCI("set", "network.wwan=interface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "network.wwan.proto='dhcp'"); err != nil {
		return err
	}

	// Configure wireless.wifinet0 for STA mode
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0=wifi-iface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.device='radio0'"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.mode='sta'"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.network='wwan'"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.ssid='"+gateway.SSID+"'"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.bssid='"+gateway.BSSID+"'"); err != nil {
		return err
	}

	// Set encryption based on gateway information
	if gateway.Encryption != "" && gateway.Encryption != "Open" {
		if _, err := c.ExecuteUCI("set", "wireless.wifinet0.encryption='"+getUCIEncryptionType(gateway.Encryption)+"'"); err != nil {
			return err
		}
		if password != "" {
			if _, err := c.ExecuteUCI("set", "wireless.wifinet0.key='"+password+"'"); err != nil {
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
	return nil
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
	cmd := exec.Command("iw", "dev", "phy0-sta0", "link")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.log.Printf("[crows_nest] WARN: Could not get connected SSID: %v, stderr: %s", err, stderr.String())
		return "", err // Not an error if not connected, but return empty string
	}

	output := stdout.String()
	// Example output:
	// Connected to 00:11:22:33:44:55 (on phy0)
	// 	SSID: MyHomeNetwork
	// 	freq: 2412
	// 	...
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "SSID:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "SSID:")), nil
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
