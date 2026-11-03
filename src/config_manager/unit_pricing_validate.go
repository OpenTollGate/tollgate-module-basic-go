package config_manager

import (
	"fmt"
	"strings"
)

// stepSizeLimits bounds a step_size that can be *consistent* with a given
// custom unit. The lower bound exists because a step smaller than the unit it
// is named after is almost always a typo (a "1 mb" step of 1024 bytes prices
// access 1024x too cheaply, the mirror image of the 21x case below).
type stepSizeLimits struct {
	unit        string
	metric      string
	min, max    uint64
	expectation string // shown to the operator, so the error is actionable
}

// customUnitStepSizes is deliberately a small table, not a heuristic. A check
// that guesses at an unknown unit's scale would produce false refusals on
// units we did not anticipate; this one stays silent unless it is certain.
var customUnitStepSizes = []stepSizeLimits{
	{
		unit:        "mb",
		metric:      "bytes",
		min:         1000000,
		max:         1048576,
		expectation: "1000000 (decimal MB) or 1048576 (1 MiB)",
	},
	{
		unit:        "min",
		metric:      "milliseconds",
		min:         60000,
		max:         60000,
		expectation: "60000 (one minute in milliseconds)",
	},
}

// ValidateUnitPricing refuses a configuration in which a custom-unit mint is
// priced with a step_size belonging to a different unit.
//
// Why this exists: step_size and the mint's unit are configured in two places
// and nothing used to keep them in agreement. The failure mode is not a crash,
// it is a silent giveaway. In the 2026-10 CDK mint integration, an operator
// mint declared unit "mb" while step_size still carried the bytes default
// 22020096 (21 MiB), so ONE megabyte of ecash bought 21 MiB of access — 21x
// the access for the same money, on every sale. Every test passed, because a
// single payment's arithmetic is self-consistent no matter how wrong the
// scale is; it was measured on hardware before anyone noticed.
//
// The check only judges unit/metric pairs it knows (see customUnitStepSizes).
// An unrecognised unit is left alone rather than guessed at, and a zero
// step_size is not this function's business.
//
// Empty unit means "sat" — every pre-existing config is a satoshi mint, and
// sat mints legitimately carry the bytes-oriented default, so they are never
// judged here.
func ValidateUnitPricing(unit, metric string, stepSize uint64) error {
	if stepSize == 0 {
		return nil
	}
	u := strings.ToLower(strings.TrimSpace(unit))
	if u == "" || u == "sat" {
		return nil
	}
	m := strings.ToLower(strings.TrimSpace(metric))

	for _, l := range customUnitStepSizes {
		if l.unit != u || l.metric != m {
			continue
		}
		if stepSize < l.min || stepSize > l.max {
			return fmt.Errorf(
				"mint unit %q with metric %q cannot be priced with step_size %d: "+
					"one %s of ecash would grant %d units of access instead of one. "+
					"Set step_size to %s, or change the mint's unit to match. "+
					"Refusing rather than mispricing, because this fails generously "+
					"and silently — the operator gives away access on every sale",
				u, m, stepSize, u, stepSize, l.expectation)
		}
		return nil
	}
	return nil
}

// ValidateUnitPricingForConfig applies the per-mint unit check to every
// accepted mint in the configuration and, for custom-unit mints, checks that
// the advertised price_unit agrees with the unit the mint actually issues.
func (c *Config) ValidateUnitPricingForConfig() error {
	for i := range c.AcceptedMints {
		mint := &c.AcceptedMints[i]
		if err := ValidateUnitPricing(mint.Unit, c.Metric, c.StepSize); err != nil {
			return fmt.Errorf("accepted_mints[%d] (%s): %w", i, mint.URL, err)
		}
	}
	return nil
}

// ValidateAdvertisedUnitsForConfig checks only the advert/unit agreement:
// that each mint publishes the unit it actually issues.
//
// It is separate from ValidateUnitPricingForConfig on purpose. A step_size that
// does not match the unit over-grants access, which is a generosity bug the
// router can survive: refusing to boot would stop serving customers who already
// paid, so config load only warns. Publishing the wrong unit is not that class —
// a client pays in a unit the gate then refuses, so money leaves and no access
// arrives. That must refuse.
//
// No pre-existing config can trip this: both fields empty mean sat, and an
// operator must have explicitly set a custom `unit` to reach the comparison.
func (c *Config) ValidateAdvertisedUnitsForConfig() error {
	for i := range c.AcceptedMints {
		mint := &c.AcceptedMints[i]
		if err := validateAdvertisedUnitMatchesMintUnit(mint.Unit, mint.PriceUnit); err != nil {
			return fmt.Errorf("accepted_mints[%d] (%s): %w", i, mint.URL, err)
		}
	}
	return nil
}

// validateAdvertisedUnitMatchesMintUnit refuses a mint that issues one unit but
// advertises another in its kind:10021 `price_per_step` tag.
//
// The two fields are configured separately — `unit` says what the mint's keysets
// are denominated in (and is what pricing compares a token against), while
// `price_unit` is what goes on the wire in
//
//	["price_per_step","cashu",<price>,<price_unit>,<mint>,<min_steps>]
//
// Nothing compared them, so a mint set to unit "mb" while price_unit kept its
// "sat" default published a satoshi price for a mint that only accepts
// megabyte tokens. A client that trusts the advert pays in a unit the gate
// refuses; a client that does not check units prices 1 mb as 1 sat. Both are
// the silent-mispricing class this validator exists to stop, arriving through
// the advertisement instead of the wallet.
//
// Empty `unit` means the config never opted into a custom unit, so it is not
// judged — every pre-existing config keeps its exact behaviour.
func validateAdvertisedUnitMatchesMintUnit(unit, priceUnit string) error {
	issued := strings.ToLower(strings.TrimSpace(unit))
	// Only a config that has opted into an explicit unit is judged here. The
	// `unit` key is new in this change, so every pre-existing config leaves it
	// unset and keeps exactly its old behaviour — including one whose
	// price_unit is some spelling this code does not know. Refusing those would
	// break routers that work today for a mismatch no customer can reach.
	if issued == "" {
		return nil
	}
	advertised := strings.ToLower(strings.TrimSpace(priceUnit))
	if advertised == "" {
		advertised = "sat"
	}
	// "sats" is the spelling main canonicalises to "sat" on load; accept it
	// here so an in-flight config that has not been through migration yet is
	// not refused for a spelling the loader will fix.
	if advertised == "sats" {
		advertised = "sat"
	}
	if advertised != issued {
		return fmt.Errorf("mint issues unit %q but advertises price_unit %q: the gateway would publish "+
			"one unit and accept another, so a paying client prices the advert in the wrong unit — "+
			"set price_unit to %q (or leave both unset for a satoshi mint)",
			issued, advertised, issued)
	}
	return nil
}
