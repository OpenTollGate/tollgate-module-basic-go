package merchant

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// A trusted note settles in place at its own mint: no melt, no fee, no round
// trip. The doc on SettlementTargetFor states this. The implementation returned
// AcceptedMints[0] on both branches, so a trusted mint that was not first in the
// operator's preference list got redirected — forcing a melt the contract says
// does not happen.
func TestTrustedNoteSettlesAtItsOwnMint(t *testing.T) {
	cfg := &config_manager.Config{AcceptedMints: []config_manager.MintConfig{
		{URL: "https://first.example"},
		{URL: "https://second.example"},
	}}
	got, err := SettlementTargetFor(cfg, "https://second.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://second.example" {
		t.Fatalf("trusted note settled to %q, want its own mint %q — this forces an unnecessary melt",
			got, "https://second.example")
	}
}

// An untrusted note is redirected to the operator's preferred mint.
func TestUntrustedNoteRedirectsToPreferredMint(t *testing.T) {
	cfg := &config_manager.Config{AcceptedMints: []config_manager.MintConfig{
		{URL: "https://first.example"},
		{URL: "https://second.example"},
	}}
	got, err := SettlementTargetFor(cfg, "https://foreign.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://first.example" {
		t.Fatalf("untrusted note settled to %q, want preferred mint %q", got, "https://first.example")
	}
}

// The two branches must not be indistinguishable: that was the bug.
func TestTrustedAndUntrustedTargetsDiffer(t *testing.T) {
	cfg := &config_manager.Config{AcceptedMints: []config_manager.MintConfig{
		{URL: "https://first.example"},
		{URL: "https://second.example"},
	}}
	trusted, _ := SettlementTargetFor(cfg, "https://second.example")
	untrusted, _ := SettlementTargetFor(cfg, "https://foreign.example")
	if trusted == untrusted {
		t.Fatalf("trusted and untrusted notes both settle to %q; IsTrustedMint is not affecting the result", trusted)
	}
}

// NeedsSettlement must agree with SettlementTargetFor: a note that settles in
// place needs no melt.
func TestNeedsSettlementAgreesWithTarget(t *testing.T) {
	cfg := &config_manager.Config{AcceptedMints: []config_manager.MintConfig{
		{URL: "https://first.example"},
		{URL: "https://second.example"},
	}}
	for _, mint := range []string{"https://second.example", "https://foreign.example"} {
		target, err := SettlementTargetFor(cfg, mint)
		if err != nil {
			t.Fatalf("%s: %v", mint, err)
		}
		needsMelt := NeedsSettlement(cfg, mint)
		inPlace := normaliseMintURL(target) == normaliseMintURL(mint)
		if !needsMelt && !inPlace {
			t.Fatalf("%s: NeedsSettlement=false but target %q is a different mint — contract mismatch",
				mint, target)
		}
	}
}
