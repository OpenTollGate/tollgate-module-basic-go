package merchant

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// ONE NOTE MUST HAVE ONE IDENTITY, WHATEVER FORM IT ARRIVES IN.
//
// The replay guard is keyed on noteFingerprint. If two encodings of the same note produce
// two fingerprints, the rail journals two records and the note can be redeemed twice —
// the invariant "a second presentation never grants twice" is defeated entirely by a
// customer pasting the link instead of the bare k1.
//
// A sibling implementation of this rail measured exactly that: the URL form and the
// bech32 form of ONE note gave 2819fac9... and 9b0c62..., two records, two grants. The
// lnurlcash package proves the decoded Q converges for the two forms; nothing proved the
// RAIL's fingerprint does, and that gap is where the sibling's hole lived.
//
// The vectors are LUD-25's own: vecPreimage is the k1 short form, vecCW1 is the full
// bech32 form of the same bearer spend.
func TestEveryEncodingOfOneNoteHasOneFingerprint(t *testing.T) {
	const preimage = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	const cw1 = "cw1qqqqqq8lllll7qpr4qsxxrwd99nvgvmxjyf9gj9mkfd5laqj5jw8xtdjez4urwzcr0t3phv8qqsuq5yjnd6vrgzf2jmckjmqxh5h5hs83fdq728vjm2500lwnt8gqwkqqqsqqqgzqvzq2ps8pqys5zcvp58q7yq3zgf3g9gkzuvpjxsmrsw3u8c6x6a4c"
	const domain = "mint.example"

	forms := map[string]lnurlcash.Note{}

	bare, err := lnurlcash.Parse(preimage, domain)
	if err != nil {
		t.Fatalf("bare k1 form: %v", err)
	}
	forms["bare k1"] = bare

	fromURL, err := lnurlcash.Parse("lnurlw://"+domain+"/w?k1="+preimage, "")
	if err != nil {
		t.Fatalf("withdraw-link form: %v", err)
	}
	forms["withdraw link"] = fromURL

	fromCW1, err := lnurlcash.Parse("lnurlw://"+domain+"/w?k1="+cw1, "")
	if err != nil {
		t.Fatalf("cw1 form: %v", err)
	}
	forms["cw1"] = fromCW1

	// The decoded spend must agree first: that is WHY the fingerprints agree, and if
	// noteFingerprint ever changes to hash the raw input instead, this is the assertion
	// that explains the failure above it.
	want := bare.Spend()
	var fp string
	for name, n := range forms {
		if n.Spend() != want {
			t.Errorf("%s decoded to spend %s, want %s", name, n.Spend(), want)
		}
		f := noteFingerprint(n.Spend())
		if fp == "" {
			fp = f
			continue
		}
		if f != fp {
			t.Fatalf("two encodings of ONE note produced two fingerprints (%s vs %s) — two journal records, and the note can be redeemed twice", fp, f)
		}
	}

	// And the rail itself must refuse the second presentation: the fingerprint agreeing
	// is a means, not the end. This is the customer-visible property.
	store := newCrashTestStore(t)
	melts := 0
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-fp", "lnbc10u1pexample", nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error {
			melts++
			return nil
		},
	)
	if _, err := rail.Redeem(bare, 21000, "00:00:00:00:00:01", "https://mint.example"); err != nil {
		t.Fatalf("first presentation should have proceeded: %v", err)
	}
	if _, err := rail.RedeemAt(fromURL, 21000, "00:00:00:00:00:01", "https://mint.example", ""); err != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("the SAME note presented as a withdraw link was not refused as a replay (got %v) — one note, two grants", err)
	}
	if _, err := rail.Redeem(fromCW1, 21000, "00:00:00:00:00:01", "https://mint.example"); err != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("the SAME note presented as cw1 was not refused as a replay (got %v)", err)
	}

	ops, lerr := store.List()
	if lerr != nil {
		t.Fatalf("listing the journal: %v", lerr)
	}
	incoming := 0
	for _, op := range ops {
		if op.Direction == paymentjournal.Incoming {
			incoming++
		}
	}
	if incoming != 1 {
		t.Fatalf("one note left %d journal records; three presentations of one note must leave exactly one", incoming)
	}
	if melts != 1 {
		t.Fatalf("one note reached the melt %d times; a second presentation must never melt again", melts)
	}
}
