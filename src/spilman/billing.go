package spilman

import "fmt"

// Exact billing arithmetic for Spilman-metered sessions.
//
// The combined amount due is ceil(bytesIn/inRate + bytesOut/outRate), computed
// with integer quotient/remainder arithmetic so that implementations never round
// each direction separately, never use floating point, and never need an LCM.
// Ported from MONAD `monad-common/src/billing.rs` (itself the reference the Rust
// rail was ported from), so the two modules price a byte identically.
//
// The denominator products are computed in 128-bit space because inRate and
// outRate are both u64: inRate*outRate overflows u64 for realistic rates.

// TollgateStepSizeBytes is TollGate's price-table step: 21 MiB.
//
// Mirrors `Config::step_size` / `config_manager/config_schema.go`.
const TollgateStepSizeBytes uint64 = 22020096

// TollgatePricePerStepMillisats is one step's price, in millisats (1 sat).
const TollgatePricePerStepMillisats uint64 = 1000

// DefaultInBytesPerMillisat is the config default, `22020096 / 1000`
// integer-truncated.
//
// Because the true retail rate is 22020.096 bytes/msat, this rate bills one
// 21 MiB step at 1001 msat rather than 1000: a 45 ppm overprice, and at sat
// granularity a whole extra sat of raw balance (ceil(1001/1000) = 2). That is
// the price of keeping an integer rate at or below the retail line. Use
// TollgateParityInBytesPerMillisat when the channel must bill each step at
// exactly 1000 msat.
const DefaultInBytesPerMillisat uint64 = 22020

// TollgateParityInBytesPerMillisat is the smallest integer rate at which one
// TollGate step bills exactly 1000 msat = 1 sat: ceil(22020096/22021) = 1000.
//
// Any integer rate r with 22021 <= r <= 22042 yields exactly 1000 msat per step;
// 22020 yields 1001. A sat-denominated channel that updates once per step would
// otherwise step its raw balance by 2 sat per 21 MiB — a 100% overcharge — so
// this is the rate the rail ships.
const TollgateParityInBytesPerMillisat uint64 = 22021

// SessionPricing holds directional billing rates for a session.
type SessionPricing struct {
	InBytesPerMillisat  uint64
	OutBytesPerMillisat uint64
}

// NewSessionPricing builds validated pricing, rejecting a zero rate rather than
// dividing by it.
func NewSessionPricing(inBytesPerMillisat, outBytesPerMillisat uint64) (SessionPricing, error) {
	if _, err := AmountDueMillisats(0, 0, inBytesPerMillisat, outBytesPerMillisat); err != nil {
		return SessionPricing{}, err
	}
	return SessionPricing{InBytesPerMillisat: inBytesPerMillisat, OutBytesPerMillisat: outBytesPerMillisat}, nil
}

// NewTollgateParityPricing builds symmetric TollGate-parity pricing.
func NewTollgateParityPricing(bytesPerMillisat uint64) (SessionPricing, error) {
	return NewSessionPricing(bytesPerMillisat, bytesPerMillisat)
}

// NewDefaultPricing is the config-default pricing (rate 22020, see the drift
// note on DefaultInBytesPerMillisat).
func NewDefaultPricing() SessionPricing {
	return SessionPricing{
		InBytesPerMillisat:  DefaultInBytesPerMillisat,
		OutBytesPerMillisat: DefaultInBytesPerMillisat,
	}
}

// AmountDueMillisats is the exact combined bill for cumulative byte totals.
//
// It takes *cumulative* totals on purpose: summing per-update ceil()s over-bills
// by up to one millisat per update.
func (p SessionPricing) AmountDueMillisats(bytesIn, bytesOut uint64) (uint64, error) {
	return AmountDueMillisats(bytesIn, bytesOut, p.InBytesPerMillisat, p.OutBytesPerMillisat)
}

// RawBalanceDue is the raw (channel-unit) balance the payer must sign, rounded
// UP so a positive msat delta always yields a non-negative raw delta.
func (p SessionPricing) RawBalanceDue(unit string, bytesIn, bytesOut uint64) (uint64, error) {
	dueMillisats, err := p.AmountDueMillisats(bytesIn, bytesOut)
	if err != nil {
		return 0, err
	}
	return MillisatsToRawUnits(unit, dueMillisats)
}

// AmountDueMillisats returns the exact combined amount due in millisats for the
// given cumulative byte totals:
//
//	ceil(bytesIn/inBytesPerMillisat + bytesOut/outBytesPerMillisat)
func AmountDueMillisats(bytesIn, bytesOut, inBytesPerMillisat, outBytesPerMillisat uint64) (uint64, error) {
	if inBytesPerMillisat == 0 || outBytesPerMillisat == 0 {
		return 0, &ZeroRateError{
			InBytesPerMillisat:  inBytesPerMillisat,
			OutBytesPerMillisat: outBytesPerMillisat,
		}
	}

	wholeIn := bytesIn / inBytesPerMillisat
	remainderIn := bytesIn % inBytesPerMillisat
	wholeOut := bytesOut / outBytesPerMillisat
	remainderOut := bytesOut % outBytesPerMillisat

	// wholeIn + wholeOut can exceed uint64 for large byte totals, so the sum and
	// the ceiling are computed in 128 bits and only converted back at the end.
	wholeHi, wholeLo, _ := add128(0, wholeIn, 0, wholeOut) // 128-bit wholeIn+wholeOut

	extra := uint64(0)
	if remainderIn != 0 || remainderOut != 0 {
		// 128-bit comparison of the combined fraction against one whole unit.
		// inFraction = remIn*outRate, outFraction = remOut*inRate; the sum of the
		// two fractions is < 2, so the ceiling adds exactly one or two.
		denomHi, denomLo := mul128(inBytesPerMillisat, outBytesPerMillisat)
		inHi, inLo := mul128(remainderIn, outBytesPerMillisat)
		outHi, outLo := mul128(remainderOut, inBytesPerMillisat)

		sumHi, sumLo, carry := add128(inHi, inLo, outHi, outLo)
		// Both fractions are < 1 (remainder < rate), so their sum is < 2 and the
		// ceiling of the combined fraction is 1 or 2.
		//
		// The comparison is written as `sum > denominator` rather than
		// `inFraction <= denominator - outFraction` because it needs no
		// subtraction that could underflow, and equality (exactly one whole
		// fractional unit) still yields 1.
		if carry != 0 || cmp128(sumHi, sumLo, denomHi, denomLo) > 0 {
			extra = 2
		} else {
			extra = 1
		}
	}

	_, total, carry := add128(wholeHi, wholeLo, 0, extra)
	if wholeHi != 0 || carry != 0 {
		return 0, fmt.Errorf("%w: amount due for bytes=(%d,%d) rates=(%d,%d) exceeds uint64",
			ErrOverflow, bytesIn, bytesOut, inBytesPerMillisat, outBytesPerMillisat)
	}
	return total, nil
}

// RemainingMillisats returns exact remaining session credit as a signed value.
// A negative result means the session is overdrawn and no further bytes may be
// served.
func RemainingMillisats(totalPaidMillisats, bytesIn, bytesOut, inBytesPerMillisat, outBytesPerMillisat uint64) (int64, error) {
	due, err := AmountDueMillisats(bytesIn, bytesOut, inBytesPerMillisat, outBytesPerMillisat)
	if err != nil {
		return 0, err
	}
	if due > uint64(maxInt64) {
		return 0, ErrOverflow
	}
	if totalPaidMillisats > uint64(maxInt64) {
		return 0, ErrOverflow
	}
	return int64(totalPaidMillisats) - int64(due), nil
}

const maxInt64 = int64(^uint64(0) >> 1)

// StepPricing is the retail, quantised model: grant whole steps only.
//
// Kept as an explicit named model because it is the safe drop-in default — it
// refuses to grant a partial step exactly as the current retail path does
// (integer division on amountSats/pricePerStep).
type StepPricing struct {
	// StepSize is bytes per granted step (21 MiB on the live config).
	StepSize uint64
	// PricePerStepMillisats is the price of one step, in millisats.
	PricePerStepMillisats uint64
}

// DefaultStepPricing returns the live retail price line.
func DefaultStepPricing() StepPricing {
	return StepPricing{
		StepSize:              TollgateStepSizeBytes,
		PricePerStepMillisats: TollgatePricePerStepMillisats,
	}
}

// StepsFor returns whole steps purchasable for amountMillisats. Integer
// division, exactly as the retail path: what cannot pay for a full step buys
// nothing.
func (s StepPricing) StepsFor(amountMillisats uint64) uint64 {
	if s.PricePerStepMillisats == 0 {
		return 0
	}
	return amountMillisats / s.PricePerStepMillisats
}

// AllotmentBytesFor returns bytes to grant, floored to whole steps.
func (s StepPricing) AllotmentBytesFor(amountMillisats uint64) uint64 {
	return s.StepsFor(amountMillisats) * s.StepSize
}
