package fips_exit

import (
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// Redemption is how a purchase becomes admission. Everything below composes
// layers that are each tested on their own: Claim proves the buyer holds the
// key and that the claim arrived over the buyer's own tunnel; Grant writes the
// durable entitlement; State/RenderClientsFile produce the file the exit
// daemon actually admits on.
//
// The reason this layer exists at all: fips-exitd admits any address present in
// its clients file. A line in that file is the whole of its admission decision.
// So whatever writes the file IS the admission authority, and it must not write
// on anything weaker than a verified claim. Without this step the exit trusts a
// text file an operator (or anything with write access to it) can edit.

var (
	// ErrGrantBeyondChallenge reports a grant window that outlives the priced
	// challenge. The buyer paid for the window in the challenge; a longer one
	// is a different, unpaid purchase.
	ErrGrantBeyondChallenge = errors.New("fips_exit: grant window exceeds the challenge")
	// ErrGrantModeMismatch reports a billing mode that differs from the one the
	// challenge priced.
	ErrGrantModeMismatch = errors.New("fips_exit: grant billing mode differs from the challenge")
	// ErrGrantKeyMismatch reports a grant for a peer other than the claim's
	// buyer. That is a wiring bug, never a purchase.
	ErrGrantKeyMismatch = errors.New("fips_exit: grant key is not the claim buyer")
	// ErrNoNonceCache reports a redemption attempted without replay protection.
	ErrNoNonceCache = errors.New("fips_exit: redemption requires a nonce cache")
)

// Redeem turns one signed claim into one durable entitlement, or refuses.
//
// Order matters and is deliberate:
//  1. the claim is verified FIRST (shape, signature, tunnel binding, expiry),
//     so nothing below ever runs for an unproven buyer;
//  2. the nonce is consumed, which is the single-use guarantee;
//  3. the grant terms are checked against what the challenge actually priced;
//  4. the entitlement is written through Grant, which carries its own refusals
//     (zero/duplicate key, inverted window, zero budget, inner-IPv4 collision).
//
// The nonce is consumed before the grant is written on purpose. A crash between
// the two loses one paid admission, which the buyer can re-purchase; consuming
// it afterwards would let a replay race the write and buy twice.
//
// observedSource is the source address the FIPS transport reported for this
// tunnel. It is never taken from the claim itself.
func Redeem(state State, now time.Time, observedSource netip.Addr, claim Claim,
	cache *NonceCache, req GrantRequest) (State, error) {
	if cache == nil {
		return State{}, ErrNoNonceCache
	}
	if err := claim.Verify(now, observedSource); err != nil {
		return State{}, err
	}
	if req.Key != claim.Buyer {
		return State{}, fmt.Errorf("%w: grant %s, claim %s",
			ErrGrantKeyMismatch, req.Key.Hex(), claim.Buyer.Hex())
	}
	if req.Mode != claim.Challenge.Mode {
		return State{}, fmt.Errorf("%w: grant %q, challenge %q",
			ErrGrantModeMismatch, req.Mode, claim.Challenge.Mode)
	}
	if req.ExpiresAt.After(claim.Challenge.ExpiresAt) {
		return State{}, fmt.Errorf("%w: grant %v, challenge %v",
			ErrGrantBeyondChallenge, req.ExpiresAt, claim.Challenge.ExpiresAt)
	}
	if err := cache.Use(claim.Challenge.Nonce, claim.Challenge.ExpiresAt, now); err != nil {
		return State{}, err
	}
	return Grant(state, req)
}
