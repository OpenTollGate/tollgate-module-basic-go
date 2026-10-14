package merchant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The PERIODIC client-list reconciliation contract (t_53b738bb).
//
// The startup pass (startup_reconciliation_test.go) asks NoDogSplash's client
// list ONCE, during merchant construction, and closes the gate of every client
// NoDogSplash still authorises that the module holds no session for. That closes
// the window a module RESTART creates. It cannot close the window a client
// NoDogSplash authorises that this module never saw at all:
//
//   - an `ndsctl auth` run by hand, by an operator script, or by a second
//     process;
//   - a client restored from NoDogSplash's own state file when the SERVICE
//     restarts (nodogsplash exports its client list on SIGTERM and imports it at
//     startup, nodogsplash 5.0.2 src/main.c:147/304);
//   - a box that came up DEGRADED (no reachable mint), where
//     MerchantDegraded.StartDataUsageMonitoring is a no-op, so the startup pass
//     does not run at all until the upgrade constructs a full merchant.
//
// Nothing read the client list again, so such a client held unmetered access —
// no session, no metering baseline, no allotment, no /balance record — until
// NoDogSplash's own session timeout. The module advertises a large ceiling for
// that timeout (86400 s, 99-tollgate-setup), so the window is a day.
//
// These tests drive the REAL sweep path against a fake `ndsctl` on PATH: the fake
// answers the CLIENT LIST (`ndsctl json`, no argument) from a file the test
// controls, and logs every auth/deauth, so the assertions are about what the
// module actually did to the enforcement layer. The addresses below belong to
// this file only; the valve's gate state is package-global and shared by the
// whole test binary, so every assertion is per-MAC (the same discipline as
// startup_nds_reconciliation_test.go).

const (
	// periodicOrphanMAC is the defect: NoDogSplash authorises it and this module
	// has no session, no tracked gate and no close in flight for it.
	periodicOrphanMAC = "aa:bb:cc:dd:ee:61"

	// periodicKnownMAC is a client the module holds a live session AND gate for:
	// a customer, not an inherited authorisation.
	periodicKnownMAC = "aa:bb:cc:dd:ee:62"

	// periodicTrackedOnlyMAC is Authenticated in NoDogSplash with NO session
	// record, but the valve still tracks its gate — the module's own client, and
	// the case that must survive the periodic pass too.
	periodicTrackedOnlyMAC = "aa:bb:cc:dd:ee:63"

	// periodicPreauthMAC is merely KNOWN to NoDogSplash (Preauthenticated). That
	// is a LEGITIMATE record — a client on its way through the splash page — it
	// cannot pass traffic, and it must never be deauthorised.
	periodicPreauthMAC = "aa:bb:cc:dd:ee:64"

	// periodicRefusedMAC is authorised-and-unknown with a refusing deauth.
	periodicRefusedMAC = "aa:bb:cc:dd:ee:66"

	// periodicSweepsToConverge is how many usage-monitor sweeps these tests allow
	// the module to notice the drift. The module's slowest reconciliation cadence
	// is 15 sweeps (~30 s at the 2 s sweep, the stale-binding precedent), and this
	// pass runs on that same rhythm: a client list read on every 2 s sweep would
	// spend an ndsctl call every 2 s. Written as a local constant on purpose — a
	// RED test must compile against the tree that does not have the pass yet.
	periodicSweepsToConverge = 15
)

// periodicNdsctl is a fake `ndsctl` on PATH which answers the client LIST and
// logs every auth/deauth.
type periodicNdsctl struct {
	logPath   string
	listPath  string
	statePath string
	failPath  string
}

func installPeriodicNdsctl(t *testing.T) *periodicNdsctl {
	t.Helper()

	dir := t.TempDir()
	n := &periodicNdsctl{
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
// with the state NoDogSplash reports for it. The map key is written verbatim into
// the record's own `mac` field, because that is the spelling the module must hand
// back to ndsctl when it closes the gate.
func (n *periodicNdsctl) setList(t *testing.T, states map[string]string) {
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
func (n *periodicNdsctl) setUnreadable(t *testing.T, unreadable bool) {
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
func (n *periodicNdsctl) failDeauth(t *testing.T, fail bool) {
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
func (n *periodicNdsctl) opsFor(t *testing.T, prefix string) int {
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

// resetGateState returns the valve to a clean state for one address before the
// test drives it, and does it through the PUBLIC close contract.
//
// It is needed because the valve's gate/close-budget state is package-global and
// shared by the whole test binary — including across repeated runs of one test
// (`-count=N`, which is how the pass is checked for flakiness). A gate left
// ABANDONED by an earlier run has spent its close budget, so the next run's
// close is refused by `closeAttemptAllowed` before ndsctl is ever invoked and the
// assertion would read a false zero. Clearing the fake's deauth failure and
// closing for real confirms the close, which retires the gate and forgets its
// budget (`clearCloseStreak` on a confirmed close). It must be called with a
// WORKING deauth, i.e. before the test makes deauth fail.
//
// The fake is taken through the deauthToggle interface rather than a concrete
// type, so the reset is shared by tests that drive either pass (the periodic one
// and the startup one): the only thing this reset needs from a fake is the
// ability to arm a refusing `deauth` and clear it again, and both fakes in this
// package have it.
type deauthToggle interface {
	failDeauth(t *testing.T, fail bool)
}

func resetGateState(t *testing.T, ndsctl deauthToggle, macAddress string) {
	t.Helper()

	ndsctl.failDeauth(t, false)
	closeGateCleanup(t, macAddress)
	if err := valve.CloseGate(macAddress); err != nil {
		// Nothing to close, or the close raced something else: the state this
		// test needs is "no gate tracked and no budget spent", and a fresh MAC
		// that was never opened satisfies it already.
		t.Logf("resetGateState: closing %s before the test reported %v (fine if it was never open)", macAddress, err)
	}
}

// TestPeriodicSweepClosesAnAuthorisationTheModuleNeverMade is the defect this
// card exists for. A client NoDogSplash authorises that this module has no record
// of holds unmetered access for as long as NoDogSplash's own session timeout,
// because nothing read the client list again after startup.
//
// The sweep must close that gate and say so, must NOT touch a client the module
// itself still holds (session or tracked gate), and must NOT touch a merely
// Preauthenticated record — that one is a client on its way through the splash
// page, it cannot pass traffic, and deauthorising it would tear up a legitimate
// record (the card's explicit warning against assuming "deauthorise" blindly).
func TestPeriodicSweepClosesAnAuthorisationTheModuleNeverMade(t *testing.T) {
	ndsctl := installPeriodicNdsctl(t)
	ndsctl.setList(t, map[string]string{
		periodicOrphanMAC:      "Authenticated",
		periodicPreauthMAC:     "Preauthenticated",
		periodicKnownMAC:       "Authenticated",
		periodicTrackedOnlyMAC: "Authenticated",
	})

	m, _ := newRenewalMerchant(t, "bytes")

	// The module's own paying customer: a session AND the gate that goes with it.
	installBytesSession(t, m, periodicKnownMAC, 1<<40)
	closeGateCleanup(t, periodicKnownMAC)
	if err := valve.OpenGate(periodicKnownMAC); err != nil {
		t.Fatalf("open gate for %s: %v", periodicKnownMAC, err)
	}

	// A client with no session record at all but a tracked gate: the module's
	// own, and what the periodic pass must not fight. This is the client the pass
	// relies on the valve knowing about on the degraded -> full upgrade, and the
	// reason knowsClient reads TrackedGates at all.
	closeGateCleanup(t, periodicTrackedOnlyMAC)
	if err := valve.OpenGate(periodicTrackedOnlyMAC); err != nil {
		t.Fatalf("open gate for %s: %v", periodicTrackedOnlyMAC, err)
	}

	logs := captureSyncLogs(t)
	sweep(t, m, periodicSweepsToConverge)

	if got := ndsctl.opsFor(t, "DEAUTH "+periodicOrphanMAC); got != 1 {
		t.Fatalf("NoDogSplash authorises %s and this module holds no record of it, so the sweep must close its gate: deauths = %d, want 1\nlog:\n%s",
			periodicOrphanMAC, got, logs.String())
	}
	if got := ndsctl.opsFor(t, "DEAUTH "+periodicPreauthMAC); got != 0 {
		t.Fatalf("a Preauthenticated client is on its way through the splash page and cannot pass traffic: it must be left alone, deauths for %s = %d, want 0", periodicPreauthMAC, got)
	}
	if got := ndsctl.opsFor(t, "DEAUTH "+periodicKnownMAC); got != 0 {
		t.Fatalf("the module's own live session must not be closed by the reconciliation: deauths for %s = %d, want 0", periodicKnownMAC, got)
	}
	if got := ndsctl.opsFor(t, "DEAUTH "+periodicTrackedOnlyMAC); got != 0 {
		t.Fatalf("a client whose gate the valve still tracks is this module's own: deauths for %s = %d, want 0", periodicTrackedOnlyMAC, got)
	}

	line := logs.String()
	for _, want := range []string{"client-list reconciliation", periodicOrphanMAC, "UNMETERED access", "CLOSED"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the operator has to be able to read what happened to %s: no line mentions %q\nlog:\n%s", periodicOrphanMAC, want, line)
		}
	}
}

// TestPeriodicSweepChangesNothingWhenTheClientListCannotBeRead: an unreadable
// list is not evidence about any client, so no gate may be closed on the strength
// of it — and the residue is named, with the check to run, rather than being
// silently ignored. This is the property #545, #595 and the startup pass all
// preserve.
func TestPeriodicSweepChangesNothingWhenTheClientListCannotBeRead(t *testing.T) {
	ndsctl := installPeriodicNdsctl(t)
	ndsctl.setList(t, map[string]string{periodicOrphanMAC: "Authenticated"})
	ndsctl.setUnreadable(t, true)

	m, _ := newRenewalMerchant(t, "bytes")

	logs := captureSyncLogs(t)
	sweep(t, m, periodicSweepsToConverge)

	if got := ndsctl.opsFor(t, "DEAUTH "+periodicOrphanMAC); got != 0 {
		t.Fatalf("a client list the module could not read is not evidence about any client: deauths for %s = %d, want 0", periodicOrphanMAC, got)
	}

	line := logs.String()
	for _, want := range []string{"could not read NoDogSplash's client list", "no client's access was changed", "ndsctl json"} {
		if !strings.Contains(line, want) {
			t.Fatalf("an unreadable client list must leave the operator a line naming the check to run (%q missing)\nlog:\n%s", want, line)
		}
	}
}

// TestPeriodicSweepReportsACloseItCouldNotConfirm: the fail-closed direction is
// only as good as what happens when the close is REFUSED. The gate must stay
// tracked (a refused deauth is not a close), the operator must be told the client
// may still hold open, unmetered access, and the pass must not claim it closed it.
func TestPeriodicSweepReportsACloseItCouldNotConfirm(t *testing.T) {
	ndsctl := installPeriodicNdsctl(t)
	ndsctl.setList(t, map[string]string{periodicRefusedMAC: "Authenticated"})

	// The refused close leaves a TRACKED gate with a partially spent budget, and
	// both live on the valve's package-global state — so the test starts from a
	// confirmed close (a working deauth, done BEFORE the failure is armed) rather
	// than inheriting the previous run's abandoned gate.
	resetGateState(t, ndsctl, periodicRefusedMAC)

	m, _ := newRenewalMerchant(t, "bytes")
	ndsctl.failDeauth(t, true)

	logs := captureSyncLogs(t)
	sweep(t, m, periodicSweepsToConverge)

	if got := ndsctl.opsFor(t, "DEAUTH "+periodicRefusedMAC); got == 0 {
		t.Fatalf("an authorised client this module has no record of must have its gate closed even when the close is refused: deauths = 0\nlog:\n%s", logs.String())
	}

	line := logs.String()
	if !strings.Contains(line, "ERROR: client-list reconciliation") {
		t.Fatalf("a refused close is an ERROR, not a WARNING\nlog:\n%s", line)
	}
	for _, want := range []string{"may still hold open, UNMETERED access", "OPERATOR ACTION"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the escalation must leave the operator the state and the action (%q missing)\nlog:\n%s", want, line)
		}
	}
	if strings.Contains(line, "its gate is now CLOSED") {
		t.Fatalf("a close that was not confirmed must never be reported as done\nlog:\n%s", line)
	}

	// Leave the valve as this test found it: the failure is cleared BEFORE the
	// cleanup close, or the gate would stay tracked with a spent budget for
	// whichever test runs next (and for the next run of THIS one under -count=N).
	t.Cleanup(func() {
		ndsctl.failDeauth(t, false)
		if err := valve.CloseGate(periodicRefusedMAC); err != nil {
			t.Logf("post-test close of %s: %v", periodicRefusedMAC, err)
		}
	})
}

// TestPeriodicSweepSaysWhenTheResidueClears: an operator must see the condition
// END, not only its start — and the module must not keep reporting a window that
// is closed.
func TestPeriodicSweepSaysWhenTheResidueClears(t *testing.T) {
	ndsctl := installPeriodicNdsctl(t)
	ndsctl.setList(t, map[string]string{periodicOrphanMAC: "Authenticated"})

	m, _ := newRenewalMerchant(t, "bytes")

	first := captureSyncLogs(t)
	sweep(t, m, periodicSweepsToConverge)
	if !strings.Contains(first.String(), periodicOrphanMAC) {
		t.Fatalf("the pass must report the residue it found\nlog:\n%s", first.String())
	}

	// NoDogSplash no longer holds the client (the close took its authorisation
	// away), so the next pass has nothing to report but "the window is closed".
	ndsctl.setList(t, map[string]string{})

	buffer := captureMerchantLog(t)
	sweep(t, m, periodicSweepsToConverge)

	if !strings.Contains(buffer.String(), "unmetered window is closed") {
		t.Fatalf("the pass must say once that the condition ended, not only once that it started\nlog:\n%s", buffer.String())
	}
}

// TestPeriodicSweepIsQuietWhenNothingIsWrong: the common case must not
// manufacture work or noise on a box whose clients are the module's own.
func TestPeriodicSweepIsQuietWhenNothingIsWrong(t *testing.T) {
	ndsctl := installPeriodicNdsctl(t)
	ndsctl.setList(t, map[string]string{periodicPreauthMAC: "Preauthenticated"})

	m, _ := newRenewalMerchant(t, "bytes")

	logs := captureSyncLogs(t)
	sweep(t, m, periodicSweepsToConverge)

	for _, macAddress := range []string{periodicOrphanMAC, periodicKnownMAC, periodicTrackedOnlyMAC, periodicPreauthMAC} {
		if got := ndsctl.opsFor(t, "DEAUTH "+macAddress); got != 0 {
			t.Fatalf("nothing is wrong on this box, yet the module ran %d deauth(s) for %s\nlog:\n%s", got, macAddress, logs.String())
		}
	}
	if strings.Contains(logs.String(), "client-list reconciliation: NoDogSplash authorises") {
		t.Fatalf("a pass that found no drift must not report one\nlog:\n%s", logs.String())
	}
}

// TestPeriodicSweepLeavesAPayingClientWhoseMACItSpellsDifferently is the case-fold
// half of the PERIODIC pass, and it is the expensive one: this pass runs on its
// cadence (~30 s) rather than once per boot, so a case-sensitive membership test
// would close a paying customer's gate once per pass instead of once per restart.
//
// NoDogSplash reports the MAC it was configured with; the module's session map is
// written by NormalizeMACAddress (lower-case) and a tracked gate keeps the
// spelling it was opened with. Both spellings below are the SAME client — the
// session is the module's record of a purchase it authorised itself — so the
// pass must leave the gate alone whichever spelling each side happens to use, and
// must not report a residue that does not exist.
func TestPeriodicSweepLeavesAPayingClientWhoseMACItSpellsDifferently(t *testing.T) {
	ndsctl := installPeriodicNdsctl(t)

	const ndsctlSpelling = "AA:BB:CC:DD:EE:67"
	moduleSpelling := strings.ToLower(ndsctlSpelling)

	ndsctl.setList(t, map[string]string{ndsctlSpelling: "Authenticated"})

	m, _ := newRenewalMerchant(t, "bytes")
	installBytesSession(t, m, moduleSpelling, 1<<40)
	closeGateCleanup(t, moduleSpelling)
	if err := valve.OpenGate(moduleSpelling); err != nil {
		t.Fatalf("open gate for %s: %v", moduleSpelling, err)
	}

	logs := captureSyncLogs(t)
	sweep(t, m, periodicSweepsToConverge)

	for _, spelling := range []string{ndsctlSpelling, moduleSpelling} {
		if got := ndsctl.opsFor(t, "DEAUTH "+spelling); got != 0 {
			t.Fatalf("NoDogSplash reports %s while this module holds the SAME client (a paid session AND its gate) under %s: the sweep must leave it alone, but it ran %d deauth(s) as %s — a case-sensitive membership test is how a paying customer's gate is closed every cadence\nlog:\n%s",
				ndsctlSpelling, moduleSpelling, got, spelling, logs.String())
		}
	}
	if strings.Contains(logs.String(), "client-list reconciliation: NoDogSplash authorises") {
		t.Fatalf("there is no residue on this box: the module's own paying client must not be reported as an inherited authorisation\nlog:\n%s", logs.String())
	}
}
