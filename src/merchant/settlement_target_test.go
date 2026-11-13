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

// An explicit default port is the same mint. The previous byte-level
// normalisation reported "https://mint.example:443/Bitcoin" and
// "https://mint.example/Bitcoin" as different, which would melt a note that was
// already trusted — the exact waste this function's doc comment forbids.
func TestTrustedMintHandlesDefaultPort(t *testing.T) {
	cfg := &config_manager.Config{AcceptedMints: []config_manager.MintConfig{
		{URL: "https://mint.example:443/Bitcoin"},
	}}
	if !IsTrustedMint(cfg, "https://mint.example/Bitcoin") {
		t.Fatal("same mint spelled with and without an explicit :443 must be one mint")
	}
	got, err := SettlementTargetFor(cfg, "https://mint.example/Bitcoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfg.AcceptedMints[0].URL {
		t.Fatalf("target %q is not the trusted mint's config spelling %q", got, cfg.AcceptedMints[0].URL)
	}
}

// Path case is not noise: "/Bitcoin" and "/Liquid" are different units on one
// mint (#375), so folding the whole URL to lower case can vouch for a note the
// operator never accepted.
func TestIsTrustedMintPreservesPathCase(t *testing.T) {
	cfg := &config_manager.Config{AcceptedMints: []config_manager.MintConfig{
		{URL: "https://mint.example/Bitcoin"},
	}}
	if IsTrustedMint(cfg, "https://mint.example/Liquid") {
		t.Fatal("a Liquid note must not be trusted because a Bitcoin note is accepted on the same host")
	}
	if !IsTrustedMint(cfg, "https://MINT.EXAMPLE/Bitcoin") {
		t.Fatal("host case is noise; the same path on the same host is the same mint")
	}
}

// NeedsSettlement must agree with SettlementTargetFor in BOTH directions: a note
// that settles in place needs no melt, and a note that needs a melt must target a
// different mint.
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
		if needsMelt && inPlace {
			t.Fatalf("%s: NeedsSettlement=true but target %q is the same mint — a melt that goes nowhere",
				mint, target)
		}
	}
}

// The returned URL is a mint identity downstream, so it must be the operator's
// config spelling, not whatever the caller's token happened to say. A
// trailing-slash or case variant is a second spelling of one mint.
func TestTrustedMintReturnsConfigSpelling(t *testing.T) {
	cfg := &config_manager.Config{AcceptedMints: []config_manager.MintConfig{
		{URL: "https://Mint.Example/"},
		{URL: "https://second.example"},
	}}
	for _, in := range []string{
		"https://Mint.Example/",
		"https://mint.example",
		"https://mint.example/",
		"  https://MINT.EXAMPLE  ",
	} {
		got, err := SettlementTargetFor(cfg, in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != cfg.AcceptedMints[0].URL {
			t.Fatalf("input %q returned %q; want the config spelling %q so downstream mint identity stays canonical",
				in, got, cfg.AcceptedMints[0].URL)
		}
	}
}
