//go:build cdk_wallet

package tollwallet

import "testing"

// A custom-unit mint must name its payment method, and the name must come from
// config.
//
// Why this test exists: the method was derived from the unit and defaulted to
// "paypal" — a name left over from a bench harness that no real processor
// answers. Against a Pecan-backed mint (which registers "branch") every
// issuance quote went to a method the mint does not have, so the mint refused
// it and the operator could not issue anything. The method is a per-mint fact:
// one router can front a counter-settled operator mint and a bolt11 sat mint at
// the same time, which a unit-derived guess cannot express.
func TestCustomUnitPaymentMethod_ComesFromConfigPerMint(t *testing.T) {
	w := &CdkWallet{
		mintUnits: map[string]string{
			"http://mint.example/mb":  "mb",
			"http://mint.example/sat": "",
		},
		mintMethods: map[string]string{
			"http://mint.example/mb": "branch", // Pecan's method
		},
	}
	if got := w.customUnitPaymentMethod("http://mint.example/mb"); got != "branch" {
		t.Fatalf("method for the mb mint = %q, want \"branch\" from config", got)
	}
	// A second mint on the same router keeps its own method (none configured
	// here), so the two mints cannot bleed into each other.
	if got := w.customUnitPaymentMethod("http://mint.example/sat"); got != "" {
		t.Fatalf("method for the unconfigured mint = %q, want \"\" (no cross-mint leak)", got)
	}
}

// No configured method must be an error, not a guess: the previous default was
// "paypal", which no processor answers, so the mint refused every quote and the
// failure surfaced far from its cause.
func TestCustomUnitPaymentMethod_NoConfiguredMethodIsNotGuessed(t *testing.T) {
	w := &CdkWallet{
		mintUnits:   map[string]string{"http://mint.example/mb": "mb"},
		mintMethods: map[string]string{}, // operator set the unit but not the method
	}
	if got := w.customUnitPaymentMethod("http://mint.example/mb"); got != "" {
		t.Fatalf("method = %q; a custom unit with no configured method must not be guessed", got)
	}
}
