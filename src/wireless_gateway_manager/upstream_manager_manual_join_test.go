package wireless_gateway_manager

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests reproduce #880: the manager's fast-check tick can fire while a
// manual `tollgate upstream connect` is parked inside SwitchUpstream's DHCP
// wait. The mid-join radio reads as not-associated, and the manager's
// candidate finder pools *disabled* sections — exactly the section the manual
// flow's competitor sweep (#848) has just disabled — so an unguarded tick
// counter-switches to it and the join never completes.
//
// Determinism: ticks are counted through EnsureRadiosEnabled (called once per
// loop iteration), association is modeled with an empty netdev (isSTAAssociated
// short-circuits without exec), and every wait is event-driven via
// require.Eventually — no wall-clock sleeps guard any assertion.

// midJoinConnector wraps the shared mock to add the phase-dependent state the
// race needs: GetActiveSTA's first call is consumed by the startup check
// (returning "still booting" keeps it inert); every later call shows the
// mid-join world. SwitchUpstream is recorded instead of trusted to a
// mock.Expectation so both the paused and unpaused verdicts can assert on it.
type midJoinConnector struct {
	*MockConnector
	ticks          atomic.Int32
	getActiveCalls atomic.Int32

	mu       sync.Mutex
	switches []string
}

func (c *midJoinConnector) EnsureRadiosEnabled() error {
	c.ticks.Add(1)
	return c.MockConnector.EnsureRadiosEnabled()
}

func (c *midJoinConnector) GetActiveSTA() (*STASection, error) {
	if c.getActiveCalls.Add(1) == 1 {
		return nil, fmt.Errorf("daemon still booting")
	}
	return &STASection{Name: "upstream_2", SSID: "2", Device: ""}, nil
}

func (c *midJoinConnector) SwitchUpstream(active, candidate, ssid, bssid string) error {
	c.mu.Lock()
	c.switches = append(c.switches, active+" -> "+candidate+" ("+ssid+")")
	c.mu.Unlock()
	return nil
}

func (c *midJoinConnector) recordedSwitches() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.switches...)
}

func (c *midJoinConnector) switchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.switches)
}

// newMidJoinFixture builds the #880 world: upstream_2 active but
// not-associated (empty netdev), tollgate_uplink the disabled known candidate.
func newMidJoinFixture(t *testing.T) (*UpstreamManager, *midJoinConnector) {
	t.Helper()

	inner := &MockConnector{}
	inner.On("EnsureRadiosEnabled").Return(nil)
	inner.On("EnsureWWANSetup").Return(nil)
	inner.On("CleanupStaleSTAs").Return(nil)
	inner.On("GetSTANetdev", "upstream_2").Return("", nil)
	inner.On("GetSTASections").Return([]STASection{
		{Name: "tollgate_uplink", SSID: "foreign", Device: "radio0", Disabled: true},
		{Name: "upstream_2", SSID: "2", Device: "radio0", Disabled: false},
	}, nil)

	scanner := &MockScanner{}
	scanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "foreign", Signal: -55, Radio: "radio0"},
		{SSID: "2", Signal: -48, Radio: "radio0"},
	}, nil).Maybe()

	connector := &midJoinConnector{MockConnector: inner}

	cfg := DefaultUpstreamManagerConfig()
	cfg.FastCheck = 2 * time.Millisecond
	cfg.ScanInterval = 10 * time.Second
	cfg.StartupGracePeriod = time.Millisecond
	cfg.StartupSettle = time.Millisecond
	cfg.StartupRetryInterval = time.Millisecond
	cfg.StartupScanInterval = time.Millisecond

	um := NewUpstreamManager(connector, scanner, nil, cfg)
	return um, connector
}

func startManager(t *testing.T, um *UpstreamManager) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		um.Start(ctx)
		close(done)
	}()
	return cancel, done
}

// TestUpstreamManagerTickUnpausedCounterSwitchesMidJoin pins the repro itself
// and the manager's recovery intent: with no pause in force, a tick that
// observes the not-associated mid-join radio scans and switches to the known
// disabled candidate. The fix must not take this away.
func TestUpstreamManagerTickUnpausedCounterSwitchesMidJoin(t *testing.T) {
	um, connector := newMidJoinFixture(t)
	cancel, done := startManager(t, um)
	defer cancel()

	require.Eventually(t, func() bool { return connector.switchCount() >= 1 },
		10*time.Second, time.Millisecond,
		"an unpaused manager must still recover a not-associated upstream (auto-manager intent)")

	switches := connector.recordedSwitches()
	require.Equal(t, "upstream_2 -> tollgate_uplink (foreign)", switches[0],
		"expected the #880 counter-switch shape: back to the section the manual flow just disabled")

	cancel()
	<-done
}

// TestUpstreamManagerTickPausedDoesNotCounterSwitchMidJoin is the #880 fence:
// while a manual connect holds the pause, the manager must keep ticking but
// must not scan, clean up, or switch — the connect flow and the manager must
// not both drive the radio. This test fails on the pre-fix code, where only
// the lost-connectivity escalation consulted the pause and the
// not-associated path switched straight through it.
func TestUpstreamManagerTickPausedDoesNotCounterSwitchMidJoin(t *testing.T) {
	um, connector := newMidJoinFixture(t)

	// The manual connect's pause, taken before its switch starts.
	um.PauseConnectivityChecks(30 * time.Second)

	cancel, done := startManager(t, um)
	defer cancel()

	require.Eventually(t, func() bool { return int(connector.ticks.Load()) >= 5 },
		10*time.Second, time.Millisecond,
		"the paused manager must keep observing ticks (each one a counter-switch opportunity pre-fix)")

	require.Zero(t, connector.switchCount(),
		"#880 repro: a paused manager counter-switched mid-join: %v", connector.recordedSwitches())

	cancel()
	<-done
}
