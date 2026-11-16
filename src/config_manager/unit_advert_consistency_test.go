package config_manager

import "testing"

// A mint that issues a custom unit must not advertise a different one.
//
// `accepted_mints[]` carries two unit fields: `unit` (what the mint's keysets
// are denominated in — PR #1040) and `price_unit` (what the kind:10021 advert's
// `price_per_step` tag publishes). The validator checks `unit` against the
// metric/step_size, and the config loader canonicalises "sats" to "sat", but
// nothing compares the two fields to each other.
//
// The consequence is a silent wrong price on the wire: an operator who sets
// unit "mb" and leaves price_unit at its default publishes
//
//	["price_per_step","cashu","1","sat","<mint>","1"]
//
// while the gateway will only accept an "mb"-denominated token. A client that
// trusts the advert pays in a unit the gate then refuses — and a client that
// does not check units at all prices 1 mb as 1 sat. Both are the mispricing
// class this feature exists to remove, arriving through the advertisement
// rather than the wallet.
func TestUnitPricing_CustomUnitMintCannotAdvertiseAnotherUnit(t *testing.T) {
	c := &Config{
		Metric:   "bytes",
		StepSize: 1048576, // valid for "mb" (1 MiB), so the step_size check passes
		AcceptedMints: []MintConfig{
			{
				URL:          "http://mint.example/mb",
				Unit:         "mb",
				PriceUnit:    "sat", // the default nobody remembered to change
				PricePerStep: 1,
			},
		},
	}
	if err := c.ValidateAdvertisedUnitsForConfig(); err == nil {
		t.Fatal("accepted a mint whose unit is \"mb\" but whose advertised price_unit is \"sat\": " +
			"the gateway would publish one unit and accept another")
	}
}

// The sat path must stay exactly as it is: an unset unit means sat, and an
// unset price_unit defaults to sat, so a legacy config keeps advertising and
// accepting sats with no new error.
func TestUnitPricing_UnsetUnitsStaySat(t *testing.T) {
	c := &Config{
		Metric:   "bytes",
		StepSize: 10485760,
		AcceptedMints: []MintConfig{
			{URL: "http://mint.example/sat", PricePerStep: 1},
		},
	}
	if err := c.ValidateUnitPricingForConfig(); err != nil {
		t.Fatalf("a legacy sat mint must keep validating, got: %v", err)
	}
}
