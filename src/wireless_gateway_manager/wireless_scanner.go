// Package wireless_gateway_manager implements the Scanner for Wi-Fi network scanning.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// ScanWirelessNetworks scans for available Wi-Fi networks.
func (s *Scanner) ScanWirelessNetworks() ([]NetworkInfo, error) {
	logger.Info("Starting Wi-Fi network scan")
	// Determine the Wi-Fi interface dynamically
	interfaceName, err := getInterfaceName()
	if err != nil {
		logger.WithError(err).Warn("Failed to get interface name, attempting to create one")
		if err := s.connector.ensureSTAInterfaceExists(); err != nil {
			logger.WithError(err).Error("Failed to create STA interface")
			return nil, err
		}
		// Retry getting the interface name after creation
		interfaceName, err = getInterfaceName()
		if err != nil {
			logger.WithError(err).Error("Failed to get interface name after attempting to create it")
			return nil, err
		}
	}

	cmd := exec.Command("iw", "dev", interfaceName, "scan")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logger.WithFields(logrus.Fields{
			"error":  err,
			"stderr": stderr.String(),
		}).Error("Failed to scan networks")
		return nil, err
	}

	logger.Info("Successfully scanned networks")

	networks, err := parseScanOutput(stdout.Bytes())
	if err != nil {
		logger.WithError(err).Error("Failed to parse scan output")
		return nil, err
	}
	logger.WithField("network_count", len(networks)).Info("Parsed scan output into NetworkInfo structures")

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

func parseScanOutput(output []byte) ([]NetworkInfo, error) {
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
				logger.WithField("line", line).Warn("Could not extract BSSID from line")
				currentNetwork = nil // Invalidate current network
				continue
			}
		} else if currentNetwork != nil {
			if strings.HasPrefix(line, "\tSSID:") {
				ssid := strings.TrimSpace(strings.TrimPrefix(line, "\tSSID:"))
				if ssid != "" {
					currentNetwork.SSID = ssid
					// Parse pricing from SSID
					currentNetwork.PricePerStep, currentNetwork.StepSize = parsePricingFromSSID(ssid)
				}
			} else if strings.HasPrefix(line, "\tsignal:") {
				signalStr := strings.TrimSpace(strings.TrimPrefix(line, "\tsignal:"))
				signalStr = strings.TrimSuffix(signalStr, " dBm")
				signal, err := strconv.ParseFloat(signalStr, 64)
				if err != nil {
					logger.WithFields(logrus.Fields{
						"signal_str": signalStr,
						"error":      err,
					}).Warn("Failed to parse signal strength")
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


func parsePricingFromSSID(ssid string) (int, int) {
	if !strings.HasPrefix(ssid, "TollGate-") {
		return 0, 0 // Not a TollGate network
	}

	parts := strings.Split(ssid, "-")
	if len(parts) < 4 {
		return 0, 0 // Invalid format
	}

	priceStr := parts[len(parts)-2]
	stepStr := parts[len(parts)-1]

	price, err := strconv.Atoi(priceStr)
	if err != nil {
		return 0, 0 // Could not parse price
	}

	step, err := strconv.Atoi(stepStr)
	if err != nil {
		return 0, 0 // Could not parse step
	}

	return price, step
}
