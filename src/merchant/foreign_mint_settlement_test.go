package merchant

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// A customer's e-cash note may come from a mint the operator does not list in
// accepted_mints. The operator cannot refuse it (the customer holds it), and cannot
// spend it upstream (the upstream only prices the mints it trusts). The honest
// settlement is: receive at the customer's mint, then melt it to a trusted mint.
//
// These tests pin the DECISION, not the transport. They must answer: is this token's
// mint trusted; and if not, is there a settlement target we may redirect into?
//
// Trusted = a mint in accepted_mints. Settlement target = the trusted mint that will
// hold the value afterwards. Default is the first accepted mint (the operator's own
// preference order); it is overridable so an operator can pin one.

func cfgWithMints(urls ...string) *config_manager.Config {
	c := &config_manager.Config{}
	for _, u := range urls {
		c.AcceptedMints = append(c.AcceptedMints, config_manager.MintConfig{URL: u})
	}
	return c
}

func TestForeignMintIsNotTrusted(t *testing.T) {
	c := cfgWithMints("https://trusted.a", "https://trusted.b")
	if IsTrustedMint(c, "https://trusted.a") != true {
		t.Fatal("a listed mint must be trusted")
	}
	if IsTrustedMint(c, "https://foreign.c") != false {
		t.Fatal("an unlisted mint must NOT be trusted")
	}
}

// Trust comparison must normalise: a trailing slash or different case is the same mint.
func TestTrustedMintComparisonNormalises(t *testing.T) {
	c := cfgWithMints("https://trusted.a")
	for _, variant := range []string{"https://trusted.a/", "https://Trusted.A", "https://trusted.a"} {
		if !IsTrustedMint(c, variant) {
			t.Fatalf("%q must match the listed mint", variant)
		}
	}
}

// The settlement target is a mint we CHOOSE to trust, never the customer's mint.
func TestSettlementTargetIsATrustedMint(t *testing.T) {
	c := cfgWithMints("https://trusted.a", "https://trusted.b")

	got, err := SettlementTargetFor(c, "https://foreign.c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "https://foreign.c" {
		t.Fatal("must never settle back into the customer's untrusted mint")
	}
	if got != "https://trusted.a" {
		t.Fatalf("expected the first accepted mint, got %q", got)
	}
}

// A token whose mint IS trusted needs no redirect: it settles in place.
func TestTrustedMintSettlesInPlace(t *testing.T) {
	c := cfgWithMints("https://trusted.a")
	got, err := SettlementTargetFor(c, "https://trusted.a")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://trusted.a" {
		t.Fatalf("a trusted mint must settle in place, got %q", got)
	}
}

// No accepted mints at all: there is nowhere to settle, and that must be an error,
// not a silent success that swallows the customer's note.
func TestNoAcceptedMintsIsAnError(t *testing.T) {
	c := cfgWithMints()
	if _, err := SettlementTargetFor(c, "https://foreign.c"); err == nil {
		t.Fatal("expected an error when the operator accepts no mints")
	}
}
