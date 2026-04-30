// Package wireless_gateway_manager implements the Scanner for Wi-Fi network scanning.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

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

