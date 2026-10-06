package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TokenFingerprint is what replaces a Cashu token in a log line: a correlate an
// operator (or a future journal) can follow, and nothing a reader can spend.
//
// Two properties make it usable and two make it safe:
//
//   - stable for the same token, so a payment can be traced through the log and
//     matched against what the customer reports;
//   - distinct for different tokens, so two payments can never be confused;
//   - salted per install, so two TollGates logging the same token produce
//     different values and the log is not a cross-router correlator;
//   - keyed, not a bare hash, so a log reader cannot check a guess against it —
//     a bare SHA-256 of a bearer instrument is a dictionary primitive and a
//     correlation, not a redaction.

// useSalt points the fingerprint salt at a fresh file inside a temporary
// directory and resets the cached salt, so a test observes its own salt rather
// than whatever the process loaded first.
func useSalt(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "token-fingerprint.salt")

	prevPath := TokenFingerprintSaltPath
	TokenFingerprintSaltPath = path
	resetTokenFingerprintSaltForTest()
	t.Cleanup(func() {
		TokenFingerprintSaltPath = prevPath
		resetTokenFingerprintSaltForTest()
	})

	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing the salt fixture: %v", err)
		}
	}
	return path
}

func TestTokenFingerprintIsStableAndDistinct(t *testing.T) {
	useSalt(t, "unit-test-salt-value-0123456789abcd")

	const token = "cashuA-test-note-0001"

	first := TokenFingerprint(token)
	if first != TokenFingerprint(token) {
		t.Fatal("the same token produced two different fingerprints")
	}
	if first == TokenFingerprint(token+"x") {
		t.Fatal("two different tokens produced the same fingerprint")
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(first) {
		t.Fatalf("fingerprint %q is not 16 lowercase hex characters", first)
	}
}

// A retry of the same token must collide deliberately: the journal design keys
// on the fingerprint so one token can never grant twice, and a customer pasting
// the same note with surrounding whitespace is the same note.
func TestTokenFingerprintIgnoresSurroundingWhitespace(t *testing.T) {
	useSalt(t, "unit-test-salt-value-0123456789abcd")

	const token = "cashuA-test-note-0001"

	if TokenFingerprint(token) != TokenFingerprint("  "+token+"\n") {
		t.Fatal("the same token in different whitespace produced different fingerprints")
	}
}

// The salt is per install: the same token on two TollGates must not produce a
// value that links the two logs.
func TestTokenFingerprintIsSaltedPerInstall(t *testing.T) {
	const token = "cashuA-test-note-0001"

	useSalt(t, "first-install-salt-0123456789abcdef")
	first := TokenFingerprint(token)

	useSalt(t, "second-install-salt-0123456789abcdef")
	second := TokenFingerprint(token)

	if first == second {
		t.Fatalf("the same token produced %q under two different salts — the fingerprint is not salted", first)
	}
}

// The salt exists on its own, with restrictive permissions, and survives the
// call that created it (a fingerprint that cannot be recomputed after a restart
// would be useless to the journal that is meant to key on it).
func TestTokenFingerprintSaltFileIsCreatedPrivateAndStable(t *testing.T) {
	path := useSalt(t, "")

	first := TokenFingerprint("cashuA-test-note-0001")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the salt file was not created at %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("salt file mode = %o, want 600", perm)
	}

	// A second process (or a restart) reads the same salt and must recompute the
	// same fingerprint.
	resetTokenFingerprintSaltForTest()
	if second := TokenFingerprint("cashuA-test-note-0001"); second != first {
		t.Fatalf("fingerprint changed after reloading the salt file: %q then %q", first, second)
	}
}

// Not a bare hash: a log reader who has a candidate token must not be able to
// confirm it against the log line.
func TestTokenFingerprintIsNotABareHash(t *testing.T) {
	useSalt(t, "unit-test-salt-value-0123456789abcd")

	const token = "cashuA-test-note-0001"

	sum := sha256.Sum256([]byte(token))
	if bare := hex.EncodeToString(sum[:])[:16]; TokenFingerprint(token) == bare {
		t.Fatalf("the fingerprint is a bare SHA-256 prefix (%s)", bare)
	}
}

// Nothing to correlate: an empty corpus still answers a defined value rather
// than a fingerprint of the empty string, which every caller would share.
func TestTokenFingerprintOfNothingIsEmpty(t *testing.T) {
	useSalt(t, "unit-test-salt-value-0123456789abcd")

	for _, in := range []string{"", "   ", "\n"} {
		if got := TokenFingerprint(in); got != "" {
			t.Fatalf("TokenFingerprint(%q) = %q, want an empty value", in, got)
		}
	}
}

// A negative on the design's one hard constraint: the module must not write a
// new secret where the previous writers did not. The salt is created lazily and
// only when it is actually needed.
func TestTokenFingerprintCreatesNothingUntilUsed(t *testing.T) {
	path := useSalt(t, "")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the salt file exists before any fingerprint was computed: %v", err)
	}
	if got := TokenFingerprint(""); got != "" {
		t.Fatalf("TokenFingerprint(\"\") = %q, want empty", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an empty token created the salt file: %v", err)
	}
}

// Regression: a random salt can begin or end with a byte whose value is
// whitespace (tab here: 0x09). The salt file is hex-encoded, so the
// whitespace-trimming reader cannot corrupt it — write, reload, fingerprint
// again: all three must agree.
func TestTokenFingerprintSaltWithWhitespaceValuedBytesSurvivesReload(t *testing.T) {
	useSalt(t, "")

	salt := make([]byte, 32)
	salt[0] = 0x09  // tab: would be trimmed as a raw byte
	salt[31] = 0x20 // space: same at the tail
	for i := 1; i < 31; i++ {
		salt[i] = byte('a')
	}
	if err := writeTokenFingerprintSalt(salt); err != nil {
		t.Fatalf("writing the salt fixture: %v", err)
	}

	first := TokenFingerprint("cashuA-test-note-0001")
	if first == "" {
		t.Fatal("no fingerprint")
	}
	resetTokenFingerprintSaltForTest()
	if second := TokenFingerprint("cashuA-test-note-0001"); second != first {
		t.Fatalf("fingerprint changed after reloading a salt with whitespace-valued edge bytes: %q then %q", first, second)
	}
}
