package fips_exit

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/nbd-wtf/go-nostr"
)

// ---- fixtures -------------------------------------------------------------

var baseTime = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

// buyer is a signing identity: its key is the PeerKey a claim names, and sk
// signs the canonical bytes.
type buyer struct {
	key PeerKey
	sk  string
}

func newBuyer(t *testing.T) buyer {
	t.Helper()
	sk := nostr.GeneratePrivateKey()
	pubHex, err := nostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("derive pubkey: %v", err)
	}
	key, err := ParsePeerKey(pubHex)
	if err != nil {
		t.Fatalf("parse pubkey: %v", err)
	}
	return buyer{key: key, sk: sk}
}

func (b buyer) sign(t *testing.T, msg []byte) string {
	t.Helper()
	raw, err := hex.DecodeString(b.sk)
	if err != nil {
		t.Fatalf("decode seckey: %v", err)
	}
	priv, _ := btcec.PrivKeyFromBytes(raw)
	sig, err := schnorr.Sign(priv, signDigest(msg))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return hex.EncodeToString(sig.Serialize())
}

func testChallenge(t *testing.T, exitKey PeerKey) Challenge {
	t.Helper()
	c, err := NewChallenge(bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)), baseTime, 10*time.Minute, exitKey, 210, BillingTime)
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	return c
}

func validClaim(t *testing.T, exitKey PeerKey, b buyer) Claim {
	t.Helper()
	ch := testChallenge(t, exitKey)
	return Claim{Challenge: ch, Buyer: b.key, Sig: b.sign(t, ch.Canonical())}
}

func observed(b buyer) netip.Addr { return b.key.OuterAddress() }

// ---- happy path -----------------------------------------------------------

func TestValidClaimVerifies(t *testing.T) {
	ex := newBuyer(t)
	b := newBuyer(t)
	c := validClaim(t, ex.key, b)
	if err := c.Verify(baseTime.Add(time.Minute), observed(b)); err != nil {
		t.Fatalf("Verify() = %v, want nil", err)
	}
}

// ---- each failure mode has its own sentinel -------------------------------

func TestClaimFailureSentinels(t *testing.T) {
	ex := newBuyer(t)
	b := newBuyer(t)
	other := newBuyer(t)

	good := validClaim(t, ex.key, b)

	zeroBuyer := good
	zeroBuyer.Buyer = PeerKey{}

	zeroExit := good
	zeroExit.Challenge.ExitKey = PeerKey{}

	badHex := good
	badHex.Sig = "zz" + good.Sig[2:]

	shortSig := good
	shortSig.Sig = good.Sig[:126]

	uppercaseSig := good
	uppercaseSig.Sig = strings.ToUpper(good.Sig)

	// Signed by "other" but claiming to be "b": the stolen-npub case.
	stolen := good
	stolen.Sig = other.sign(t, good.Challenge.Canonical())

	tampered := good
	tampered.Challenge.PriceSats = 1

	tests := []struct {
		name    string
		claim   Claim
		now     time.Time
		src     netip.Addr
		wantErr error
	}{
		{"zero buyer", zeroBuyer, baseTime.Add(time.Minute), observed(b), ErrClaimMalformed},
		{"zero exit key", zeroExit, baseTime.Add(time.Minute), observed(b), ErrClaimMalformed},
		{"sig not hex", badHex, baseTime.Add(time.Minute), observed(b), ErrClaimMalformed},
		{"sig too short", shortSig, baseTime.Add(time.Minute), observed(b), ErrClaimMalformed},
		{"sig uppercase", uppercaseSig, baseTime.Add(time.Minute), observed(b), ErrClaimMalformed},
		{"tampered terms", tampered, baseTime.Add(time.Minute), observed(b), ErrClaimSignature},
		{"stolen npub", stolen, baseTime.Add(time.Minute), observed(b), ErrClaimSignature},
		{"unbound source", good, baseTime.Add(time.Minute), observed(other), ErrClaimUnbound},
		{"unbound invalid source", good, baseTime.Add(time.Minute), netip.Addr{}, ErrClaimUnbound},
		{"expired", good, baseTime.Add(10 * time.Minute), observed(b), ErrClaimExpired},
		{"expired just after", good, baseTime.Add(10*time.Minute + time.Nanosecond), observed(b), ErrClaimExpired},
		{"not yet valid", good, baseTime.Add(-2 * time.Minute), observed(b), ErrClaimNotYetValid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.claim.Verify(tc.now, tc.src)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestClaimStolenNpubIsRejected(t *testing.T) {
	ex := newBuyer(t)
	victim := newBuyer(t)
	attacker := newBuyer(t)

	ch := testChallenge(t, ex.key)

	// The attacker submits the victim's npub as the buyer but can only sign
	// with its own key. This must not be accepted.
	c := Claim{Challenge: ch, Buyer: victim.key, Sig: attacker.sign(t, ch.Canonical())}
	if err := c.Verify(baseTime.Add(time.Minute), observed(victim)); !errors.Is(err, ErrClaimSignature) {
		t.Fatalf("Verify() = %v, want ErrClaimSignature", err)
	}
}

func TestClaimNotMyTunnelIsRejected(t *testing.T) {
	ex := newBuyer(t)
	b := newBuyer(t)
	other := newBuyer(t)

	// Correctly signed by b, but the tunnel the exit observed belongs to a
	// different peer: the claim proves key possession, not this tunnel.
	c := validClaim(t, ex.key, b)
	if err := c.Verify(baseTime.Add(time.Minute), observed(other)); !errors.Is(err, ErrClaimUnbound) {
		t.Fatalf("Verify() = %v, want ErrClaimUnbound", err)
	}
}

// A valid claim delivered from the unspecified source address must be refused,
// never quietly accepted.
func TestClaimUnspecifiedSourceNeverAccepted(t *testing.T) {
	ex := newBuyer(t)
	b := newBuyer(t)
	c := validClaim(t, ex.key, b)
	for _, src := range []netip.Addr{netip.Addr{}, netip.IPv6Unspecified(), netip.MustParseAddr("::")} {
		if err := c.Verify(baseTime.Add(time.Minute), src); !errors.Is(err, ErrClaimUnbound) {
			t.Fatalf("Verify(%v) = %v, want ErrClaimUnbound", src, err)
		}
	}
}

// A buyer key that is not a curve point cannot carry a valid signature, and a
// signature with an out-of-range scalar cannot be parsed: both are simply not a
// valid signature, reported as ErrClaimSignature.
func TestClaimUnparseableCryptoIsSignatureFailure(t *testing.T) {
	ex := newBuyer(t)
	b := newBuyer(t)
	good := validClaim(t, ex.key, b)

	notOnCurve := good
	notOnCurve.Buyer = PeerKey{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if err := notOnCurve.Verify(baseTime.Add(time.Minute), notOnCurve.Buyer.OuterAddress()); !errors.Is(err, ErrClaimSignature) {
		t.Fatalf("Verify() with off-curve key = %v, want ErrClaimSignature", err)
	}

	outOfRange := good
	outOfRange.Sig = strings.Repeat("ff", 64)
	if err := outOfRange.Verify(baseTime.Add(time.Minute), observed(b)); !errors.Is(err, ErrClaimSignature) {
		t.Fatalf("Verify() with out-of-range signature = %v, want ErrClaimSignature", err)
	}
}

// ---- ordering -------------------------------------------------------------

func TestClaimVerifyOrder(t *testing.T) {
	ex := newBuyer(t)
	b := newBuyer(t)
	other := newBuyer(t)
	good := validClaim(t, ex.key, b)

	t.Run("unbound beats expired", func(t *testing.T) {
		err := good.Verify(baseTime.Add(24*time.Hour), observed(other))
		if !errors.Is(err, ErrClaimUnbound) {
			t.Fatalf("Verify() = %v, want ErrClaimUnbound", err)
		}
	})

	t.Run("signature beats unbound", func(t *testing.T) {
		bad := good
		bad.Sig = other.sign(t, good.Challenge.Canonical())
		err := bad.Verify(baseTime.Add(time.Minute), observed(other))
		if !errors.Is(err, ErrClaimSignature) {
			t.Fatalf("Verify() = %v, want ErrClaimSignature", err)
		}
	})

	t.Run("malformed beats signature", func(t *testing.T) {
		bad := good
		bad.Buyer = PeerKey{}
		bad.Sig = "nonsense"
		err := bad.Verify(baseTime.Add(time.Minute), observed(b))
		if !errors.Is(err, ErrClaimMalformed) {
			t.Fatalf("Verify() = %v, want ErrClaimMalformed", err)
		}
	})

	t.Run("expired beats not yet valid", func(t *testing.T) {
		// now is past ExpiresAt AND IssuedAt is more than the 60s skew ahead of
		// now: both expiry and not-yet-valid hold, and expiry must win.
		c := good
		c.Challenge.IssuedAt = baseTime.Add(30 * time.Minute)
		c.Challenge.ExpiresAt = baseTime.Add(20 * time.Minute)
		c.Sig = b.sign(t, c.Challenge.Canonical())
		err := c.Verify(baseTime.Add(25*time.Minute), observed(b))
		if !errors.Is(err, ErrClaimExpired) {
			t.Fatalf("Verify() = %v, want ErrClaimExpired", err)
		}
	})
}

// ---- Canonical ------------------------------------------------------------

func TestCanonicalDeterministicAndFieldDistinct(t *testing.T) {
	ex := newBuyer(t)
	base := testChallenge(t, ex.key)

	prefix := "tollgate-fips-exit/claim\x00"
	b := base.Canonical()
	if !bytes.HasPrefix(b, []byte(prefix)) {
		t.Fatalf("Canonical() does not start with domain prefix %q: %x", prefix, b)
	}
	if b[len(prefix)] != canonicalVersion {
		t.Fatalf("version byte = %d, want %d", b[len(prefix)], canonicalVersion)
	}
	if !bytes.Equal(b, base.Canonical()) {
		t.Fatal("Canonical() is not deterministic")
	}
	// Mutating the returned slice must not affect later calls.
	b[0] ^= 0xff
	if !bytes.Equal(base.Canonical(), base.Canonical()) || base.Canonical()[0] == 0xff {
		t.Fatal("Canonical() returned an aliased buffer")
	}

	variants := map[string]Challenge{}
	c := base
	c.ExitKey[0] ^= 0x01
	variants["ExitKey"] = c
	c = base
	c.Nonce[0] ^= 0x01
	variants["Nonce"] = c
	c = base
	c.IssuedAt = c.IssuedAt.Add(time.Nanosecond)
	variants["IssuedAt"] = c
	c = base
	c.ExpiresAt = c.ExpiresAt.Add(time.Nanosecond)
	variants["ExpiresAt"] = c
	c = base
	c.PriceSats++
	variants["PriceSats"] = c
	c = base
	c.Mode = BillingBytes
	variants["Mode"] = c

	seen := map[string]string{"base": string(base.Canonical())}
	for name, v := range variants {
		got := string(v.Canonical())
		if got == seen["base"] {
			t.Errorf("%s: Canonical() unchanged after flipping the field", name)
		}
		for other, otherBytes := range seen {
			if got == otherBytes {
				t.Errorf("%s collides with %s", name, other)
			}
		}
		seen[name] = got
	}
}

func TestCanonicalModeIsUnambiguous(t *testing.T) {
	ex := newBuyer(t)
	a := testChallenge(t, ex.key)
	b := a
	b.Mode = BillingMode(string(a.Mode) + "x")
	if bytes.Equal(a.Canonical(), b.Canonical()) {
		t.Fatal("a longer mode string serialised identically; length prefix missing")
	}
}

// ---- NewChallenge refusals ------------------------------------------------

func TestNewChallengeRefusesBadInput(t *testing.T) {
	ex := newBuyer(t)
	r := func() *bytes.Reader { return bytes.NewReader(bytes.Repeat([]byte{0x11}, 64)) }

	tests := []struct {
		name  string
		ttl   time.Duration
		key   PeerKey
		price uint64
	}{
		{"zero exit key", 10 * time.Minute, PeerKey{}, 210},
		{"zero ttl", 0, ex.key, 210},
		{"negative ttl", -time.Minute, ex.key, 210},
		{"ttl beyond 15m", 15*time.Minute + time.Nanosecond, ex.key, 210},
		{"zero price", 10 * time.Minute, ex.key, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewChallenge(r(), baseTime, tc.ttl, tc.key, tc.price, BillingTime); err == nil {
				t.Fatal("NewChallenge() = nil error, want refusal")
			}
		})
	}
}

func TestNewChallengeAcceptsMaxTTL(t *testing.T) {
	ex := newBuyer(t)
	c, err := NewChallenge(bytes.NewReader(make([]byte, 16)), baseTime, 15*time.Minute, ex.key, 1, BillingBytes)
	if err != nil {
		t.Fatalf("NewChallenge(15m) = %v, want nil", err)
	}
	if !c.IssuedAt.Equal(baseTime) {
		t.Errorf("IssuedAt = %v, want %v", c.IssuedAt, baseTime)
	}
	if !c.ExpiresAt.Equal(baseTime.Add(15 * time.Minute)) {
		t.Errorf("ExpiresAt = %v, want %v", c.ExpiresAt, baseTime.Add(15*time.Minute))
	}
	if c.Nonce.Hex() != strings.Repeat("00", 16) {
		t.Errorf("Nonce = %s, want zeroes from the injected reader", c.Nonce.Hex())
	}
	if len(c.Nonce.Hex()) != 32 {
		t.Errorf("Nonce.Hex() length = %d, want 32", len(c.Nonce.Hex()))
	}
}

func TestNewChallengeRefusesShortRandom(t *testing.T) {
	ex := newBuyer(t)
	if _, err := NewChallenge(bytes.NewReader([]byte{1, 2, 3}), baseTime, time.Minute, ex.key, 1, BillingTime); err == nil {
		t.Fatal("NewChallenge() with 3 random bytes = nil error, want failure")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestNewChallengePropagatesRandomError(t *testing.T) {
	ex := newBuyer(t)
	if _, err := NewChallenge(errReader{}, baseTime, time.Minute, ex.key, 1, BillingTime); err == nil {
		t.Fatal("NewChallenge() with failing reader = nil error, want failure")
	}
}

func TestClaimNonceHexIsFixedWidth(t *testing.T) {
	var n ClaimNonce
	if len(n.Hex()) != 32 || n.Hex() != strings.Repeat("0", 32) {
		t.Fatalf("zero nonce hex = %q", n.Hex())
	}
	n[15] = 0xff
	if !strings.HasSuffix(n.Hex(), "ff") {
		t.Fatalf("nonce hex = %q, want lower-case suffix ff", n.Hex())
	}
}

// ---- NonceCache -----------------------------------------------------------

func TestNonceCacheRejectsReplay(t *testing.T) {
	ex := newBuyer(t)
	b := newBuyer(t)
	c := validClaim(t, ex.key, b)

	// Replay into a fresh cache (a restarted process) is accepted: the cache
	// is in-memory only, which is exactly why the challenge TTL must be short.
	cache := NewNonceCache(8, baseTime)
	other := NewNonceCache(8, baseTime)
	if err := other.Use(c.Challenge.Nonce, c.Challenge.ExpiresAt, baseTime); err != nil {
		t.Fatalf("first Use() = %v, want nil", err)
	}
	if err := other.Use(c.Challenge.Nonce, c.Challenge.ExpiresAt, baseTime.Add(time.Second)); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("second Use() = %v, want ErrNonceReused", err)
	}
	_ = cache
}

func TestNonceCacheCapacityEvictsEarliestExpiry(t *testing.T) {
	now := baseTime
	cache := NewNonceCache(2, now)
	n1, n2, n3 := ClaimNonce{1}, ClaimNonce{2}, ClaimNonce{3}

	if err := cache.Use(n1, now.Add(10*time.Minute), now); err != nil {
		t.Fatalf("Use(n1) = %v", err)
	}
	if err := cache.Use(n2, now.Add(20*time.Minute), now); err != nil {
		t.Fatalf("Use(n2) = %v", err)
	}
	if cache.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", cache.Len())
	}
	// Full: n3 evicts n1, the entry with the earliest expiry.
	if err := cache.Use(n3, now.Add(30*time.Minute), now); err != nil {
		t.Fatalf("Use(n3) = %v", err)
	}
	if cache.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (bounded by capacity)", cache.Len())
	}
	if err := cache.Use(n2, now.Add(20*time.Minute), now); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("Use(n2) again = %v, want ErrNonceReused", err)
	}
	if err := cache.Use(n1, now.Add(time.Minute), now); err != nil {
		t.Fatalf("Use(n1) after eviction = %v, want nil", err)
	}
}

func TestNonceCachePrunesExpired(t *testing.T) {
	now := baseTime
	cache := NewNonceCache(4, now)
	n1, n2 := ClaimNonce{1}, ClaimNonce{2}

	if err := cache.Use(n1, now.Add(time.Minute), now); err != nil {
		t.Fatalf("Use(n1) = %v", err)
	}
	if cache.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", cache.Len())
	}
	// One second after n1 expired, a new Use prunes it. The same nonce is then
	// acceptable only because its window is over.
	later := now.Add(time.Minute + time.Second)
	if err := cache.Use(n2, later.Add(5*time.Minute), later); err != nil {
		t.Fatalf("Use(n2) = %v", err)
	}
	if cache.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 after pruning n1", cache.Len())
	}
	if err := cache.Use(n1, later.Add(time.Hour), later); err != nil {
		t.Fatalf("Use(n1) after expiry = %v, want nil", err)
	}
}

func TestNonceCacheNeverGrowsUnbounded(t *testing.T) {
	now := baseTime
	cache := NewNonceCache(8, now)
	for i := 0; i < 1000; i++ {
		var n ClaimNonce
		n[0] = byte(i)
		n[1] = byte(i >> 8)
		if err := cache.Use(n, now.Add(time.Duration(i+1)*time.Minute), now); err != nil {
			t.Fatalf("Use(%d) = %v", i, err)
		}
		if cache.Len() > 8 {
			t.Fatalf("Len() = %d after %d uses, want <= 8", cache.Len(), i+1)
		}
	}
}

func TestNewNonceCacheTinyCapacity(t *testing.T) {
	now := baseTime
	cache := NewNonceCache(0, now)
	if err := cache.Use(ClaimNonce{9}, now.Add(time.Minute), now); err != nil {
		t.Fatalf("Use() = %v", err)
	}
	if cache.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 (capacity clamped to at least 1)", cache.Len())
	}
}
