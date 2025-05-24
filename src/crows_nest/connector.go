// Package crows_nest implements the Connector for managing OpenWRT network configurations.
package crows_nest

import (
	"bytes"
	"log"
	"os/exec"
)

// Connector manages OpenWRT network configurations via UCI commands.
type Connector struct {
	log *log.Logger
}

// Connect configures the network to connect to the specified gateway.
func (c *Connector) Connect(gateway Gateway) error {
	// Configure network.wwan (STA interface) with DHCP
	if _, err := c.ExecuteUCI("set", "network.wwan=interface"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("set", "network.wwan.proto='dhcp'"); err != nil {
		return err
	}

	// Disable existing wlan0 AP, configure wireless.wifinetX for STA mode
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
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.encryption='psk2'"); err != nil {
		return err
	}
	// Assuming password is stored securely elsewhere and passed here
	password := "your_password_here"
	if _, err := c.ExecuteUCI("set", "wireless.wifinet0.key='"+password+"'"); err != nil {
		return err
	}

	// Commit changes and restart network
	if _, err := c.ExecuteUCI("commit", "network"); err != nil {
		return err
	}
	if _, err := c.ExecuteUCI("commit", "wireless"); err != nil {
		return err
	}
	if err := c.restartNetwork(); err != nil {
		return err
	}

	return nil
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
