package merchant

import (
	"errors"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The boundary test reviewer blocker 3 asked for: the janitor and the session
// ticket must not undo each other.
//
// The collision: entitlement is keyed to a MAC address, so #575's janitor retires
// the record of an address whose client has gone (~60 s after it drops off the NDS
// client list, with the grace window). But RebindSession needs that record to exist
// — it looks the session up and refuses with ErrTicketUnknown when it is gone. A
// device that took longer than the grace window to come back (a lid-closed laptop
// resuming, an iOS device that rotates at re-association) therefore presented a
// perfectly valid ticket and lost the remainder it had paid for. That is exactly
// the loss this branch exists to prevent.
//
// The decision: retirement yields to a live ticket. The record is PARKED — the gate
// is still closed (access is not what is being preserved; the address is
// deauthorised either way, so nothing is inherited and nothing is left unmetered)
// and the record lives only until the ticket's own horizon.
//
// Both directions are asserted here, because a parking rule that never retires is
// just a leak: a live horizon parks, an expired horizon retires.

// TestStaleBindingParksASessionThatStillHoldsALiveTicket is the direction that
// protects the customer's money.
func TestStaleBindingParksASessionThatStillHoldsALiveTicket(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, janitorBytesMAC)
	t.Cleanup(func() { closeGateCleanup(t, ticketMACNew) })

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	if err := valve.SetDataBaseline(janitorBytesMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}
	// Counters of the abandoned address: readable, frozen, far below allotment.
	ndsctl.setClientKB(t, 1024, 512)

	// A ticket issued through the real path is what makes the remainder
	// claimable — and it is the only reason the record may be parked.
	ticket, expiresAt, err := m.IssueSessionTicket(janitorBytesMAC)
	if err != nil {
		t.Fatalf("IssueSessionTicket: %v", err)
	}
	if expiresAt <= time.Now().Unix() {
		t.Fatalf("issued ticket expires at %d, want a future horizon", expiresAt)
	}

	stubClientProbe(t, m, clientIsGone)

	deauthsBefore := deauthsFor(t, ndsctl, janitorBytesMAC)
	sweep(t, m, staleBindingSweeps)

	// The access half is unchanged: the abandoned address must still be closed.
	if got := deauthsFor(t, ndsctl, janitorBytesMAC) - deauthsBefore; got == 0 {
		t.Fatal("the address of a client that is gone is still authorised: parking must not keep access alive, only the record")
	}

	// The record half is the fix: it is the only carrier of the paid remainder.
	if !hasSession(m, janitorBytesMAC) {
		t.Fatal("the session of a departed client that still holds a live ticket was retired: a device returning inside that ticket's horizon would present a valid ticket and get ErrTicketUnknown, losing the remainder it paid for — the exact loss this branch exists to prevent")
	}

	// And the remainder is genuinely still claimable. The assertion is deliberately
	// narrow: a full rebind has to satisfy the invariant-3 probe and a gate
	// authorization, whose fake-ndsctl semantics belong to the rebind tests. What
	// is under test here is the record's survival — so the failure this must catch
	// is ErrTicketUnknown, the refusal the janitor used to cause.
	if _, err := m.RebindSession(ticket, ticketMACNew); errors.Is(err, ErrTicketUnknown) {
		t.Fatalf("a ticket issued before the address was abandoned no longer resolves to a session (%v): the janitor retired the record the customer's remainder lives in", err)
	}
}

// TestStaleBindingRetiresASessionWhoseTicketHorizonHasPassed is the bound: parking
// lasts only as long as the ticket does, so parked records cannot accumulate.
func TestStaleBindingRetiresASessionWhoseTicketHorizonHasPassed(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, janitorBytesMAC)

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	if err := valve.SetDataBaseline(janitorBytesMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}
	ndsctl.setClientKB(t, 1024, 512)

	// A horizon that has already passed: nobody can come back for this remainder,
	// so it is retired exactly as before parking existed.
	m.sessionMu.Lock()
	m.customerSessions[janitorBytesMAC].ticketExpiresAt = time.Now().Add(-time.Minute).Unix()
	m.sessionMu.Unlock()

	stubClientProbe(t, m, clientIsGone)

	sweep(t, m, staleBindingSweeps)

	if hasSession(m, janitorBytesMAC) {
		t.Fatal("a session whose ticket horizon has passed is still tracked: parking must be bounded by the ticket, or the module holds records for devices that can never claim them again")
	}
}
