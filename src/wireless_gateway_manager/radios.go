// Package wireless_gateway_manager: radio-to-band mapping for uci wireless
// sections.
package wireless_gateway_manager

import (
	"bufio"
	"os"
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
	return bandMap(parseUciShowWireless(showOutput))
}

// bandMap maps each band to the first radio reporting that band, in config
// order. Bands nobody claims are absent.
func bandMap(radios []uciRadio) map[string]string {
	bands := make(map[string]string)
	for _, radio := range radios {
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

// parseUciConfigWireless extracts the wifi-device sections and their
// band/hwmode/channel options from a RAW /etc/config/wireless file, in the
// order the sections appear. That is the format GetRadios() already reads
// ("config wifi-device 'radio0'" plus "option band '2g'"), NOT the
// "wireless.radio0=wifi-device" rendering of `uci show wireless` that
// parseUciShowWireless handles. Pure, so it is unit-tested against fixtures.
func parseUciConfigWireless(config string) []uciRadio {
	var radios []uciRadio
	index := -1
	scanner := bufio.NewScanner(strings.NewReader(config))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "config":
			if fields[1] != "wifi-device" || len(fields) < 3 {
				index = -1
				continue
			}
			name := strings.Trim(fields[2], "'\"")
			if name == "" {
				index = -1
				continue
			}
			radios = append(radios, uciRadio{Section: name})
			index = len(radios) - 1
		case "option":
			if index < 0 || len(fields) < 3 {
				continue
			}
			value := strings.Trim(fields[2], "'\"")
			switch fields[1] {
			case "band":
				radios[index].Band = value
			case "hwmode":
				radios[index].Hwmode = value
			case "channel":
				radios[index].Channel = value
			}
		}
	}
	return radios
}

// radioBandMapFromConfigText is the pure core of radioBandMapFromConfig: the
// band->radio map of a RAW /etc/config/wireless, in section order. Keeping the
// file format and the `uci show` format apart is the point — the scan path
// reads the FILE, so handing it to the `uci show` parser produced an empty map
// and every scanned network was stamped band "unknown" (#490 follow-up).
func radioBandMapFromConfigText(config string) map[string]string {
	return bandMap(parseUciConfigWireless(config))
}

// radioBandMapFromConfig reads the live /etc/config/wireless and returns the
// band→radio map in config order. A missing/unreadable config yields an empty
// map (callers then fall back to "unknown" bands), never an error.
func radioBandMapFromConfig() map[string]string {
	data, err := os.ReadFile("/etc/config/wireless")
	if err != nil {
		return map[string]string{}
	}
	return radioBandMapFromConfigText(string(data))
}

// radioBandBySection inverts a band->radio map (the shape radioBandMap and
// radioBandMapFromConfigText return) into the radio->band map that
// assignNetworkBands consumes. The scan path used to hand the band->radio map
// straight to assignNetworkBands, whose lookup is by radio section: no key ever
// matched and every network came back band "unknown" even with a perfect band
// map (#490 follow-up).
func radioBandBySection(bandRadios map[string]string) map[string]string {
	bySection := make(map[string]string, len(bandRadios))
	for band, section := range bandRadios {
		if section != "" {
			bySection[section] = band
		}
	}
	return bySection
}

// bandByRadioFromConfig reads the live /etc/config/wireless and returns the
// radio->band map the scan path stamps each scanned network with.
func bandByRadioFromConfig() map[string]string {
	return radioBandBySection(radioBandMapFromConfig())
}

// assignNetworkBands stamps each scanned NetworkInfo with the band of the
// radio that produced it, resolved through radioBandMap (`band` option,
// legacy hwmode, then channel). Networks from a radio with no band
// information at all get "unknown" — the consumer must never guess a band
// from the section number, since radio0 is not always 2.4 GHz.
func assignNetworkBands(networks []NetworkInfo, bandByRadio map[string]string) []NetworkInfo {
	out := make([]NetworkInfo, len(networks))
	for i, net := range networks {
		out[i] = net
		if band, ok := bandByRadio[net.Radio]; ok {
			out[i].Band = band
		} else {
			out[i].Band = "unknown"
		}
	}
	return out
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
