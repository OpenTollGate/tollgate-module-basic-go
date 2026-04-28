package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockResellerChecker struct {
	mock.Mock
}

func (m *MockResellerChecker) IsResellerModeActive() bool {
	args := m.Called()
	return args.Bool(0)
}

func TestUpstreamManager_DefaultConfig(t *testing.T) {
	config := DefaultUpstreamManagerConfig()
	assert.Equal(t, 300, int(config.ScanInterval.Seconds()))
	assert.Equal(t, 30, int(config.FastCheck.Seconds()))
	assert.Equal(t, 2, config.LostThreshold)
	assert.Equal(t, 12, config.HysteresisDB)
	assert.Equal(t, -85, config.SignalFloor)
}

func TestUpstreamManager_NewUpstreamManager(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)
	assert.NotNil(t, um)
	assert.Equal(t, connector, um.connector)
	assert.Equal(t, scanner, um.scanner)
	assert.Equal(t, reseller, um.reseller)
}

func TestUpstreamManager_NewUpstreamManager_SetsDefaults(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}

	um := NewUpstreamManager(connector, scanner, reseller, UpstreamManagerConfig{})
	assert.Equal(t, 300, int(um.config.ScanInterval.Seconds()))
	assert.Equal(t, 30, int(um.config.FastCheck.Seconds()))
	assert.Equal(t, 2, um.config.LostThreshold)
	assert.Equal(t, 12, um.config.HysteresisDB)
	assert.Equal(t, -85, um.config.SignalFloor)
}

func TestUpstreamManager_FindStrongestCandidate_NoKnownSSIDs(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetSTASections").Return([]STASection{}, nil)

	networks := []NetworkInfo{
		{SSID: "SomeNet", Signal: -50, Radio: "radio0"},
	}

	candidate, err := um.findStrongestCandidate(networks)
	assert.Error(t, err)
	assert.Nil(t, candidate)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_FindStrongestCandidate_WithKnownSSID(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_mynet", SSID: "MyNet", Device: "radio0", Disabled: true},
	}, nil)

	networks := []NetworkInfo{
		{SSID: "MyNet", Signal: -45, Radio: "radio1", BSSID: "AA:BB:CC:DD:EE:FF"},
		{SSID: "OtherNet", Signal: -50, Radio: "radio0"},
	}

	candidate, err := um.findStrongestCandidate(networks)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "MyNet", candidate.SSID)
	assert.Equal(t, -45, candidate.Signal)
	assert.Equal(t, "radio1", candidate.Radio)
	assert.Equal(t, "upstream_mynet", candidate.IfaceName)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_FindStrongestCandidate_MultipleKnownSSIDs(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_neta", SSID: "NetA", Device: "radio0", Disabled: true},
		{Name: "upstream_netb", SSID: "NetB", Device: "radio1", Disabled: true},
	}, nil)

	networks := []NetworkInfo{
		{SSID: "NetA", Signal: -60, Radio: "radio0"},
		{SSID: "NetB", Signal: -40, Radio: "radio1"},
	}

	candidate, err := um.findStrongestCandidate(networks)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "NetB", candidate.SSID)
	assert.Equal(t, -40, candidate.Signal)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_IsResellerModeActive_True(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)
	reseller.On("IsResellerModeActive").Return(true)

	assert.True(t, um.isResellerModeActive())
	reseller.AssertExpectations(t)
}

func TestUpstreamManager_IsResellerModeActive_False(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)
	reseller.On("IsResellerModeActive").Return(false)

	assert.False(t, um.isResellerModeActive())
	reseller.AssertExpectations(t)
}

func TestUpstreamManager_IsResellerModeActive_NilChecker(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, nil, config)
	assert.False(t, um.isResellerModeActive())
}

func TestUpstreamManager_RunScanCycle_NoActiveUpstream(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "MyNet", Signal: -45, Radio: "radio0"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_mynet", SSID: "MyNet", Device: "radio0", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "", "upstream_mynet", "MyNet").Return(nil)

	um.runScanCycle("", "", 0, "no-active-upstream")

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
}

func TestUpstreamManager_RunScanCycle_BelowHysteresis_NoSwitch(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "MyNet", Signal: -55, Radio: "radio0"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_mynet", SSID: "MyNet", Device: "radio0", Disabled: true},
	}, nil)

	um.runScanCycle("upstream_othernet", "OtherNet", -60, "scheduled")

	connector.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpstreamManager_RunScanCycle_AboveHysteresis_Switches(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "MyNet", Signal: -40, Radio: "radio0"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_mynet", SSID: "MyNet", Device: "radio0", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_othernet", "upstream_mynet", "MyNet").Return(nil)

	um.runScanCycle("upstream_othernet", "OtherNet", -60, "scheduled")

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
}

func TestUpstreamManager_RunScanCycle_BelowSignalFloor(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "MyNet", Signal: -80, Radio: "radio0"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_mynet", SSID: "MyNet", Device: "radio0", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_othernet", "upstream_mynet", "MyNet").Return(nil)

	um.runScanCycle("upstream_othernet", "OtherNet", -90, "scheduled")

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
}

func TestUpstreamManager_RunScanCycle_ScanFails(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	scanner.On("ScanAllRadios").Return([]NetworkInfo{}, assert.AnError)

	um.runScanCycle("", "", 0, "scheduled")

	scanner.AssertExpectations(t)
	connector.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything)
}
