package config_manager

import "testing"

// The advertisement is a protocol surface: whatever an operator writes into
// `price_unit`, the kind:10021 tag must carry the unit spelling NUT-01/NUT-04
// use. A consumer compares it against the unit inside the customer's token
// ("sat"), so a literal "sats" makes valid e-cash look invalid.
func TestCanonicalPriceUnit(t *testing.T) {
	cases := []struct {
		in   string
		want string
		why  string
	}{
		{"sat", "sat", "the canonical spelling"},
		{"sats", "sat", "plural — what Archipelago provisioning wrote into config.json"},
		{"SAT", "sat", "case is not semantics"},
		{"Sats", "sat", "case is not semantics"},
		{" sat ", "sat", "surrounding whitespace"},
		{"", "sat", "unset means the satoshi unit, never an empty tag field"},
		{"satoshi", "sat", "spelled-out singular"},
		{"satoshis", "sat", "spelled-out plural"},
		{"msat", "msat", "a different real unit passes through"},
		{"usd", "usd", "custom-unit mints pass through"},
		{"MSAT", "msat", "pass-through is still normalised for case"},
	}

	for _, tc := range cases {
		if got := CanonicalPriceUnit(tc.in); got != tc.want {
			t.Errorf("CanonicalPriceUnit(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.why)
		}
	}
}

// Idempotence matters because the value is written back by the migration and
// then read again on the next start.
func TestCanonicalPriceUnitIsIdempotent(t *testing.T) {
	for _, in := range []string{"sat", "sats", "Sats", "", "msat"} {
		once := CanonicalPriceUnit(in)
		if twice := CanonicalPriceUnit(once); twice != once {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}
