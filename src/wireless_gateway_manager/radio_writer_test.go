package wireless_gateway_manager

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// EnsureRadiosEnabled is a mutator: the real connector can commit wireless
// configuration and run wifi up. It cannot also be a harmless tick heartbeat.
type radioOwnerConnector struct {
	*MockConnector
	manualWindow atomic.Bool
	calls        atomic.Int32
	violations   chan struct{}
}

func (c *radioOwnerConnector) EnsureRadiosEnabled() error {
	if c.manualWindow.Load() {
		select {
		case c.violations <- struct{}{}:
		default:
		}
	}
	c.calls.Add(1)
	return c.MockConnector.EnsureRadiosEnabled()
}

func TestManagerDoesNotEnableRadiosDuringManualWriter(t *testing.T) {
	inner := &MockConnector{}
	inner.On("EnsureRadiosEnabled").Return(nil)
	inner.On("EnsureWWANSetup").Return(nil)
	inner.On("CleanupStaleSTAs").Return(nil)
	inner.On("GetActiveSTA").Return(nil, errors.New("no active STA"))
	inner.On("GetSTASections").Return([]STASection{}, nil)
	c := &radioOwnerConnector{MockConnector: inner, violations: make(chan struct{}, 1)}
	scanner := &MockScanner{}
	scanner.On("ScanAllRadios").Return([]NetworkInfo{}, nil).Maybe()
	cfg := DefaultUpstreamManagerConfig()
	cfg.FastCheck = 2 * time.Millisecond
	cfg.ScanInterval = 10 * time.Second
	cfg.StartupGracePeriod = time.Millisecond
	cfg.StartupSettle = time.Millisecond
	cfg.StartupRetryInterval = time.Millisecond
	cfg.StartupScanInterval = time.Millisecond
	um := NewUpstreamManager(c, scanner, nil, cfg)
	um.DiscoveryLog = NewDiscoveryLogger(filepath.Join(t.TempDir(), "discovery.jsonl"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); um.Start(ctx) }()
	require.Eventually(t, func() bool { return c.calls.Load() >= 3 }, time.Second, time.Millisecond)

	// The manual writer holds an exclusive radio-mutation lease
	// (BeginManualWriter): every automatic manager path, including the
	// per-tick radio re-enable, must decline — not queue — while it is
	// held.
	writer := um.BeginManualWriter()
	defer writer.End()
	c.manualWindow.Store(true)
	// A bounded negative observation covers many fast-check opportunities.
	// Cancellation below separately proves the loop did not queue behind
	// the manual writer, without requiring another mutating connector call.
	select {
	case <-c.violations:
		t.Error("manager called the radio-enabling mutator while the manual writer owned the radio")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("manager cancellation blocked behind the manual writer")
	}
}
