package cli

import (
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// serializingConnector records every radio mutation performed by two real
// manual connect handlers and parks the first handler's switch exactly
// like the real DHCP wait does.
type serializingConnector struct {
	*MockCLIConnector
	mu      sync.Mutex
	mutator []string
	parked  chan struct{}
	release chan struct{}
	events  chan string
}

func (c *serializingConnector) record(step string) {
	c.mu.Lock()
	c.mutator = append(c.mutator, step)
	c.mu.Unlock()
	c.events <- step
}

func (c *serializingConnector) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.mutator...)
}

func (c *serializingConnector) EnsureRadiosEnabled() error {
	c.record("enable")
	return c.MockCLIConnector.EnsureRadiosEnabled()
}

func (c *serializingConnector) FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio string) (string, error) {
	c.record("create:" + ssid)
	return c.MockCLIConnector.FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio)
}

func (c *serializingConnector) SwitchUpstream(active, candidate, ssid, bssid string) error {
	c.record("switch:" + ssid)
	if ssid == "First" {
		close(c.parked)
		<-c.release // park inside the DHCP wait
	}
	return c.MockCLIConnector.SwitchUpstream(active, candidate, ssid, bssid)
}

// TestTwoRealManualConnectHandlersSerialize is the handler-level writer
// fence: two concurrent real manual connects must serialize on the
// manager's exclusive writer lease. The second handler may not touch the
// radio (enable, create, switch) until the first handler — parked in its
// DHCP wait — has fully finished. A regression that removes the lease (or
// lets a handler mutate without holding it) interleaves the recordings
// and fails this test.
func TestTwoRealManualConnectHandlersSerialize(t *testing.T) {
	inner := &MockCLIConnector{}
	inner.On("EnsureRadiosEnabled").Return(nil)
	inner.On("EnsureWWANSetup").Return(nil)
	inner.On("GetActiveSTA").Return(nil, nil)
	inner.On("GetSTASections").Return([]wireless_gateway_manager.STASection{}, nil)
	inner.On("FindOrCreateSTAForSSID", "First", "", "none", "radio0").Return("upstream_first", nil)
	inner.On("FindOrCreateSTAForSSID", "Second", "", "none", "radio0").Return("upstream_second", nil)
	inner.On("SwitchUpstream", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	networks := []wireless_gateway_manager.NetworkInfo{
		{SSID: "First", Signal: -50, Radio: "radio0", Encryption: "none"},
		{SSID: "Second", Signal: -55, Radio: "radio0", Encryption: "none"},
	}
	scanner := &MockCLIScanner{}
	scanner.On("ScanAllRadios").Return(networks, nil)
	scanner.On("FindBestRadioForSSID", "First", networks).Return("radio0", nil)
	scanner.On("FindBestRadioForSSID", "Second", networks).Return("radio0", nil)
	scanner.On("DetectEncryption", "none").Return("none")

	connector := &serializingConnector{
		MockCLIConnector: inner,
		parked:           make(chan struct{}),
		release:          make(chan struct{}),
		events:           make(chan string, 16),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(connector.release) }) }
	t.Cleanup(release)
	um := wireless_gateway_manager.NewUpstreamManager(connector, scanner, nil, wireless_gateway_manager.DefaultUpstreamManagerConfig())
	s := &CLIServer{
		connector:       connector,
		scanner:         scanner,
		upstreamManager: um,
		startTime:       time.Now(),
	}

	firstDone := make(chan CLIResponse, 1)
	go func() {
		firstDone <- s.handleUpstreamConnect([]string{"First"})
	}()

	select {
	case <-connector.parked:
	case <-time.After(10 * time.Second):
		t.Fatal("first manual connect never reached its SwitchUpstream (DHCP wait)")
	}
	for _, step := range []string{"enable", "create:First", "switch:First"} {
		require.Equal(t, step, <-connector.events)
	}

	// While the first handler parks holding the writer lease, the second
	// real handler must not mutate the radio at all: a bounded negative
	// observation across plenty of scheduling opportunities.
	secondDone := make(chan CLIResponse, 1)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		secondDone <- s.handleUpstreamConnect([]string{"Second"})
	}()
	<-secondStarted
	select {
	case step := <-connector.events:
		t.Errorf("second handler mutated the radio during the first handler's lease: %s", step)
	case <-time.After(50 * time.Millisecond):
	}
	require.Equal(t, []string{"enable", "create:First", "switch:First"}, connector.recorded(),
		"the second manual connect mutated the radio while the first handler owned it")

	// Releasing the DHCP wait lets the first handler finish; only after
	// that may the second handler take the radio.
	release()
	select {
	case resp := <-firstDone:
		require.True(t, resp.Success)
	case <-time.After(10 * time.Second):
		t.Fatal("first manual connect did not complete after the DHCP wait was released")
	}

	select {
	case resp := <-secondDone:
		require.True(t, resp.Success)
	case <-time.After(10 * time.Second):
		t.Fatal("second manual connect did not resume after the first handler finished")
	}

	// Serialization is total: every mutation of the first handler precedes
	// every mutation of the second — no interleaving anywhere.
	require.Equal(t,
		[]string{"enable", "create:First", "switch:First", "enable", "create:Second", "switch:Second"},
		connector.recorded(),
		"the two manual connects interleaved radio mutations")
}
