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
	// iwinfo addresses *interfaces*, not uci radio sections. On modern OpenWrt
	// the radio's interfaces are named phy<idx>-ap<k> (e.g. phy0-ap1), so
	// `iwinfo radio0 scan` is a usage error and the scan silently comes back
	// empty. Resolve the radio to a usable iwinfo device first.
	device := s.resolveIwinfoDevice(radio)
	var lastErr error
	for retry := 0; retry < 3; retry++ {
		cmd := exec.Command("iwinfo", device, "scan")
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
		bssid     string
		ssid      string
		signal    int
		encrypt   string
		channel   string
		hasSignal bool
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.Contains(line, "Address:") {
			if current != nil && current.ssid != "" && current.hasSignal {
				// STAGED (this PR): IsTollGate/RawIEs/PricePerStep/StepSize
				// stay zero here — the vendor-IE parse that fills them is
				// wired in the follow-up that gates on VendorIEDiscovery.
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
				bssid     string
				ssid      string
				signal    int
				encrypt   string
				channel   string
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
		if os.IsNotExist(err) {
			return nil, nil
		}
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

// radioIndex parses the numeric index from a uci radio section name
// ("radio0" -> 0). Returns ok=false for names that are not of that shape.
func radioIndex(radio string) (int, bool) {
	if !strings.HasPrefix(radio, "radio") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(radio, "radio"))
	if err != nil {
		return 0, false
	}
	return n, true
}

// phyInterfaces parses `iw dev` and returns the interface names belonging to
// phy#<idx>, in the order they appear (e.g. phy0-ap0, phy0-ap1). Returns nil
// when `iw` is unavailable or the phy has no interfaces.
func phyInterfaces(idx int) []string {
	out, err := exec.Command("iw", "dev").Output()
	if err != nil {
		return nil
	}
	return parsePhyInterfaces(string(out), idx)
}

// parsePhyInterfaces extracts the interface names under `phy#<idx>` from `iw
// dev` output. Pure, so it is unit-tested against a captured fixture.
func parsePhyInterfaces(out string, idx int) []string {
	var names []string
	cur := -1
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "phy#") {
			if n, err := strconv.Atoi(strings.TrimPrefix(l, "phy#")); err == nil {
				cur = n
			} else {
				cur = -1
			}
			continue
		}
		if cur == idx && strings.HasPrefix(l, "Interface ") {
			names = append(names, strings.TrimSpace(strings.TrimPrefix(l, "Interface ")))
		}
	}
	return names
}

// resolveIwinfoDevice maps a uci radio section ("radio0") to an iwinfo device
// name. If the argument already resolves as an iwinfo device it is returned
// unchanged; otherwise the first interface of the matching phy (phy<idx>-ap<k>)
// is used. Falls back to the original value when no mapping can be derived, so
// callers never lose the diagnostic error.
func (s *Scanner) resolveIwinfoDevice(radio string) string {
	if err := exec.Command("iwinfo", radio, "info").Run(); err == nil {
		return radio
	}
	idx, ok := radioIndex(radio)
	if !ok {
		return radio
	}
	if names := phyInterfaces(idx); len(names) > 0 {
		return names[0]
	}
	return radio
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

func FormatScanResults(networks []NetworkInfo) string {
	if len(networks) == 0 {
		return "No networks found"
	}

	var result string
	result = fmt.Sprintf("%-30s %-15s %-20s %s\n", "SSID", "Signal", "Encryption", "Radio")
	result += fmt.Sprintf("%s\n", "----------------------------------------------------------------------")
	for _, net := range networks {
		result += fmt.Sprintf("%-30s %-15s %-20s %s\n", net.SSID, fmt.Sprintf("%d dBm", net.Signal), net.Encryption, net.Radio)
	}
	result += fmt.Sprintf("\n%d network(s) found.", len(networks))
	return result
}

func init() {
	logger.WithField("module", "wireless_gateway_manager").Info("Wireless gateway manager module loaded")
}
