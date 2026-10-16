// Package wireless_gateway_manager implements the Scanner for Wi-Fi network scanning.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	targets, err := s.scanTargets()
	if err != nil {
		return nil, err
	}

	if len(targets) == 0 {
		return nil, errors.New("no radios found")
	}

	type scanResult struct {
		networks []NetworkInfo
		err      error
	}

	results := make(chan scanResult, len(targets))
	for _, target := range targets {
		go func(t scanTarget) {
			networks, err := s.scanRadio(t.Radio, t.Device)
			results <- scanResult{networks: networks, err: err}
		}(target)
	}

	var allNetworks []NetworkInfo
	for i := 0; i < len(targets); i++ {
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

	// Stamp the band of the radio each network was scanned on, so consumers
	// (installer / admin SPA) can tell 2.4 GHz from 5 GHz SSIDs without
	// assuming radio0 is 2.4 GHz. Falls back to "unknown" when the wireless
	// config carries no band information (#452). bandByRadioFromConfig reads
	// /etc/config/wireless itself (the file GetRadios() parses) and hands the
	// map over in the radio->band orientation assignNetworkBands looks up by.
	allNetworks = assignNetworkBands(allNetworks, bandByRadioFromConfig())

	return allNetworks, nil
}

// scanTarget pairs a uci radio section name with the iwinfo device to scan it
// on. Device may be empty, in which case scanRadio resolves it itself.
type scanTarget struct {
	Radio  string
	Device string
}

// scanTargets builds the list of radios to scan. netifd's wireless status is
// the authoritative radio-to-interface mapping (it is what LuCI and travelmate
// use), so it is preferred when available: it also reports which radios are
// down or disabled, which are skipped instead of burning three failed scan
// attempts each. Everything else falls back to reading the wireless config.
func (s *Scanner) scanTargets() ([]scanTarget, error) {
	if data, err := wirelessStatusOutput(); err == nil {
		if status, err := parseWirelessStatus(data); err == nil && len(status) > 0 {
			targets := scanTargetsFromStatus(status)
			if skipped := len(status) - len(targets); skipped > 0 {
				logger.WithField("skipped", skipped).Debug("Skipping down or disabled radios")
			}
			return targets, nil
		}
		logger.Debug("No usable wireless status, falling back to wireless config")
	} else {
		logger.WithError(err).Debug("ubus wireless status unavailable, falling back to wireless config")
	}

	radios, err := s.GetRadios()
	if err != nil {
		return nil, err
	}
	targets := make([]scanTarget, 0, len(radios))
	for _, radio := range radios {
		targets = append(targets, scanTarget{Radio: radio})
	}
	return targets, nil
}

func (s *Scanner) scanRadio(radio, device string) ([]NetworkInfo, error) {
	// iwinfo addresses *interfaces*, not uci radio sections. On modern OpenWrt
	// the radio's interfaces are named phy<idx>-ap<k> (e.g. phy0-ap1), so
	// `iwinfo radio0 scan` is a usage error and the scan silently comes back
	// empty. Resolve the radio to a usable iwinfo device first.
	if device == "" {
		device = s.resolveIwinfoDevice(radio)
	}
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

		networks := s.ParseIwinfoOutput(stdout.Bytes(), radio)

		// `iwinfo` prints NO information elements (measured on
		// tollgate-A04E, 2026-10-16), so it can never identify a TollGate by
		// its vendor element. When vendor-IE discovery is enabled, merge in
		// an `iw` scan — the only tool that emits elements — so the
		// identification and pricing fields can be populated. iwinfo is
		// still run first because it reports hidden APs that iw can omit.
		if s.vendorIEDiscovery {
			if iwScan := s.scanRadioWithIW(radio, device); len(iwScan) > 0 {
				networks = mergeScanResults(networks, iwScan)
			}
		}

		return networks, nil
	}

	return nil, lastErr
}

// scanRadioWithIW runs `iw dev <device> scan`, whose output is the only source
// of information elements (including the TollGate vendor element). Best-effort:
// an iw failure or empty result returns nil and the caller keeps the iwinfo
// view rather than failing the whole scan.
func (s *Scanner) scanRadioWithIW(radio, device string) []NetworkInfo {
	cmd := exec.Command("iw", "dev", device, "scan")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		logger.WithError(err).Debug("iw scan failed; vendor-element discovery unavailable")
		return nil
	}
	if stdout.Len() == 0 {
		return nil
	}
	return s.ParseIwinfoOutput(stdout.Bytes(), radio)
}

// mergeScanResults folds a second scan view into the first, keyed by BSSID.
// The second view (iw) carries the information elements, so its vendor-element
// fields win; the first view (iwinfo) keeps entries the second one lacks.
func mergeScanResults(base, extra []NetworkInfo) []NetworkInfo {
	idx := make(map[string]int, len(base))
	for i, n := range base {
		idx[n.BSSID] = i
	}

	for _, e := range extra {
		i, ok := idx[e.BSSID]
		if !ok {
			base = append(base, e)
			idx[e.BSSID] = len(base) - 1
			continue
		}
		// Enrich in place: take the element-derived fields from the iw view,
		// and fill any gap the iwinfo view left (signal, encryption, channel).
		if e.IsTollGate {
			base[i].IsTollGate = true
		}
		if len(e.RawIEs) > 0 {
			base[i].RawIEs = e.RawIEs
		}
		if e.PricePerStep > 0 {
			base[i].PricePerStep = e.PricePerStep
		}
		if e.StepSize > 0 {
			base[i].StepSize = e.StepSize
		}
		if base[i].Signal == 0 && e.Signal != 0 {
			base[i].Signal = e.Signal
		}
		if base[i].Encryption == "" {
			base[i].Encryption = e.Encryption
		}
	}

	return base
}

// ParseIwinfoOutput parses a wireless scan into NetworkInfo records.
//
// It accepts BOTH real formats measured in the field (tollgate-A04E,
// 2026-10-16):
//
//   - `iwinfo <dev> scan` — "Cell NN - Address: …" blocks with
//     "ESSID:", "Signal:", "Encryption:" lines. NOTE: this tool prints NO
//     information elements, so it can never yield IsTollGate via the vendor
//     element. It is still parsed because it carries hidden APs (shown as
//     "unknown") that iw may not report.
//   - `iw dev <dev> scan` — "BSS <mac>(on …)" blocks with tab-indented
//     decoded elements ("SSID:", "RSN:", "HT capabilities:", …). This is the
//     ONLY source of the vendor element, printed as "Vendor specific: <hex>"
//     with colon-separated bytes.
//
// Detection is per-record on the "Address:" / "BSS " headers, so a stream
// mixing both formats (or one tool's output fed in wholesale) parses.
func (s *Scanner) ParseIwinfoOutput(output []byte, radio string) []NetworkInfo {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var networks []NetworkInfo

	var current *scanEntry

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// --- record boundaries ---
		if strings.Contains(line, "Address:") {
			// iwinfo record header.
			if current != nil {
				networks = s.appendScanEntry(networks, current, radio)
			}
			fields := strings.Fields(line)
			current = &scanEntry{}
			if len(fields) > 0 {
				current.bssid = fields[len(fields)-1]
			}
			continue
		}
		if strings.HasPrefix(line, "BSS ") {
			// iw record header: "BSS 94:83:c4:d1:57:59(on phy0-ap0)".
			if current != nil {
				networks = s.appendScanEntry(networks, current, radio)
			}
			current = &scanEntry{}
			rest := strings.TrimPrefix(line, "BSS ")
			if i := strings.Index(rest, "("); i >= 0 {
				rest = rest[:i]
			}
			current.bssid = strings.TrimSpace(rest)
			continue
		}

		if current == nil {
			continue
		}

		// --- SSID (both formats) ---
		if strings.HasPrefix(line, "ESSID:") || strings.HasPrefix(line, "SSID:") {
			key := "ESSID:"
			if strings.HasPrefix(line, "SSID:") {
				key = "SSID:"
			}
			v := strings.TrimSpace(strings.TrimPrefix(line, key))
			if strings.HasPrefix(v, `"`) {
				if start := strings.Index(v, `"`); start >= 0 {
					if end := strings.LastIndex(v, `"`); end > start {
						v = v[start+1 : end]
					}
				}
			}
			current.ssid = v
			continue
		}

		// --- signal (both formats) ---
		if strings.Contains(line, "Signal:") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "Signal:" && i+1 < len(fields) {
					if sig, err := strconv.Atoi(strings.TrimSuffix(fields[i+1], "dBm")); err == nil {
						current.signal = sig
						current.hasSignal = true
					}
					break
				}
			}
			continue
		}

		// --- encryption (both formats) ---
		if strings.Contains(line, "Encryption:") {
			idx := strings.Index(line, "Encryption:")
			if idx >= 0 {
				current.encrypt = strings.TrimSpace(line[idx+len("Encryption:"):])
			}
			continue
		}

		// iw emits no "Encryption:" line; the RSN element is its equivalent.
		if strings.HasPrefix(line, "RSN:") {
			if current.encrypt == "" {
				current.encrypt = "WPA2"
			}
			continue
		}

		// --- channel (iwinfo) / freq (iw) ---
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
		if strings.HasPrefix(line, "freq:") {
			current.channel = strings.TrimSpace(strings.TrimPrefix(line, "freq:"))
			continue
		}

		// --- the vendor element: the ONLY source of IsTollGate on the scan
		// path. iw prints it decoded, as colon-separated hex bytes. iwinfo
		// prints nothing of the kind, which is why this producer can only
		// ever fire on iw output. A malformed hex body is ignored rather than
		// poisoning the record.
		if strings.HasPrefix(line, "Vendor specific:") {
			if b, ok := parseColonHex(strings.TrimSpace(strings.TrimPrefix(line, "Vendor specific:"))); ok {
				// iw prints the element BODY only — it strips the
				// element ID (0xDD) and length byte that the IE parser
				// expects in a raw scan blob. Re-frame it, or every
				// parsed element is silently dropped (measured
				// 2026-10-16: parseColonHex yielded 36 good bytes and
				// ParseVendorIEsFromScanData returned 0).
				current.rawIEs = append(current.rawIEs, 0xDD, byte(len(b)))
				current.rawIEs = append(current.rawIEs, b...)
			}
			continue
		}
	}

	if current != nil {
		networks = s.appendScanEntry(networks, current, radio)
	}

	return networks
}

// scanEntry is the in-flight scan record while a tool's output is consumed.
type scanEntry struct {
	bssid     string
	ssid      string
	signal    int
	encrypt   string
	channel   string
	rawIEs    []byte
	hasSignal bool
}

// appendScanEntry admits a completed record and wires the vendor-element
// producer. It preserves the pre-existing admission rules (SSID present and
// non-empty) with two measured refinements:
//
//   - iw prints "SSID: unknown" when the frame carries no SSID. That is a
//     literal placeholder, not a name, and admitting it would create a
//     selectable network called "unknown" (the "(hidden)" guard below was for
//     the iwinfo spelling of the same condition).
//   - the trailing-entry guard drops placeholder names the same way.
func (s *Scanner) appendScanEntry(dst []NetworkInfo, e *scanEntry, radio string) []NetworkInfo {
	if e.ssid == "" || e.ssid == "(hidden)" || e.ssid == "unknown" {
		return dst
	}
	return append(dst, s.buildNetworkInfo(e.bssid, e.ssid, e.signal, e.encrypt, radio, e.rawIEs))
}

// parseColonHex decodes "21:21:21:01:…" into bytes.
func parseColonHex(s string) ([]byte, bool) {
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ":")
	out := make([]byte, 0, len(parts))
	for _, p := range parts {
		if len(p) > 2 {
			// iw may append a note after the last byte; take the first two
			// hex digits only when they are valid.
			p = p[:2]
		}
		v, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return nil, false
		}
		out = append(out, byte(v))
	}
	return out, true
}

// buildNetworkInfo assembles a NetworkInfo and, when the scan carried a
// TollGate vendor element, fills the definitive-identification and pricing
// fields from it. This is the producer the scan boundary was previously staged
// for: IsTollGate/RawIEs/PricePerStep/StepSize used to be hard-zero here.
//
// Authority note: the element is an UNSIGNED hint. OUI identification is the
// scan-side signal that selects a candidate for the signed kind-10021 probe
// served on :2121, which remains the authority for price. A zero pricing value
// therefore means "not advertised here", never "free".
func (s *Scanner) buildNetworkInfo(bssid, ssid string, signal int, encrypt, radio string, rawIEs []byte) NetworkInfo {
	n := NetworkInfo{
		BSSID:      bssid,
		SSID:       ssid,
		Signal:     signal,
		Encryption: encrypt,
		Radio:      radio,
	}

	if len(rawIEs) == 0 {
		return n
	}

	n.RawIEs = rawIEs
	for _, adv := range ParseVendorIEsFromScanData(rawIEs) {
		if adv == nil {
			continue
		}
		n.IsTollGate = true
		if adv.PricePerStep > 0 {
			n.PricePerStep = int(adv.PricePerStep)
		}
		if adv.StepSize > 0 {
			n.StepSize = int(adv.StepSize)
		}
		// One TollGate advertisement per AP is the contract; take the first
		// and stop rather than letting a later element overwrite it.
		break
	}

	return n
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

// ubusRadioIface is one interface entry of a radio in netifd's wireless
// status. Ifname is absent while the interface is down.
type ubusRadioIface struct {
	Section string `json:"section"`
	Ifname  string `json:"ifname"`
}

// ubusRadioStatus is one radio entry of `ubus call network.wireless status`,
// keyed in the payload by the uci section name (radio0, radio1, ...).
type ubusRadioStatus struct {
	Up         bool             `json:"up"`
	Disabled   bool             `json:"disabled"`
	Interfaces []ubusRadioIface `json:"interfaces"`
}

// wirelessStatusOutput runs the netifd wireless status query. Failing (no
// ubus, no wireless system) is a supported condition handled by the caller.
func wirelessStatusOutput() ([]byte, error) {
	return exec.Command("ubus", "call", "network.wireless", "status").Output()
}

// parseWirelessStatus decodes the `ubus call network.wireless status` payload.
// Pure, so it is unit-tested against captured fixtures.
func parseWirelessStatus(data []byte) (map[string]ubusRadioStatus, error) {
	var status map[string]ubusRadioStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("failed to parse wireless status JSON: %w", err)
	}
	return status, nil
}

// scanTargetsFromStatus turns the wireless status into scan targets: one per
// live radio, in section-name order for deterministic behaviour. Radios that
// are down or disabled, or whose interfaces have no ifname yet, are skipped —
// they cannot be scanned and would only produce error noise.
func scanTargetsFromStatus(status map[string]ubusRadioStatus) []scanTarget {
	sections := make([]string, 0, len(status))
	for section := range status {
		sections = append(sections, section)
	}
	sort.Strings(sections)

	targets := make([]scanTarget, 0, len(sections))
	for _, section := range sections {
		radio := status[section]
		if radio.Disabled || !radio.Up {
			continue
		}
		for _, iface := range radio.Interfaces {
			if iface.Ifname != "" {
				targets = append(targets, scanTarget{Radio: section, Device: iface.Ifname})
				break
			}
		}
	}
	return targets
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
	// "WPA PSK" without a WPA2 marker is ambiguous in the field: iwinfo
	// prints it both for genuine WPA1 networks and for mixed/WPA2-preferring
	// APs whose beacon leads with the WPA1 IE (measured: such an AP rejects
	// a `psk` section's WPA1 auth and only completes WPA2 — #817's second
	// root cause). psk-mixed negotiates either, so map the ambiguous shape
	// there instead of gambling on WPA1.
	if strings.Contains(e, "wpa") && strings.Contains(e, "psk") {
		return "psk-mixed"
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
