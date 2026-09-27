package merchant

import (
	"os"
	"strings"
	"testing"
)

// Regression test for reviewer blocker 1 on #573: RebindSession moved the record,
// re-keyed it and set a new metering baseline, but never authorized the new
// address at the gate — and never closed the one it left.
//
// Consequence of the missing authorization: after a 200-OK rebind the customer
// still sits behind the captive portal, with a session record and a baseline
// metering an address whose traffic is blocked. In the whole tree gates are
// opened only by purchase settlement (openGateForSession), and nothing else
// authorizes a rotated client: it arrives as a fresh preauthenticated NDS
// client and the portal JS cannot authorize itself. The gap stayed invisible
// because no rebind test asserted an AUTH line at the new MAC.
//
// The mirror half is the old address's gate: leaving it authorized leaves an
// open, unmetered, unenforced gate behind the moving customer, inheritable by
// whoever takes the address next.
//
// The fake ndsctl in this package logs `AUTH <mac>` and `DEAUTH <mac>` for every
// auth/deauth it is asked to perform, so both halves are observable directly.
func TestSessionRebindAuthorizesTheNewAttachmentAndClosesTheOldOne(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}

	// The setup above closes gates of its own (bytesSessionFor deauths first) and
	// the fake logs every call, so scope the assertions to the calls the REBIND
	// itself makes. Asserting on the whole log would pass on setup noise — the
	// vacuous-assertion trap this suite exists to avoid.
	before := ndsctlCalls(t, ndsctl)

	if _, err := m.RebindSession(ticket, ticketMACNew); err != nil {
		t.Fatalf("RebindSession: %v", err)
	}

	rebindCalls := ndsctlCalls(t, ndsctl)[len(before):]

	// The record moved (pre-existing behaviour, kept as the control).
	session := sessionFor(t, m, ticketMACNew)
	if session.MacAddress != ticketMACNew {
		t.Fatalf("session addresses %s, want %s", session.MacAddress, ticketMACNew)
	}

	log := rebindCalls

	// Blocker 1a: the new attachment must be authorized, or the customer holds a
	// session they cannot use.
	if !containsLine(log, "AUTH "+ticketMACNew) {
		t.Errorf("the rebind did not authorize the new attachment:\n  want a call `auth %s`\n  got: %v", ticketMACNew, log)
	}

	// Blocker 1b: the address the session left must be deauthorized, or the old
	// gate stays open — unmetered, unenforced, and inheritable by the next device
	// that takes that address.
	if !containsLine(log, "DEAUTH "+ticketMACOld) {
		t.Errorf("the rebind left the previous attachment's gate authorized:\n  want a call `deauth %s`\n  got: %v", ticketMACOld, log)
	}

	// Ordering matters: the new attachment must be authorized BEFORE the old one
	// is torn down. Closing first would open a window in which the customer has
	// no access at all — the failure mode this fix exists to avoid.
	authAt := indexOfLine(log, "AUTH "+ticketMACNew)
	deauthAt := indexOfLine(log, "DEAUTH "+ticketMACOld)
	if authAt >= 0 && deauthAt >= 0 && deauthAt < authAt {
		t.Errorf("the previous attachment was deauthorized before the new one was authorized (deauth at %d, auth at %d) — a rebind must make before it breaks:\n  %v",
			deauthAt, authAt, log)
	}
}

// ndsctlCalls reads the fake ndsctl's AUTH/DEAUTH log for one test.
func ndsctlCalls(t *testing.T, n *rotatingNdsctl) []string {
	t.Helper()

	data, err := os.ReadFile(n.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read fake ndsctl log: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

func containsLine(lines []string, want string) bool {
	return indexOfLine(lines, want) >= 0
}

func indexOfLine(lines []string, want string) int {
	for i, line := range lines {
		if line == want {
			return i
		}
	}
	return -1
}
