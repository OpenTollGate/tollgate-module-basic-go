package merchant

import (
	"sync"
	"testing"
	"time"
)

// Regression test for the data race the session-ticket review found (blocker 2):
// the usage monitor writes CustomerSession.attachmentUsage on every sweep
// (noteAttachmentUsage), while GetSession cloned the shared record AFTER
// releasing sessionMu — so every /usage and /balance poll read a field a live
// record mutates.
//
// The clone-outside-the-lock pattern was benign until a field of a live record
// started mutating in place: before the rebind work, no field of a tracked
// session was ever written after creation, so copying it outside the lock was
// safe. attachmentUsage is the first one that is.
//
// Nothing in the shipped suite polled concurrently with the monitor, which is
// why -race stayed quiet; on the 32-bit mips/mipsel router targets a torn uint64
// read is a real misread, not a theoretical one.
//
// The shape is the production one: the monitor's write path
// (noteAttachmentUsage, called by checkDataUsage every 2 s sweep) raced against
// the read path every poller takes (GetSession, which /usage, /balance and
// /session-state all call). Run under -race.
func TestSessionAttachmentUsageIsClonedUnderTheLock(t *testing.T) {
	const mac = "aa:bb:cc:dd:ee:07"
	const sweeps = 5000

	m := newRaceProbeMerchant(mac)

	var wg sync.WaitGroup

	// The monitor goroutine: exactly the write checkDataUsage performs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= sweeps; i++ {
			m.noteAttachmentUsage(mac, uint64(i))
		}
	}()

	// Four pollers: exactly the read an HTTP handler performs.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < sweeps; j++ {
				session, err := m.GetSession(mac)
				if err != nil {
					t.Errorf("GetSession(%s): %v", mac, err)
					return
				}
				if session.MacAddress != mac {
					t.Errorf("clone lost the MAC: got %q, want %q", session.MacAddress, mac)
					return
				}
			}
		}()
	}

	wg.Wait()

	// The clone must carry the highest usage the monitor recorded: a rebind
	// carries this figure forward when the attachment's counters are gone, so a
	// torn or stale read here is a lost interval of the customer's meter.
	session, err := m.GetSession(mac)
	if err != nil {
		t.Fatalf("GetSession after the run: %v", err)
	}
	if session.attachmentUsage != sweeps {
		t.Errorf("attachmentUsage carried by the clone = %d, want %d", session.attachmentUsage, sweeps)
	}
}

// The same race through the wire-facing entry point: GetUsage's milliseconds
// branch is what /usage answers with, and it reads the clone GetSession returns.
// Kept hermetic on purpose — a milliseconds session never touches ndsctl.
func TestGetUsageIsConcurrencySafeAgainstTheMonitor(t *testing.T) {
	const mac = "aa:bb:cc:dd:ee:08"
	const sweeps = 2000

	m := newRaceProbeMerchant(mac)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= sweeps; i++ {
			m.noteAttachmentUsage(mac, uint64(i))
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < sweeps; j++ {
				if _, err := m.GetUsage(mac); err != nil {
					t.Errorf("GetUsage(%s): %v", mac, err)
					return
				}
			}
		}()
	}

	wg.Wait()
}

// newRaceProbeMerchant builds the minimum Merchant the two paths above touch: a
// tracked milliseconds session (so GetUsage never reaches ndsctl) with a fresh
// start time (so GetSession does not take the expiry path).
func newRaceProbeMerchant(mac string) *Merchant {
	m := &Merchant{customerSessions: map[string]*CustomerSession{}}
	m.customerSessions[mac] = &CustomerSession{
		MacAddress: mac,
		StartTime:  time.Now().Unix(),
		Metric:     "milliseconds",
		Allotment:  uint64(time.Hour / time.Millisecond),
	}
	return m
}
