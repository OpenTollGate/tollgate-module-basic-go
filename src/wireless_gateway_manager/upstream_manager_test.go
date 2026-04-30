package wireless_gateway_manager

import (
	"fmt"
	"testing"
	"time"

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

func TestUpstreamManager_FindKnownCandidates_NoKnownSSIDs(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetSTASections").Return([]STASection{}, nil)

	networks := []NetworkInfo{
		{SSID: "SomeNet", Signal: -50, Radio: "radio0"},
	}

	candidate, err := um.findCandidates(networks, false, false)
	assert.Error(t, err)
	assert.Nil(t, candidate)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_FindKnownCandidates_WithKnownSSID(t *testing.T) {
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

	candidate, err := um.findCandidates(networks, false, false)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "MyNet", candidate.SSID)
	assert.Equal(t, -45, candidate.Signal)
	assert.Equal(t, "radio1", candidate.Radio)
	assert.Equal(t, "upstream_mynet", candidate.IfaceName)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_FindKnownCandidates_MultipleKnownSSIDs(t *testing.T) {
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

	candidate, err := um.findCandidates(networks, false, false)
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

	um.runScanCycle("", "", 0, "no-active-upstream", false)

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

	um.runScanCycle("upstream_othernet", "OtherNet", -60, "scheduled", false)

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

	um.runScanCycle("upstream_othernet", "OtherNet", -60, "scheduled", false)

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

	um.runScanCycle("upstream_othernet", "OtherNet", -90, "scheduled", false)

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

	um.runScanCycle("", "", 0, "scheduled", false)

	scanner.AssertExpectations(t)
	connector.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpstreamManager_ResellerFallbackToDisabledSTA(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.blacklistSSID("TollGate-XYZ")

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_homewifi", SSID: "HomeWiFi", Device: "radio0", Disabled: true},
		{Name: "upstream_tollgate_xyz", SSID: "TollGate-XYZ", Device: "radio0", Disabled: true},
	}, nil)

	networks := []NetworkInfo{
		{SSID: "HomeWiFi", Signal: -45, Radio: "radio0"},
		{SSID: "TollGate-XYZ", Signal: -30, Radio: "radio0"},
	}

	candidate, err := um.findCandidates(networks, true, false)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "HomeWiFi", candidate.SSID)
	assert.Equal(t, "upstream_homewifi", candidate.IfaceName)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_ResellerPrefersTollGateOverDisabledSTA(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_homewifi", SSID: "HomeWiFi", Device: "radio0", Disabled: true},
		{Name: "upstream_tollgate_abc", SSID: "TollGate-ABC", Device: "radio0", Disabled: true},
	}, nil)

	networks := []NetworkInfo{
		{SSID: "HomeWiFi", Signal: -50, Radio: "radio0"},
		{SSID: "TollGate-ABC", Signal: -30, Radio: "radio0", Encryption: "none"},
	}

	candidate, err := um.findCandidates(networks, true, false)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "TollGate-ABC", candidate.SSID)
	assert.Equal(t, -30, candidate.Signal)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_EmergencyScan_PrefersFallbackOverTollGate(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_homewifi", SSID: "HomeWiFi", Device: "radio0", Disabled: true},
		{Name: "upstream_tollgate_abc", SSID: "TollGate-ABC", Device: "radio0", Disabled: true},
	}, nil)

	networks := []NetworkInfo{
		{SSID: "HomeWiFi", Signal: -45, Radio: "radio0"},
		{SSID: "TollGate-ABC", Signal: -30, Radio: "radio0", Encryption: "none"},
	}

	candidate, err := um.findCandidates(networks, true, true)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "HomeWiFi", candidate.SSID, "During emergency, fallback should win over stronger TollGate (-45 actual vs -30-20=penalized -50)")
	connector.AssertExpectations(t)
}

func TestUpstreamManager_EmergencyScan_TollGateWinsOnlyIfMuchStronger(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_homewifi", SSID: "HomeWiFi", Device: "radio0", Disabled: true},
		{Name: "upstream_tollgate_abc", SSID: "TollGate-ABC", Device: "radio0", Disabled: true},
	}, nil)

	networks := []NetworkInfo{
		{SSID: "HomeWiFi", Signal: -60, Radio: "radio0"},
		{SSID: "TollGate-ABC", Signal: -20, Radio: "radio0", Encryption: "none"},
	}

	candidate, err := um.findCandidates(networks, true, true)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "TollGate-ABC", candidate.SSID, "TollGate wins if penalized signal (-40) still beats fallback (-60)")
	connector.AssertExpectations(t)
}

func TestUpstreamManager_PauseConnectivityChecks(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	assert.False(t, um.isPaused(), "should not be paused initially")

	um.PauseConnectivityChecks(100 * time.Millisecond)
	assert.True(t, um.isPaused(), "should be paused immediately after pause call")

	time.Sleep(150 * time.Millisecond)
	assert.False(t, um.isPaused(), "should not be paused after duration expires")
}

func TestUpstreamManager_Stop(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	select {
	case <-um.stopChan:
		t.Fatal("stopChan should be open initially")
	default:
	}

	um.Stop()

	select {
	case <-um.stopChan:
	default:
		t.Fatal("stopChan should be closed after Stop()")
	}
}

func TestUpstreamManager_BlacklistSSID(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	assert.False(t, um.isBlacklisted("TestNet"), "should not be blacklisted initially")

	um.blacklistSSID("TestNet")
	assert.True(t, um.isBlacklisted("TestNet"), "should be blacklisted after adding")
	assert.False(t, um.isBlacklisted("OtherNet"), "other SSIDs should not be affected")
}

func TestUpstreamManager_BlacklistTTLExpiry(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.BlacklistTTL = 100 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.blacklistSSID("TestNet")
	assert.True(t, um.isBlacklisted("TestNet"), "should be blacklisted immediately")

	time.Sleep(150 * time.Millisecond)
	assert.False(t, um.isBlacklisted("TestNet"), "should expire after TTL")
}

func TestUpstreamManager_PurgeBlacklist(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.BlacklistTTL = 100 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.blacklistSSID("OldNet")
	um.blacklistSSID("RecentNet")

	um.blacklistMu.Lock()
	um.blacklist["OldNet"] = time.Now().Add(-200 * time.Millisecond)
	um.blacklistMu.Unlock()

	time.Sleep(10 * time.Millisecond)
	um.purgeBlacklist()

	assert.False(t, um.isBlacklisted("OldNet"), "expired entry should be purged")
	assert.True(t, um.isBlacklisted("RecentNet"), "fresh entry should remain")
}

func TestUpstreamManager_CircuitBreaker_BlocksScanAfterFailures(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.recordSwitchFailure()
	um.recordSwitchFailure()
	assert.False(t, um.isInCooldown(), "Should not be in cooldown after 2 failures")

	um.recordSwitchFailure()
	assert.True(t, um.isInCooldown(), "Should be in cooldown after 3 failures")

	um.resetSwitchFailures()
	assert.False(t, um.isInCooldown(), "Should not be in cooldown after reset")
}

func TestUpstreamManager_CircuitBreaker_SkipsScanCycleWhenInCooldown(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.recordSwitchFailure()
	um.recordSwitchFailure()
	um.recordSwitchFailure()

	um.runScanCycle("upstream_old", "OldNet", -60, "emergency", false)

	scanner.AssertNotCalled(t, "ScanAllRadios")
	connector.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpstreamManager_CircuitBreaker_ResetOnSuccess(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.recordSwitchFailure()
	um.recordSwitchFailure()
	assert.Equal(t, 2, um.consecutiveFails)

	um.resetSwitchFailures()
	assert.Equal(t, 0, um.consecutiveFails)
	assert.False(t, um.isInCooldown())
}

func TestUpstreamManager_SwitchFailure_CountsAndCooldowns(t *testing.T) {
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
	connector.On("SwitchUpstream", "upstream_old", "upstream_mynet", "MyNet").Return(fmt.Errorf("switch failed"))

	um.runScanCycle("upstream_old", "OldNet", -90, "emergency", false)
	assert.Equal(t, 1, um.consecutiveFails)

	um.runScanCycle("upstream_old", "OldNet", -90, "emergency", false)
	assert.Equal(t, 2, um.consecutiveFails)

	um.runScanCycle("upstream_old", "OldNet", -90, "emergency", false)
	assert.Equal(t, 3, um.consecutiveFails)
	assert.True(t, um.isInCooldown())
}

func TestUpstreamManager_PostSwitch_NoBlacklistWhenConnectivityOK(t *testing.T) {
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
	connector.On("SwitchUpstream", "upstream_old", "upstream_mynet", "MyNet").Return(nil)

	um.runScanCycle("upstream_old", "OldNet", -90, "emergency", false)

	assert.False(t, um.isBlacklisted("MyNet"), "SSID should NOT be blacklisted when post-switch ping succeeds")
	assert.True(t, um.isBlacklisted("OldNet"), "Old SSID should be blacklisted (emergency switch)")
	connector.AssertExpectations(t)
}
