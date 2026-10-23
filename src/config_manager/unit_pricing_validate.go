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
// accepted mint in the configuration.
func (c *Config) ValidateUnitPricingForConfig() error {
	for i := range c.AcceptedMints {
		mint := &c.AcceptedMints[i]
		if err := ValidateUnitPricing(mint.Unit, c.Metric, c.StepSize); err != nil {
			return fmt.Errorf("accepted_mints[%d] (%s): %w", i, mint.URL, err)
		}
	}
	return nil
}
