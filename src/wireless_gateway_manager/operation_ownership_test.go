package wireless_gateway_manager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// These tests fence the explicit ownership contract: manual writers hold
// an exclusive, handler-scoped lease (BeginManualWriter) backed by an
// ordinary mutex; automatic paths decline nonblocking when the lease is
// busy; nested pause scopes stay paused until all end but grant no
// exclusivity; and the tick loop keeps making progress — counted through
// LoopTicks, a read-only signal, never through a mutating connector call —
// while a manual join parks in its DHCP wait.
//
// A mutation that lets the cross-goroutine switch through fails the busy
// test; a mutation that blocks the tick behind a held writer stalls the
// progress test and the cancellation fence in radio_writer_test.go;
// a mutation that treats a pause-only scope as exclusive fails the
// pause-scope test.

func TestManualWriterStopsAutomaticSwitch(t *testing.T) {
	connector := &MockConnector{}
	um := NewUpstreamManager(connector, &MockScanner{}, nil, DefaultUpstreamManagerConfig())
	writer := um.BeginManualWriter()

	// While the manual writer is held, the manager's switch path must
	// decline without waiting and without writing the radio: a busy
	// writer lease is a declined opportunity (#880), never a queue — the
	// tick loop must not stall behind a manual join parked in its DHCP
	// wait. The generation below is current, so only the held lease can
	// justify declining.
	done := make(chan error, 1)
	go func() {
		_, err := um.switchAutomatically(um.connectionGeneration.Load(), "active_sta", &Candidate{
			SSID:      "Net",
			BSSID:     "aa:bb:cc:dd:ee:ff",
			Radio:     "radio0",
			IfaceName: "upstream_net",
		})
		done <- err
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, errOperationBusy,
			"the switch path must decline while a manual writer owns the radio")
	case <-time.After(5 * time.Second):
		t.Fatal("switch path blocked behind a held manual writer (#880 regression: ticks must decline, not queue)")
	}
	connector.AssertNotCalled(t, "SwitchUpstream", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	writer.End()

	// With the lease free again, a switch carrying a stale generation must
	// decline as superseded instead of publishing a stale switch.
	staleGeneration := um.connectionGeneration.Load()
	um.connectionGeneration.Add(1)
	_, err := um.switchAutomatically(staleGeneration, "active_sta", &Candidate{
		SSID:      "Net",
		BSSID:     "aa:bb:cc:dd:ee:ff",
		Radio:     "radio0",
		IfaceName: "upstream_net",
	})
	require.ErrorIs(t, err, errSupersededOperation)
}

func TestPauseScopesNestWithoutGrantingExclusivity(t *testing.T) {
	um := NewUpstreamManager(&MockConnector{}, &MockScanner{}, nil, DefaultUpstreamManagerConfig())

	// Nested pause scopes stay paused until every scope ends.
	um.BeginManualOperation()
	um.BeginManualOperation()
	require.True(t, um.isPaused())
	um.EndManualOperation()
	require.True(t, um.isPaused(), "the manager must stay paused until every nested scope ends")
	um.EndManualOperation()
	require.False(t, um.isPaused())

	// A pause-only scope is NOT exclusive radio ownership: a manual writer
	// can still take the radio — only a ManualWriter lease serializes
	// writers — so a pause scope must never be advertised as exclusive.
	um.BeginManualOperation()
	require.True(t, um.isPaused())
	acquired := make(chan struct{})
	go func() {
		w := um.BeginManualWriter()
		close(acquired)
		w.End()
	}()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("a pause-only scope was treated as exclusive radio ownership")
	}
	um.EndManualOperation()
	require.False(t, um.isPaused())
}

// heldTickConnector counts EnsureRadiosEnabled calls (a real radio
// mutation in the connector: UCI commit + `wifi up`) while keeping the
// world switch-free: no active STA and no known candidates mean every
// unpaused tick is a declined opportunity, deterministically.
type heldTickConnector struct {
	*MockConnector
	mutations atomic.Int32
}

func (c *heldTickConnector) EnsureRadiosEnabled() error {
	c.mutations.Add(1)
	return c.MockConnector.EnsureRadiosEnabled()
}

func TestTickProgressesAndDeclinesWhileManualWriterHeld(t *testing.T) {
	inner := &MockConnector{}
	inner.On("EnsureRadiosEnabled").Return(nil)
	inner.On("EnsureWWANSetup").Return(nil)
	inner.On("CleanupStaleSTAs").Return(nil)
	inner.On("GetActiveSTA").Return(nil, errors.New("no active STA"))
	inner.On("GetSTASections").Return([]STASection{}, nil)
	connector := &heldTickConnector{MockConnector: inner}

	scanner := &MockScanner{}
	scanner.On("ScanAllRadios").Return([]NetworkInfo{}, nil).Maybe()

	cfg := DefaultUpstreamManagerConfig()
	cfg.FastCheck = 2 * time.Millisecond
	cfg.ScanInterval = 10 * time.Second
	cfg.StartupGracePeriod = time.Millisecond
	cfg.StartupSettle = time.Millisecond
	cfg.StartupRetryInterval = time.Millisecond
	cfg.StartupScanInterval = time.Millisecond
	um := NewUpstreamManager(connector, scanner, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		um.Start(ctx)
		close(done)
	}()

	require.Eventually(t, func() bool { return connector.mutations.Load() >= 3 },
		10*time.Second, time.Millisecond, "manager loop must be running its per-tick housekeeping")

	// A manual join parks inside its DHCP wait holding the writer lease.
	// The loop must keep iterating (LoopTicks advances: progress is the
	// loop itself, not a mutating call), while the radio-enabling mutator
	// is never invoked (exclusion) — busy means decline, never queue.
	writer := um.BeginManualWriter()
	mutationsAtBegin := connector.mutations.Load()
	loopsAtBegin := um.LoopTicks()
	require.Eventually(t, func() bool { return um.LoopTicks() >= loopsAtBegin+5 },
		10*time.Second, time.Millisecond,
		"loop progress stalled behind a held manual writer (#880 regression: the busy lease must be declined, not queued)")
	require.Equal(t, mutationsAtBegin, connector.mutations.Load(),
		"the manager invoked the radio-enabling mutator while a manual writer owned the radio")

	writer.End()

	// Positive progress barrier: with the lease released and the pause
	// scope closed, the manager resumes its per-tick housekeeping.
	require.Eventually(t, func() bool { return connector.mutations.Load() > mutationsAtBegin },
		10*time.Second, time.Millisecond,
		"the manager did not resume radio housekeeping after the manual writer ended")

	cancel()
	<-done
}
