package fips_exit

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/nbd-wtf/go-nostr"
)

// redeemFixture builds a signed claim for the given buyer and terms.
func redeemFixture(t *testing.T, now time.Time) (Claim, netip.Addr, PeerKey) {
	t.Helper()
	sk := nostr.GeneratePrivateKey()
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	buyer, err := ParsePeerKey(pk)
	if err != nil {
		t.Fatalf("parse buyer: %v", err)
	}
	exitKey := PeerKey{0x11}
	ch, err := NewChallenge(bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64)),
		now, 10*time.Minute, exitKey, 2, BillingBytes)
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}
	claim := Claim{Challenge: ch, Buyer: buyer}
	raw, err := hex.DecodeString(sk)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	priv, _ := btcec.PrivKeyFromBytes(raw)
	sig, err := schnorr.Sign(priv, signDigest(ch.Canonical()))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claim.Sig = hex.EncodeToString(sig.Serialize())
	return claim, buyer.OuterAddress(), buyer
}

func grantFor(c Claim, expires time.Time) GrantRequest {
	return GrantRequest{
		Key:        c.Buyer,
		Name:       "buyer",
		Mode:       c.Challenge.Mode,
		GrantedAt:  c.Challenge.IssuedAt,
		ExpiresAt:  expires,
		ByteBudget: 5000,
	}
}

// The whole point of the claim layer: a purchase that would otherwise be
// honoured on file presence must be refused when it cannot prove key
// possession and tunnel binding.
func TestRedeemRefusesUnverifiedClaim(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	cache := NewNonceCache(8, now)

	// A claim signed by someone else must not buy admission.
	forged := claim
	forged.Buyer = PeerKey{0x22}
	if _, err := Redeem(State{}, now, source, forged, cache, grantFor(forged, now.Add(time.Minute))); !errors.Is(err, ErrClaimSignature) {
		t.Fatalf("forged buyer: got %v, want ErrClaimSignature", err)
	}

	// A genuine claim presented from a tunnel that is not the buyer's must not
	// buy admission either: this is the remote-peer-spoofing case.
	other := netip.MustParseAddr("fd11:2233:4455:6677::9")
	if _, err := Redeem(State{}, now, other, claim, cache, grantFor(claim, now.Add(time.Minute))); !errors.Is(err, ErrClaimUnbound) {
		t.Fatalf("wrong tunnel: got %v, want ErrClaimUnbound", err)
	}

	// Neither failure may have consumed the nonce or written an entitlement.
	if cache.Len() != 0 {
		t.Fatalf("a refused claim consumed the nonce: Len()=%d", cache.Len())
	}
}

func TestRedeemGrantsAndRefusesReplay(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	cache := NewNonceCache(8, now)
	expires := claim.Challenge.ExpiresAt

	state, err := Redeem(State{}, now, source, claim, cache, grantFor(claim, expires))
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	ent, ok := state.Entitlement(claim.Buyer)
	if !ok {
		t.Fatal("a verified claim produced no entitlement")
	}
	if !ent.ExpiresAt.Equal(expires) {
		t.Fatalf("expiry = %v, want %v", ent.ExpiresAt, expires)
	}
	if state.Revision != 1 {
		t.Fatalf("revision = %d, want 1", state.Revision)
	}

	// Replaying the same signed claim into the same cache must be refused, and
	// it must not silently renew the entitlement it already bought.
	if _, err := Redeem(state, now, source, claim, cache, grantFor(claim, expires)); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("replay: got %v, want ErrNonceReused", err)
	}
}

// A caller must not be able to buy a longer window than the challenge priced,
// nor switch the billing mode after the fact.
func TestRedeemRefusesTermsBeyondTheChallenge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	cache := NewNonceCache(8, now)

	longer := grantFor(claim, claim.Challenge.ExpiresAt.Add(time.Hour))
	if _, err := Redeem(State{}, now, source, claim, cache, longer); !errors.Is(err, ErrGrantBeyondChallenge) {
		t.Fatalf("over-long window: got %v, want ErrGrantBeyondChallenge", err)
	}

	switched := grantFor(claim, claim.Challenge.ExpiresAt)
	switched.Mode = BillingTime
	if _, err := Redeem(State{}, now, source, claim, cache, switched); !errors.Is(err, ErrGrantModeMismatch) {
		t.Fatalf("switched mode: got %v, want ErrGrantModeMismatch", err)
	}

	// A grant for a different peer than the claim's buyer is a programming
	// error, not a purchase.
	mismatched := grantFor(claim, claim.Challenge.ExpiresAt)
	mismatched.Key = PeerKey{0x33}
	if _, err := Redeem(State{}, now, source, claim, cache, mismatched); !errors.Is(err, ErrGrantKeyMismatch) {
		t.Fatalf("mismatched key: got %v, want ErrGrantKeyMismatch", err)
	}

	if cache.Len() != 0 {
		t.Fatalf("a refused redemption consumed the nonce: Len()=%d", cache.Len())
	}
}

// A nil cache is not a free pass: without replay protection the caller gets an
// error rather than an admission decision nobody can audit.
func TestRedeemRequiresANonceCache(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	if _, err := Redeem(State{}, now, source, claim, nil, grantFor(claim, now.Add(time.Minute))); !errors.Is(err, ErrNoNonceCache) {
		t.Fatalf("nil cache: got %v, want ErrNoNonceCache", err)
	}
}

// An expired challenge must be refused by Verify before any grant is made, so
// a replayed purchase cannot be honoured after the fact.
func TestRedeemRefusesExpiredClaim(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	late := claim.Challenge.ExpiresAt.Add(time.Second)
	cache := NewNonceCache(8, late)
	if _, err := Redeem(State{}, late, source, claim, cache, grantFor(claim, claim.Challenge.ExpiresAt)); !errors.Is(err, ErrClaimExpired) {
		t.Fatalf("expired: got %v, want ErrClaimExpired", err)
	}
}
