package wireless_gateway_manager

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func portalTestManager(t *testing.T) (*UpstreamManager, *time.Time, upstreamIdentity) {
	t.Helper()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	m := NewUpstreamManager(&MockConnector{}, &MockScanner{}, nil, UpstreamManagerConfig{})
	m.DiscoveryLog = nil
	m.now = func() time.Time { return now }
	return m, &now, upstreamIdentity{generation: m.connectionGeneration.Load(), iface: "venue", ssid: "Venue"}
}

func TestPortalLoginDefaults(t *testing.T) {
	require.Equal(t, 2*time.Minute, DefaultUpstreamManagerConfig().PortalLoginWindow)
	m := NewUpstreamManager(nil, nil, nil, UpstreamManagerConfig{})
	require.Equal(t, 2*time.Minute, m.config.PortalLoginWindow)
	m = NewUpstreamManager(nil, nil, nil, UpstreamManagerConfig{PortalLoginWindow: 17 * time.Second})
	require.Equal(t, 17*time.Second, m.config.PortalLoginWindow)
	m = NewUpstreamManager(nil, nil, nil, UpstreamManagerConfig{PortalLoginWindow: -time.Second})
	require.Equal(t, 2*time.Minute, m.config.PortalLoginWindow)
}

func TestPortalLoginFixedDeadlineAcrossObservations(t *testing.T) {
	m, now, id := portalTestManager(t)
	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	deadline := now.Add(2 * time.Minute)
	*now = deadline.Add(-time.Nanosecond)
	// Learning the BSSID and duplicate callbacks cannot buy another allowance.
	id.bssid = "02:00:00:00:00:01"
	for _, reason := range []string{"captive", "no-route", "no-dns", "no-http"} {
		require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: reason}), reason)
	}
	*now = deadline
	require.Equal(t, portalFallback, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	*now = deadline.Add(time.Hour)
	require.Equal(t, portalFallback, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	require.Equal(t, portalFallback, m.portalLoginDisposition(id.generation))
}

func TestPortalLoginRequiresEvidenceAndAssociation(t *testing.T) {
	for _, reason := range []string{"no-route", "no-dns", "no-http", "not-associated"} {
		t.Run(reason, func(t *testing.T) {
			m, _, id := portalTestManager(t)
			require.Equal(t, portalOrdinary, m.observePortalLogin(id, true, ConnectivityResult{Reason: reason}))
		})
	}
	m, now, id := portalTestManager(t)
	require.Equal(t, portalOrdinary, m.observePortalLogin(id, false, ConnectivityResult{Reason: "captive"}))
	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	*now = now.Add(time.Second)
	require.Equal(t, portalOrdinary, m.observePortalLogin(id, false, ConnectivityResult{Reason: "not-associated"}))
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	// Reassociation without a new attempt must not restart the allowance.
	require.Equal(t, portalOrdinary, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
}

func TestPortalLoginOnlineAndNewGeneration(t *testing.T) {
	m, now, id := portalTestManager(t)
	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	require.Equal(t, portalOrdinary, m.observePortalLogin(id, true, ConnectivityResult{OK: true, Reason: "ok"}))
	require.Equal(t, portalOrdinary, m.portalLoginDisposition(id.generation))
	// A later portal episode can follow genuinely verified Internet access.
	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	*now = now.Add(3 * time.Minute)
	require.Equal(t, portalFallback, m.portalLoginDisposition(id.generation))
	old := id
	id.generation = m.nextConnectionGeneration()
	id.bssid = "02:00:00:00:00:02"
	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	for _, result := range []ConnectivityResult{{OK: true}, {Reason: "captive"}, {Reason: "no-dns"}} {
		require.Equal(t, portalSuperseded, m.observePortalLogin(old, true, result))
		require.Equal(t, portalHold, m.portalLoginDisposition(id.generation))
	}
}

func TestPortalLoginRecognizedTollGateExempt(t *testing.T) {
	m, _, id := portalTestManager(t)
	m.isTollGateConnection.Store(true)
	m.tollGateGeneration.Store(id.generation)
	require.Equal(t, portalOrdinary, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	// Recognition for the previous generation must not exempt a new venue.
	id.generation = m.nextConnectionGeneration()
	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
}
