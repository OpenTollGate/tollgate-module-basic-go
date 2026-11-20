package wireless_gateway_manager

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// These seams fail closed on unexpected commands; no fixture reaches iwinfo,
// ip, ifup, a real route or a venue gateway.
func portalCallerManager(t *testing.T) (*UpstreamManager, *MockConnector, *MockScanner, *time.Time, upstreamIdentity, *int) {
	t.Helper()
	m, now, id := portalTestManager(t)
	c := m.connector.(*MockConnector)
	s := m.scanner.(*MockScanner)
	sta := &STASection{Name: id.iface, SSID: id.ssid, Device: "radio0"}
	c.On("GetActiveSTA").Return(sta, nil)
	c.On("GetSTANetdev", id.iface).Return("wlan0", nil)
	nudges := new(int)
	m.runCommandFn = func(name string, args ...string) ([]byte, error) {
		switch name {
		case "iwinfo":
			return []byte("Access Point: 02:00:00:00:00:01\nSignal: -45 dBm\n"), nil
		case "ip":
			return []byte("default via 192.0.2.1 dev wlan0"), nil
		case "ifup":
			*nudges++
			return nil, nil
		default:
			t.Fatalf("unexpected command %s %v", name, args)
			return nil, errors.New("unexpected command")
		}
	}
	m.tollGateProbeFn = func(string) bool { return false }
	m.config.StartupSettle = -time.Nanosecond
	m.config.PostSwitchWait = time.Nanosecond
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{Reason: "captive"} }
	return m, c, s, now, id, nudges
}

func TestPortalLoginStartupDoesNotNudgeOrScan(t *testing.T) {
	m, c, s, _, id, nudges := portalCallerManager(t)
	m.startupConnectivityCheck()
	require.Zero(t, *nudges)
	require.Equal(t, portalHold, m.portalLoginDisposition(id.generation))
	c.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "ScanAllRadios")
	require.Empty(t, m.blacklist)
}

func TestPortalLoginTickAndVerificationShareDeadline(t *testing.T) {
	m, c, s, now, id, _ := portalCallerManager(t)
	state := upstreamLoopState{lostCount: 7, scanCounter: 99}
	m.runConnectivityTick(time.Time{}, &state)
	require.Equal(t, portalHold, m.portalLoginDisposition(id.generation))
	require.Zero(t, state.lostCount)
	c.AssertNotCalled(t, "EnsureRadiosEnabled")
	c.AssertNotCalled(t, "CleanupStaleSTAs")
	s.AssertNotCalled(t, "ScanAllRadios")
	*now = now.Add(119 * time.Second)
	m.verifyPostSwitchConnectivity(id.ssid, "02:00:00:00:00:01", id.generation)
	require.Empty(t, m.blacklist)
	*now = now.Add(time.Second)
	require.Equal(t, portalFallback, m.portalLoginDisposition(id.generation))
	// No saved alternative: expiry must retain the connection, even in reseller mode.
	c.On("EnsureRadiosEnabled").Return(nil)
	c.On("CleanupStaleSTAs").Return(nil)
	c.On("GetSTASections").Return([]STASection{}, nil)
	s.On("ScanAllRadios").Return([]NetworkInfo{{SSID: "!TollGate-AB12", Signal: -10}}, nil)
	m.runConnectivityTick(time.Time{}, &state)
	c.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	c.AssertNotCalled(t, "FindOrCreateSTAForSSID", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	require.Equal(t, portalFallback, m.portalLoginDisposition(id.generation))
	c.AssertNotCalled(t, "CleanupStaleSTAs")
}

func TestPortalLoginExpiryUsesSavedBackupInResellerMode(t *testing.T) {
	m, c, s, now, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	*now = now.Add(2 * time.Minute)
	s.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "Unknown", Signal: -10}, {SSID: "!TollGate-AB12", Signal: -20},
		{SSID: "SavedBackup", Signal: -80, BSSID: "02:00:00:00:00:02"},
	}, nil)
	c.On("GetSTASections").Return([]STASection{{Name: "backup", SSID: "SavedBackup", Disabled: true}}, nil)
	// Stop before post-switch asynchronous verification; selection is the contract.
	c.On("SwitchUpstream", id.iface, "backup", "SavedBackup", "02:00:00:00:00:02").Return(errors.New("fixture stops after selection"))
	m.runScanCycleGeneration(id.generation, id.iface, id.ssid, "", -45, portalExpiredReason, true)
	c.AssertCalled(t, "SwitchUpstream", id.iface, "backup", "SavedBackup", "02:00:00:00:00:02")
	c.AssertNotCalled(t, "FindOrCreateSTAForSSID", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPortalLoginOwnerGuards(t *testing.T) {
	m, c, s, now, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	require.False(t, m.tryOperation(func() { t.Error("maintenance entered portal window") }))
	m.maintainAutomatically(func() { t.Error("startup maintenance entered portal window") })
	_, err := m.switchAutomatically(id.generation, id.iface, &Candidate{})
	require.Error(t, err)
	m.runScanCycleGeneration(id.generation, id.iface, id.ssid, "", -45, "scheduled", true)
	*now = now.Add(2 * time.Minute)
	m.cooldownUntil = time.Now().Add(time.Hour)
	m.runScanCycleGeneration(id.generation, id.iface, id.ssid, "", -45, portalExpiredReason, true)
	m.cooldownUntil = time.Time{}
	m.PauseConnectivityChecks(time.Hour)
	m.runScanCycleGeneration(id.generation, id.iface, id.ssid, "", -45, portalExpiredReason, true)
	require.Equal(t, portalFallback, m.portalLoginDisposition(id.generation))
	c.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "ScanAllRadios")
}

func TestPortalLoginStartupGraceDoesNotRenewWindow(t *testing.T) {
	m, c, s, now, id, _ := portalCallerManager(t)
	m.startupConnectivityCheck()
	graceEnd := now.Add(3 * time.Minute)
	*now = now.Add(2 * time.Minute)
	c.On("EnsureRadiosEnabled").Return(nil)
	m.runConnectivityTick(graceEnd, &upstreamLoopState{})
	s.AssertNotCalled(t, "ScanAllRadios")
	require.Equal(t, portalFallback, m.portalLoginDisposition(id.generation))
}

func TestPortalLoginTypedStartupPreservesLegacyHookPrecedence(t *testing.T) {
	m, _, _, _, _, _ := portalCallerManager(t)
	m.connectivityCheckFn = func() bool { return true }
	require.True(t, m.checkConnectivityForStartup("wlan0"))
	require.True(t, m.probeConnectivityForStartup("wlan0").OK)
}

func TestPortalLoginVerificationObservesEveryResult(t *testing.T) {
	m, _, _, now, id, _ := portalCallerManager(t)
	// Production polling callback observes the first captive result, then exits
	// without turning it into a failed/blacklisted uplink.
	m.verifyPostSwitchConnectivity(id.ssid, "", id.generation)
	require.Equal(t, portalHold, m.portalLoginDisposition(id.generation))
	*now = now.Add(time.Second)
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{Reason: "no-dns"} }
	m.verifyPostSwitchConnectivity(id.ssid, "", id.generation)
	require.Empty(t, m.blacklist)
	require.Equal(t, portalHold, m.portalLoginDisposition(id.generation))
}

func TestPortalLoginStaleVerificationBarrier(t *testing.T) {
	m, c, _, _, id, _ := portalCallerManager(t)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.connectivityProbeFn = func() ConnectivityResult { close(entered); <-release; return ConnectivityResult{Reason: "captive"} }
	go func() { m.verifyPostSwitchConnectivity(id.ssid, "", id.generation); close(done) }()
	<-entered
	writer := m.BeginManualWriter()
	newer := id
	newer.generation = m.connectionGeneration.Load()
	newer.bssid = "02:00:00:00:00:02"
	writer.End()
	m.observePortalLogin(newer, true, ConnectivityResult{Reason: "captive"})
	close(release)
	<-done
	require.Empty(t, m.blacklist)
	require.Equal(t, portalHold, m.portalLoginDisposition(newer.generation))
	c.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPortalLoginAssociationLossResumesRecovery(t *testing.T) {
	m, c, s, _, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	m.runCommandFn = func(name string, args ...string) ([]byte, error) {
		if name == "iwinfo" {
			return []byte("Not associated"), nil
		}
		t.Fatalf("unexpected %s %s", name, strings.Join(args, " "))
		return nil, nil
	}
	c.On("EnsureRadiosEnabled").Return(nil)
	c.On("CleanupStaleSTAs").Return(nil)
	c.On("GetSTASections").Return([]STASection{}, nil)
	s.On("ScanAllRadios").Return([]NetworkInfo{}, nil)
	m.runConnectivityTick(time.Time{}, &upstreamLoopState{})
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	s.AssertCalled(t, "ScanAllRadios")
}

func TestPortalLoginOnlineTickClearsLossBookkeeping(t *testing.T) {
	m, c, s, _, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }
	c.On("EnsureRadiosEnabled").Return(nil)
	state := upstreamLoopState{lostCount: 8}
	m.runConnectivityTick(time.Time{}, &state)
	require.Zero(t, state.lostCount)
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	s.AssertNotCalled(t, "ScanAllRadios")
}

func TestPortalLoginInitialHardFailureUsesNormalRecovery(t *testing.T) {
	m, c, s, _, id, _ := portalCallerManager(t)
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{Reason: "no-dns"} }
	c.On("EnsureRadiosEnabled").Return(nil)
	c.On("CleanupStaleSTAs").Return(nil)
	c.On("GetSTASections").Return([]STASection{}, nil)
	s.On("ScanAllRadios").Return([]NetworkInfo{}, nil)
	state := upstreamLoopState{}
	for i := 0; i < m.config.LostThreshold; i++ {
		m.runConnectivityTick(time.Time{}, &state)
	}
	s.AssertCalled(t, "ScanAllRadios")
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
}

func TestPortalLoginNoActiveSTAReleasesHold(t *testing.T) {
	m, c, s, _, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	c.ExpectedCalls = nil
	c.On("GetActiveSTA").Maybe().Return(nil, nil)
	c.On("EnsureRadiosEnabled").Return(nil)
	c.On("CleanupStaleSTAs").Return(nil)
	c.On("GetSTASections").Return([]STASection{}, nil)
	s.On("ScanAllRadios").Return([]NetworkInfo{}, nil)
	m.runConnectivityTick(time.Time{}, &upstreamLoopState{})
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	s.AssertCalled(t, "ScanAllRadios")
}

func TestPortalLoginSavedOnlyRequestSurvivesOnlineDuringScan(t *testing.T) {
	m, c, s, now, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	*now = now.Add(2 * time.Minute)
	s.On("ScanAllRadios").Run(func(mock.Arguments) {
		m.observePortalLogin(id, true, ConnectivityResult{OK: true, Reason: "ok"})
	}).Return([]NetworkInfo{{SSID: "!TollGate-AB12", Signal: -10}, {SSID: "Saved", Signal: -80}}, nil)
	c.On("GetSTASections").Return([]STASection{{Name: "saved", SSID: "Saved", Disabled: true}}, nil)
	c.On("SwitchUpstream", id.iface, "saved", "Saved", "").Return(errors.New("fixture stops after selection"))
	m.runScanCycleGeneration(id.generation, id.iface, id.ssid, "", -45, portalExpiredReason, true)
	c.AssertNotCalled(t, "FindOrCreateSTAForSSID", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	c.AssertCalled(t, "SwitchUpstream", id.iface, "saved", "Saved", "")
}

func TestPortalLoginObservationSharesWriterAdmission(t *testing.T) {
	m, c, _, _, id, _ := portalCallerManager(t)
	c.On("GetSTASections").Run(func(mock.Arguments) {
		// Candidate construction owns the writer. A concurrent observation must
		// defer, rather than announce a hold during an admitted mutation.
		require.Equal(t, portalDeferred, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	}).Return([]STASection{}, nil)
	_, _ = m.findCandidatesForGeneration(id.generation, nil, true, false)
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
}

func TestPortalLoginGraceContinuesProbesAndAcceptsSuccess(t *testing.T) {
	m, c, s, now, id, _ := portalCallerManager(t)
	m.startupConnectivityCheck()
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }
	c.On("EnsureRadiosEnabled").Return(nil)
	state := upstreamLoopState{lostCount: 8, scanCounter: 99}
	m.runConnectivityTick(now.Add(time.Minute), &state)
	require.Zero(t, state.lostCount)
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	s.AssertNotCalled(t, "ScanAllRadios")
}

func TestPortalLoginRecognizedTollGateKeepsExemption(t *testing.T) {
	m, _, _, _, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	m.tollGateProbeFn = func(string) bool { return true }
	m.connectivityProbeFn = func() ConnectivityResult {
		t.Fatal("recognized gateway must keep existing internet-probe exemption")
		return ConnectivityResult{}
	}
	m.verifyPostSwitchConnectivity(id.ssid, "", id.generation)
	require.True(t, m.isTollGateConnection.Load())
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	require.Empty(t, m.blacklist)
}

func TestPortalLoginManualStopPeeringRemainsAuthoritative(t *testing.T) {
	m, c, s, _, id, _ := portalCallerManager(t)
	m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"})
	c.On("GetSTASections").Return([]STASection{{Name: "backup", SSID: "Saved", Disabled: true}}, nil)
	s.On("ScanAllRadios").Return([]NetworkInfo{{SSID: "Saved", BSSID: "02:00:00:00:00:02", Signal: -70}}, nil)
	c.On("SwitchUpstream", id.iface, "backup", "Saved", "02:00:00:00:00:02").Return(nil)
	// Poll-tolerant stub for the post-switch verifier (#1243): a plain
	// On(...).Return matches unlimited calls; nil makes the verifier exit
	// benignly instead of panicking an unstubbed strict mock.
	c.On("GetActiveSTA").Maybe().Return(nil, nil)
	m.fallbackVerifyFn = func() bool { return true }
	ssid, err := m.FallbackToKnownGoodNonTollGate()
	require.NoError(t, err)
	require.Equal(t, "Saved", ssid)
}

func TestPortalLoginDeferredVerificationRetainsTypedEvidence(t *testing.T) {
	m, _, _, _, id, _ := portalCallerManager(t)
	m.connectivityProbeFn = func() ConnectivityResult {
		t.Fatal("publication must reuse its existing result")
		return ConnectivityResult{}
	}
	m.publishInternetVerification(id, true, ConnectivityResult{Reason: "captive"}, portalDeferred)
	require.Equal(t, portalHold, m.portalLoginDisposition(id.generation))
	require.Empty(t, m.blacklist)
}
