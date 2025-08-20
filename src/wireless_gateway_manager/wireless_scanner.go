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

// ScanWirelessNetworks scans for available Wi-Fi networks on all available STA interfaces.
func (s *Scanner) ScanWirelessNetworks() ([]NetworkInfo, error) {
	logger.Info("Starting Wi-Fi network scan on all STA interfaces")

	interfaces, err := getSTAManagedInterfaces()
	if err != nil {
		logger.WithError(err).Error("Failed to get STA managed interfaces")
		// If no interfaces exist, try to create one and retry.
		if err := s.connector.ensureSTAInterfaceExists(); err != nil {
			logger.WithError(err).Error("Failed to create STA interface")
			return nil, err
		}
		interfaces, err = getSTAManagedInterfaces()
		if err != nil {
			logger.WithError(err).Error("Still failed to get STA managed interfaces after creation attempt")
			return nil, err
		}
	}

	if len(interfaces) == 0 {
		logger.Warn("No STA managed interfaces found to scan on.")
		return []NetworkInfo{}, nil
	}

	var allNetworks []NetworkInfo
	for iface, radio := range interfaces {
		logger.WithFields(logrus.Fields{"interface": iface, "radio": radio}).Info("Scanning on interface")
		cmd := exec.Command("iw", "dev", iface, "scan")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			// A scan can fail if the interface is busy, which is not a fatal error for the whole process.
			logger.WithFields(logrus.Fields{
				"interface": iface,
				"error":     err,
				"stderr":    stderr.String(),
			}).Warn("Failed to scan on a specific interface, continuing with others")
			continue
		}

		networks, err := parseScanOutput(stdout.Bytes(), radio)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"interface": iface,
				"error":     err,
			}).Warn("Failed to parse scan output for an interface")
			continue
		}
		allNetworks = append(allNetworks, networks...)
	}

	logger.WithField("total_network_count", len(allNetworks)).Info("Finished scanning all interfaces")
	return allNetworks, nil
}

// getSTAManagedInterfaces finds all network interfaces of type 'managed' (STA).
// It returns a map of the interface name to its corresponding radio device (e.g., "phy0-sta0" -> "radio0").
func getSTAManagedInterfaces() (map[string]string, error) {
	cmd := exec.Command("iw", "dev")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	interfaces := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	var currentPhy, currentInterface string

	phyRegex := regexp.MustCompile(`phy#(\d+)`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "phy#") {
			matches := phyRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				currentPhy = "radio" + matches[1]
			}
		} else if strings.HasPrefix(line, "Interface") {
			parts := strings.Fields(line)
			if len(parts) > 1 {
				currentInterface = parts[1]
			}
		} else if strings.HasPrefix(line, "type") && strings.Contains(line, "managed") {
			if currentInterface != "" && currentPhy != "" {
				interfaces[currentInterface] = currentPhy
				// Reset for next interface block
				currentInterface = ""
				// currentPhy is sticky until the next phy is found
			}
		}
	}

	if len(interfaces) == 0 {
		return nil, errors.New("no managed Wi-Fi interfaces found")
	}
	return interfaces, nil
}


func parseScanOutput(output []byte, radio string) ([]NetworkInfo, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var networks []NetworkInfo
	var currentNetwork *NetworkInfo

	bssidRegex := regexp.MustCompile(`BSS ([0-9a-fA-F:]{17})\(on`)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "BSS ") {
			if currentNetwork != nil && currentNetwork.SSID != "" { // Only add if SSID was found
				if currentNetwork.Encryption == "" {
					currentNetwork.Encryption = "Open" // Default to Open if no encryption is detected
				}
				networks = append(networks, *currentNetwork)
			}
			matches := bssidRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				currentNetwork = &NetworkInfo{BSSID: matches[1], Radio: radio}
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
