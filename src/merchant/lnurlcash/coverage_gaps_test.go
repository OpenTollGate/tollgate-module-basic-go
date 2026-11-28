package lnurlcash

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// parseMsat reads an issuer-supplied field out of an untrusted JSON body. Every
// branch here is reachable from a hostile note, so each one is pinned: an absent
// field, a JSON number, a JSON string of digits, a string with padding, and the
// three shapes that must be REFUSED rather than silently coerced to zero.
//
// The refusal half is the load-bearing half. A parser that turned "10,000" or
// "1e9" or "-1" into 0 would make a note look worthless instead of refusing it,
// and a zero claim is a value the caller acts on (dust refusal, or the journalled
// fallback) rather than a parse failure it can see.
func TestParseMsatAcceptsOnlyNumbersAndRefusesEverythingElse(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    uint64
		wantErr bool
	}{
		{name: "json number", raw: `10000`, want: 10000},
		{name: "json number zero", raw: `0`, want: 0},
		{name: "json string of digits", raw: `"10000"`, want: 10000},
		{name: "json string padded", raw: `"  42  "`, want: 42},
		{name: "json string zero", raw: `"0"`, want: 0},

		{name: "absent", raw: ``, wantErr: true},
		{name: "json null", raw: `null`, wantErr: true},
		{name: "empty string", raw: `""`, wantErr: true},
		{name: "whitespace only", raw: `"   "`, wantErr: true},
		{name: "negative", raw: `-1`, wantErr: true},
		{name: "string negative", raw: `"-1"`, wantErr: true},
		{name: "decimal", raw: `"10.5"`, wantErr: true},
		{name: "thousands separators", raw: `"10,000"`, wantErr: true},
		{name: "exponent", raw: `"1e9"`, wantErr: true},
		{name: "hex", raw: `"0x10"`, wantErr: true},
		{name: "bool", raw: `true`, wantErr: true},
		{name: "object", raw: `{"msat":1}`, wantErr: true},
		{name: "suffixed unit", raw: `"10000msat"`, wantErr: true},
		{name: "overflow", raw: `"18446744073709551616"`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMsat(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseMsat(%s) = %d, want an error: a bad value must be "+
						"refused, never coerced to a number the caller acts on", tc.raw, got)
				}
				if got != 0 {
					t.Fatalf("parseMsat(%s) returned %d alongside its error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMsat(%s): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parseMsat(%s) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// Overflow must be refused at the exact boundary rather than wrapping. The value is
// satoshis' worth of millisatoshis from a third party; a wrapped number is a note
// that appears to be worth something arbitrary.
func TestParseMsatRefusesOverflowWithoutWrapping(t *testing.T) {
	// MaxUint64 in decimal: accepted.
	if got, err := parseMsat(json.RawMessage(`"18446744073709551615"`)); err != nil || got != ^uint64(0) {
		t.Fatalf("max uint64 should parse: got %d err %v", got, err)
	}
	// One above it: refused.
	if _, err := parseMsat(json.RawMessage(`"18446744073709551616"`)); err == nil {
		t.Fatal("max uint64 + 1 must be refused, not wrapped")
	}
}

// List feeds operator tooling and reconciliation. It must return the records
// oldest-first (the order an operator reads a journal in), survive a closed store
// by refusing rather than reading a nil map, and actually contain what was written.
func TestListReturnsEveryRecordOldestFirstAndRefusesWhenClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenIntentStore(filepath.Join(dir, "intents.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}

	empty, err := OpenIntentStore(filepath.Join(t.TempDir(), "empty.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore(empty): %v", err)
	}
	defer empty.Close()
	if recs, err := empty.List(); err != nil || len(recs) != 0 {
		t.Fatalf("empty store: recs=%v err=%v", recs, err)
	}

	base := time.Unix(1_700_000_000, 0).UTC()
	// Fingerprints are 64 lowercase hex chars; the store refuses anything else.
	fps := []string{
		strings.Repeat("c", 64), strings.Repeat("a", 64), strings.Repeat("b", 64),
	}
	for i, fp := range fps {
		if _, err := s.Begin(fp, Intent{
			MacAddress: "aa:bb:cc:dd:ee:ff",
			MintURL:    testMint,
			AmountSats: uint64(100 + i),
			IssuerHost: "issuer.example",
		}); err != nil {
			t.Fatalf("Begin(%s): %v", fp, err)
		}
		s.now = func() time.Time { return base.Add(time.Duration(i) * time.Minute) }
	}

	recs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("List returned %d records, want 3", len(recs))
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].CreatedAt.Before(recs[i-1].CreatedAt) {
			t.Fatalf("List is not oldest-first: %v", []time.Time{
				recs[0].CreatedAt, recs[1].CreatedAt, recs[2].CreatedAt})
		}
	}

	// A note secret must never appear in a List-able record: List is what an
	// operator dumps when debugging, and a k1 in a log is a k1 in a screenshot.
	for _, rec := range recs {
		blob, _ := json.Marshal(rec)
		if strings.Contains(string(blob), "k1secret") || strings.Contains(string(blob), "withdraw?k1=") {
			t.Fatalf("record carries the note secret: %s", blob)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.List(); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("List on a closed store: err = %v, want ErrIntentConflict", err)
	}
}

// Intent.String is the log rendering. It exists so a record can be logged without
// carrying a note secret; a future field added to Intent must not leak through it.
func TestIntentStringCarriesOnlyIdentifiers(t *testing.T) {
	rec := Intent{
		State:      IntentSubmitted,
		QuoteID:    "quote-1",
		AmountSats: 250,
		MintURL:    testMint,
		MacAddress: "aa:bb:cc:dd:ee:ff",
		IssuerHost: "issuer.example",
		Invoice:    "lnbc250n1invoice",
	}
	got := rec.String()

	for _, want := range []string{"lnurlcash intent", "quote-1", "250", testMint} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() = %q, missing %q", got, want)
		}
	}
	// The invoice is a payment request, and the MAC identifies a customer. Neither
	// belongs in a log line by default.
	for _, unwanted := range []string{"lnbc250n1invoice", "aa:bb:cc:dd:ee:ff", "issuer.example"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("String() leaks %q: %q", unwanted, got)
		}
	}
}

// The disabled path has its own contract: no journal, no network, and the
// operator-facing accessors agree with the configuration the redeemer was built
// with. A half-enabled redeemer (route off, gate on) would still move money.
func TestDisabledRedeemerReportsItsConfigurationAndTouchesNothing(t *testing.T) {
	h := newHarness(t, Config{Enabled: false, MaxNoteSats: 100_000})
	// The harness forces Enabled for money-path tests; undo that for this one.
	h.svc.cfg.Enabled = false

	if h.svc.Enabled() {
		t.Fatal("Enabled() true for a disabled redeemer")
	}
	if got := h.svc.Config().MaxNoteSats; got != 100_000 {
		t.Fatalf("Config().MaxNoteSats = %d, want 100000", got)
	}

	before := h.issuer.callbackCount()
	res, err := h.svc.Redeem(t.Context(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Fatal("a disabled redeemer must refuse, not redeem")
	}
	if res.Granted {
		t.Fatal("a disabled redeemer granted access")
	}
	if after := h.issuer.callbackCount(); after != before {
		t.Fatalf("a disabled redeemer called the issuer (%d -> %d callbacks)", before, after)
	}
	if invs := h.gate.invoiceList(); len(invs) != 0 {
		t.Fatalf("a disabled redeemer asked for an invoice: %v", invs)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "lnurlcash-intents.json")); err == nil {
		recs, lerr := h.svc.store.List()
		if lerr == nil && len(recs) != 0 {
			t.Fatalf("a disabled redeemer journalled %d record(s)", len(recs))
		}
	}
}

// FingerprintOfPresented is the API layer's log correlate. It must give the same
// value for the bech32 and the URL spelling of one note — otherwise a double
// presentation looks like two different notes in the log, which is the
// double-grant bug in a quieter form — and it must not panic on garbage.
func TestFingerprintOfPresentedIsSpellingStableAndNeverPanics(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 100_000})
	presented := h.issuer.note()

	viaURL := h.svc.FingerprintOfPresented(presented)
	viaSame := h.svc.FingerprintOfPresented(presented)
	if viaURL == "" || viaURL != viaSame {
		t.Fatalf("FingerprintOfPresented is not stable: %q vs %q", viaURL, viaSame)
	}
	decoded, err := decodeNote(presented, forClient(h.svc.client))
	if err != nil {
		t.Fatalf("decodeNote: %v", err)
	}
	if direct := h.svc.Fingerprint(decoded); direct != viaURL {
		t.Fatalf("presented fingerprint %q != Fingerprint(note) %q", viaURL, direct)
	}

	garbage := []string{"", "not a note", "lnurl1qqqqq", "https://", "\x00\xff", strings.Repeat("a", 4096)}
	for _, g := range garbage {
		if got := h.svc.FingerprintOfPresented(g); got == "" {
			t.Fatalf("FingerprintOfPresented(%q) returned empty: an undecodable note still "+
				"needs a correlate for the refusal log line", g)
		}
	}
}
