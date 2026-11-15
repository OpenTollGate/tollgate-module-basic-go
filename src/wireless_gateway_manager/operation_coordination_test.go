package wireless_gateway_manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartupRejectsHealthFromBeforeManualJoin(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "nudged")
	if err := os.WriteFile(filepath.Join(dir, "ifup"), []byte("#!/bin/sh\n: >\"$TOLLGATE_UNIT_NUDGE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TOLLGATE_UNIT_NUDGE", marker)
	c, s := &MockConnector{}, &MockScanner{}
	c.On("GetActiveSTA").Return(&STASection{Name: "old", SSID: "Old", Device: "radio0"}, nil)
	c.On("GetSTANetdev", "old").Return("sta0", nil)
	s.On("ScanAllRadios").Return([]NetworkInfo{}, nil).Maybe()
	c.On("GetSTASections").Return([]STASection{}, nil).Maybe()
	cfg := DefaultUpstreamManagerConfig()
	cfg.StartupSettle = time.Nanosecond
	cfg.StartupRetryInterval = time.Nanosecond
	cfg.StartupScanInterval = time.Nanosecond
	m := NewUpstreamManager(c, s, nil, cfg)
	m.connectivityCheckFn = func() bool { m.BeginManualOperation(); m.EndManualOperation(); return false }
	m.startupConnectivityCheck()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("startup nudged WWAN using health from before a manual operation")
	}
	s.AssertNotCalled(t, "ScanAllRadios")
}

func TestManualConnectsCannotWriteConcurrently(t *testing.T) {
	m := NewUpstreamManager(&MockConnector{}, nil, nil, DefaultUpstreamManagerConfig())
	writer := m.BeginManualWriter()
	started, entered, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		second := m.BeginManualWriter()
		close(entered)
		second.End()
		close(done)
	}()
	<-started
	select {
	case <-entered:
		writer.End()
		<-done
		t.Fatal("two manual writers acquired the radio concurrently")
	case <-time.After(20 * time.Millisecond):
	}
	writer.End()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queued manual writer did not resume")
	}
}

func TestStaleVerificationCannotBlacklistAfterManualJoin(t *testing.T) {
	m := NewUpstreamManager(&MockConnector{}, nil, nil, DefaultUpstreamManagerConfig())
	generation := m.connectionGeneration.Load()
	m.BeginManualOperation()
	m.EndManualOperation()
	applied := false
	if m.applyVerification(generation, func() { applied = true }) || applied {
		t.Fatal("verification of an older connection was published after a manual join")
	}
}

func TestAutomaticBookkeepingDeclinesWhileManualWriterHeld(t *testing.T) {
	m := NewUpstreamManager(&MockConnector{}, nil, nil, DefaultUpstreamManagerConfig())
	writer := m.BeginManualWriter()
	defer writer.End()
	done := make(chan bool, 1)
	go func() {
		done <- m.applyAutomaticResult(m.connectionGeneration.Load(), func() { t.Error("automatic bookkeeping entered manual window") })
	}()
	select {
	case applied := <-done:
		if applied {
			t.Error("busy automatic bookkeeping was applied")
		}
	case <-time.After(time.Second):
		t.Fatal("automatic bookkeeping queued behind manual writer")
	}
}
