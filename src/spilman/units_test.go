package spilman

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// Non-multiples of 1000 msat must round UP: rounding down leaves sub-sat value
// unclaimed on every update, which compounds across a session.
func TestSatConversionRoundsUp(t *testing.T) {
	for _, tc := range []struct{ msat, want uint64 }{
		{0, 0}, {1, 1}, {999, 1}, {1000, 1}, {1001, 2}, {1999, 2}, {2000, 2}, {5000, 5},
	} {
		got, err := MillisatsToRawUnits(UnitSat, tc.msat)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "msat=%d", tc.msat)
	}
}

func TestMillisatConversionIsIdentity(t *testing.T) {
	got, err := MillisatsToRawUnits(UnitMillisat, 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(1234), got)
	back, err := RawUnitsToMillisats(UnitMillisat, 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(1234), back)
}

func TestRawToMillisatsIsExactForSat(t *testing.T) {
	for _, raw := range []uint64{0, 1, 2, 49, 1000} {
		msats, err := RawUnitsToMillisats(UnitSat, raw)
		require.NoError(t, err)
		back, err := MillisatsToRawUnits(UnitSat, msats)
		require.NoError(t, err)
		require.Equalf(t, raw, back, "raw=%d", raw)
	}
}

func TestUnknownUnitsAreRefused(t *testing.T) {
	_, err := MillisatsToRawUnits("usd", 1)
	require.ErrorIs(t, err, ErrUnsupportedUnit)
	_, err = RawUnitsToMillisats("usd", 1)
	require.ErrorIs(t, err, ErrUnsupportedUnit)
}

func TestRawToMillisatsRefusesOverflow(t *testing.T) {
	_, err := RawUnitsToMillisats(UnitSat, math.MaxUint64)
	require.ErrorIs(t, err, ErrOverflow)
}
