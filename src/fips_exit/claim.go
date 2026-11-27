// Peer-claim binding for a paid FIPS exit.
//
// This file closes one hole in the exit-admission model: a caller who submits a
// peer key must not gain anything by submitting someone else's npub. A Claim
// carries a schnorr signature made by the very key it names, over terms the
// exit itself issued, so possession of the private key is proven rather than
// asserted, and the proof is neither replayable (nonce cache) nor reusable
// across different terms (every term field is inside the signed bytes).
//
// Cryptography. The signature is BIP340 schnorr over
// SHA-256(Challenge.Canonical()), produced by a 32-byte x-only secret key, and
// spelled as 128 lower-case hex characters. Verification uses
// github.com/btcsuite/btcd/btcec/v2/schnorr, already in this module's graph via
// go-nostr, so there is no new dependency and no cgo: it builds unchanged on
// mips, mips64, arm, arm64 and x86. Tests construct keys with
// github.com/nbd-wtf/go-nostr (GeneratePrivateKey / GetPublicKey) and sign with
// btcec, which is why both appear in the test file.
//
// What this layer does NOT do, by design:
//   - No HTTP handler, no payment or Cashu verification, no nftables, no
//     SIGHUP, no filesystem path. Nothing here is wired into the TollGate
//     purchase path or the admission ledger.
//   - No live exit is observed. A passing test proves the decision function is
//     right for the inputs supplied; it proves nothing about a running exit,
//     about whether the caller actually paid, or about whether the transport's
//     observed source address is trustworthy. The caller that samples
//     observedSource owns that guarantee.
//   - NonceCache is in-memory only. See its doc comment: the short challenge
//     TTL is the real replay bound, not the cache.
package fips_exit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// Sentinel failures of a claim. They are distinct on purpose: a caller can tell
// "you did not prove the key" from "you proved the key but not this tunnel"
// from "the terms aged out", and Verify stops at the first one in the
// documented order.
var (
	// ErrClaimMalformed reports a claim that cannot even be parsed into a
	// checkable shape: zero buyer key, zero challenge exit key, or a signature
	// that is not 128 characters of lower-case hex.
	ErrClaimMalformed = errors.New("fips_exit: claim is malformed")
	// ErrClaimSignature reports a well-formed claim whose schnorr signature
	// does not verify over the canonical challenge bytes under the buyer key.
	// This is the proof-of-possession failure: the classic stolen-npub case.
	ErrClaimSignature = errors.New("fips_exit: claim signature does not verify under the buyer key")
	// ErrClaimUnbound reports a validly signed claim whose buyer key does not
	// derive to the source address the transport observed for this tunnel, so
	// the proof belongs to some other peer.
	ErrClaimUnbound = errors.New("fips_exit: claim buyer is not bound to the observed tunnel source")
	// ErrClaimExpired reports that now is not before the challenge expiry.
	ErrClaimExpired = errors.New("fips_exit: claim challenge has expired")
	// ErrClaimNotYetValid reports an issue time more than the clock-skew
	// allowance in the future.
	ErrClaimNotYetValid = errors.New("fips_exit: claim challenge is not yet valid")
	// ErrNonceReused reports a challenge nonce already accepted inside its
	// validity window.
	ErrNonceReused = errors.New("fips_exit: challenge nonce has already been used")
)

const (
	// canonicalVersion is the serialization version, mixed in right after the
	// domain prefix so a future layout can never be mistaken for this one.
	canonicalVersion byte = 0x01

	// canonicalPrefix is the domain-separation tag. It makes these signatures
	// useless as Nostr event signatures or as signatures over any other
	// protocol's bytes, and vice versa.
	canonicalPrefix = "tollgate-fips-exit/claim\x00"

	// maxChallengeTTL bounds how long a nonce could possibly be replayable.
	// Combined with the in-memory-only NonceCache this is the real replay
	// defence: a restart loses the cache, so a stale proof stays useful only for
	// as long as the exit still considers the challenge live.
	maxChallengeTTL = 15 * time.Minute

	// clockSkewAllowance is how far in the future IssuedAt may sit before the
	// claim is refused as not yet valid.
	clockSkewAllowance = 60 * time.Second

	// sigHexLen is the length of a hex-encoded 64-byte BIP340 signature.
	sigHexLen = 2 * schnorr.SignatureSize
)

// ClaimNonce is the 16-byte random value the exit puts in a Challenge. It is the
// single-use handle a claim is bound to, so a captured claim cannot be presented
// twice within its window.
type ClaimNonce [16]byte

// Hex returns the lower-case 32-character hex spelling.
func (n ClaimNonce) Hex() string { return hex.EncodeToString(n[:]) }

// Challenge is the exit's offer: which exit key is being bought, a fresh nonce,
// the validity window, the price and the billing mode. Everything here is
// signed, so a buyer cannot later substitute cheaper terms.
type Challenge struct {
	// ExitKey identifies the exit that issued the offer.
	ExitKey PeerKey
	// Nonce is the single-use value that makes the claim non-replayable.
	Nonce ClaimNonce
	// IssuedAt is when the exit created the offer.
	IssuedAt time.Time
	// ExpiresAt is the instant the offer stops being acceptable.
	ExpiresAt time.Time
	// PriceSats is the quoted price. It is signed so the buyer and the exit
	// cannot disagree about it later.
	PriceSats uint64
	// Mode is the billing mode the price buys.
	Mode BillingMode
}

// NewChallenge builds a Challenge from an injected random source. Random is a
// parameter rather than an internal crypto/rand call so tests are deterministic;
// production callers pass crypto/rand.Reader.
//
// It refuses, with a plain error rather than a sentinel: a zero exit key, a
// non-positive TTL, a TTL above 15 minutes (which would widen the replay window
// that a restart-exposed nonce cache can cover), and a zero price.
func NewChallenge(random io.Reader, now time.Time, ttl time.Duration, exitKey PeerKey, priceSats uint64, mode BillingMode) (Challenge, error) {
	if exitKey.IsZero() {
		return Challenge{}, errors.New("fips_exit: challenge exit key must not be zero")
	}
	if ttl <= 0 {
		return Challenge{}, fmt.Errorf("fips_exit: challenge ttl must be positive, got %s", ttl)
	}
	if ttl > maxChallengeTTL {
		return Challenge{}, fmt.Errorf("fips_exit: challenge ttl %s exceeds the %s maximum", ttl, maxChallengeTTL)
	}
	if priceSats == 0 {
		return Challenge{}, errors.New("fips_exit: challenge price must not be zero")
	}
	var nonce ClaimNonce
	if _, err := io.ReadFull(random, nonce[:]); err != nil {
		return Challenge{}, fmt.Errorf("fips_exit: read challenge nonce: %w", err)
	}
	return Challenge{
		ExitKey:   exitKey,
		Nonce:     nonce,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
		PriceSats: priceSats,
		Mode:      mode,
	}, nil
}

// Canonical returns the exact bytes a buyer signs, freshly allocated on every
// call so a caller cannot mutate the package's view by writing into the slice.
//
// Layout, big-endian throughout, no padding and no optional fields:
//
//	offset  size  field
//	0       24    domain prefix "tollgate-fips-exit/claim\x00"
//	24      1     format version, 0x01
//	25      32    ExitKey
//	57      16    Nonce
//	73      8     IssuedAt as int64 Unix nanoseconds
//	81      8     ExpiresAt as int64 Unix nanoseconds
//	89      8     PriceSats as uint64
//	97      2     len(Mode) as uint16
//	99      N     Mode bytes
//
// Every variable-length field is length-prefixed and every fixed-width field is
// exactly its width, so two different Challenges cannot produce the same bytes:
// shifting a byte between PriceSats and the mode length, or extending a mode
// string, changes the framing rather than sliding into the neighbouring field.
// The prefix and version mean these bytes are not a Nostr event id and cannot be
// confused with a later serialization version.
func (c Challenge) Canonical() []byte {
	mode := string(c.Mode)
	buf := make([]byte, 0, len(canonicalPrefix)+1+32+16+8+8+8+2+len(mode))
	buf = append(buf, canonicalPrefix...)
	buf = append(buf, canonicalVersion)
	buf = append(buf, c.ExitKey[:]...)
	buf = append(buf, c.Nonce[:]...)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], uint64(c.IssuedAt.UnixNano()))
	buf = append(buf, num[:]...)
	binary.BigEndian.PutUint64(num[:], uint64(c.ExpiresAt.UnixNano()))
	buf = append(buf, num[:]...)
	binary.BigEndian.PutUint64(num[:], c.PriceSats)
	buf = append(buf, num[:]...)
	var modeLen [2]byte
	binary.BigEndian.PutUint16(modeLen[:], uint16(len(mode)))
	buf = append(buf, modeLen[:]...)
	return append(buf, mode...)
}

// signDigest is the 32-byte BIP340 message a buyer signs: SHA-256 over the
// canonical bytes. It is separate from Canonical so the schnorr call always gets
// the fixed width the curve requires, and so the hash is pinned in one place.
func signDigest(canonical []byte) []byte {
	sum := sha256.Sum256(canonical)
	return sum[:]
}

// Claim is a buyer's answer to a Challenge: the terms, the buyer key, and a
// signature by that key over Challenge.Canonical().
type Claim struct {
	Challenge Challenge
	Buyer     PeerKey
	// Sig is 128 characters of lower-case hex: a BIP340 schnorr signature over
	// signDigest(Challenge.Canonical()).
	Sig string
}

// Verify checks a claim against the current time and the source address the FIPS
// transport observed for this tunnel. It returns nil only when the claim is well
// formed, signed by Buyer, bound to observedSource, and inside its window.
//
// The checks run in this fixed order and stop at the first failure, because the
// order decides which failure a caller learns about:
//
//	a. shape      -> ErrClaimMalformed   (zero Buyer, zero Challenge.ExitKey,
//	                                      or Sig not 128 lower-case hex chars)
//	b. signature  -> ErrClaimSignature   (schnorr over Challenge.Canonical()
//	                                      does not verify under Buyer)
//	c. binding    -> ErrClaimUnbound     (Derive(Buyer) outer != observedSource,
//	                                      or observedSource invalid/unspecified)
//	d. expiry     -> ErrClaimExpired     (now is not before ExpiresAt)
//	e. not yet    -> ErrClaimNotYetValid (IssuedAt > now + 60s)
//
// The signature check comes before the binding check on purpose: a claim signed
// by the wrong key is an impersonation attempt and must be reported as such even
// if the tunnel is also wrong, and a claim for the wrong tunnel is reported as
// unbound even if it has also expired. Binding before expiry means an expired
// claim from a foreign tunnel is never mistaken for a lapsed purchase.
//
// Binding is what turns a signature into a purchase for THIS tunnel. An
// unparseable, unspecified or zero observedSource is refused as unbound: there is
// no address to compare against, and accepting such a claim or falling back to
// the zero address would let a transport bug become a free pass.
func (c Claim) Verify(now time.Time, observedSource netip.Addr) error {
	// a. shape.
	if c.Buyer.IsZero() {
		return fmt.Errorf("%w: buyer key is zero", ErrClaimMalformed)
	}
	if c.Challenge.ExitKey.IsZero() {
		return fmt.Errorf("%w: challenge exit key is zero", ErrClaimMalformed)
	}
	if len(c.Sig) != sigHexLen || c.Sig != strings.ToLower(c.Sig) {
		return fmt.Errorf("%w: signature is not %d lower-case hex characters", ErrClaimMalformed, sigHexLen)
	}
	sigBytes, err := hex.DecodeString(c.Sig)
	if err != nil {
		return fmt.Errorf("%w: signature is not hex: %v", ErrClaimMalformed, err)
	}

	// b. proof of key possession.
	if !verifySchnorr(c.Buyer, signDigest(c.Challenge.Canonical()), sigBytes) {
		return fmt.Errorf("%w", ErrClaimSignature)
	}

	// c. binding to the tunnel the transport actually saw.
	if !observedSource.IsValid() || observedSource.IsUnspecified() {
		return fmt.Errorf("%w: observed source %v is not a usable address", ErrClaimUnbound, observedSource)
	}
	outer, _ := Derive(c.Buyer)
	if outer != observedSource {
		return fmt.Errorf("%w: buyer %s derives %s, transport observed %s",
			ErrClaimUnbound, c.Buyer.Hex(), outer, observedSource)
	}

	// d. expiry.
	if !now.Before(c.Challenge.ExpiresAt) {
		return fmt.Errorf("%w: expired at %s, now %s", ErrClaimExpired, c.Challenge.ExpiresAt, now)
	}

	// e. not yet valid.
	if c.Challenge.IssuedAt.After(now.Add(clockSkewAllowance)) {
		return fmt.Errorf("%w: issued at %s, now %s", ErrClaimNotYetValid, c.Challenge.IssuedAt, now)
	}
	return nil
}

// verifySchnorr reports whether sig is a valid BIP340 signature over digest under
// the x-only key. A malformed key or signature is simply not a valid signature,
// so it reports false rather than an error the caller would have to distinguish
// from a bad signature anyway.
func verifySchnorr(key PeerKey, digest, sig []byte) bool {
	pub, err := schnorr.ParsePubKey(key[:])
	if err != nil {
		return false
	}
	parsed, err := schnorr.ParseSignature(sig)
	if err != nil {
		return false
	}
	return parsed.Verify(digest, pub)
}

// NonceCache remembers accepted challenge nonces so the same proof cannot be
// spent twice.
//
// It is IN-MEMORY ONLY. A process restart clears it, so it is not a durable replay
// defence: the real bound on replay is the short challenge TTL (at most 15
// minutes). A restart inside that window re-opens replay for the remainder of the
// window, which is why the TTL is capped rather than freely configurable. Any
// durable defence belongs to a layer that persists state, which this one
// deliberately does not touch.
type NonceCache struct {
	capacity int
	seen     map[ClaimNonce]time.Time
}

// NewNonceCache returns a cache holding at most capacity entries. A capacity below
// 1 is clamped to 1 so the cache can always record one nonce rather than silently
// accepting everything. now is accepted for symmetry with Use and to document
// that this type has no clock of its own: every operation takes the caller's
// current time.
func NewNonceCache(capacity int, now time.Time) *NonceCache {
	_ = now
	if capacity < 1 {
		capacity = 1
	}
	return &NonceCache{capacity: capacity, seen: make(map[ClaimNonce]time.Time, capacity)}
}

// Use records nonce as spent until expiresAt. It returns ErrNonceReused if the
// nonce is already recorded and its window has not passed, and nil otherwise.
//
// Before deciding, it prunes every entry whose expiry is at or before now: an
// expired nonce guards nothing, and keeping it would evict live entries. When the
// cache is still full it evicts the entry with the earliest expiry, which is the
// one closest to being useless anyway, so Len() never exceeds capacity.
func (c *NonceCache) Use(nonce ClaimNonce, expiresAt time.Time, now time.Time) error {
	for k, exp := range c.seen {
		if !now.Before(exp) {
			delete(c.seen, k)
		}
	}
	if _, ok := c.seen[nonce]; ok {
		return fmt.Errorf("%w: nonce %s", ErrNonceReused, nonce.Hex())
	}
	if len(c.seen) >= c.capacity {
		var victim ClaimNonce
		var earliest time.Time
		first := true
		for k, exp := range c.seen {
			if first || exp.Before(earliest) {
				victim, earliest, first = k, exp, false
			}
		}
		delete(c.seen, victim)
	}
	c.seen[nonce] = expiresAt
	return nil
}

// Len reports how many nonces are currently recorded. It never exceeds the cache
// capacity.
func (c *NonceCache) Len() int { return len(c.seen) }
