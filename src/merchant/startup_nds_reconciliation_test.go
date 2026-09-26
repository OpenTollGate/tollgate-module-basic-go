package merchant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The startup reconciliation contract (t_83e6ab0f, deliverable 3).
//
// A module restart is the one event that makes the module's view of the world
// diverge from the enforcement layer in the DANGEROUS direction: the module's
// gate, session and baseline bookkeeping is process-local, so a restarted module
// starts from an empty session set, while NoDogSplash — a separate service —
// keeps every client it had authorised. Measured on the bench MT3000 (pre17,
// 2026-09-26, `~/tg-manual/restart-drift-ln-20260926T112454Z.log`): buy, gate
// open, restart ONLY `tollgate-wrt`, and the client is still Authenticated in
// `ndsctl json` with the gate passing traffic while the freshly restarted module
// holds no session at all. Nothing meters that client until NoDogSplash's own
// timeout, i.e. free internet.
//
// The harness drives the real startup path against a fake `ndsctl` on PATH: the
// fake answers the CLIENT LIST (`ndsctl json`, no argument) from a file a test
// controls, and logs every auth/deauth, so the assertions are about what the
// module actually did to the enforcement layer.
//
// The addresses below belong to this file only; the valve's gate state is
// package-global and shared by the whole test binary, so every assertion is
// per-MAC (the same discipline as stale_binding_janitor_test.go).

const (
	// inheritedOrphanMAC is the restart case: NoDogSplash authorises it and the
	// module holds no session for it.
	inheritedOrphanMAC = "aa:bb:cc:dd:ee:51"

	// inheritedKnownMAC is a client the module still holds a session for: the
	// pass must leave it alone.
	inheritedKnownMAC = "aa:bb:cc:dd:ee:52"

	// inheritedPreauthMAC is merely KNOWN to NoDogSplash (Preauthenticated): it
	// cannot pass traffic, so it is not an access leak and must be left alone.
	inheritedPreauthMAC = "aa:bb:cc:dd:ee:53"
)

// inheritedNdsctl is a fake `ndsctl` on PATH which answers the client LIST as
// well as the per-MAC record, and logs every auth/deauth.
type inheritedNdsctl struct {
	logPath   string
	listPath  string
	statePath string
	failPath  string
}

func installInheritedNdsctl(t *testing.T) *inheritedNdsctl {
	t.Helper()

	dir := t.TempDir()
	n := &inheritedNdsctl{
		logPath:   filepath.Join(dir, "ndsctl.log"),
		listPath:  filepath.Join(dir, "ndsctl.list"),
		statePath: filepath.Join(dir, "ndsctl.listreadable"),
		failPath:  filepath.Join(dir, "ndsctl.deauthfail"),
	}

	script := fmt.Sprintf(`#!/bin/sh
LOG=%q
LIST=%q
STATE=%q
FAIL=%q
mac="$2"
case "$1" in
  json)
    if [ -z "$mac" ]; then
      # The CLIENT LIST read: "ndsctl json" with no argument.
      if [ -r "$STATE" ] && [ "$(cat "$STATE")" = "unreadable" ]; then
        # What the bench measured while the control socket was wedged, and what
        # a permission problem looks like: no answer, exit status 1.
        echo "Failed to send request: Operation not permitted"
        exit 1
      fi
      cat "$LIST"
      exit 0
    fi
    # The per-MAC read: a record only for a MAC the list carries.
    if [ -r "$LIST" ] && grep -qi "\"$mac\"" "$LIST"; then
      printf '{"id":1,"ip":"192.0.2.10","mac":"%%s","added":1,"active":1,"duration":60,"token":"t","state":"Authenticated","downloaded":1024,"avg_down_speed":0,"uploaded":512,"avg_up_speed":0}\n' "$mac"
      exit 0
    fi
    echo '{}'
    exit 0
    ;;
  deauth)
    echo "DEAUTH $mac" >> "$LOG"
    if [ -r "$FAIL" ]; then
      echo "Failed to deauthenticate client"
      exit 1
    fi
    echo "Auth: $mac - Removed"
    exit 0
    ;;
  auth)
    echo "AUTH $mac" >> "$LOG"
    echo "Auth: $mac - Granted"
    exit 0
    ;;
esac
echo OK
exit 0
`, n.logPath, n.listPath, n.statePath, n.failPath)

	if err := os.WriteFile(filepath.Join(dir, "ndsctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ndsctl: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	n.setUnreadable(t, false)
	n.setList(t, map[string]string{})
	return n
}

// setList drives what `ndsctl json` (no argument) answers: one entry per MAC,
// with the state NoDogSplash reports for it.
func (n *inheritedNdsctl) setList(t *testing.T, states map[string]string) {
	t.Helper()

	type entry struct {
		IP         string `json:"ip"`
		MAC        string `json:"mac"`
		State      string `json:"state"`
		Downloaded uint64 `json:"downloaded"`
		Uploaded   uint64 `json:"uploaded"`
	}

	payload := struct {
		ClientLength int              `json:"client_length"`
		Clients      map[string]entry `json:"clients"`
	}{ClientLength: len(states), Clients: map[string]entry{}}

	for macAddress, state := range states {
		payload.Clients[macAddress] = entry{
			IP: "192.0.2.10", MAC: macAddress, State: state,
			Downloaded: 1024, Uploaded: 512,
		}
	}

	encoded, err := json.Marshal(&payload)
	if err != nil {
		t.Fatalf("marshal fake client list: %v", err)
	}
	if err := os.WriteFile(n.listPath, append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("write fake client list: %v", err)
	}
}

// setUnreadable models a client list the module cannot read at all.
func (n *inheritedNdsctl) setUnreadable(t *testing.T, unreadable bool) {
	t.Helper()

	state := "readable"
	if unreadable {
		state = "unreadable"
	}
	if err := os.WriteFile(n.statePath, []byte(state), 0o644); err != nil {
		t.Fatalf("write client-list state: %v", err)
	}
}

// failDeauth makes every `ndsctl deauth` fail, which is the state in which the
// client still holds the open gate the module is trying to take away.
func (n *inheritedNdsctl) failDeauth(t *testing.T, fail bool) {
	t.Helper()

	if !fail {
		if err := os.Remove(n.failPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clear deauth failure: %v", err)
		}
		return
	}
	if err := os.WriteFile(n.failPath, []byte("fail\n"), 0o644); err != nil {
		t.Fatalf("write deauth failure marker: %v", err)
	}
}

// opsFor counts the invocations of one operation for ONE address ("DEAUTH
// <mac>", "AUTH <mac>"), so an assertion cannot be satisfied by another test's
// closes.
func (n *inheritedNdsctl) opsFor(t *testing.T, prefix string) int {
	t.Helper()

	data, err := os.ReadFile(n.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read fake ndsctl log: %v", err)
	}

	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, prefix) {
			total++
		}
	}
	return total
}

// installInheritedClientSession gives macAddress a paid bytes session AND the
// open gate that goes with it, in the order the purchase path uses (session
// first — grantSessionAccess records the session before it opens the gate), so
// the pass sees a client that is genuinely the module's own.
func installInheritedClientSession(t *testing.T, m *Merchant, macAddress string) {
	t.Helper()

	installBytesSession(t, m, macAddress, 1<<40)
	if err := valve.OpenGate(macAddress); err != nil {
		t.Fatalf("open gate for %s: %v", macAddress, err)
	}
	t.Cleanup(func() {
		if err := valve.CloseGate(macAddress); err != nil {
			t.Logf("cleanup: could not close the gate of %s: %v", macAddress, err)
		}
	})
}

// inheritedMerchant builds the merchant the startup path runs on.
func inheritedMerchant(t *testing.T) *Merchant {
	t.Helper()

	m, _ := newRenewalMerchant(t, "bytes")
	return m
}

// TestStartupMonitoringClosesTheAuthorisationsTheModuleInherits is the measured
// defect: after a module restart, every client NoDogSplash was holding stays
// Authenticated — with its gate open and nothing metering it — for as long as
// NoDogSplash's own session timeout, and no code path looks at the client list.
//
// The pass must close that gate and say so, must NOT touch a client the module
// itself still holds (it is a customer whose session is live), and must NOT
// touch a merely Preauthenticated record (no access to leak).
func TestStartupMonitoringClosesTheAuthorisationsTheModuleInherits(t *testing.T) {
	ndsctl := installInheritedNdsctl(t)
	ndsctl.setList(t, map[string]string{
		inheritedOrphanMAC:  "Authenticated",
		inheritedPreauthMAC: "Preauthenticated",
		inheritedKnownMAC:   "Authenticated",
	})

	m := inheritedMerchant(t)
	installInheritedClientSession(t, m, inheritedKnownMAC)

	logs := captureSyncLogs(t)
	m.StartDataUsageMonitoring()

	if got := ndsctl.opsFor(t, "DEAUTH "+inheritedOrphanMAC); got != 1 {
		t.Fatalf("the module inherited an authorised client it holds no session for and did not close its gate: deauths for %s = %d, want 1\nlog:\n%s",
			inheritedOrphanMAC, got, logs.String())
	}
	if got := ndsctl.opsFor(t, "DEAUTH "+inheritedPreauthMAC); got != 0 {
		t.Fatalf("a merely Preauthenticated client cannot pass traffic and must be left alone: deauths for %s = %d, want 0", inheritedPreauthMAC, got)
	}
	if got := ndsctl.opsFor(t, "DEAUTH "+inheritedKnownMAC); got != 0 {
		t.Fatalf("the module's own live session must not be closed by the startup reconciliation: deauths for %s = %d, want 0", inheritedKnownMAC, got)
	}

	line := logs.String()
	for _, want := range []string{"startup reconciliation", inheritedOrphanMAC, "held open, UNMETERED access", "CLOSED"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the operator has to be able to read what happened to %s: no line mentions %q\nlog:\n%s", inheritedOrphanMAC, want, line)
		}
	}
}

// TestStartupReconciliationChangesNothingWhenTheClientListCannotBeRead: an
// unreadable list is not evidence about any client, so no gate may be closed on
// the strength of it — and the residue (a client that may keep unmetered access)
// is named once, with the check to run, instead of being silently ignored.
func TestStartupReconciliationChangesNothingWhenTheClientListCannotBeRead(t *testing.T) {
	ndsctl := installInheritedNdsctl(t)
	ndsctl.setList(t, map[string]string{inheritedOrphanMAC: "Authenticated"})
	ndsctl.setUnreadable(t, true)

	m := inheritedMerchant(t)
	installInheritedClientSession(t, m, inheritedKnownMAC)

	logs := captureSyncLogs(t)
	m.ReconcileNdsAuthorisationsOnStartup()

	if got := ndsctl.opsFor(t, "DEAUTH "+inheritedOrphanMAC); got != 0 {
		t.Fatalf("a client list the module could not read is not evidence about any client: deauths for %s = %d, want 0", inheritedOrphanMAC, got)
	}
	if got := ndsctl.opsFor(t, "DEAUTH "+inheritedKnownMAC); got != 0 {
		t.Fatalf("deauths for the module's own live session = %d, want 0", got)
	}

	line := logs.String()
	for _, want := range []string{"could not read NoDogSplash's client list", "ndsctl json"} {
		if !strings.Contains(line, want) {
			t.Fatalf("an unreadable client list must leave the operator a line naming the check to run (%q missing)\nlog:\n%s", want, line)
		}
	}
}

// TestStartupReconciliationIsQuietWhenNoDogSplashHoldsNobody: the fresh-box
// path must not manufacture work or noise.
func TestStartupReconciliationIsQuietWhenNoDogSplashHoldsNobody(t *testing.T) {
	ndsctl := installInheritedNdsctl(t)
	ndsctl.setList(t, map[string]string{})

	m := inheritedMerchant(t)
	logs := captureSyncLogs(t)
	m.ReconcileNdsAuthorisationsOnStartup()

	// Per-MAC, like every assertion in this file: building the merchant closes
	// the gate of the harness's own MAC, and the valve's state is shared by the
	// whole test binary.
	for _, macAddress := range []string{inheritedOrphanMAC, inheritedKnownMAC, inheritedPreauthMAC} {
		if got := ndsctl.opsFor(t, "DEAUTH "+macAddress); got != 0 {
			t.Fatalf("NoDogSplash holds nobody, yet the module ran %d deauth(s) for %s", got, macAddress)
		}
	}
	if !strings.Contains(logs.String(), "holds no authorised client") {
		t.Fatalf("the pass must say it found nothing rather than stay silent\nlog:\n%s", logs.String())
	}
}

// TestStartupReconciliationReportsACloseItCouldNotConfirm: the fail-closed
// direction is only as good as what happens when the close is REFUSED. The gate
// must stay tracked (a failed deauth is not a close), the operator must be told
// the client may still hold open, unmetered access, and the pass must not
// pretend it succeeded.
func TestStartupReconciliationReportsACloseItCouldNotConfirm(t *testing.T) {
	ndsctl := installInheritedNdsctl(t)
	ndsctl.setList(t, map[string]string{inheritedOrphanMAC: "Authenticated"})
	ndsctl.failDeauth(t, true)

	m := inheritedMerchant(t)
	logs := captureSyncLogs(t)
	m.ReconcileNdsAuthorisationsOnStartup()

	if got := ndsctl.opsFor(t, "DEAUTH "+inheritedOrphanMAC); got == 0 {
		t.Fatalf("the gate of an inherited authorised client must be closed even when the close is refused: deauths = 0")
	}

	line := logs.String()
	if !strings.Contains(line, "ERROR: startup reconciliation") {
		t.Fatalf("a refused close is an ERROR, not a WARNING\nlog:\n%s", line)
	}
	for _, want := range []string{"may still hold open, unmetered access", "stays tracked", "OPERATOR ACTION"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the escalation must leave the operator the state and the action (%q missing)\nlog:\n%s", want, line)
		}
	}
	if strings.Contains(line, "its gate is now CLOSED") {
		t.Fatalf("a close that was not confirmed must never be reported as done\nlog:\n%s", line)
	}
}

// TestStartupReconciliationIsBoundedAndReadable is a guard against the
// reconciliation becoming the kind of storm this card exists to remove: one
// client, one close, one escalation — not a line per sweep.
func TestStartupReconciliationIsBoundedAndReadable(t *testing.T) {
	ndsctl := installInheritedNdsctl(t)
	ndsctl.setList(t, map[string]string{inheritedOrphanMAC: "Authenticated"})

	m := inheritedMerchant(t)
	logs := captureSyncLogs(t)
	m.StartDataUsageMonitoring()

	time.Sleep(50 * time.Millisecond)

	if got := ndsctl.opsFor(t, "DEAUTH "+inheritedOrphanMAC); got != 1 {
		t.Fatalf("deauths for the inherited client = %d, want exactly 1 (the pass runs once, at startup)", got)
	}
	if got := strings.Count(logs.String(), inheritedOrphanMAC); got > 4 {
		t.Fatalf("%s is mentioned %d times for ONE inherited client: the reconciliation is not bounded\nlog:\n%s", inheritedOrphanMAC, got, logs.String())
	}
}
