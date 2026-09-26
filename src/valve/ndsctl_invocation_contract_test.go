package valve

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// The ndsctl invocation contract (t_83e6ab0f, measured on the bench MT3000,
// pre17, 2026-09-26).
//
// `ndsctl` is the module's only way to take access away, and the module runs it
// under a deadline it owns (`ndsctlTimeout`), so the module is one of the two
// things that can end a child. The other is the environment (procd on a service
// restart, the OOM killer, an operator).
//
// The measured log could not tell them apart: 97 lines of
//
//	Error deauthorizing MAC address   error="signal: killed"
//
// on a box whose nodogsplash control socket had stopped answering. `signal:
// killed` is what Go prints for a child that died on a signal, so an invocation
// THE MODULE ITSELF killed after its own deadline — and one killed by whatever
// else owns the box — were logged identically, and every state machine reading
// those lines drew its own conclusion. A restart adds a second case: the module
// has no shutdown path at all (`Stop` is never called), so in-flight children
// are killed with the process and their failures are reported as ndsctl
// failures.
//
// The contract these tests pin:
//
//  1. an ndsctl invocation the MODULE ended is reported as such, naming what
//     happened, instead of as a bare `signal: killed`;
//  2. `Stop` DRAINS in-flight children before it returns, so a service restart
//     does not kill an invocation that was about to answer;
//  3. an invocation interrupted because the module is stopping is reported as a
//     shutdown, never as an ndsctl failure — and the module does not start new
//     children while it is stopping.
//
// The tests drive the REAL runner (`runNdsctl`'s production implementation) with
// a fake `ndsctl` binary on PATH: that is the only way to exercise the deadline,
// the kill and the drain, which is what the defect is about.

// captureValveLogAtLevel redirects the package logger into a buffer, so a test
// can assert what the operator would see at any level. (captureValveLog in
// gate_close_retry_test.go pins the ERROR level; these tests need to see the
// difference between an escalation and an informational shutdown line.)
func captureValveLogAtLevel(t *testing.T, level logrus.Level) *bytes.Buffer {
	t.Helper()

	buffer := &bytes.Buffer{}
	previousOut, previousLevel := logrus.StandardLogger().Out, logrus.GetLevel()
	logrus.SetOutput(buffer)
	logrus.SetLevel(level)
	t.Cleanup(func() {
		logrus.SetOutput(previousOut)
		logrus.SetLevel(previousLevel)
	})
	return buffer
}

// ndsctlFake is a fake `ndsctl` binary on PATH plus the files it touches, so a
// test can observe what the module actually did to the enforcement layer.
type ndsctlFake struct {
	dir      string
	spawnLog string
	doneLog  string
}

// spawns returns how many times the module started an ndsctl child.
func (f *ndsctlFake) spawns(t *testing.T) int {
	t.Helper()

	data, err := os.ReadFile(f.spawnLog)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read ndsctl spawn log: %v", err)
	}
	return len(strings.Fields(string(data)))
}

// finished reports whether the child that was started has run to completion.
func (f *ndsctlFake) finished() bool {
	_, err := os.Stat(f.doneLog)
	return err == nil
}

// installFakeNdsctl writes an `ndsctl` into a temp dir and puts it on PATH. The
// body is a shell script, so a test can make the child hang (a wedged control
// socket) or finish slowly (a restart landing mid-invocation).
//
// It also guarantees the package's ndsctl state is restored afterwards: the
// runner seam, the stop channel and the shutdown flag. Retry timers are stopped
// first, so a leaked retry can never reach the restored seam.
func installFakeNdsctl(t *testing.T, body string) *ndsctlFake {
	t.Helper()

	origRunNdsctl := runNdsctl
	origStopCh := stopCh

	fake := &ndsctlFake{
		dir:      t.TempDir(),
		spawnLog: filepath.Join(t.TempDir(), "spawns"),
		doneLog:  filepath.Join(t.TempDir(), "finished"),
	}

	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\n%s\n", fake.spawnLog, body)
	if err := os.WriteFile(filepath.Join(fake.dir, "ndsctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ndsctl: %v", err)
	}
	t.Setenv("PATH", fake.dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	t.Cleanup(func() {
		gatesMutex.Lock()
		for mac, timer := range pendingCloseRetries {
			timer.Stop()
			delete(pendingCloseRetries, mac)
		}
		gatesMutex.Unlock()

		runNdsctl = origRunNdsctl
		stopCh = origStopCh
	})

	return fake
}

// TestNdsctlInvocationTheModuleKilledIsAttributed: an invocation the module's own
// deadline ended must say so. Before this fix the error was the bare
// `signal: killed` Go prints for any child that died on a signal, so an operator
// could not tell the module's own timeout apart from a restart, an OOM kill or a
// real ndsctl failure — and the 97-line storm in the bench log was read as the
// latter.
func TestNdsctlInvocationTheModuleKilledIsAttributed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the ndsctl deadline test in short mode")
	}

	fake := installFakeNdsctl(t, "exec sleep 60")
	log := captureValveLogAtLevel(t, logrus.InfoLevel)

	macAddress := "aa:bb:cc:dd:ee:60"
	err := deauthorizeMAC(macAddress)
	if err == nil {
		t.Fatal("deauthorizeMAC reported success for an invocation that never answered: a failed close must not look like a close")
	}
	if err.Error() == "signal: killed" || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("the error of an ndsctl invocation the MODULE killed is not attributed (got %q): it must name the deadline and which side ended the child", err)
	}

	logged := log.String()
	if strings.Contains(logged, "signal: killed") {
		t.Fatalf("the module logged its own deadline kill as an unattributed ndsctl failure: %q", logged)
	}
	if !strings.Contains(logged, "did not answer") {
		t.Fatalf("the module did not say that ndsctl never answered; the operator cannot tell a wedged NoDogSplash from a refusal. Log was %q", logged)
	}
	if got := fake.spawns(t); got != 1 {
		t.Fatalf("ndsctl was started %d times for one deauthorization, want 1", got)
	}
}

// TestStopDrainsInFlightNdsctlInvocations: a service restart must not kill an
// ndsctl child that was about to answer. `tollgate-wrt restart` is what the
// bench defect's reproduction does, and before this fix Stop() returned
// immediately — nothing waited for the child, and the process died with it.
func TestStopDrainsInFlightNdsctlInvocations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the ndsctl drain test in short mode")
	}

	// A child that answers, but not instantly.
	fake := installFakeNdsctl(t, "sleep 2\nprintf 'Auth: %s - Removed\\n' \"$2\"\nexit 0")

	invocationDone := make(chan error, 1)
	go func() {
		_, err := runNdsctl("deauth", "aa:bb:cc:dd:ee:61")
		invocationDone <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for fake.spawns(t) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fake.spawns(t) == 0 {
		t.Fatal("the fake ndsctl was never started: the production ndsctl runner is not the one under test")
	}

	Stop()

	if !fake.finished() {
		t.Fatal("Stop() returned while an ndsctl invocation was still in flight: a service restart kills that child with the process, and the module reports the kill as an ndsctl failure")
	}
	if err := <-invocationDone; err != nil {
		t.Fatalf("the drained invocation returned %v, want the child's own answer", err)
	}
}

// TestInvocationInterruptedByShutdownIsNotAnNdsctlFailure: once the module is
// stopping, an invocation is not an ndsctl failure — it is the module leaving.
// It must be reported as such, and the module must not start new children on the
// way out (that is what turns a restart into a burst of indistinguishable
// failures).
func TestInvocationInterruptedByShutdownIsNotAnNdsctlFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the ndsctl shutdown test in short mode")
	}

	fake := installFakeNdsctl(t, "exec sleep 60")
	Stop()
	log := captureValveLogAtLevel(t, logrus.InfoLevel)

	macAddress := "aa:bb:cc:dd:ee:62"
	err := deauthorizeMAC(macAddress)
	if err == nil {
		t.Fatal("deauthorizeMAC reported a confirmed close for an invocation the module never completed")
	}

	logged := log.String()
	if strings.Contains(logged, "Error deauthorizing MAC address") {
		t.Fatalf("a shutdown was escalated as an ndsctl failure: %q", logged)
	}
	if !strings.Contains(logged, "stopping") {
		t.Fatalf("the interruption was not attributed to the module's shutdown; log was %q", logged)
	}
	if got := fake.spawns(t); got != 0 {
		t.Fatalf("the module started %d ndsctl children while it was stopping, want 0: a restart must not spawn invocations it cannot wait for", got)
	}
}
