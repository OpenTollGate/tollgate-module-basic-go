package spilman

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two rate constants must never drift: 22021 is the parity rate (exactly
// 1 sat per 21 MiB step) and 22020 is the config default, which over-bills by
// one millisat per step. Both are pinned so neither silently becomes the other.
func TestBillingRateConstantsArePinned(t *testing.T) {
	require.Equal(t, uint64(22020096), TollgateStepSizeBytes)
	require.Equal(t, uint64(1000), TollgatePricePerStepMillisats)
	require.Equal(t, uint64(22020), DefaultInBytesPerMillisat)
	require.Equal(t, uint64(22021), TollgateParityInBytesPerMillisat)
}

// One TollGate step (21 MiB) bills exactly 1000 msat = 1 sat at the parity rate.
func TestParityRateBillsExactlyOneThousandMillisatsPerStep(t *testing.T) {
	due, err := AmountDueMillisats(TollgateStepSizeBytes, 0, TollgateParityInBytesPerMillisat, TollgateParityInBytesPerMillisat)
	require.NoError(t, err)
	require.Equal(t, uint64(1000), due)
}

// At 22020 the exact rate is 22020.096 bytes/msat, so one step ceils to 1001
// msat. Pinned so the overcharge cannot be mistaken for parity.
func TestDefaultRateBillsOneMillisatAboveTheRetailLinePerStep(t *testing.T) {
	due, err := AmountDueMillisats(TollgateStepSizeBytes, 0, DefaultInBytesPerMillisat, DefaultInBytesPerMillisat)
	require.NoError(t, err)
	require.Equal(t, uint64(1001), due)
}

// At sat granularity the 1001 msat bill rounds up to 2 sat: a 100% overcharge.
// This is why the rail ships 22021.
func TestDefaultRateDoublesTheRawSatChargeAtStepGranularity(t *testing.T) {
	due, err := AmountDueMillisats(TollgateStepSizeBytes, 0, DefaultInBytesPerMillisat, DefaultInBytesPerMillisat)
	require.NoError(t, err)
	raw, err := MillisatsToRawUnits(UnitSat, due)
	require.NoError(t, err)
	require.Equal(t, uint64(2), raw)

	parityDue, err := AmountDueMillisats(TollgateStepSizeBytes, 0, TollgateParityInBytesPerMillisat, TollgateParityInBytesPerMillisat)
	require.NoError(t, err)
	parityRaw, err := MillisatsToRawUnits(UnitSat, parityDue)
	require.NoError(t, err)
	require.Equal(t, uint64(1), parityRaw)
}

func TestZeroRatesAreRejected(t *testing.T) {
	for _, tc := range []struct{ inRate, outRate uint64 }{{0, 1}, {1, 0}, {0, 0}} {
		_, err := AmountDueMillisats(0, 0, tc.inRate, tc.outRate)
		require.ErrorIs(t, err, ErrZeroRate, "rates (%d,%d)", tc.inRate, tc.outRate)
	}
}

func TestZeroBytesAndExactDivision(t *testing.T) {
	for _, tc := range []struct {
		want                       uint64
		bytesIn, bytesOut, in, out uint64
	}{
		{0, 0, 0, 1, 1},
		{0, 0, 0, math.MaxUint64, math.MaxUint64},
		{6, 6, 9, 2, 3},
		{2, 4, 0, 2, 7},
		{2, 0, 6, 7, 3},
	} {
		got, err := AmountDueMillisats(tc.bytesIn, tc.bytesOut, tc.in, tc.out)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "bytes=(%d,%d) rates=(%d,%d)", tc.bytesIn, tc.bytesOut, tc.in, tc.out)
	}
}

// The combined fractional part is rounded once, never per direction.
func TestCombinedFractionIsRoundedOnce(t *testing.T) {
	for _, tc := range []struct {
		want                       uint64
		bytesIn, bytesOut, in, out uint64
	}{
		{4, 5, 5, 3, 3}, // 2 + ceil(4/3)
		{1, 2, 1, 3, 4}, // 11/12 -> 1
		{1, 3, 1, 4, 4}, // exactly one
		{2, 3, 1, 4, 2}, // 5/4
		{2, 3, 3, 4, 4}, // 3/2
		{1, 1, 0, 3, 7},
		{1, 0, 1, 7, 3},
	} {
		got, err := AmountDueMillisats(tc.bytesIn, tc.bytesOut, tc.in, tc.out)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "bytes=(%d,%d) rates=(%d,%d)", tc.bytesIn, tc.bytesOut, tc.in, tc.out)
	}
}

// referenceDue is the naive single-ceil formula the implementation must match.
func referenceDue(bytesIn, bytesOut, inRate, outRate uint64) (uint64, error) {
	numIn, numOut := newBig(bytesIn), newBig(bytesOut)
	inR, outR := newBig(inRate), newBig(outRate)
	numerator := numIn.Mul(numIn, outR)
	numerator.Add(numerator, numOut.Mul(numOut, inR))
	denominator := inR.Mul(inR, outR)
	return bigCeilDivUint64(numerator, denominator)
}

func TestMatchesReferenceOverSmallDomain(t *testing.T) {
	for inRate := uint64(1); inRate <= 12; inRate++ {
		for outRate := uint64(1); outRate <= 12; outRate++ {
			for bytesIn := uint64(0); bytesIn <= 40; bytesIn++ {
				for bytesOut := uint64(0); bytesOut <= 40; bytesOut++ {
					want, err := referenceDue(bytesIn, bytesOut, inRate, outRate)
					require.NoError(t, err)
					got, err := AmountDueMillisats(bytesIn, bytesOut, inRate, outRate)
					require.NoError(t, err)
					require.Equalf(t, want, got, "bytes=(%d,%d) rates=(%d,%d)", bytesIn, bytesOut, inRate, outRate)
				}
			}
		}
	}
}

func TestMatchesReferenceForDeterministicLargeSamples(t *testing.T) {
	state := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return state
	}
	for i := 0; i < 2000; i++ {
		inRate := next()%1_000_000 + 1
		outRate := next()%1_000_000 + 1
		bytesIn := next()
		bytesOut := next()
		want, err := referenceDue(bytesIn, bytesOut, inRate, outRate)
		require.NoError(t, err)
		got, err := AmountDueMillisats(bytesIn, bytesOut, inRate, outRate)
		require.NoError(t, err)
		require.Equalf(t, want, got, "bytes=(%d,%d) rates=(%d,%d)", bytesIn, bytesOut, inRate, outRate)
	}
}

func TestDirectionSymmetryAndMonotonicity(t *testing.T) {
	for a := uint64(1); a <= 12; a++ {
		for b := uint64(1); b <= 12; b++ {
			for x := uint64(0); x < 40; x++ {
				for y := uint64(0); y < 40; y++ {
					due, err := AmountDueMillisats(x, y, a, b)
					require.NoError(t, err)
					swapped, err := AmountDueMillisats(y, x, b, a)
					require.NoError(t, err)
					require.Equal(t, due, swapped)

					moreIn, err := AmountDueMillisats(x+1, y, a, b)
					require.NoError(t, err)
					require.LessOrEqual(t, due, moreIn)

					moreOut, err := AmountDueMillisats(x, y+1, a, b)
					require.NoError(t, err)
					require.LessOrEqual(t, due, moreOut)
				}
			}
		}
	}
}

// A bill that cannot be represented in 64 bits must be refused, never truncated.
func TestOverflowIsRefusedNotTruncated(t *testing.T) {
	_, err := AmountDueMillisats(math.MaxUint64, math.MaxUint64, 1, 1)
	require.ErrorIs(t, err, ErrOverflow)
}

func TestRemainingMillisatsIsSignedAndExact(t *testing.T) {
	remaining, err := RemainingMillisats(5000, TollgateStepSizeBytes, 0, TollgateParityInBytesPerMillisat, TollgateParityInBytesPerMillisat)
	require.NoError(t, err)
	require.Equal(t, int64(4000), remaining)

	overdrawn, err := RemainingMillisats(500, TollgateStepSizeBytes, 0, TollgateParityInBytesPerMillisat, TollgateParityInBytesPerMillisat)
	require.NoError(t, err)
	require.Equal(t, int64(-500), overdrawn)
}

func TestSessionPricingValidatesAndPrices(t *testing.T) {
	_, err := NewSessionPricing(0, 1)
	require.ErrorIs(t, err, ErrZeroRate)

	pricing, err := NewTollgateParityPricing(TollgateParityInBytesPerMillisat)
	require.NoError(t, err)
	due, err := pricing.AmountDueMillisats(TollgateStepSizeBytes, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1000), due)

	raw, err := pricing.RawBalanceDue(UnitSat, TollgateStepSizeBytes, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), raw)

	raw5, err := pricing.RawBalanceDue(UnitSat, 5*TollgateStepSizeBytes, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(5), raw5)

	// One byte past the step still bills 1000 msat, and at sat granularity that
	// is exactly 1 sat — the raw unit must not round the bill down to 0.
	oneByteMore, err := pricing.RawBalanceDue(UnitSat, TollgateStepSizeBytes+1, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), oneByteMore)

	// One further rate-step past it bills 1001 msat, which rounds UP to 2 sat
	// rather than down to 1: rounding down here is the payee's loss.
	rawMsat, err := pricing.RawBalanceDue(UnitMillisat, TollgateStepSizeBytes+TollgateParityInBytesPerMillisat, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1001), rawMsat)

	rawSat, err := pricing.RawBalanceDue(UnitSat, TollgateStepSizeBytes+TollgateParityInBytesPerMillisat, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(2), rawSat)
}

func TestStepPricingGrantsWholeStepsOnly(t *testing.T) {
	step := DefaultStepPricing()
	require.Equal(t, uint64(1), step.StepsFor(1000))
	require.Equal(t, uint64(1), step.StepsFor(1999))
	require.Equal(t, uint64(0), step.StepsFor(999))
	require.Equal(t, uint64(0), step.AllotmentBytesFor(999))
	require.Equal(t, TollgateStepSizeBytes, step.AllotmentBytesFor(1000))
	require.Equal(t, 5*TollgateStepSizeBytes, step.AllotmentBytesFor(5000))

	zero := StepPricing{StepSize: 10, PricePerStepMillisats: 0}
	require.Equal(t, uint64(0), zero.StepsFor(1000))
}

func TestZeroRateErrorCarriesBothRates(t *testing.T) {
	_, err := AmountDueMillisats(1, 1, 0, 7)
	require.True(t, errors.Is(err, ErrZeroRate))
	require.Contains(t, err.Error(), "in=0")
	require.Contains(t, err.Error(), "out=7")
}
