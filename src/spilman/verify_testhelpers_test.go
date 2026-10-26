package spilman

// Test-only signing helpers.
//
// The pure-Go verifier must never be able to SIGN: a payee that can produce
// balance updates can mint itself credit. These helpers live in a _test.go file
// precisely so nothing in the shipped package can reach them. They exist to build
// the "validly signed, but by the wrong key" negative case, which the Rust fixture
// generator cannot produce: the real signer only ever signs with the payer's key,
// which is the entire point.

import (
	"encoding/hex"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// testingT is the slice of *testing.T these helpers need.
type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
}

// testSecrets maps the compressed pubkeys the golden vector was built from back
// to their secp256k1 secrets, so a test can produce REAL signatures for the
// parties involved. It is computed rather than transcribed: the fixture uses key
// 1 (even Y) and key 6 (odd Y) for Alice, and a hand-copied table is a place for
// a typo to hide.
var testSecrets = func() map[string]string {
	secrets := make(map[string]string, 8)
	for key := uint64(1); key <= 8; key++ {
		var raw [32]byte
		raw[24] = byte(key >> 56)
		raw[25] = byte(key >> 48)
		raw[26] = byte(key >> 40)
		raw[27] = byte(key >> 32)
		raw[28] = byte(key >> 24)
		raw[29] = byte(key >> 16)
		raw[30] = byte(key >> 8)
		raw[31] = byte(key)

		var scalar secp.ModNScalar
		if overflow := scalar.SetByteSlice(raw[:]); overflow {
			panic("test key out of range")
		}
		var point secp.JacobianPoint
		secp.ScalarBaseMultNonConst(&scalar, &point)
		point.ToAffine()
		pubkey := secp.NewPublicKey(&point.X, &point.Y)
		secrets[hex.EncodeToString(pubkey.SerializeCompressed())] = hex.EncodeToString(raw[:])
	}
	return secrets
}()

// secretForPubkey returns the test secret matching a compressed pubkey, or fails
// the test. A key with no registered secret is a test bug, never a silent skip.
func secretForPubkey(t testingT, pubkey *secp.PublicKey) *secp.ModNScalar {
	t.Helper()

	compressed := hex.EncodeToString(pubkey.SerializeCompressed())
	secretHex, ok := testSecrets[compressed]
	if !ok {
		t.Fatalf("no test secret is registered for pubkey %s", compressed)
	}
	raw, err := hex.DecodeString(secretHex)
	if err != nil {
		t.Fatalf("test secret for %s is not hex: %v", compressed, err)
	}
	var scalar secp.ModNScalar
	if overflow := scalar.SetByteSlice(raw); overflow {
		t.Fatalf("test secret for %s is out of range", compressed)
	}
	return &scalar
}

// signDigestForTest produces a real, valid BIP-340 signature over digest under the
// given secret scalar. btcec's default signer draws a random nonce; a test that
// flakes is worse than one that is visibly deterministic, and these are throwaway
// test keys, so the nonce is not a concern here.
func signDigestForTest(t testingT, secret *secp.ModNScalar, digest [32]byte) string {
	t.Helper()

	secretBytes := secret.Bytes()
	private, _ := btcec.PrivKeyFromBytes(secretBytes[:])
	signature, err := schnorr.Sign(private, digest[:])
	if err != nil {
		t.Fatalf("test signature: %v", err)
	}
	return hex.EncodeToString(signature.Serialize())
}

// deriveBlindedSecretForTest is the secret-key counterpart of deriveBlindedPubkey
// (params.rs:220 derive_blinded_secret_key): the signing key k satisfies
// k*G = P', so k = (±s) + tweak, with s negated when the pubkey's Y is odd.
//
// It is used to produce a signature that is GENUINELY VALID under a blinded key
// that is not the sender's — the strongest form of the "wrong signer" negative
// case, because the refusal cannot be blamed on a malformed signature.
func deriveBlindedSecretForTest(t testingT, pubkey *secp.PublicKey, tweak *secp.ModNScalar) *secp.ModNScalar {
	t.Helper()

	secret := secretForPubkey(t, pubkey)
	if pubkey.SerializeCompressed()[0] == 0x03 {
		secret.Negate()
	}
	var blinded secp.ModNScalar
	blinded.Add2(secret, tweak)
	if blinded.IsZero() {
		t.Fatalf("blinded test secret is zero")
	}
	return &blinded
}

// assertSignatureIsValidForKey is a control assertion: it proves a test signature
// is genuinely valid under the key it was made for, so when the verifier refuses
// it, the refusal is about which key may authorise a balance — not about the test
// having produced junk.
func assertSignatureIsValidForKey(t testingT, pubkey *secp.PublicKey, digest [32]byte, signature string) {
	t.Helper()

	if err := verifyBIP340(pubkey, digest, signature); err != nil {
		t.Errorf("control failure: the test signature must be valid under its own key, got %v", err)
	}
}

// mustParsePoint parses a compressed pubkey or fails the test.
func mustParsePoint(t testingT, text string) *secp.PublicKey {
	t.Helper()
	point, err := parseCompressedPoint(text)
	if err != nil {
		t.Fatalf("test pubkey %q: %v", text, err)
	}
	return point
}
