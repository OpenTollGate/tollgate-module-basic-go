package wireless_gateway_manager

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEmergencyRecoveryUsesWeakerSavedNetwork(t *testing.T) {
	connector := &MockConnector{}
	scanner := &MockScanner{}
	manager := NewUpstreamManager(connector, scanner, nil, DefaultUpstreamManagerConfig())
	scanner.On("ScanAllRadios").Return([]NetworkInfo{{SSID: "SavedBackup", Signal: -70, Radio: "radio1"}, {SSID: "Unknown", Signal: -20, Radio: "radio1"}}, nil)
	connector.On("GetSTASections").Return([]STASection{{Name: "backup", SSID: "SavedBackup", Disabled: true}}, nil)
	connector.On("SwitchUpstream", "current", "backup", "SavedBackup", "").Return(errors.New("fixture stops after selection"))
	manager.runScanCycle("current", "UnavailableInternet", "", -45, "emergency", false)
	require.True(t, connector.AssertCalled(t, "SwitchUpstream", "current", "backup", "SavedBackup", ""))
}

func TestManualOperationOutlivesTimerAndPreservesOtherOperations(t *testing.T) {
	c := &MockConnector{}
	sc := &MockScanner{}
	m := NewUpstreamManager(c, sc, nil, DefaultUpstreamManagerConfig())
	m.BeginManualOperation()
	m.BeginManualOperation()
	m.PauseConnectivityChecks(time.Nanosecond)
	time.Sleep(time.Millisecond)
	m.EndManualOperation()
	require.True(t, m.isPaused())
	m.runScanCycle("current", "Current", "", -45, "emergency", false)
	sc.AssertNotCalled(t, "ScanAllRadios")
	m.EndManualOperation()
	require.False(t, m.isPaused())
}
func TestManualPauseDuringScanPreventsAutomaticSwitch(t *testing.T) {
	c := &MockConnector{}
	sc := &MockScanner{}
	m := NewUpstreamManager(c, sc, nil, DefaultUpstreamManagerConfig())
	sc.On("ScanAllRadios").Run(func(mock.Arguments) { m.BeginManualOperation() }).Return([]NetworkInfo{{SSID: "Backup", Signal: -20, Radio: "radio1"}}, nil)
	c.On("GetSTASections").Return([]STASection{{Name: "backup", SSID: "Backup", Disabled: true}}, nil)
	m.runScanCycle("current", "Current", "", -45, "emergency", false)
	c.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	m.EndManualOperation()
}
func TestPostSwitchVerificationWaitsForConvergence(t *testing.T) {
	calls := 0
	result := waitForPostSwitchConnectivity(func() ConnectivityResult { calls++; return ConnectivityResult{OK: calls >= 3, Reason: "no-route"} }, func() bool { return true }, time.Second, time.Millisecond)
	require.True(t, result.OK)
	require.Equal(t, 3, calls)
}
func TestPostSwitchVerificationRetainsFailureReason(t *testing.T) {
	result := waitForPostSwitchConnectivity(func() ConnectivityResult { return ConnectivityResult{Reason: "no-dns"} }, func() bool { return true }, time.Millisecond, time.Millisecond)
	require.False(t, result.OK)
	require.Equal(t, "no-dns", result.Reason)
}
func TestManualSuccessClearsOnlySelectedBSSID(t *testing.T) {
	m := NewUpstreamManager(&MockConnector{}, &MockScanner{}, nil, DefaultUpstreamManagerConfig())
	m.blacklistUpstream("Saved", "02:00:00:00:00:01")
	m.blacklistUpstream("Saved", "02:00:00:00:00:02")
	m.ClearSavedNetworkFailure("Saved", "02:00:00:00:00:01")
	require.False(t, m.isBlacklisted("Saved", "02:00:00:00:00:01"))
	require.True(t, m.isBlacklisted("Saved", "02:00:00:00:00:02"))
}

func TestPostSwitchVerificationDiscardsSupersededProbe(t *testing.T) {
	current := true
	result := waitForPostSwitchConnectivity(func() ConnectivityResult { current = false; return ConnectivityResult{OK: true} }, func() bool { return current }, time.Second, time.Millisecond)
	require.False(t, result.OK)
	require.Equal(t, "superseded", result.Reason)
}

func TestStaleVerificationCannotPublishAfterManualJoin(t *testing.T) {
	m := NewUpstreamManager(&MockConnector{}, &MockScanner{}, nil, DefaultUpstreamManagerConfig())
	generation := m.nextConnectionGeneration()
	m.BeginManualOperation()
	applied := false
	require.False(t, m.applyVerification(generation, func() { applied = true }))
	require.False(t, applied)
	m.EndManualOperation()
}
