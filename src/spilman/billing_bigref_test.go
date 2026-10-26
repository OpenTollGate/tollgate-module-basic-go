package spilman

import "math/big"

// newBig is a tiny helper for the independent big.Int reference formula.
func newBig(v uint64) *big.Int { return new(big.Int).SetUint64(v) }

// bigCeilDivUint64 returns ceil(numerator/denominator) as a uint64, or
// ErrOverflow when the quotient does not fit.
func bigCeilDivUint64(numerator, denominator *big.Int) (uint64, error) {
	q, r := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsUint64() {
		return 0, ErrOverflow
	}
	return q.Uint64(), nil
}
