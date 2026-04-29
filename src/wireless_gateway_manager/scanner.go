// Package wireless_gateway_manager implements the Scanner for Wi-Fi network scanning.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// ScanWirelessNetworks scans for available Wi-Fi networks.
func (s *Scanner) ScanWirelessNetworks() ([]NetworkInfo, error) {
	logger.Info("Starting Wi-Fi network scan")
	// Determine the Wi-Fi interface dynamically
	interfaceName, err := getInterfaceName()
	if err != nil {
		logger.WithError(err).Warn("Failed to get interface name, attempting to create one")
		if err := s.Connector.ensureSTAInterfaceExists(); err != nil {
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

// Ensure Scanner implements ScannerInterface
var _ ScannerInterface = (*Scanner)(nil)

func (s *Scanner) ScanAllRadios() ([]NetworkInfo, error) {
	radios, err := s.GetRadios()
	if err != nil {
		return nil, err
	}

	if len(radios) == 0 {
		return nil, errors.New("no radios found")
	}

	type scanResult struct {
		networks []NetworkInfo
		err      error
	}

	results := make(chan scanResult, len(radios))
	for _, radio := range radios {
		go func(r string) {
			networks, err := s.scanRadio(r)
			results <- scanResult{networks: networks, err: err}
		}(radio)
	}

	var allNetworks []NetworkInfo
	for i := 0; i < len(radios); i++ {
		result := <-results
		if result.err != nil {
			logger.WithError(result.err).Warn("Radio scan failed")
			continue
		}
		allNetworks = append(allNetworks, result.networks...)
	}

	sort.Slice(allNetworks, func(i, j int) bool {
		return allNetworks[i].Signal > allNetworks[j].Signal
	})

	return allNetworks, nil
}

func (s *Scanner) scanRadio(radio string) ([]NetworkInfo, error) {
	var lastErr error
	for retry := 0; retry < 3; retry++ {
		cmd := exec.Command("iwinfo", radio, "scan")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}

		output := stdout.String()
		if output == "" || strings.Contains(strings.ToLower(output), "no scan result") {
			lastErr = errors.New("empty scan result")
			time.Sleep(2 * time.Second)
			continue
		}

		return s.ParseIwinfoOutput(stdout.Bytes(), radio), nil
	}

	return nil, lastErr
}

func (s *Scanner) ParseIwinfoOutput(output []byte, radio string) []NetworkInfo {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var networks []NetworkInfo

	var current *struct {
		bssid    string
		ssid     string
		signal   int
		encrypt  string
		channel  string
		hasSignal bool
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.Contains(line, "Address:") {
			if current != nil && current.ssid != "" && current.hasSignal {
				networks = append(networks, NetworkInfo{
					BSSID:      current.bssid,
					SSID:       current.ssid,
					Signal:     current.signal,
					Encryption: current.encrypt,
					Radio:      radio,
				})
			}
			fields := strings.Fields(line)
			current = &struct {
				bssid    string
				ssid     string
				signal   int
				encrypt  string
				channel  string
				hasSignal bool
			}{}
			if len(fields) > 0 {
				current.bssid = fields[len(fields)-1]
			}
			continue
		}

		if current == nil {
			continue
		}

		if strings.Contains(line, "ESSID:") {
			start := strings.Index(line, `"`)
			end := strings.LastIndex(line, `"`)
			if start >= 0 && end > start {
				current.ssid = line[start+1 : end]
			}
			if current.ssid == "" {
				current.ssid = "(hidden)"
			}
			continue
		}

		if strings.Contains(line, "Signal:") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "Signal:" && i+1 < len(fields) {
					sig, err := strconv.Atoi(strings.TrimSuffix(fields[i+1], "dBm"))
					if err == nil {
						current.signal = sig
						current.hasSignal = true
					}
					break
				}
			}
			continue
		}

		if strings.Contains(line, "Encryption:") {
			idx := strings.Index(line, "Encryption:")
			if idx >= 0 {
				current.encrypt = strings.TrimSpace(line[idx+len("Encryption:"):])
			}
			continue
		}

		if strings.Contains(line, "Channel:") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "Channel:" && i+1 < len(fields) {
					current.channel = fields[i+1]
					break
				}
			}
			continue
		}
	}

	if current != nil && current.ssid != "" && current.hasSignal && current.ssid != "(hidden)" {
		networks = append(networks, NetworkInfo{
			BSSID:      current.bssid,
			SSID:       current.ssid,
			Signal:     current.signal,
			Encryption: current.encrypt,
			Radio:      radio,
		})
	}

	return networks
}

func (s *Scanner) GetRadios() ([]string, error) {
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

func (s *Scanner) DetectEncryption(encryptionStr string) string {
	e := strings.ToLower(encryptionStr)

	if strings.Contains(e, "none") || strings.Contains(e, "open") || strings.HasPrefix(e, "wep") {
		return "none"
	}

	if strings.Contains(e, "sae") && strings.Contains(e, "mixed") {
		return "sae-mixed"
	}
	if strings.Contains(e, "sae") {
		return "sae"
	}
	if strings.Contains(e, "wpa2") && strings.Contains(e, "psk") {
		return "psk2"
	}
	if strings.Contains(e, "wpa") && strings.Contains(e, "psk") {
		return "psk"
	}
	if strings.Contains(e, "eap") {
		return "wpa2-eap"
	}

	return "psk2"
}

func (s *Scanner) FindBestRadioForSSID(ssid string, networks []NetworkInfo) (string, error) {
	for _, net := range networks {
		if net.SSID == ssid {
			return net.Radio, nil
		}
	}
	return "", fmt.Errorf("SSID '%s' not found in scan results", ssid)
}

func (s *Scanner) FindAlternateRadioForSSID(ssid, avoidRadio string, networks []NetworkInfo) (string, error) {
	var best *NetworkInfo
	for i := range networks {
		net := &networks[i]
		if net.SSID == ssid && net.Radio != avoidRadio {
			if best == nil || net.Signal > best.Signal {
				best = net
			}
		}
	}
	if best != nil {
		return best.Radio, nil
	}
	return "", fmt.Errorf("SSID '%s' not found on alternate radio", ssid)
}
