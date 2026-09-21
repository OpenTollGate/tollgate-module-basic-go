// Package wireless_gateway_manager: radio-to-band mapping for uci wireless
// sections.
package wireless_gateway_manager

import (
	"bufio"
	"strconv"
	"strings"
)

// uciRadio is a wifi-device section with the options that identify its band.
type uciRadio struct {
	Section string
	Band    string
	Hwmode  string
	Channel string
}

// classifyRadioBand derives the frequency band ("2g", "5g", "6g", "60g") of a
// wifi-device section from its uci band/hwmode/channel options, mirroring the
// resolution order OpenWrt itself applies (wifi-scripts' set_device_defaults
// and the legacy wifi_fixup_hwmode): the band option on 21.02+, the legacy
// hwmode on older releases, and finally the channel number (channels above 14
// are 5 GHz). Returns "" when nothing identifies the band, e.g. channel
// "auto" without band or hwmode.
func classifyRadioBand(band, hwmode, channel string) string {
	switch strings.ToLower(strings.TrimSpace(band)) {
	case "2g", "5g", "6g", "60g":
		return strings.ToLower(strings.TrimSpace(band))
	}
	switch strings.ToLower(strings.TrimSpace(hwmode)) {
	case "11ad", "ad":
		return "60g"
	case "a", "11a", "11na":
		return "5g"
	case "b", "g", "bg", "11b", "11g", "11bg", "11ng":
		return "2g"
	}
	ch, err := strconv.Atoi(strings.TrimSpace(channel))
	if err != nil {
		return ""
	}
	if ch > 14 {
		return "5g"
	}
	return "2g"
}

// parseUciShowWireless extracts the wifi-device sections and their
// band/hwmode/channel options from `uci show wireless` output. Pure, so it is
// unit-tested against captured fixtures.
func parseUciShowWireless(showOutput string) []uciRadio {
	var radios []uciRadio
	index := make(map[string]int)

	scanner := bufio.NewScanner(strings.NewReader(showOutput))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "wireless.") {
			continue
		}
		body := strings.TrimPrefix(line, "wireless.")
		eq := strings.Index(body, "=")
		if eq < 0 {
			continue
		}
		section := body[:eq]
		value := strings.Trim(body[eq+1:], "'")

		if name, option, isOption := strings.Cut(section, "."); isOption {
			idx, ok := index[name]
			if !ok {
				continue
			}
			switch option {
			case "band":
				radios[idx].Band = value
			case "hwmode":
				radios[idx].Hwmode = value
			case "channel":
				radios[idx].Channel = value
			}
			continue
		}
		if value == "wifi-device" {
			index[section] = len(radios)
			radios = append(radios, uciRadio{Section: section})
		}
	}
	return radios
}

// wifiDeviceSections returns the wifi-device section names in the order they
// appear in `uci show wireless` output.
func wifiDeviceSections(showOutput string) []string {
	radios := parseUciShowWireless(showOutput)
	sections := make([]string, 0, len(radios))
	for _, radio := range radios {
		sections = append(sections, radio.Section)
	}
	return sections
}

// radioBandMap maps each band to the first wifi-device section reporting that
// band, in `uci show wireless` order. Bands nobody claims are absent.
func radioBandMap(showOutput string) map[string]string {
	bands := make(map[string]string)
	for _, radio := range parseUciShowWireless(showOutput) {
		band := classifyRadioBand(radio.Band, radio.Hwmode, radio.Channel)
		if band == "" {
			continue
		}
		if _, taken := bands[band]; !taken {
			bands[band] = radio.Section
		}
	}
	return bands
}

// radioForBand picks the wifi-device section for a band. When the config
// carries no band information at all, the legacy section name is used if it
// exists; otherwise "" tells the caller to skip creating an interface for
// that band.
func radioForBand(bandRadios map[string]string, deviceSections []string, band, legacyName string) string {
	if section := bandRadios[band]; section != "" {
		return section
	}
	for _, section := range deviceSections {
		if section == legacyName {
			return legacyName
		}
	}
	return ""
}
