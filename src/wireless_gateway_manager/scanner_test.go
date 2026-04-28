package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScanner_DetectEncryption(t *testing.T) {
	s := &Scanner{}

	tests := []struct {
		input    string
		expected string
	}{
		{"none", "none"},
		{"None", "none"},
		{"OPEN", "none"},
		{"WEP-40", "none"},
		{"WPA2 PSK (CCMP)", "psk2"},
		{"WPA PSK (TKIP)", "psk"},
		{"WPA3 SAE (CCMP)", "sae"},
		{"WPA3 SAE mixed (CCMP)", "sae-mixed"},
		{"WPA2 EAP (CCMP)", "wpa2-eap"},
		{"WPA3 EAP (CCMP)", "wpa2-eap"},
		{"unknown encryption", "psk2"},
		{"mixed WPA2/WPA PSK (TKIP, CCMP)", "psk2"},
	}

	for _, tt := range tests {
		result := s.DetectEncryption(tt.input)
		assert.Equal(t, tt.expected, result, "DetectEncryption(%q) = %q, want %q", tt.input, result, tt.expected)
	}
}

func TestScanner_ParseIwinfoOutput(t *testing.T) {
	s := &Scanner{}

	input := `Cell 01 - Address: AA:BB:CC:DD:EE:FF
          ESSID: "TestNetwork"
          Protocol: 802.11
          Mode: Master
          Channel: 6
          Encryption: WPA2 PSK (CCMP)
          Signal: -55 dBm
          Quality: 55/70

Cell 02 - Address: 11:22:33:44:55:66
          ESSID: "OpenNetwork"
          Protocol: 802.11
          Mode: Master
          Channel: 11
          Encryption: none
          Signal: -70 dBm
          Quality: 30/70
`

	networks := s.ParseIwinfoOutput([]byte(input), "radio0")

	assert.Len(t, networks, 2, "Expected 2 networks from iwinfo output")

	assert.Equal(t, "TestNetwork", networks[0].SSID)
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", networks[0].BSSID)
	assert.Equal(t, -55, networks[0].Signal)
	assert.Equal(t, "WPA2 PSK (CCMP)", networks[0].Encryption)
	assert.Equal(t, "radio0", networks[0].Radio)

	assert.Equal(t, "OpenNetwork", networks[1].SSID)
	assert.Equal(t, "11:22:33:44:55:66", networks[1].BSSID)
	assert.Equal(t, -70, networks[1].Signal)
	assert.Equal(t, "none", networks[1].Encryption)
	assert.Equal(t, "radio0", networks[1].Radio)
}

func TestScanner_ParseIwinfoOutput_HiddenSSID(t *testing.T) {
	s := &Scanner{}

	input := `Cell 01 - Address: AA:BB:CC:DD:EE:FF
          ESSID: ""
          Protocol: 802.11
          Signal: -55 dBm
`
	networks := s.ParseIwinfoOutput([]byte(input), "radio0")
	assert.Len(t, networks, 0, "Hidden SSIDs should be skipped")
}

func TestScanner_ParseIwinfoOutput_Empty(t *testing.T) {
	s := &Scanner{}
	networks := s.ParseIwinfoOutput([]byte(""), "radio0")
	assert.Len(t, networks, 0)
}

func TestScanner_FindBestRadioForSSID_Found(t *testing.T) {
	s := &Scanner{}
	networks := []NetworkInfo{
		{SSID: "NetA", Signal: -50, Radio: "radio1"},
		{SSID: "NetB", Signal: -60, Radio: "radio0"},
		{SSID: "NetA", Signal: -55, Radio: "radio0"},
	}

	radio, err := s.FindBestRadioForSSID("NetA", networks)
	assert.NoError(t, err)
	assert.Equal(t, "radio1", radio)
}

func TestScanner_FindBestRadioForSSID_NotFound(t *testing.T) {
	s := &Scanner{}
	networks := []NetworkInfo{
		{SSID: "NetA", Signal: -50, Radio: "radio0"},
	}

	_, err := s.FindBestRadioForSSID("NetB", networks)
	assert.Error(t, err)
}

func TestScanner_ScanAllRadios_MockError(t *testing.T) {
	m := &MockScanner{}
	m.On("ScanAllRadios").Return([]NetworkInfo{}, assert.AnError)

	networks, err := m.ScanAllRadios()
	assert.Error(t, err)
	assert.Empty(t, networks)
}

func TestScanner_GetRadios_Mock(t *testing.T) {
	m := &MockScanner{}
	m.On("GetRadios").Return([]string{"radio0", "radio1"}, nil)

	radios, err := m.GetRadios()
	assert.NoError(t, err)
	assert.Equal(t, []string{"radio0", "radio1"}, radios)
}
