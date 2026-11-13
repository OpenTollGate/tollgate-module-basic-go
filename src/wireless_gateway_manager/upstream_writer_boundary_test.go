package wireless_gateway_manager

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
)

// Park at the connector boundary, after the automatic decision's last pause
// check and generation reservation, but before its simulated radio write.
// A manual writer requested here must wait for that automatic operation to
// finish; a pause flag alone lets the two mutation windows overlap.
func TestAutomaticSwitchAndManualWriterBoundary(t *testing.T) {
	c, scanner := &MockConnector{}, &MockScanner{}
	um := NewUpstreamManager(c, scanner, nil, DefaultUpstreamManagerConfig())
	um.DiscoveryLog = NewDiscoveryLogger(filepath.Join(t.TempDir(), "discovery.jsonl"))
	scanner.On("ScanAllRadios").Return([]NetworkInfo{{SSID: "SavedBackup", Signal: -70, Radio: "radio1"}}, nil)
	c.On("GetSTASections").Return([]STASection{{Name: "backup", SSID: "SavedBackup", Disabled: true}}, nil)
	autoEntered, autoDone := make(chan struct{}), make(chan struct{})
	releaseAuto, manualAdmitted, releaseManual, manualDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var autoOnce, manualOnce sync.Once
	releaseA := func() { autoOnce.Do(func() { close(releaseAuto) }) }
	releaseM := func() { manualOnce.Do(func() { close(releaseManual) }) }
	t.Cleanup(releaseA)
	t.Cleanup(releaseM)
	var manualWindow, overlapped atomic.Bool
	c.On("SwitchUpstream", "current", "backup", "SavedBackup", "").Run(func(mock.Arguments) {
		close(autoEntered)
		<-releaseAuto
		overlapped.Store(manualWindow.Load())
	}).Return(errors.New("synthetic stop after radio boundary"))
	go func() {
		defer close(autoDone)
		um.runScanCycle("current", "UnavailableInternet", "", -45, "emergency", false)
	}()
	select {
	case <-autoEntered:
	case <-time.After(time.Second):
		t.Fatal("automatic switch did not reach connector boundary")
	}
	manualRequested := make(chan struct{})
	go func() {
		defer close(manualDone)
		close(manualRequested)
		writer := um.BeginManualWriter()
		defer writer.End()
		manualWindow.Store(true)
		close(manualAdmitted)
		<-releaseManual
		manualWindow.Store(false)
	}()
	<-manualRequested
	select {
	case <-manualAdmitted:
		t.Error("manual writer was admitted inside the automatic switch mutation window")
	case <-time.After(50 * time.Millisecond):
	}
	releaseA()
	select {
	case <-manualAdmitted:
	case <-time.After(time.Second):
		t.Fatal("manual writer did not resume after automatic switch")
	}
	releaseM()
	select {
	case <-manualDone:
	case <-time.After(time.Second):
		t.Fatal("manual writer did not finish")
	}
	// Generation-fenced bookkeeping may wait for the just-admitted writer;
	// it need not finish before that independent manual operation returns.
	select {
	case <-autoDone:
	case <-time.After(time.Second):
		t.Fatal("automatic switch did not finish")
	}
	if overlapped.Load() {
		t.Error("automatic radio write overlapped the manual mutation window")
	}
	c.AssertExpectations(t)
	scanner.AssertExpectations(t)
}
