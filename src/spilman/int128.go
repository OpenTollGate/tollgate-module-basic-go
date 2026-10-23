package spilman

import "math/bits"

// Minimal unsigned 128-bit helpers.
//
// Go has no u128 and the billing formula's denominator (inRate*outRate)
// overflows u64 for realistic rates, so the exact fractional comparison needs
// 128-bit arithmetic. Kept deliberately tiny — only what the billing proof
// needs, using math/bits so the primitives are the standard ones.

// mul128 returns the full 128-bit product of two uint64s as (hi, lo).
func mul128(a, b uint64) (uint64, uint64) {
	hi, lo := bits.Mul64(a, b)
	return hi, lo
}

// add128 adds two 128-bit values, returning the sum and the carry out.
func add128(aHi, aLo, bHi, bLo uint64) (uint64, uint64, uint64) {
	lo, carry := bits.Add64(aLo, bLo, 0)
	hi, carry2 := bits.Add64(aHi, bHi, carry)
	return hi, lo, carry2
}

// cmp128 compares two 128-bit values, returning -1, 0 or 1.
func cmp128(aHi, aLo, bHi, bLo uint64) int {
	switch {
	case aHi < bHi:
		return -1
	case aHi > bHi:
		return 1
	case aLo < bLo:
		return -1
	case aLo > bLo:
		return 1
	default:
		return 0
	}
}
