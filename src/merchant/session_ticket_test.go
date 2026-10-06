package merchant

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The session-ticket contract (docs/architecture/session-ticket-decision.md).
//
// A ticket is a memory-only handle onto a session; a MAC is only the delivery
// address its access is delivered to. These tests drive the real merchant, the
// real valve and a fake `ndsctl` on PATH, so the meter they assert on is the one
// that runs on a router — not a stub.

const (
	ticketMACOld = "02:11:22:33:44:55"
	ticketMACNew = "02:11:22:33:44:66"
	ticketMACAlt = "02:11:22:33:44:77"

	ticketAllotmentBytes = 100 * 1024 * 1024
	ticketConsumedBytes  = 40 * 1024 * 1024
	ticketExtraBytes     = 20 * 1024 * 1024
)

// rotatingNdsctl is a fake `ndsctl` whose answer is PER MAC: `ndsctl json <mac>`
// reports the state and the byte counters recorded for that address, and "{}"
// for an address it does not list — the answer that means NoDogSplash has no
// record of the client (after a deauth, or after it dropped an idle device).
//
// The renewal harness in this package cannot express this: it has one global
// client, and a rebind is entirely about two.
type rotatingNdsctl struct {
	statePath string
	logPath   string
}

func installRotatingNdsctl(t *testing.T) *rotatingNdsctl {
	t.Helper()

	dir := t.TempDir()
	n := &rotatingNdsctl{
		statePath: filepath.Join(dir, "ndsctl.clients"),
		logPath:   filepath.Join(dir, "ndsctl.log"),
	}

	script := fmt.Sprintf(`#!/bin/sh
STATE=%q
LOG=%q
mac="$2"
case "$1" in
  auth)
    echo "AUTH $mac" >> "$LOG"
    echo "Auth: $mac - Granted"
    exit 0
    ;;
  deauth)
    echo "DEAUTH $mac" >> "$LOG"
    echo "Auth: $mac - Removed"
    exit 0
    ;;
  json)
    row=$(awk -v m="$mac" 'tolower($1) == tolower(m) { print $2 " " $3 " " $4; exit }' "$STATE" 2>/dev/null)
    if [ -z "$row" ]; then
      echo '{}'
      exit 0
    fi
    state=$(echo "$row" | cut -d' ' -f1)
    down=$(echo "$row" | cut -d' ' -f2)
    up=$(echo "$row" | cut -d' ' -f3)
    printf '{"id":1,"ip":"192.0.2.60","mac":"%%s","added":1,"active":1,"duration":60,"token":"t","state":"%%s","downloaded":%%s,"avg_down_speed":0,"uploaded":%%s,"avg_up_speed":0}\n' "$mac" "$state" "$down" "$up"
    exit 0
    ;;
esac
echo "unknown command: $1" >&2
exit 1
`, n.statePath, n.logPath)

	if err := os.WriteFile(filepath.Join(dir, "ndsctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ndsctl: %v", err)
	}
	if err := os.WriteFile(n.statePath, []byte(""), 0o644); err != nil {
		t.Fatalf("write fake ndsctl state: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return n
}

// setClient records what `ndsctl json <mac>` reports for one address: its state
// ("Authenticated" is what the gate knows as live) and its byte counters in kB,
// which is the unit the real ndsctl reports.
func (n *rotatingNdsctl) setClient(t *testing.T, macAddress, state string, downloadedKB, uploadedKB uint64) {
	t.Helper()

	lines := n.lines(t)
	entry := fmt.Sprintf("%s %s %d %d", macAddress, state, downloadedKB, uploadedKB)

	replaced := false
	for i, line := range lines {
		if strings.EqualFold(strings.Fields(line)[0], macAddress) {
			lines[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, entry)
	}
	n.write(t, lines)
}

// clearClient removes an address from the NDS client list: `ndsctl json <mac>`
// then answers "{}", which is what a departed device looks like to the module.
func (n *rotatingNdsctl) clearClient(t *testing.T, macAddress string) {
	t.Helper()

	var kept []string
	for _, line := range n.lines(t) {
		if strings.TrimSpace(line) == "" || strings.EqualFold(strings.Fields(line)[0], macAddress) {
			continue
		}
		kept = append(kept, line)
	}
	n.write(t, kept)
}

func (n *rotatingNdsctl) lines(t *testing.T) []string {
	t.Helper()

	data, err := os.ReadFile(n.statePath)
	if err != nil {
		t.Fatalf("read fake ndsctl state: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (n *rotatingNdsctl) write(t *testing.T, lines []string) {
	t.Helper()

	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(n.statePath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fake ndsctl state: %v", err)
	}
}

// bytesSessionFor installs a bytes session for macAddress and captures its
// metering baseline where the client's counters stand right now, so the meter in
// the test measures from the same point it would on a router.
func bytesSessionFor(t *testing.T, m *Merchant, macAddress string, allotment uint64) {
	t.Helper()

	valve.ClearDataBaseline(macAddress)
	_ = valve.CloseGate(macAddress)

	m.sessionMu.Lock()
	m.customerSessions[macAddress] = &CustomerSession{
		MacAddress: macAddress,
		StartTime:  time.Now().Unix(),
		Metric:     "bytes",
		Allotment:  allotment,
	}
	m.sessionMu.Unlock()

	// A baseline recorded from zero (NoDogSplash has no counters for the client
	// yet) is the production behaviour, not a failure: the customer's grant must
	// not fail over it, and neither must the test.
	if err := valve.SetDataBaseline(macAddress); err != nil && !errors.Is(err, valve.ErrClientCountersUnavailable) {
		t.Fatalf("SetDataBaseline(%s): %v", macAddress, err)
	}
}

// usageOf reads the session's wire answer and splits it, failing the test when
// the client has no session at all ("-1/-1").
func usageOf(t *testing.T, m *Merchant, macAddress string) (used, allotment uint64) {
	t.Helper()

	raw, err := m.GetUsage(macAddress)
	if err != nil {
		t.Fatalf("GetUsage(%s): %v", macAddress, err)
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		t.Fatalf("usage for %s = %q, want used/allotment", macAddress, raw)
	}
	used, err = strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("usage %q for %s is not numeric: %v", raw, macAddress, err)
	}
	allotment, err = strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("allotment %q for %s is not numeric: %v", raw, macAddress, err)
	}
	return used, allotment
}

func sessionFor(t *testing.T, m *Merchant, macAddress string) *CustomerSession {
	t.Helper()

	m.sessionMu.RLock()
	defer m.sessionMu.RUnlock()

	session, exists := m.customerSessions[macAddress]
	if !exists {
		t.Fatalf("no session for %s", macAddress)
	}
	return cloneCustomerSession(session)
}

// TestSessionTicketIssueAndVerify covers the shape of the ticket itself: it is
// issued for the caller's own session, it verifies to the same handle, and the
// three ways a ticket can be unusable are refusals rather than silent successes.
func TestSessionTicketIssueAndVerify(t *testing.T) {
	installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	if _, _, err := m.IssueSessionTicket(ticketMACOld); !errors.Is(err, ErrTicketNoSession) {
		t.Fatalf("issuing a ticket for a client with no session = %v, want ErrTicketNoSession: a ticket must never be a grant", err)
	}

	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)

	ticket, expiresAt, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}
	if ticket == "" || expiresAt <= time.Now().Unix() {
		t.Fatalf("issued ticket %q expires at %d, want a non-empty ticket with a future expiry", ticket, expiresAt)
	}
	if strings.Contains(ticket, ticketMACOld) {
		t.Fatal("the ticket carries the MAC address: it must carry an opaque handle and nothing that identifies the customer")
	}

	verified, err := m.VerifySessionTicket(ticket)
	if err != nil {
		t.Fatalf("VerifySessionTicket: %v", err)
	}
	if verified.MacAddress != ticketMACOld {
		t.Fatalf("verified ticket names %s, want %s", verified.MacAddress, ticketMACOld)
	}

	// Issuing again must not invalidate the ticket a second tab is holding: it
	// is the same handle, re-signed.
	second, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("second IssueSessionTicket: %v", err)
	}
	secondVerified, err := m.VerifySessionTicket(second)
	if err != nil {
		t.Fatalf("VerifySessionTicket(second): %v", err)
	}
	if secondVerified.Handle != verified.Handle {
		t.Fatalf("a second issue produced handle %q, want the same handle %q: re-issuing must not strand the first ticket",
			secondVerified.Handle, verified.Handle)
	}
	if _, err := m.VerifySessionTicket(ticket); err != nil {
		t.Fatalf("the first ticket stopped verifying after a re-issue: %v", err)
	}

	// A ticket from another process (a module restart, which draws a new key)
	// does not verify here.
	other, _ := newRenewalMerchant(t, "bytes")
	bytesSessionFor(t, other, ticketMACOld, ticketAllotmentBytes)
	foreign, _, err := other.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("foreign IssueSessionTicket: %v", err)
	}
	if _, err := m.VerifySessionTicket(foreign); !errors.Is(err, ErrTicketSignature) {
		t.Fatalf("verifying another process's ticket = %v, want ErrTicketSignature (a restart invalidates every ticket)", err)
	}

	// Tampering with the payload is caught by the signature.
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 {
		t.Fatalf("ticket %q is not a v1.payload.signature envelope", ticket)
	}
	tampered := parts[0] + "." + parts[1][:len(parts[1])-2] + "AAA" + "." + parts[2]
	if _, err := m.VerifySessionTicket(tampered); !errors.Is(err, ErrTicketSignature) {
		t.Fatalf("verifying a tampered ticket = %v, want ErrTicketSignature", err)
	}

	if _, err := m.VerifySessionTicket("not-a-ticket"); !errors.Is(err, ErrTicketMalformed) {
		t.Fatalf("verifying junk = %v, want ErrTicketMalformed", err)
	}

	// Expiry is the ticket's own, not the session's.
	m.tickets.mu.Lock()
	m.tickets.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	m.tickets.mu.Unlock()
	if _, err := m.VerifySessionTicket(ticket); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("verifying an old ticket = %v, want ErrTicketExpired", err)
	}
}

// TestSessionRebindCarriesTheByteMeter is the headline acceptance test of
// docs/architecture/session-ticket-decision.md: after a rotation the remaining
// allotment is `allotment - consumed`, not `allotment`.
//
// The customer spent 40 MB of a 100 MB session, rotates their address, and the
// session follows them. Before this change the new address had no session at
// all; the naive rebuild — re-grant, then meter from a fresh baseline — would
// have reported 0/100 MB and handed the 40 MB back.
func TestSessionRebindCarriesTheByteMeter(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	ndsctl.setClient(t, ticketMACOld, "Authenticated", 0, 0)
	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)
	startTime := sessionFor(t, m, ticketMACOld).StartTime

	ndsctl.setClient(t, ticketMACOld, "Authenticated", ticketConsumedBytes/1024, 0)
	m.checkDataUsage()

	used, allotment := usageOf(t, m, ticketMACOld)
	if used != ticketConsumedBytes || allotment != ticketAllotmentBytes {
		t.Fatalf("before the rotation usage = %d/%d, want %d/%d", used, allotment, ticketConsumedBytes, ticketAllotmentBytes)
	}

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}

	// The device rotates: its old address leaves the network, and the new one
	// has just associated through the captive portal.
	ndsctl.clearClient(t, ticketMACOld)
	ndsctl.setClient(t, ticketMACNew, "Preauthenticated", 0, 0)

	moved, err := m.RebindSession(ticket, ticketMACNew)
	if err != nil {
		t.Fatalf("RebindSession: %v", err)
	}

	if moved.MacAddress != ticketMACNew {
		t.Fatalf("rebound session is filed under %s, want %s", moved.MacAddress, ticketMACNew)
	}
	if moved.StartTime != startTime {
		t.Fatalf("rebind rewrote StartTime (%d -> %d): a rotation is not a renewal", startTime, moved.StartTime)
	}
	if moved.Allotment != ticketAllotmentBytes {
		t.Fatalf("rebind changed the allotment to %d, want %d", moved.Allotment, ticketAllotmentBytes)
	}
	if moved.Consumed != ticketConsumedBytes {
		t.Fatalf("rebind carried %d consumed bytes, want %d — the meter must carry, never restart", moved.Consumed, ticketConsumedBytes)
	}

	used, allotment = usageOf(t, m, ticketMACNew)
	if used != ticketConsumedBytes {
		t.Fatalf("after the rotation usage = %d, want %d: N rotations must not become N free allotments", used, ticketConsumedBytes)
	}
	if allotment-used != ticketAllotmentBytes-ticketConsumedBytes {
		t.Fatalf("remaining after the rotation = %d, want %d (allotment - consumed)", allotment-used, ticketAllotmentBytes-ticketConsumedBytes)
	}

	// The session is delivered to the new address only.
	if raw, err := m.GetUsage(ticketMACOld); err != nil || raw != "-1/-1" {
		t.Fatalf("the address the session left answers %q (err %v), want -1/-1", raw, err)
	}

	// And the meter keeps counting from there, not from zero: the new attachment
	// is measured against its own baseline, and its traffic adds to what carried.
	ndsctl.setClient(t, ticketMACNew, "Authenticated", ticketExtraBytes/1024, 0)
	m.checkDataUsage()
	used, _ = usageOf(t, m, ticketMACNew)
	if used != ticketConsumedBytes+ticketExtraBytes {
		t.Fatalf("usage on the new attachment after %d MB of new traffic = %d, want %d (carried + new)",
			ticketExtraBytes/(1024*1024), used, ticketConsumedBytes+ticketExtraBytes)
	}
}

// TestSessionRebindCarriesAcrossSeveralRotations is the "N rotations = N free
// allotments" case stated as a test: three addresses, one ledger.
func TestSessionRebindCarriesAcrossSeveralRotations(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	ndsctl.setClient(t, ticketMACOld, "Authenticated", 0, 0)
	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)

	ndsctl.setClient(t, ticketMACOld, "Authenticated", ticketConsumedBytes/1024, 0)
	m.checkDataUsage()

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}

	ndsctl.clearClient(t, ticketMACOld)
	ndsctl.setClient(t, ticketMACNew, "Preauthenticated", 0, 0)
	if _, err := m.RebindSession(ticket, ticketMACNew); err != nil {
		t.Fatalf("first rotation: %v", err)
	}

	// The customer uses 20 MB more from the new address.
	ndsctl.setClient(t, ticketMACNew, "Authenticated", ticketExtraBytes/1024, 0)
	m.checkDataUsage()

	used, _ := usageOf(t, m, ticketMACNew)
	if used != ticketConsumedBytes+ticketExtraBytes {
		t.Fatalf("usage after 40 MB + 20 MB = %d, want %d", used, ticketConsumedBytes+ticketExtraBytes)
	}

	// A second rotation: same ticket, third address.
	ndsctl.clearClient(t, ticketMACNew)
	ndsctl.setClient(t, ticketMACAlt, "Preauthenticated", 0, 0)
	if _, err := m.RebindSession(ticket, ticketMACAlt); err != nil {
		t.Fatalf("second rotation: %v", err)
	}

	used, allotment := usageOf(t, m, ticketMACAlt)
	if used != ticketConsumedBytes+ticketExtraBytes {
		t.Fatalf("usage after two rotations = %d, want %d: the meter must survive every rotation", used, ticketConsumedBytes+ticketExtraBytes)
	}
	if used == 0 || used == allotment {
		t.Fatalf("usage after two rotations = %d/%d: neither a fresh allotment nor a spent one", used, allotment)
	}
}

// TestSessionRebindTakesTheLiveReadingWhenTheAttachmentIsStillListed covers the
// other half of the carry: a deauthenticated attachment whose client record is
// still in NoDogSplash answers with its counters, so the bytes used since the
// last monitor sweep are carried rather than round-trip-lost.
func TestSessionRebindTakesTheLiveReadingWhenTheAttachmentIsStillListed(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	ndsctl.setClient(t, ticketMACOld, "Authenticated", 0, 0)
	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)

	ndsctl.setClient(t, ticketMACOld, "Authenticated", ticketConsumedBytes/1024, 0)
	m.checkDataUsage()

	// 5 MB more, then the rotation: the monitor has not swept since, but the
	// counters are still readable for the address being left.
	usedSinceLastSweep := uint64(5 * 1024 * 1024)
	ndsctl.setClient(t, ticketMACOld, "Preauthenticated", (ticketConsumedBytes+usedSinceLastSweep)/1024, 0)

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}
	ndsctl.setClient(t, ticketMACNew, "Preauthenticated", 0, 0)

	if _, err := m.RebindSession(ticket, ticketMACNew); err != nil {
		t.Fatalf("RebindSession: %v", err)
	}

	used, _ := usageOf(t, m, ticketMACNew)
	if used != ticketConsumedBytes+usedSinceLastSweep {
		t.Fatalf("carried usage = %d, want %d: the live reading of the address being left is higher than the last sweep's and must be what carries",
			used, ticketConsumedBytes+usedSinceLastSweep)
	}
}

// TestSessionRebindRefusesWhileTheOldAttachmentIsAuthenticated is invariant 3:
// one session is delivered to one live address. Without this refusal a copied
// ticket — or a rotation the device performed while it was still associated —
// would put the same session on two clients at once.
func TestSessionRebindRefusesWhileTheOldAttachmentIsAuthenticated(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	ndsctl.setClient(t, ticketMACOld, "Authenticated", 0, 0)
	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)
	ndsctl.setClient(t, ticketMACNew, "Preauthenticated", 0, 0)

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}

	if _, err := m.RebindSession(ticket, ticketMACNew); !errors.Is(err, ErrAttachmentActive) {
		t.Fatalf("rebind while the old attachment is authenticated = %v, want ErrAttachmentActive", err)
	}

	// Nothing moved: the session is still delivered to the address that is live,
	// and the address that asked has no session at all.
	if raw, err := m.GetUsage(ticketMACNew); err != nil || raw != "-1/-1" {
		t.Fatalf("the address that asked for the refused rebind answers %q (err %v), want -1/-1", raw, err)
	}
	session := sessionFor(t, m, ticketMACOld)
	if session.MacAddress != ticketMACOld {
		t.Fatalf("the refused rebind still moved the session to %s", session.MacAddress)
	}

	// Once the old address is gone, the same ticket works.
	ndsctl.clearClient(t, ticketMACOld)
	if _, err := m.RebindSession(ticket, ticketMACNew); err != nil {
		t.Fatalf("rebind after the old attachment left: %v", err)
	}
}

// TestSessionRebindPreservesPaidTime is invariant 2 on the metric where it bites
// hardest: a milliseconds session has no meter to carry, only a StartTime, and
// computing it afresh (as AddAllotment does for a renewal) would hand back every
// second the customer had already spent.
func TestSessionRebindPreservesPaidTime(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "milliseconds")

	const allotmentMS = 600000 // 10 minutes
	const spent = 120 * time.Second

	ndsctl.setClient(t, ticketMACOld, "Authenticated", 0, 0)
	wantStart := time.Now().Add(-spent).Unix()
	m.sessionMu.Lock()
	m.customerSessions[ticketMACOld] = &CustomerSession{
		MacAddress: ticketMACOld,
		StartTime:  wantStart,
		Metric:     "milliseconds",
		Allotment:  allotmentMS,
	}
	m.sessionMu.Unlock()

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}

	ndsctl.clearClient(t, ticketMACOld)
	ndsctl.setClient(t, ticketMACNew, "Preauthenticated", 0, 0)

	moved, err := m.RebindSession(ticket, ticketMACNew)
	if err != nil {
		t.Fatalf("RebindSession: %v", err)
	}
	if moved.StartTime != wantStart {
		t.Fatalf("StartTime after the rebind = %d, want %d verbatim: a rotation must never rewrite it (that is what hands back the %s already spent)",
			moved.StartTime, wantStart, spent)
	}

	used, allotment := usageOf(t, m, ticketMACNew)
	if allotment != allotmentMS {
		t.Fatalf("allotment after the rebind = %d, want %d", allotment, allotmentMS)
	}
	if used < uint64(spent.Milliseconds()) || used == 0 {
		t.Fatalf("time usage after the rebind = %d ms, want at least the %d ms already spent (never a fresh allotment)",
			used, spent.Milliseconds())
	}
}

// TestRebindIsIdempotentAtTheAttachedAddress: a portal that retries a rotation
// it already performed must not double-count or error.
func TestRebindIsIdempotentAtTheAttachedAddress(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	ndsctl.setClient(t, ticketMACOld, "Authenticated", 0, 0)
	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)
	ndsctl.setClient(t, ticketMACOld, "Authenticated", ticketConsumedBytes/1024, 0)
	m.checkDataUsage()

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}

	first, err := m.RebindSession(ticket, ticketMACOld)
	if err != nil {
		t.Fatalf("rebind at the attached address: %v", err)
	}
	if first.MacAddress != ticketMACOld {
		t.Fatalf("rebind at the attached address moved the session to %s", first.MacAddress)
	}
	if first.Consumed != 0 {
		t.Fatalf("carried total at the attached address = %d, want 0: nothing was left behind, so nothing carries", first.Consumed)
	}

	used, _ := usageOf(t, m, ticketMACOld)
	if used != ticketConsumedBytes {
		t.Fatalf("usage after a no-op rebind = %d, want %d", used, ticketConsumedBytes)
	}

	second, err := m.RebindSession(ticket, ticketMACOld)
	if err != nil {
		t.Fatalf("second rebind at the attached address: %v", err)
	}
	if second.MacAddress != ticketMACOld {
		t.Fatalf("second no-op rebind moved the session to %s", second.MacAddress)
	}

	used, _ = usageOf(t, m, ticketMACOld)
	if used != ticketConsumedBytes {
		t.Fatalf("usage after a repeated rebind = %d, want %d: a retry must not count the same bytes twice", used, ticketConsumedBytes)
	}
}

// TestRebindCannotMoveASessionTheTicketNeverNamed: a handle names ONE session. A
// session that was retired and re-bought under the same MAC is a different
// session, and a ticket issued before that must not be able to move it — that is
// what stops an old ticket from becoming a standing claim on an address.
func TestRebindCannotMoveASessionTheTicketNeverNamed(t *testing.T) {
	ndsctl := installRotatingNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	ndsctl.setClient(t, ticketMACOld, "Authenticated", 0, 0)
	bytesSessionFor(t, m, ticketMACOld, ticketAllotmentBytes)

	ticket, _, err := m.IssueSessionTicket(ticketMACOld)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}

	// The session runs out and is retired; the MAC buys a new one.
	m.sessionMu.Lock()
	m.expireSessionLocked(ticketMACOld)
	m.sessionMu.Unlock()
	if _, err := m.AddAllotment(ticketMACOld, "bytes", ticketAllotmentBytes); err != nil {
		t.Fatalf("AddAllotment for the new session: %v", err)
	}

	ndsctl.clearClient(t, ticketMACOld)
	ndsctl.setClient(t, ticketMACNew, "Preauthenticated", 0, 0)

	if _, err := m.RebindSession(ticket, ticketMACNew); !errors.Is(err, ErrTicketUnknown) {
		t.Fatalf("rebind with a ticket from the retired session = %v, want ErrTicketUnknown", err)
	}

	// The new session stays where it was bought.
	if _, err := m.GetSession(ticketMACOld); err != nil {
		t.Fatalf("the new session was moved off its own address: %v", err)
	}
	if _, err := m.GetUsage(ticketMACNew); err != nil {
		t.Fatalf("GetUsage(%s): %v", ticketMACNew, err)
	}
}
