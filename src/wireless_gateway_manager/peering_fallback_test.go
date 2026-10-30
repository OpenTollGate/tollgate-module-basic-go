package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFallbackToKnownGoodNonTollGate_Success(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	um := NewUpstreamManager(connector, scanner, &MockResellerChecker{}, DefaultUpstreamManagerConfig())

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_peer", SSID: "!TollGate-AB12", Device: "radio0", Disabled: false},
		{Name: "upstream_home", SSID: "HomeWiFi", Device: "radio0", Disabled: true},
	}, nil)
	connector.On("GetActiveSTA").Return(&STASection{Name: "upstream_peer", SSID: "!TollGate-AB12", Disabled: false}, nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "!TollGate-AB12", Signal: -40, Radio: "radio0", BSSID: "AA:AA:AA:AA:AA:AA", IsTollGate: true},
		{SSID: "HomeWiFi", Signal: -55, Radio: "radio0", BSSID: "BB:BB:BB:BB:BB:BB"},
	}, nil)
	connector.On("SwitchUpstream", "upstream_peer", "upstream_home", "HomeWiFi", "BB:BB:BB:BB:BB:BB").Return(nil)
	um.fallbackVerifyFn = func() bool { return true }

	ssid, err := um.FallbackToKnownGoodNonTollGate()
	assert.NoError(t, err)
	assert.Equal(t, "HomeWiFi", ssid)
	connector.AssertExpectations(t)
}

func TestFallbackToKnownGoodNonTollGate_NoCandidate(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	um := NewUpstreamManager(connector, scanner, &MockResellerChecker{}, DefaultUpstreamManagerConfig())

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_peer", SSID: "!TollGate-AB12", Device: "radio0", Disabled: false},
		{Name: "upstream_private", SSID: "Private-c08r4d0r-AB12", Device: "radio1", Disabled: true},
	}, nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{}, nil)

	ssid, err := um.FallbackToKnownGoodNonTollGate()
	assert.Error(t, err)
	assert.Empty(t, ssid)
	assert.Contains(t, err.Error(), "staying on the current peer")
	connector.AssertNotCalled(t, "SwitchUpstream", "", "", "", "")
}

func TestFallbackToKnownGoodNonTollGate_RestoresPeerWhenAllFail(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	um := NewUpstreamManager(connector, scanner, &MockResellerChecker{}, DefaultUpstreamManagerConfig())
	peer := &STASection{Name: "upstream_peer", SSID: "!TollGate-AB12", Device: "radio0", Disabled: false}

	connector.On("GetSTASections").Return([]STASection{
		*peer,
		{Name: "upstream_home", SSID: "HomeWiFi", Device: "radio0", Disabled: true},
	}, nil)
	connector.On("GetActiveSTA").Return(peer, nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "HomeWiFi", Signal: -55, Radio: "radio0", BSSID: "BB:BB:BB:BB:BB:BB"},
	}, nil)
	connector.On("SwitchUpstream", "upstream_peer", "upstream_home", "HomeWiFi", "BB:BB:BB:BB:BB:BB").Return(nil)
	connector.On("SwitchUpstream", "", "upstream_peer", "!TollGate-AB12", "").Return(nil)
	um.fallbackVerifyFn = func() bool { return false }

	ssid, err := um.FallbackToKnownGoodNonTollGate()
	assert.Error(t, err)
	assert.Empty(t, ssid)
	assert.Contains(t, err.Error(), "restored the current peer")
	connector.AssertExpectations(t)
}
