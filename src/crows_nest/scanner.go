// Package crows_nest implements the Scanner for Wi-Fi network scanning.
package crows_nest

import (
	"bufio"
	"bytes"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// Scanner handles Wi-Fi network scanning.
type Scanner struct {
	log *log.Logger
}

// NetworkInfo represents information about a Wi-Fi network.
type NetworkInfo struct {
	BSSID      string
	SSID       string
	Signal     int
	Encryption string
	RawIEs     []byte
}

// ScanNetworks scans for available Wi-Fi networks.
func (s *Scanner) ScanNetworks() ([]NetworkInfo, error) {
	s.log.Println("[crows_nest] Starting Wi-Fi network scan")
	// Determine the Wi-Fi interface dynamically
	interfaceName, err := getInterfaceName()
	if err != nil {
		s.log.Printf("[crows_nest] ERROR: Failed to get interface name: %v", err)
		return nil, err
	}

	cmd := exec.Command("iw", "dev", interfaceName, "scan")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.log.Printf("[crows_nest] ERROR: Failed to scan networks: %v, stderr: %s", err, stderr.String())
		return nil, err
	}

	s.log.Printf("[crows_nest] Successfully scanned networks")

	networks, err := parseScanOutput(stdout.Bytes())
	if err != nil {
		s.log.Printf("[crows_nest] ERROR: Failed to parse scan output: %v", err)
		return nil, err
	}
	s.log.Printf("[crows_nest] Parsed scan output into %d NetworkInfo structures", len(networks))

	return networks, nil
}

func getInterfaceName() (string, error) {
	// Implement dynamic interface name retrieval
	return "wlan0", nil // Hardcoded for now
}

func parseScanOutput(output []byte) ([]NetworkInfo, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var networks []NetworkInfo
	var currentNetwork *NetworkInfo

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "BSS") && strings.Contains(line, "(") && strings.Contains(line, ")") {
			if currentNetwork != nil {
				networks = append(networks, *currentNetwork)
			}
			bssid := strings.TrimSpace(strings.Split(line, "(")[1])
			bssid = strings.TrimSuffix(bssid, ")")
			currentNetwork = &NetworkInfo{BSSID: bssid}
		} else if currentNetwork != nil {
			if strings.HasPrefix(line, "\tSSID:") {
				currentNetwork.SSID = strings.TrimSpace(strings.TrimPrefix(line, "\tSSID:"))
			} else if strings.HasPrefix(line, "\tsignal:") {
				signalStr := strings.TrimSpace(strings.TrimPrefix(line, "\tsignal:"))
				signal, err := strconv.ParseFloat(signalStr, 64)
				if err != nil {
					return nil, err
				}
				currentNetwork.Signal = int(signal)
			} else if strings.Contains(line, "RSN:") || strings.Contains(line, "WPA:") {
				currentNetwork.Encryption = "WPA/WPA2"
			}
		}
	}

	if currentNetwork != nil {
		networks = append(networks, *currentNetwork)
	}

	return networks, scanner.Err()
}
