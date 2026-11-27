package merchant

import (
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// A SECOND DEVICE MUST NOT BE TOLD A NOTE WAS GRANTED.
//
// The other LNURLcash lane fixed a defect where a note granted to one device answered a second
// device with the first device's success, so the second got access it had not paid for. That
// lane's design returns the grant AS the response, so the confusion was possible there.
//
// This rail's shape should make it impossible: the grant is never a response to a redemption
// request. Redeem either returns a quoteId (money moved, access comes later via the existing
// Lightning grant path, bound to the presenting device's MAC) or it refuses. There is no code
// path that answers device B with device A's outcome.
//
// Asserting that by reading the code is what I have been wrong about three times today, so
// this measures it: device A pays and its quote settles, then device B presents the same note
// and must get a refusal — not a quoteId, and not A's quote.
//
// KNOWN WEAKNESS, recorded because it bit me: this test passes even if the durable clause is
// broken, because the in-memory byNote set refuses device B first and the journal is never
// consulted. It is a fine end-to-end check of the refusal's shape (no quote id, no second
// invoice, one paid record) but it does NOT test durability. TestASecondDeviceIsRefusedAfterARestartToo
// does, and that is the one that fails when the paid-state clause is removed.
func TestASecondDeviceIsRefusedRatherThanToldItWasGranted(t *testing.T) {
	store := newCrashTestStore(t)
	quotes := 0
	rail := NewLNURLCashRail(
		store, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			quotes++
			return "quote-for-" + macAddress, "lnbc10u1pe" + macAddress, nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error { return nil },
	)

	note, err := lnurlcash.Parse("303132333435363738393a3b3c3d3e3f404142434445464748494a4b4c4d4e4f", "mint.example")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	const deviceA, deviceB = "aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"

	qA, err := rail.Redeem(note, 21000, deviceA, "https://mint.example")
	if err != nil {
		t.Fatalf("device A redeem: %v", err)
	}
	if qA != "quote-for-"+deviceA {
		t.Fatalf("device A got quote %q, not its own", qA)
	}
	// A's sale settles: A has been granted access.
	rail.Settle("lnbc10u1pe" + deviceA)

	// Device B now presents the SAME note.
	qB, errB := rail.Redeem(note, 21000, deviceB, "https://mint.example")
	if errB == nil {
		t.Fatalf("device B was handed quote %q for a note device A already paid with — that is a second grant from one note", qB)
	}
	if errB != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("device B got %v, want the replay refusal; any other error text risks reading as an outcome", errB)
	}
	if qB != "" {
		t.Fatalf("a refused redemption returned a quote id %q; a caller could mistake that for A's outcome", qB)
	}
	// And B must not have caused a second invoice.
	if quotes != 1 {
		t.Fatalf("%d invoices were requested; device B's presentation reached the mint instead of being refused locally", quotes)
	}
	// The journal must still describe exactly one sale.
	ops, _ := store.List()
	var paid int
	for _, op := range ops {
		if op.State == paymentjournal.Paid {
			paid++
		}
	}
	if paid != 1 {
		t.Fatalf("%d paid records; one note paid once must journal once", paid)
	}
}

// THE SAME QUESTION ACROSS A RESTART, WHERE `byNote` IS EMPTY.
//
// The test above passed even when I made paid notes re-presentable, because the in-memory
// byNote set caught device B first and the durable clause was never reached. So it proves the
// process still remembers, which is the weak version. The claim that matters is the one in the
// ADR: that the refusal survives a reboot. This closes the store, reopens it from disk, and
// asks with a completely empty memory.
func TestASecondDeviceIsRefusedAfterARestartToo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")

	store, err := paymentjournal.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rail := NewLNURLCashRail(
		store, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-a", "lnbc10u1peDEVICEA", nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error { return nil },
	)
	note, _ := lnurlcash.Parse("505152535455565758595a5b5c5d5e5f606162636465666768696a6b6c6d6e6f", "mint.example")
	if _, err := rail.Redeem(note, 21000, "aa:bb:cc:dd:ee:01", "https://mint.example"); err != nil {
		t.Fatalf("device A redeem: %v", err)
	}
	rail.Settle("lnbc10u1peDEVICEA")
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// --- reboot: empty memory, journal from disk ---
	store2, err := paymentjournal.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = store2.Close() }()

	melted := false
	rail2 := NewLNURLCashRail(
		store2, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			t.Fatal("device B was sent to the mint for a note device A already paid with, after a restart")
			return "", "", nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error { melted = true; return nil },
	)
	qB, errB := rail2.Redeem(note, 21000, "aa:bb:cc:dd:ee:02", "https://mint.example")
	if errB != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("after a restart device B got %v, want the replay refusal — with empty memory only the journal stands between B and a second grant", errB)
	}
	if qB != "" || melted {
		t.Fatalf("device B obtained %q / reached the issuer (%v) after a restart", qB, melted)
	}
}
