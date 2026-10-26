package spilman

import (
	"fmt"
	"math"
)

// Raw channel balance units this rail understands.
const (
	// UnitMillisat: one raw unit is one millisatoshi.
	UnitMillisat = "msat"
	// UnitSat: one raw unit is 1000 millisatoshis.
	UnitSat = "sat"
)

// MillisatsToRawUnits converts a millisatoshi amount into the channel's raw
// balance unit, rounding UP at non-multiples.
//
// Rounding down would short-change the payee by <1 sat on every update, which
// compounds to tens of sats of unclaimed value across a long session. Round up,
// always — and never accumulate per-update rounded values: compute the bill from
// cumulative byte totals once (see AmountDueMillisats).
func MillisatsToRawUnits(unit string, amountMillisats uint64) (uint64, error) {
	switch unit {
	case UnitMillisat:
		return amountMillisats, nil
	case UnitSat:
		if amountMillisats == 0 {
			return 0, nil
		}
		// (n-1)/1000 + 1 is ceil(n/1000) without the overflow that n+999 has.
		return (amountMillisats-1)/1000 + 1, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedUnit, unit)
	}
}

// RawUnitsToMillisats converts a raw balance-unit amount into millisatoshis.
func RawUnitsToMillisats(unit string, amountRaw uint64) (uint64, error) {
	switch unit {
	case UnitMillisat:
		return amountRaw, nil
	case UnitSat:
		if amountRaw > math.MaxUint64/1000 {
			return 0, fmt.Errorf("%w: raw amount %d -> millisats", ErrOverflow, amountRaw)
		}
		return amountRaw * 1000, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedUnit, unit)
	}
}
