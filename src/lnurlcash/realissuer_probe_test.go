package lnurlcash

// PROBE, NOT A SUITE TEST: does this client speak to a REAL LUD-25 issuer?
//
// Skipped unless LNURLCASH_REAL_ISSUER is set to a note URL, so the normal suite is unaffected.
// It exists because every other test in this package talks to httptest mocks that I wrote — and
// mocks agree with my reading of the spec by construction. This is the only check that a real
// independent implementation (moneyer) accepts what we send and that we parse what it answers.

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestProbeRealIssuer(t *testing.T) {
	// The invoice to melt into. Against the fake funding backend any well-formed bolt11 will do,
	// because nothing routes. Set it to a REAL invoice from a different node and the melt becomes
	// a genuine HTLC over a genuine channel; then check the far side's listinvoices for `paid`,
	// which is the only thing that proves money moved rather than a mint saying yes.
	probeInvoice := os.Getenv("LNURLCASH_REAL_INVOICE")
	raw := os.Getenv("LNURLCASH_REAL_ISSUER")
	if raw == "" {
		t.Skip("set LNURLCASH_REAL_ISSUER to a note URL (e.g. https://1.2.3.7/w?k1=...&amount=21000)")
	}

	note, err := Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	t.Logf("parsed note: domain=%q spend=%x", note.Domain, note.Spend())

	// Our SSRF guard rejects loopback and cleartext by design, so this probe cannot be pointed
	// at 127.0.0.1 at all: it needs a public-looking address reached over https, which is why
	// the rig behind it binds 1.2.3.7 on lo with a matching SAN. The address policy therefore
	// DOES apply here — it is satisfied, not bypassed.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, cerr := NewIssuerClient(note.Domain)
	if cerr != nil {
		t.Fatalf("NewIssuerClient(%q): %v", note.Domain, cerr)
	}
	// The withdraw path comes from the note's own URL; moneyer serves it at /w.
	state, err := c.Check(ctx, note, "/w")
	if err != nil {
		t.Fatalf("Check against a real issuer: %v", err)
	}
	t.Logf("withdrawable=%d msat callback=%q", state.WithdrawableMSat, state.Callback)
	if state.WithdrawableMSat != 21000 {
		t.Fatalf("withdrawable=%d, want 21000", state.WithdrawableMSat)
	}
	if state.Callback == nil || state.Callback.Host == "" {
		t.Fatal("issuer published no callback; RedeemAt would have nothing to POST to")
	}

	// THE MONEY PATH: ask the real mint to melt this note into a real (fake-funded) invoice.
	// This is the call the rail makes after the gate has been paid. It must be a POST with a
	// k1+pr body; anything a mock would accept here proves nothing about a foreign issuer.
	melted := make(chan error, 1)
	go func() {
		melted <- c.Melt(ctx, note, state.Callback, state.CallbackParams, probeInvoice)
	}()
	select {
	case err := <-melted:
		if err != nil {
			t.Fatalf("Melt against a real issuer: %v", err)
		}
		t.Logf("MELT ACCEPTED by a real LUD-25 implementation")
	case <-time.After(20 * time.Second):
		t.Fatal("Melt did not answer within 20s")
	}

	// And the note must now be spent as far as the issuer is concerned: a second melt of the
	// same k1 must be refused. This is the property the rail depends on for its replay guard
	// to be a second line of defence rather than the only one.
	if err := c.Melt(ctx, note, state.Callback, state.CallbackParams, probeInvoice); err == nil {
		t.Fatal("the issuer melted the SAME note twice — a bearer note that can be melted twice is not a bearer note")
	} else {
		t.Logf("second melt refused as expected: %v", err)
	}
}
