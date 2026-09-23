package merchant

import (
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The session-state contract. `/usage` answers "-1/-1" for a MAC with no session
// — the same bytes for a device that has never paid and for one whose paid
// session ran out — so a portal cannot tell a first-time visitor from an expiring
// customer. GetSessionState is the read-only, machine-readable answer:
//
//	none    the MAC has never had a session
//	active  the MAC has a session with allotment left
//	expired the MAC had a session that is used up (the record is gone)
//
// The state is derived from live session records plus a bounded in-memory
// history of MACs whose session was observed to expire, so it survives repeated
// lookups after the session record itself has been deleted. Like every other
// enforcement datum in this module it is process-memory: a daemon restart
// forgets it and answers "none", which is the same answer a restart gives for
// the session itself.

func TestGetSessionStateNoneForUnknownMAC(t *testing.T) {
	m := &Merchant{}

	state, err := m.GetSessionState(renewalMAC)
	if err != nil {
		t.Fatalf("GetSessionState returned error: %v", err)
	}
	if state != SessionStateNone {
		t.Fatalf("GetSessionState for a MAC that never paid = %q, want %q", state, SessionStateNone)
	}
}

func TestGetSessionStateActiveForLiveSession(t *testing.T) {
	m := &Merchant{
		customerSessions: map[string]*CustomerSession{
			renewalMAC: {
				MacAddress: renewalMAC,
				StartTime:  time.Now().Add(-time.Second).Unix(),
				Metric:     "milliseconds",
				Allotment:  5000,
			},
		},
	}

	state, err := m.GetSessionState(renewalMAC)
	if err != nil {
		t.Fatalf("GetSessionState returned error: %v", err)
	}
	if state != SessionStateActive {
		t.Fatalf("GetSessionState for a live session = %q, want %q", state, SessionStateActive)
	}
}

// An expired session must read "expired" every time it is asked, including after
// the lookup that removed the record — a portal polls this endpoint repeatedly
// while it renders the renewal screen.
func TestGetSessionStateExpiredIsStableAcrossLookups(t *testing.T) {
	m := &Merchant{
		customerSessions: map[string]*CustomerSession{
			renewalMAC: {
				MacAddress: renewalMAC,
				StartTime:  time.Now().Add(-3 * time.Second).Unix(),
				Metric:     "milliseconds",
				Allotment:  1000,
			},
		},
	}

	for i := 1; i <= 3; i++ {
		state, err := m.GetSessionState(renewalMAC)
		if err != nil {
			t.Fatalf("lookup %d returned error: %v", i, err)
		}
		if state != SessionStateExpired {
			t.Fatalf("lookup %d returned %q, want %q", i, state, SessionStateExpired)
		}
		if _, exists := m.customerSessions[renewalMAC]; exists {
			t.Fatalf("lookup %d left the spent session record in place", i)
		}
	}

	// The distinction this contract exists for: /usage still answers -1/-1 (the
	// deployed portal parses those bytes) while the state says which -1/-1 it is.
	usage, err := m.GetUsage(renewalMAC)
	if err != nil {
		t.Fatalf("GetUsage returned error: %v", err)
	}
	if usage != "-1/-1" {
		t.Fatalf("GetUsage = %q, want %q", usage, "-1/-1")
	}
}

// The bytes metric has no lazy expiry: the usage monitor closes the gate and
// drops the record when the allotment is reached. That path must record the
// expiry too, otherwise a bytes customer is reported as a first-time visitor.
func TestGetSessionStateExpiredAfterUsageMonitorClosesGate(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	m.sessionMu.Lock()
	m.customerSessions[renewalMAC] = &CustomerSession{
		MacAddress: renewalMAC,
		StartTime:  time.Now().Unix(),
		Metric:     "bytes",
		Allotment:  1,
	}
	m.sessionMu.Unlock()

	if err := valve.SetDataBaseline(renewalMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}
	// Drive the reported usage well past the 1-byte allotment.
	ndsctl.setClientKB(t, 4096, 512)

	deauthsBefore := ndsctl.count(t, "DEAUTH ")
	m.checkDataUsage()

	if _, exists := m.customerSessions[renewalMAC]; exists {
		t.Fatal("expected the usage monitor to drop the spent session record")
	}
	if got := ndsctl.count(t, "DEAUTH ") - deauthsBefore; got != 1 {
		t.Fatalf("ndsctl deauth calls from the usage monitor = %d, want 1", got)
	}

	state, err := m.GetSessionState(renewalMAC)
	if err != nil {
		t.Fatalf("GetSessionState returned error: %v", err)
	}
	if state != SessionStateExpired {
		t.Fatalf("GetSessionState after the usage monitor expired the session = %q, want %q", state, SessionStateExpired)
	}
}

// #537 made MAC handling case-insensitive at the API boundary; the state lookup
// must not reintroduce a second spelling of the same client.
func TestGetSessionStateIsCaseInsensitive(t *testing.T) {
	m := &Merchant{
		customerSessions: map[string]*CustomerSession{
			renewalMAC: {
				MacAddress: renewalMAC,
				StartTime:  time.Now().Add(-time.Second).Unix(),
				Metric:     "milliseconds",
				Allotment:  5000,
			},
		},
	}

	for _, spelling := range []string{"aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF", "Aa:Bb:Cc:Dd:Ee:Ff", "  aa:bb:cc:dd:ee:ff  "} {
		state, err := m.GetSessionState(spelling)
		if err != nil {
			t.Fatalf("GetSessionState(%q) returned error: %v", spelling, err)
		}
		if state != SessionStateActive {
			t.Fatalf("GetSessionState(%q) = %q, want %q", spelling, state, SessionStateActive)
		}
	}
}

// The /usage contract at its source, unchanged by the state work: the endpoint
// is a bare `used/total` string and "-1/-1" when there is no session. The
// shipped portal parses exactly these bytes, so this stays a regression pin.
func TestGetUsageContractUnchanged(t *testing.T) {
	t.Run("no session", func(t *testing.T) {
		m := &Merchant{}

		usage, err := m.GetUsage(renewalMAC)
		if err != nil {
			t.Fatalf("GetUsage returned error: %v", err)
		}
		if usage != "-1/-1" {
			t.Fatalf("GetUsage = %q, want %q", usage, "-1/-1")
		}
	})

	t.Run("live milliseconds session", func(t *testing.T) {
		m := &Merchant{
			customerSessions: map[string]*CustomerSession{
				renewalMAC: {
					MacAddress: renewalMAC,
					StartTime:  time.Now().Add(-123 * time.Second).Unix(),
					Metric:     "milliseconds",
					Allotment:  600000,
				},
			},
		}

		usage, err := m.GetUsage(renewalMAC)
		if err != nil {
			t.Fatalf("GetUsage returned error: %v", err)
		}

		parts := strings.Split(usage, "/")
		if len(parts) != 2 {
			t.Fatalf("GetUsage = %q, want used/total", usage)
		}
		if parts[1] != "600000" {
			t.Fatalf("GetUsage total = %q, want the session allotment 600000", parts[1])
		}
		used, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			t.Fatalf("GetUsage used %q is not a number: %v", parts[0], err)
		}
		// Elapsed time at call time: ~123 s, in ms, with a second of slack.
		if used < 123000 || used > 124000 {
			t.Fatalf("GetUsage used = %d ms, want ~123000", used)
		}
	})
}

// A purchase that could not open the gate (#403's rollback path) must not be
// remembered as an expiry: the customer never had access, and marking the MAC
// expired would also let the next attempt skip the fund-safety pre-flight.
func TestFailedGateOpenRollbackDoesNotMarkTheMACExpired(t *testing.T) {
	m := &Merchant{customerSessions: make(map[string]*CustomerSession)}

	session, err := m.AddAllotment(renewalMAC, "milliseconds", 1000)
	if err != nil {
		t.Fatalf("AddAllotment: %v", err)
	}
	// grantSessionAccess restores the previous state when the gate cannot be
	// opened; for a first-time purchase that means "no session at all".
	m.restoreSession(renewalMAC, nil, false)
	if _, exists := m.customerSessions[renewalMAC]; exists {
		t.Fatalf("expected the rolled-back session for %s to be gone (had %+v)", renewalMAC, session)
	}

	state, err := m.GetSessionState(renewalMAC)
	if err != nil {
		t.Fatalf("GetSessionState returned error: %v", err)
	}
	if state != SessionStateNone {
		t.Fatalf("state after a failed gate open = %q, want %q", state, SessionStateNone)
	}
}
