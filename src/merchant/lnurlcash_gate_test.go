package merchant

import (
	"errors"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant/lnurlcash"
)

// The settlement mint is a policy fact, not a per-note choice: a note may come
// from any issuer, but the invoice we ask the issuer to pay must come from a
// mint this operator actually accepts. Two accepted mints must not turn into
// "whichever one the note's URL resembles".
//
// The rule is the first entry in `accepted_mints` — the same rule the upstream
// settlement path (#1011) uses, so the two do not diverge.
func TestLNURLCashGateSettlementMintIsFirstAccepted(t *testing.T) {
	gate := NewLNURLCashGate(&Merchant{})
	gate.settlementMintForTest = func() (string, error) {
		return firstAcceptedMint([]config_manager.MintConfig{
			{URL: "https://first.example"},
			{URL: "https://second.example"},
		})
	}
	got, err := gate.SettlementMint()
	if err != nil {
		t.Fatalf("SettlementMint: %v", err)
	}
	if got != "https://first.example" {
		t.Fatalf("settlement mint = %q, want the first accepted mint", got)
	}
}

// With nothing accepted there is nowhere to settle, and the failure must be an
// error the caller surfaces — not a silent fallback to an arbitrary mint.
func TestLNURLCashGateRefusesWithoutAcceptedMints(t *testing.T) {
	if _, err := firstAcceptedMint(nil); err == nil {
		t.Fatal("firstAcceptedMint returned no error with zero accepted mints")
	}
	if _, err := firstAcceptedMint([]config_manager.MintConfig{{URL: "  "}}); err == nil {
		t.Fatal("firstAcceptedMint accepted a blank URL as a settlement target")
	}
}

// The gate adapter must report the mint's own verdict, and it must distinguish
// "paid" from "granted": value can arrive before the session exists, and a
// caller that conflates them grants access on an unpaid quote.
func TestLNURLCashGateReportsPaidAndGrantedSeparately(t *testing.T) {
	cases := []struct {
		name        string
		status      *LightningQuoteStatus
		wantPaid    bool
		wantGranted bool
	}{
		{"paid but not yet granted", &LightningQuoteStatus{State: "PAID", AccessGranted: false}, true, false},
		{"issued and granted", &LightningQuoteStatus{State: "ISSUED", AccessGranted: true}, true, true},
		{"still pending", &LightningQuoteStatus{State: "PENDING"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := NewLNURLCashGate(&Merchant{})
			gate.statusForTest = func(quoteID, mac string) (*LightningQuoteStatus, error) {
				return tc.status, nil
			}
			paid, granted, err := gate.RedemptionSettled("q1", "aa:bb:cc:dd:ee:ff")
			if err != nil {
				t.Fatalf("RedemptionSettled: %v", err)
			}
			if paid != tc.wantPaid || granted != tc.wantGranted {
				t.Fatalf("paid/granted = %v/%v, want %v/%v", paid, granted, tc.wantPaid, tc.wantGranted)
			}
		})
	}
}

// A status error is not a refusal: it means the mint did not answer, which the
// redeemer must treat as ambiguity and never as "spent". The adapter propagates
// it unchanged so that distinction survives.
func TestLNURLCashGatePropagatesStatusError(t *testing.T) {
	sentinel := errors.New("mint unreachable")
	gate := NewLNURLCashGate(&Merchant{})
	gate.statusForTest = func(quoteID, mac string) (*LightningQuoteStatus, error) {
		return nil, sentinel
	}
	if _, _, err := gate.RedemptionSettled("q1", "aa:bb:cc:dd:ee:ff"); !errors.Is(err, sentinel) {
		t.Fatalf("RedemptionSettled error = %v, want the sentinel", err)
	}
}

// The redeemer's Config and the merchant's config section are one fact in two
// places; a translation that drops a field silently changes the safety bounds.
func TestLNURLCashConfigTranslationKeepsEveryBound(t *testing.T) {
	in := config_manager.LNURLCashConfig{
		Enabled:                  true,
		MaxNoteSats:              4242,
		RedemptionTimeoutSeconds: 77,
		RetentionSeconds:         12345,
	}
	out := LNURLCashConfigFrom(in)
	if out.Enabled != in.Enabled || out.MaxNoteSats != in.MaxNoteSats ||
		out.RedemptionTimeoutSeconds != in.RedemptionTimeoutSeconds ||
		out.RetentionSeconds != in.RetentionSeconds {
		t.Fatalf("translation lost a bound: got %+v from %+v", out, in)
	}
	if !out.Enabled {
		t.Fatal("enabled did not survive the translation")
	}
}

// An absent section must translate to DISABLED, whatever the other fields say.
// A zero Config is the shape a config written before this feature produces.
//
// The bounds are NOT defaulted here, deliberately: the config LOADER owns that
// (withLNURLCashDefaults), and the schema declares min 1 for both, so a zeroed
// section is invalid input the loader has already rejected. When this layer
// re-applied defaults too, `retention_seconds: 0` meant "keep forever" in the
// schema and "a week" in the code. This test asserts the ownership split rather
// than the papering-over.
func TestLNURLCashTranslationOfAbsentSectionIsDisabled(t *testing.T) {
	out := LNURLCashConfigFrom(config_manager.LNURLCashConfig{})
	if out.Enabled {
		t.Fatal("a zero LNURLCashConfig translated to ENABLED")
	}
	// Pass-through: the loader's values arrive unmodified, including a legitimate
	// zero retention ("keep everything").
	in := config_manager.LNURLCashConfig{
		Enabled:                  true,
		MaxNoteSats:              4242,
		RedemptionTimeoutSeconds: 17,
		RetentionSeconds:         0, // meaningful: keep everything
	}
	got := LNURLCashConfigFrom(in)
	if got.MaxNoteSats != 4242 || got.RedemptionTimeoutSeconds != 17 {
		t.Fatalf("translation altered the loader's bounds: %+v", got)
	}
	if got.RetentionSeconds != 0 {
		t.Fatalf("retention 0 became %d: the gate re-defaulted a value the schema promises", got.RetentionSeconds)
	}
	var _ = lnurlcash.DefaultConfig
}
