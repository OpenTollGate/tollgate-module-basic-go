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
	assert.Equal(t, 60, int(config.BlacklistTTL.Minutes()))
	assert.Equal(t, 20, config.EmergencyPenalty)
	assert.Equal(t, 3, config.MaxConsecutiveFailures)
	assert.Equal(t, 10, int(config.SwitchCooldown.Minutes()))
	assert.Equal(t, 90, int(config.StartupGracePeriod.Seconds()))
	assert.Equal(t, 5, int(config.PostSwitchWait.Seconds()))
	assert.Equal(t, 15, int(config.StartupSettle.Seconds()))
	assert.Equal(t, 10, int(config.StartupRetryInterval.Seconds()))
	assert.Equal(t, 10, int(config.StartupScanInterval.Seconds()))
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
	assert.Equal(t, 60, int(um.config.BlacklistTTL.Minutes()))
	assert.Equal(t, 20, um.config.EmergencyPenalty)
	assert.Equal(t, 3, um.config.MaxConsecutiveFailures)
	assert.Equal(t, 10, int(um.config.SwitchCooldown.Minutes()))
	assert.Equal(t, 90, int(um.config.StartupGracePeriod.Seconds()))
	assert.Equal(t, 5, int(um.config.PostSwitchWait.Seconds()))
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

func TestUpstreamManager_CleanupStaleSTAs_InterfaceContract(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("CleanupStaleSTAs").Return(nil)
	err := um.connector.CleanupStaleSTAs()
	assert.NoError(t, err)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_CleanupStaleSTAs_InterfaceContract_Error(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("CleanupStaleSTAs").Return(assert.AnError)
	err := um.connector.CleanupStaleSTAs()
	assert.Error(t, err)
	connector.AssertExpectations(t)
}

func TestUpstreamManager_ConfigOverride_NewFields(t *testing.T) {
	config := UpstreamManagerConfig{
		EmergencyPenalty:       50,
		MaxConsecutiveFailures: 5,
		SwitchCooldown:         20 * time.Minute,
		StartupGracePeriod:     60 * time.Second,
		PostSwitchWait:         10 * time.Second,
	}
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}

	um := NewUpstreamManager(connector, scanner, reseller, config)
	assert.Equal(t, 50, um.config.EmergencyPenalty)
	assert.Equal(t, 5, um.config.MaxConsecutiveFailures)
	assert.Equal(t, 20*time.Minute, um.config.SwitchCooldown)
	assert.Equal(t, 60*time.Second, um.config.StartupGracePeriod)
	assert.Equal(t, 10*time.Second, um.config.PostSwitchWait)
	assert.Equal(t, 15*time.Second, um.config.StartupSettle)
	assert.Equal(t, 10*time.Second, um.config.StartupRetryInterval)
	assert.Equal(t, 10*time.Second, um.config.StartupScanInterval)
}

func TestUpstreamManager_StartupCheck_NoActiveSTA(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetActiveSTA").Return(nil, nil)

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertNotCalled(t, "ScanAllRadios")
	assert.Empty(t, um.blacklist)
}

func TestUpstreamManager_StartupCheck_GetActiveSTAError(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)

	connector.On("GetActiveSTA").Return(nil, fmt.Errorf("uci error"))

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertNotCalled(t, "ScanAllRadios")
	assert.Empty(t, um.blacklist)
}

func TestUpstreamManager_StartupCheck_HasInternet(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return true }

	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_mynet", SSID: "MyNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_mynet").Return("phy0-sta0", nil)

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertNotCalled(t, "ScanAllRadios")
	assert.Empty(t, um.blacklist)
}

func TestUpstreamManager_StartupCheck_NoInternet_SwitchesToCandidate(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(false)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "GoodNet", Signal: -40, Radio: "radio1"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_goodnet", SSID: "GoodNet", Device: "radio1", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_badnet", "upstream_goodnet", "GoodNet").Return(nil)

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	assert.True(t, um.isBlacklisted("BadNet"), "non-internet SSID should be blacklisted after successful switch")
	assert.False(t, um.isBlacklisted("GoodNet"), "working candidate should not be blacklisted")
}

func TestUpstreamManager_StartupCheck_NoInternet_NoCandidateAvailable(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(false)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "UnknownNet", Signal: -40, Radio: "radio0"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{}, nil)

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	assert.False(t, um.isBlacklisted("BadNet"), "SSID should NOT be blacklisted when no candidate found — deferred to main loop")
	connector.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpstreamManager_StartupCheck_NoInternet_SwitchFails(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(false)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "GoodNet", Signal: -40, Radio: "radio1"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_goodnet", SSID: "GoodNet", Device: "radio1", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_badnet", "upstream_goodnet", "GoodNet").Return(fmt.Errorf("wifi reload failed"))

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	assert.False(t, um.isBlacklisted("BadNet"), "SSID should NOT be blacklisted when switch fails — deferred to main loop")
	assert.Equal(t, 3, um.consecutiveFails, "switch failures should be recorded for each retry")
}

func TestUpstreamManager_StartupCheck_ResellerMode(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(true)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "HomeWiFi", Signal: -45, Radio: "radio0"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_homewifi", SSID: "HomeWiFi", Device: "radio0", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_badnet", "upstream_homewifi", "HomeWiFi").Return(nil)

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	reseller.AssertExpectations(t)
	assert.True(t, um.isBlacklisted("BadNet"))
}

func TestUpstreamManager_ConfigOverride_ZeroValues_Default(t *testing.T) {
	config := UpstreamManagerConfig{}
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}

	um := NewUpstreamManager(connector, scanner, reseller, config)
	assert.Equal(t, 20, um.config.EmergencyPenalty)
	assert.Equal(t, 3, um.config.MaxConsecutiveFailures)
	assert.Equal(t, 10*time.Minute, um.config.SwitchCooldown)
	assert.Equal(t, 90*time.Second, um.config.StartupGracePeriod)
	assert.Equal(t, 5*time.Second, um.config.PostSwitchWait)
	assert.Equal(t, 15*time.Second, um.config.StartupSettle)
	assert.Equal(t, 10*time.Second, um.config.StartupRetryInterval)
	assert.Equal(t, 10*time.Second, um.config.StartupScanInterval)
}

func TestUpstreamManager_StartupCheck_ScanFailsThenSucceedsOnRetry(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(false)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{}, fmt.Errorf("scan busy")).Once()
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "GoodNet", Signal: -40, Radio: "radio1"},
	}, nil).Once()
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_goodnet", SSID: "GoodNet", Device: "radio1", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_badnet", "upstream_goodnet", "GoodNet").Return(nil)

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	assert.True(t, um.isBlacklisted("BadNet"), "should be blacklisted after successful switch on retry")
	assert.False(t, um.isBlacklisted("GoodNet"))
}

func TestUpstreamManager_StartupCheck_CandidateNotFoundFirstScan(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(false)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "UnknownNet", Signal: -50, Radio: "radio0"},
	}, nil).Once()
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "GoodNet", Signal: -40, Radio: "radio1"},
	}, nil).Once()
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_goodnet", SSID: "GoodNet", Device: "radio1", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_badnet", "upstream_goodnet", "GoodNet").Return(nil)

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	assert.True(t, um.isBlacklisted("BadNet"))
}

func TestUpstreamManager_StartupCheck_SwitchFailsThenSucceedsOnRetry(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(false)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "GoodNet", Signal: -40, Radio: "radio1"},
	}, nil)
	connector.On("GetSTASections").Return([]STASection{
		{Name: "upstream_goodnet", SSID: "GoodNet", Device: "radio1", Disabled: true},
	}, nil)
	connector.On("SwitchUpstream", "upstream_badnet", "upstream_goodnet", "GoodNet").Return(fmt.Errorf("failed")).Once()
	connector.On("SwitchUpstream", "upstream_badnet", "upstream_goodnet", "GoodNet").Return(nil).Once()

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	assert.True(t, um.isBlacklisted("BadNet"), "should be blacklisted after successful switch on retry")
}

func TestUpstreamManager_StartupCheck_AllScanRetriesFail_NoBlacklist(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.StartupSettle = 1 * time.Millisecond
	config.StartupRetryInterval = 1 * time.Millisecond
	config.StartupScanInterval = 1 * time.Millisecond

	um := NewUpstreamManager(connector, scanner, reseller, config)
	um.connectivityCheckFn = func() bool { return false }

	reseller.On("IsResellerModeActive").Return(false)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "upstream_badnet", SSID: "BadNet", Device: "radio0",
	}, nil)
	connector.On("GetSTANetdev", "upstream_badnet").Return("phy0-sta0", nil)
	scanner.On("ScanAllRadios").Return([]NetworkInfo{}, fmt.Errorf("scan busy"))

	um.startupConnectivityCheck()

	connector.AssertExpectations(t)
	scanner.AssertExpectations(t)
	assert.False(t, um.isBlacklisted("BadNet"), "should NOT be blacklisted when all scan retries fail — deferred to main loop")
}

// --- PinUpstream Tests ---

func TestUpstreamManager_PinUpstream_SetsPin(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "sta_net", SSID: "MyNet", Device: "radio0",
	}, nil)

	um.PinUpstream("MyNet", 10*time.Minute)

	assert.True(t, um.isPinned())
	assert.Equal(t, "MyNet", um.getPinnedSSID())
}

func TestUpstreamManager_PinUpstream_EmptySSID_ResolvesToActive(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)
	connector.On("GetActiveSTA").Return(&STASection{
		Name: "sta_net", SSID: "ActiveNet", Device: "radio0",
	}, nil)

	um.PinUpstream("", 5*time.Minute)

	assert.True(t, um.isPinned())
	assert.Equal(t, "ActiveNet", um.getPinnedSSID())
}

func TestUpstreamManager_PinUpstream_Expires(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.PinUpstream("TestNet", 1*time.Nanosecond)

	time.Sleep(10 * time.Millisecond)

	assert.False(t, um.isPinned())
	assert.Equal(t, "", um.getPinnedSSID())
}

func TestUpstreamManager_PinUpstream_NotPinnedByDefault(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	assert.False(t, um.isPinned())
	assert.Equal(t, "", um.getPinnedSSID())
}

func TestUpstreamManager_PinUpstream_Overwrite(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()

	um := NewUpstreamManager(connector, scanner, reseller, config)

	um.PinUpstream("FirstNet", 10*time.Minute)
	assert.Equal(t, "FirstNet", um.getPinnedSSID())

	um.PinUpstream("SecondNet", 10*time.Minute)
	assert.Equal(t, "SecondNet", um.getPinnedSSID())
}
