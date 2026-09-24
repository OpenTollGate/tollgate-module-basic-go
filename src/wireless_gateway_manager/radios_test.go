package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyRadioBand(t *testing.T) {
	tests := []struct {
		band, hwmode, channel string
		expected              string
	}{
		// OpenWrt 21.02+ band option
		{"2g", "", "", "2g"},
		{"5g", "", "", "5g"},
		{"6g", "", "", "6g"},
		{"60g", "", "", "60g"},
		{" 5G ", "", "", "5g"},
		// band wins over conflicting hwmode/channel
		{"2g", "11a", "36", "2g"},
		// legacy hwmode
		{"", "11a", "", "5g"},
		{"", "11na", "", "5g"},
		{"", "11ad", "", "60g"},
		{"", "11g", "", "2g"},
		{"", "11ng", "", "2g"},
		{"", "11b", "", "2g"},
		{"", "bg", "", "2g"},
		// channel fallback (driver-independent, pre-21.02 heuristic)
		{"", "", "36", "5g"},
		{"", "", "165", "5g"},
		{"", "", "6", "2g"},
		{"", "", "13", "2g"},
		// nothing identifies the band
		{"", "", "auto", ""},
		{"", "", "0", ""}, // channel 0 means "auto" to the driver, not 2.4 GHz
		{"", "", "-1", ""},
		{"", "", "", ""},
		{"nonsense", "", "auto", ""},
	}
	for _, tt := range tests {
		result := classifyRadioBand(tt.band, tt.hwmode, tt.channel)
		assert.Equal(t, tt.expected, result, "classifyRadioBand(%q, %q, %q)", tt.band, tt.hwmode, tt.channel)
	}
}

// Fixture: `uci show wireless` on a standard dual-band router (radio0 = 2.4 GHz).
const uciShowModern = `wireless.radio0=wifi-device
wireless.radio0.type='mac80211'
wireless.radio0.path='platform/soc/a000000.wifi'
wireless.radio0.channel='1'
wireless.radio0.band='2g'
wireless.radio0.htmode='HT20'
wireless.radio0.disabled='0'
wireless.default_radio0=wifi-iface
wireless.default_radio0.device='radio0'
wireless.default_radio0.network='lan'
wireless.default_radio0.mode='ap'
wireless.default_radio0.ssid='OpenWrt'
wireless.default_radio0.encryption='none'
wireless.radio1=wifi-device
wireless.radio1.type='mac80211'
wireless.radio1.path='pci0000:00/0000:00:00.0'
wireless.radio1.channel='36'
wireless.radio1.band='5g'
wireless.radio1.htmode='HE80'
wireless.radio1.disabled='0'
wireless.default_radio1=wifi-iface
wireless.default_radio1.device='radio1'
wireless.default_radio1.network='lan'
wireless.default_radio1.mode='ap'
wireless.default_radio1.ssid='OpenWrt'
wireless.default_radio1.encryption='none'
`

// Fixture: same radio sections, swapped bands — radio0 is the 5 GHz radio.
const uciShowSwapped = `wireless.radio0=wifi-device
wireless.radio0.type='mac80211'
wireless.radio0.path='pci0000:00/0000:00:00.0'
wireless.radio0.channel='36'
wireless.radio0.band='5g'
wireless.radio0.htmode='HE80'
wireless.radio1=wifi-device
wireless.radio1.type='mac80211'
wireless.radio1.path='platform/soc/a000000.wifi'
wireless.radio1.channel='1'
wireless.radio1.band='2g'
wireless.radio1.htmode='HT20'
`

// Fixture: legacy pre-21.02 config carrying hwmode instead of band.
const uciShowLegacyHwmode = `wireless.radio0=wifi-device
wireless.radio0.type='mac80211'
wireless.radio0.hwmode='11g'
wireless.radio0.channel='3'
wireless.radio1=wifi-device
wireless.radio1.type='mac80211'
wireless.radio1.hwmode='11a'
wireless.radio1.channel='36'
`

// Fixture: ancient config with neither band nor hwmode — only channels.
const uciShowLegacyChannel = `wireless.radio0=wifi-device
wireless.radio0.type='mac80211'
wireless.radio0.channel='auto'
wireless.radio1=wifi-device
wireless.radio1.type='mac80211'
wireless.radio1.channel='auto'
`

// Fixture: single-band 2.4 GHz router.
const uciShowSingleBand = `wireless.radio0=wifi-device
wireless.radio0.type='mac80211'
wireless.radio0.band='2g'
wireless.radio0.channel='6'
`

func TestWifiDeviceSections(t *testing.T) {
	assert.Equal(t, []string{"radio0", "radio1"}, wifiDeviceSections(uciShowModern))
	assert.Equal(t, []string{"radio0"}, wifiDeviceSections(uciShowSingleBand))
	assert.Empty(t, wifiDeviceSections("uci: Entry not found"))
}

func TestRadioBandMap_Modern(t *testing.T) {
	bands := radioBandMap(uciShowModern)
	assert.Equal(t, "radio0", bands["2g"])
	assert.Equal(t, "radio1", bands["5g"])
}

func TestRadioBandMap_Swapped(t *testing.T) {
	// The radioN names say nothing about the band; classification must follow
	// the band option, not the section index.
	bands := radioBandMap(uciShowSwapped)
	assert.Equal(t, "radio1", bands["2g"])
	assert.Equal(t, "radio0", bands["5g"])
}

func TestRadioBandMap_LegacyHwmode(t *testing.T) {
	bands := radioBandMap(uciShowLegacyHwmode)
	assert.Equal(t, "radio0", bands["2g"])
	assert.Equal(t, "radio1", bands["5g"])
}

func TestRadioBandMap_NoBandInfo(t *testing.T) {
	assert.Empty(t, radioBandMap(uciShowLegacyChannel))
}

func TestRadioBandMap_SingleBand(t *testing.T) {
	bands := radioBandMap(uciShowSingleBand)
	assert.Equal(t, "radio0", bands["2g"])
	assert.NotContains(t, bands, "5g")
}

func TestRadioForBand(t *testing.T) {
	sections := []string{"radio0", "radio1"}

	// Band info present: it wins over the legacy literal.
	assert.Equal(t, "radio1", radioForBand(map[string]string{"2g": "radio1"}, sections, "2g", "radio0"))

	// No band info anywhere: fall back to the historical section name.
	assert.Equal(t, "radio0", radioForBand(map[string]string{}, sections, "2g", "radio0"))
	assert.Equal(t, "radio1", radioForBand(map[string]string{}, sections, "5g", "radio1"))

	// No band info and the legacy section does not exist (single-radio box):
	// skip rather than bind to a phantom radio.
	assert.Equal(t, "", radioForBand(map[string]string{}, []string{"radio0"}, "5g", "radio1"))
	assert.Equal(t, "", radioForBand(map[string]string{}, nil, "2g", "radio0"))
}

// TestAssignNetworkBands guards the scan-path half of #452: the installer and
// admin SPA see a network's Radio (e.g. "radio1") but not which band that
// radio is on. Each scanned NetworkInfo must carry the classified band so the
// consumer can tell a 2.4 GHz SSID from a 5 GHz one without re-deriving it.
func TestAssignNetworkBands(t *testing.T) {
	networks := []NetworkInfo{
		{SSID: "Home24", Radio: "radio1"},
		{SSID: "Home50", Radio: "radio0"},
		{SSID: "UnknownRadio", Radio: "radio9"},
	}
	// Swapped hardware: radio0 = 5 GHz, radio1 = 2.4 GHz (the #452 box).
	bandByRadio := map[string]string{"radio0": "5g", "radio1": "2g"}
	out := assignNetworkBands(networks, bandByRadio)
	assert.Equal(t, "2g", out[0].Band, "radio1 is the 2.4 GHz radio")
	assert.Equal(t, "5g", out[1].Band, "radio0 is the 5 GHz radio")
	assert.Equal(t, "unknown", out[2].Band, "band unknown on a radio without band info")
}

func TestRadioForBand_NeverBindsAnUnclassifiedRadio(t *testing.T) {
	sections := []string{"radio0", "radio1"}

	// Partially classified config: radio0 reports 2g, radio1 reports nothing.
	// Falling back to the literal "radio1" for the 5 GHz interface would
	// re-introduce the #452 defect (on swapped hardware the unclassified radio
	// is the 2.4 GHz one), so the caller must skip that band instead.
	partial := map[string]string{"2g": "radio0"}
	assert.Equal(t, "", radioForBand(partial, sections, "5g", "radio1"))

	// Fully classified config: the band map still wins.
	complete := map[string]string{"2g": "radio0", "5g": "radio1"}
	assert.Equal(t, "radio1", radioForBand(complete, sections, "5g", "radio1"))

	// Single-band device: the band nobody reports is skipped, never guessed.
	single := map[string]string{"2g": "radio0"}
	assert.Equal(t, "", radioForBand(single, []string{"radio0"}, "5g", "radio1"))
}

// ---------------------------------------------------------------------------
// The scan path reads /etc/config/wireless itself (scanner.go: GetRadios and
// radioBandMapFromConfig), so the band resolution must understand the CONFIG
// FILE format, not only `uci show` output. `uci show` renders a section as
// "wireless.radio0=wifi-device" while the file on disk holds
// "config wifi-device 'radio0'" plus "option band '2g'" — feeding the file to
// the `uci show` parser produced an empty map, so the scan path stamped
// band="unknown" on every network on real hardware (#490 follow-up).
// ---------------------------------------------------------------------------

// Fixture: /etc/config/wireless on the swapped dual-band hardware this card is
// about (radio0 is the 5 GHz radio, radio1 the 2.4 GHz one).
const wirelessConfigSwapped = `config wifi-device 'radio0'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:00.0'
	option channel '36'
	option band '5g'
	option htmode 'HE80'
	option disabled '0'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option network 'lan'
	option mode 'ap'
	option ssid 'OpenWrt'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '1'
	option band '2g'
	option htmode 'HT20'
	option disabled '0'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option network 'lan'
	option mode 'ap'
	option ssid 'OpenWrt'
`

// Fixture: the same file on a router where radio section order matches the
// band order (radio0 = 2.4 GHz).
const wirelessConfigModern = `config wifi-device 'radio0'
	option type 'mac80211'
	option channel '1'
	option band '2g'

config wifi-device 'radio1'
	option type 'mac80211'
	option channel '36'
	option band '5g'
`

// Fixture: legacy pre-21.02 file carrying hwmode instead of band.
const wirelessConfigLegacyHwmode = `config wifi-device 'radio0'
	option type 'mac80211'
	option hwmode '11g'
	option channel '3'

config wifi-device 'radio1'
	option type 'mac80211'
	option hwmode '11a'
	option channel '36'
`

// Fixture: ancient file with neither band nor hwmode — only auto channels.
const wirelessConfigNoBand = `config wifi-device 'radio0'
	option type 'mac80211'
	option channel 'auto'

config wifi-device 'radio1'
	option type 'mac80211'
	option channel 'auto'
`

// Fixture: single-band 2.4 GHz router.
const wirelessConfigSingleBand = `config wifi-device 'radio0'
	option type 'mac80211'
	option band '2g'
	option channel '6'
`

func TestParseUciConfigWireless(t *testing.T) {
	radios := parseUciConfigWireless(wirelessConfigSwapped)
	assert.Len(t, radios, 2, "only wifi-device sections are radios")
	assert.Equal(t, "radio0", radios[0].Section)
	assert.Equal(t, "5g", radios[0].Band)
	assert.Equal(t, "36", radios[0].Channel)
	assert.Equal(t, "radio1", radios[1].Section)
	assert.Equal(t, "2g", radios[1].Band)
	assert.Equal(t, "1", radios[1].Channel)

	assert.Empty(t, parseUciConfigWireless(""), "a missing config file is an empty device list")
	assert.Empty(t, parseUciConfigWireless("config wifi-iface 'default_radio0'\n	option device 'radio0'\n"),
		"wifi-iface sections are not radios")
}

func TestRadioBandMapFromConfigText_Swapped(t *testing.T) {
	// The radioN names say nothing about the band; classification must follow
	// the band option, not the section index.
	bands := radioBandMapFromConfigText(wirelessConfigSwapped)
	assert.Equal(t, "radio1", bands["2g"])
	assert.Equal(t, "radio0", bands["5g"])
}

func TestRadioBandMapFromConfigText_Modern(t *testing.T) {
	bands := radioBandMapFromConfigText(wirelessConfigModern)
	assert.Equal(t, "radio0", bands["2g"])
	assert.Equal(t, "radio1", bands["5g"])
}

func TestRadioBandMapFromConfigText_LegacyHwmode(t *testing.T) {
	bands := radioBandMapFromConfigText(wirelessConfigLegacyHwmode)
	assert.Equal(t, "radio0", bands["2g"])
	assert.Equal(t, "radio1", bands["5g"])
}

func TestRadioBandMapFromConfigText_NoBandInfo(t *testing.T) {
	assert.Empty(t, radioBandMapFromConfigText(wirelessConfigNoBand))
}

func TestRadioBandMapFromConfigText_SingleBand(t *testing.T) {
	bands := radioBandMapFromConfigText(wirelessConfigSingleBand)
	assert.Equal(t, "radio0", bands["2g"])
	assert.NotContains(t, bands, "5g")
}

// TestRadioBandBySection pins the orientation the scan path needs:
// assignNetworkBands looks its map up BY RADIO SECTION, while the parsers
// return a band->radio map. Feeding the band->radio map straight in (the
// shipped #490 wiring) matched no key and stamped every network "unknown".
func TestRadioBandBySection(t *testing.T) {
	bandRadios := map[string]string{"2g": "radio1", "5g": "radio0"}
	bySection := radioBandBySection(bandRadios)
	assert.Equal(t, map[string]string{"radio0": "5g", "radio1": "2g"}, bySection)
	// Empty and partially filled maps invert without inventing entries.
	assert.Empty(t, radioBandBySection(map[string]string{}))
	assert.Empty(t, radioBandBySection(nil))
	assert.Equal(t, map[string]string{"radio0": "2g"}, radioBandBySection(map[string]string{"2g": "radio0"}))
}

// TestScanPathBandResolution_FromWirelessConfig is the regression guard for the
// scan path itself: it mirrors the composition scanner.go uses
// (`assignNetworkBands(allNetworks, bandByRadioFromConfig())`) over a captured
// config file, so `tollgate upstream scan` reports real bands instead of
// "unknown" for every network.
func TestScanPathBandResolution_FromWirelessConfig(t *testing.T) {
	networks := []NetworkInfo{
		{SSID: "Home24", Radio: "radio1"},
		{SSID: "Home50", Radio: "radio0"},
		{SSID: "Unseated", Radio: "radio9"},
	}
	// Exactly the scanner's composition, over the file text instead of the file.
	bandByRadio := radioBandBySection(radioBandMapFromConfigText(wirelessConfigSwapped))
	out := assignNetworkBands(networks, bandByRadio)
	assert.Equal(t, "2g", out[0].Band, "radio1 is the 2.4 GHz radio on this hardware")
	assert.Equal(t, "5g", out[1].Band, "radio0 is the 5 GHz radio on this hardware")
	assert.Equal(t, "unknown", out[2].Band, "a radio the config does not mention stays unknown")
}
