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
