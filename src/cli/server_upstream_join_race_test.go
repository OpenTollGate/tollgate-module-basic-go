package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// This test reproduces the #880 handler-side race end to end: a manual
// `tollgate upstream connect` parks inside SwitchUpstream's DHCP wait (the
// mock blocks on a channel) while the real UpstreamManager — wired to the
// same connector — keeps fast-checking. The manager must observe several
// ticks of the mid-join radio without counter-switching to the disabled
// section the connect flow just swept (#848).
//
// Determinism: the manual SwitchUpstream blocks on a channel (not a timer),
// ticks are counted through EnsureRadiosEnabled, association is modeled with
// an empty netdev (no exec), and all waits are require.Eventually.

const (
	phaseBoot      = iota // startup check: no active STA yet
	phasePreManual        // tollgate_uplink still enabled (pre-sweep world)
	phaseMidJoin          // upstream_2 active mid-join, tollgate_uplink swept
)

// joinBlockingConnector delegates to the shared CLI mocks and adds the
// phase-dependent world plus a SwitchUpstream that records, flips the world
// to mid-join, then parks until the test releases it (the DHCP wait).
type joinBlockingConnector struct {
	*MockCLIConnector
	mu        sync.Mutex
	phase     atomic.Int32
	ticks     atomic.Int32
	switches  []string
	entered   chan struct{}
	enterOnce sync.Once
	release   chan struct{}
}

func (c *joinBlockingConnector) setPhase(p int32) { c.phase.Store(p) }

func (c *joinBlockingConnector) currentPhase() int32 { return c.phase.Load() }

func (c *joinBlockingConnector) EnsureRadiosEnabled() error {
	c.ticks.Add(1)
	return c.MockCLIConnector.EnsureRadiosEnabled()
}

func (c *joinBlockingConnector) GetActiveSTA() (*wireless_gateway_manager.STASection, error) {
	switch c.currentPhase() {
	case phaseBoot:
		return nil, errors.New("daemon still booting")
	case phasePreManual:
		return &wireless_gateway_manager.STASection{Name: "tollgate_uplink", SSID: "foreign", Device: ""}, nil
	default:
		return &wireless_gateway_manager.STASection{Name: "upstream_2", SSID: "2", Device: ""}, nil
	}
}

func (c *joinBlockingConnector) GetSTASections() ([]wireless_gateway_manager.STASection, error) {
	if c.currentPhase() == phaseMidJoin {
		// Post-sweep world (#848): the stale foreign section is disabled —
		// and therefore pooled by the manager's known-candidate finder.
		return []wireless_gateway_manager.STASection{
			{Name: "tollgate_uplink", SSID: "foreign", Device: "radio0", Disabled: true},
			{Name: "upstream_2", SSID: "2", Device: "radio0", Disabled: false},
		}, nil
	}
	return []wireless_gateway_manager.STASection{
		{Name: "tollgate_uplink", SSID: "foreign", Device: "radio0", Disabled: false},
	}, nil
}

func (c *joinBlockingConnector) SwitchUpstream(active, candidate, ssid string) error {
	c.mu.Lock()
	c.switches = append(c.switches, active+" -> "+candidate+" ("+ssid+")")
	c.mu.Unlock()

	c.setPhase(phaseMidJoin)
	c.enterOnce.Do(func() { close(c.entered) })
	<-c.release // park inside the DHCP wait
	return nil
}

func (c *joinBlockingConnector) recordedSwitches() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.switches...)
}

func (c *joinBlockingConnector) switchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.switches)
}

// TestHandleUpstreamConnectStreaming_PausesManagerBeforeSwitch is the #880
// fence at the handler level: the auto-manager must be paused BEFORE the
// manual SwitchUpstream begins its DHCP wait, not after it returns. Pre-fix,
// the pause lands after the switch — the manager is live through the whole
// join window and counter-switches to the swept section within a few ticks.
func TestHandleUpstreamConnectStreaming_PausesManagerBeforeSwitch(t *testing.T) {
	inner := &MockCLIConnector{}
	inner.On("EnsureRadiosEnabled").Return(nil)
	inner.On("EnsureWWANSetup").Return(nil)
	inner.On("CleanupStaleSTAs").Return(nil)
	inner.On("GetSTANetdev", mock.Anything).Return("", nil)
	inner.On("FindOrCreateSTAForSSID", "2", "", "none", "radio0").Return("upstream_2", nil)

	scanner := &MockCLIScanner{}
	scanner.On("ScanAllRadios").Return([]wireless_gateway_manager.NetworkInfo{
		{SSID: "2", Signal: -48, Radio: "radio0", Encryption: "none"},
		{SSID: "foreign", Signal: -55, Radio: "radio0", Encryption: "none"},
	}, nil)
	scanner.On("FindBestRadioForSSID", "2", mock.Anything).Return("radio0", nil)
	scanner.On("DetectEncryption", "none").Return("none")

	connector := &joinBlockingConnector{
		MockCLIConnector: inner,
		entered:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	connector.setPhase(phaseBoot)

	cfg := wireless_gateway_manager.DefaultUpstreamManagerConfig()
	cfg.FastCheck = 2 * time.Millisecond
	cfg.ScanInterval = 10 * time.Second
	cfg.StartupGracePeriod = time.Millisecond
	cfg.StartupSettle = time.Millisecond
	cfg.StartupRetryInterval = time.Millisecond
	cfg.StartupScanInterval = time.Millisecond
	um := wireless_gateway_manager.NewUpstreamManager(connector, scanner, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	managerDone := make(chan struct{})
	go func() {
		um.Start(ctx)
		close(managerDone)
	}()

	// Startup check consumed the boot-phase GetActiveSTA; wait until the
	// loop is live before the manual connect enters the pre-sweep world.
	require.Eventually(t, func() bool { return connector.ticks.Load() >= 2 },
		10*time.Second, time.Millisecond, "manager loop must be ticking")
	connector.setPhase(phasePreManual)

	s := &CLIServer{
		connector:       connector,
		scanner:         scanner,
		upstreamManager: um,
		startTime:       time.Now(),
	}

	conn, drainConn := net.Pipe()
	go io.Copy(io.Discard, drainConn)
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		s.handleUpstreamConnectStreaming(conn, CLIMessage{
			Command: "upstream",
			Args:    []string{"connect", "2"},
		})
	}()

	select {
	case <-connector.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("manual connect never reached its SwitchUpstream (DHCP wait)")
	}

	// The join window. Post-fix the manager keeps ticking (each tick a
	// declined opportunity); pre-fix it counter-switches and parks inside
	// the recorded SwitchUpstream — either outcome ends this wait, and the
	// verdict below decides which one was correct.
	ticksAtEntry := connector.ticks.Load()
	require.Eventually(t, func() bool {
		return connector.ticks.Load() >= ticksAtEntry+5 || connector.switchCount() >= 2
	},
		10*time.Second, time.Millisecond,
		"manager neither ticked nor counter-switched inside the join window (harness stall)")

	// THE FENCE: no counter-switch may have happened while the manual
	// switch was parked. Pre-fix this is exactly where the manager drove
	// the radio back to tollgate_uplink.
	require.Equal(t, 1, connector.switchCount(),
		"#880 repro: the auto-manager switched the radio during the manual join: %v", connector.recordedSwitches())
	require.Equal(t, "tollgate_uplink -> upstream_2 (2)", connector.recordedSwitches()[0])

	close(connector.release)
	select {
	case <-handlerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not complete after the DHCP wait was released")
	}

	cancel()
	<-managerDone
	conn.Close()
	drainConn.Close()

	// The manual switch is still the only one after completion.
	require.Equal(t, []string{"tollgate_uplink -> upstream_2 (2)"}, connector.recordedSwitches(),
		"the auto-manager must stay out of the radio for the whole manual connect")
}
