// Package wireless_gateway_manager implements the Scanner for Wi-Fi network scanning.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"errors"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ScanWirelessNetworks scans for available Wi-Fi networks.
func (s *Scanner) ScanWirelessNetworks() ([]NetworkInfo, error) {
	s.log.Println("[wireless_gateway_manager] Starting Wi-Fi network scan")
	// Determine the Wi-Fi interface dynamically
	interfaceName, err := getInterfaceName()
	if err != nil {
		s.log.Printf("[wireless_gateway_manager] ERROR: Failed to get interface name: %v", err)
		return nil, err
	}

	cmd := exec.Command("iw", "dev", interfaceName, "scan")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.log.Printf("[wireless_gateway_manager] ERROR: Failed to scan networks: %v, stderr: %s", err, stderr.String())
		return nil, err
	}

	s.log.Printf("[wireless_gateway_manager] Successfully scanned networks")

	networks, err := parseScanOutput(stdout.Bytes(), s.log)
	if err != nil {
		s.log.Printf("[wireless_gateway_manager] ERROR: Failed to parse scan output: %v", err)
		return nil, err
	}
	s.log.Printf("[wireless_gateway_manager] Parsed scan output into %d NetworkInfo structures", len(networks))

	return networks, nil
}

func getInterfaceName() (string, error) {
	cmd := exec.Command("iw", "dev")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	var currentInterface string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Interface") {
			parts := strings.Fields(line)
			if len(parts) > 1 {
				currentInterface = parts[1]
			}
		} else if strings.HasPrefix(line, "type") && strings.Contains(line, "managed") {
			if currentInterface != "" {
				return currentInterface, nil
			}
		}
	}
	return "", errors.New("no managed Wi-Fi interface found")
}

func parseScanOutput(output []byte, logger *log.Logger) ([]NetworkInfo, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var networks []NetworkInfo
	var currentNetwork *NetworkInfo

	bssidRegex := regexp.MustCompile(`BSS ([0-9a-fA-F:]{17})\(on`)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "BSS ") {
			if currentNetwork != nil && currentNetwork.SSID != "" { // Only add if SSID was found
				networks = append(networks, *currentNetwork)
			}
			matches := bssidRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				currentNetwork = &NetworkInfo{BSSID: matches[1]}
			} else {
				logger.Printf("[wireless_gateway_manager] WARN: Could not extract BSSID from line: %s", line)
				currentNetwork = nil // Invalidate current network
				continue
			}
		} else if currentNetwork != nil {
			if strings.HasPrefix(line, "\tSSID:") {
				ssid := strings.TrimSpace(strings.TrimPrefix(line, "\tSSID:"))
				if ssid != "" {
					currentNetwork.SSID = ssid
					// Parse hop count from SSID
					currentNetwork.HopCount = parseHopCountFromSSID(ssid)
				}
			} else if strings.HasPrefix(line, "\tsignal:") {
				signalStr := strings.TrimSpace(strings.TrimPrefix(line, "\tsignal:"))
				signalStr = strings.TrimSuffix(signalStr, " dBm")
				signal, err := strconv.ParseFloat(signalStr, 64)
				if err != nil {
					logger.Printf("[wireless_gateway_manager] WARN: Failed to parse signal strength '%s': %v", signalStr, err)
				} else {
					currentNetwork.Signal = int(signal)
				}
			} else if strings.Contains(line, "RSN:") || strings.Contains(line, "WPA:") {
				currentNetwork.Encryption = "WPA/WPA2"
			} else if strings.Contains(line, "Authentication suites: Open") {
				currentNetwork.Encryption = "Open"
			}
		}
	}

	if currentNetwork != nil && currentNetwork.SSID != "" {
		networks = append(networks, *currentNetwork)
	}

	return networks, scanner.Err()
}

func parseHopCountFromSSID(ssid string) int {
	if !strings.HasPrefix(ssid, "TollGate-") {
		return 0 // Not a TollGate network, hop count is 0
	}

	parts := strings.Split(ssid, "-")
	if len(parts) < 4 {
		return 0 // Invalid format
	}

	hopCountStr := parts[len(parts)-1]
	hopCount, err := strconv.Atoi(hopCountStr)
	if err != nil {
		return 0 // Could not parse hop count
	}

	return hopCount
}
