package merchant

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// A DEFINITIVE issuer refusal must retire the record; an ambiguous one must not.
//
// Client.Melt already distinguishes the two: IssuerError.Definitive is true for the refusals
// LUD-25 defines as permanent ("already in use", "unknown note"), which say the note will never
// work at this issuer. Before this test existed the melt branch ignored the flag entirely, so a
// definitive refusal left the record `awaiting` and told the customer "if payment went through,
// access will follow shortly" — which is false, because nothing was spent. The note stayed
// wedged and the slot stayed held.
//
// The distinction is the whole point: conflating "the issuer said no" with "we do not know" is
// what turns a refusal into a permanent wedge.
func TestDefinitiveIssuerRefusalRetiresTheRecordButAmbiguousDoesNot(t *testing.T) {
	cases := []struct {
		name string
		melt error
		// wantRefusedAfter reports whether a SECOND presentation must still be refused.
		// A definitive refusal is spent, so the ordinary `failed` history keeps refusing it.
		wantState paymentjournal.State
	}{
		{
			name: "definitive refusal",
			melt: &lnurlcash.IssuerError{Reason: "already in use", Definitive: true},
			// `failed` is terminal: the note is gone at this issuer and the slot is freed.
			wantState: paymentjournal.Failed,
		},
		{
			name: "ambiguous transport failure",
			melt: &lnurlcash.IssuerError{Err: errors.New("connection reset by peer")},
			// Must stay `awaiting`: the issuer may have burned the note.
			wantState: paymentjournal.Awaiting,
		},
		{
			name:      "ambiguous 5xx-shaped failure with a reason",
			melt:      &lnurlcash.IssuerError{Reason: "internal error"},
			wantState: paymentjournal.Awaiting,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newCrashTestStore(t)
			rail := NewLNURLCashRail(
				store,
				0,
				func(macAddress, mintURL string, sats uint64) (string, string, error) {
					return "quote-1", "lnbc10u1pdefinitive", nil
				},
				func(n lnurlcash.Note, cb IssuerCallback, invoice string) error {
					return tc.melt
				},
			)

			note, err := lnurlcash.Parse(fmt.Sprintf("%064x", 0xd1), "mint.example")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); err == nil {
				t.Fatal("the melt failed but Redeem reported success")
			}

			ops, lerr := store.List()
			if lerr != nil {
				t.Fatalf("list: %v", lerr)
			}
			var found *paymentjournal.Operation
			for i := range ops {
				if ops[i].Direction == paymentjournal.Incoming {
					found = &ops[i]
					break
				}
			}
			if found == nil {
				t.Fatal("the attempt wrote no journal record at all, so this test is asserting about nothing")
			}
			if found.State != tc.wantState {
				t.Fatalf("record state = %q, want %q (a definitive refusal must retire, an ambiguous one must not)",
					found.State, tc.wantState)
			}

			// And the note is still refused either way: `failed` refuses through the retry
			// ledger, `awaiting` refuses through the pending guard. Neither path may reopen it.
			if _, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); err == nil {
				t.Fatal("the note was accepted again after its melt failed — a refused note must not be redeemable twice")
			}
		})
	}
}

// The previous behaviour, pinned so a regression is visible in words rather than only in a
// state string: an ambiguous outcome must never be reported to the customer as a success.
func TestAmbiguousMeltIsNotReportedAsGranted(t *testing.T) {
	store := newCrashTestStore(t)
	rail := NewLNURLCashRail(
		store, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-1", "lnbc10u1pambiguous", nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error {
			return &lnurlcash.IssuerError{Err: errors.New("timeout")}
		},
	)
	note, _ := lnurlcash.Parse(fmt.Sprintf("%064x", 0xd2), "mint.example")
	_, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example")
	if err == nil {
		t.Fatal("an ambiguous melt returned no error")
	}
	if strings.Contains(strings.ToLower(err.Error()), "grant") {
		t.Fatalf("the ambiguous-melt error claims a grant: %v", err)
	}
}
