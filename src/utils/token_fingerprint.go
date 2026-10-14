// Token fingerprinting: the correlate that replaces a spendable Cashu token
// wherever a log line, or a future journal, needs to refer to one.
package utils

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// TokenFingerprintSaltPath is the per-install salt file. It is a *file* rather
// than a constant for two reasons: a fingerprint has to remain comparable across
// a restart (the journal that is meant to key on it survives one), and a value
// that is the same on every TollGate would let two operators correlate their
// logs. It is created lazily, on first use, with 0600 — a pod that never logs a
// token never writes it.
//
// It is a package-level var, like the MAC-resolution paths in main.go, so a unit
// test can point it at a fixture instead of the router's filesystem.
var TokenFingerprintSaltPath = "/etc/tollgate/token-fingerprint.salt"

var (
	tokenFingerprintSaltOnce sync.Once
	tokenFingerprintSalt     []byte
)

// TokenFingerprint returns a stable, non-reversible correlate for a Cashu token:
// 16 hex characters of HMAC-SHA256(salt, canonical token bytes).
//
// Why not the token, even truncated: a logged token is a spendable token, and
// whoever reads the log can claim the value. Why not a bare SHA-256 of the
// token: a bearer instrument in a log invites dictionary attacks on small notes
// and is a correlation across every reader of that log — the salt is per install
// and never leaves the router.
//
// An empty (or whitespace-only) input yields an empty fingerprint: there is no
// token to correlate, and answering with the fingerprint of "" would give every
// such caller the same value. Surrounding whitespace is ignored so the same note
// pasted twice collides deliberately — that property is what lets a future
// journal refuse to grant twice for one token.
func TokenFingerprint(token string) string {
	canonical := strings.TrimSpace(token)
	if canonical == "" {
		return ""
	}

	mac := hmac.New(sha256.New, tokenFingerprintSaltBytes())
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func tokenFingerprintSaltBytes() []byte {
	tokenFingerprintSaltOnce.Do(loadTokenFingerprintSalt)
	return tokenFingerprintSalt
}

func loadTokenFingerprintSalt() {
	if data, err := os.ReadFile(TokenFingerprintSaltPath); err == nil {
		if salt := decodeSaltFile(data); len(salt) >= 16 {
			tokenFingerprintSalt = append([]byte(nil), salt...)
			return
		}
	}

	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		// Not survivable in any useful way, and not worth failing a payment for:
		// fall back to a per-boot constant so fingerprints stay stable within
		// this process and are still not a bare hash.
		log.Printf("token fingerprint: crypto/rand unavailable (%v); using an ephemeral salt", err)
		tokenFingerprintSalt = []byte("tollgate-token-fingerprint-ephemeral")
		return
	}

	if err := writeTokenFingerprintSalt(salt); err != nil {
		// A read-only or full filesystem is a normal router condition, not a
		// reason to stop logging a correlate: keep the salt in memory. The cost
		// is stated where it is incurred — fingerprints will not match across a
		// restart, which matters to the journal, not to today's log line.
		log.Printf("token fingerprint: could not persist the salt at %s (%v); fingerprints will not survive a restart",
			TokenFingerprintSaltPath, err)
	}
	tokenFingerprintSalt = salt
}

func writeTokenFingerprintSalt(salt []byte) error {
	if dir := filepath.Dir(TokenFingerprintSaltPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	// Create-only: an existing salt is never overwritten, because rewriting it
	// would silently change every fingerprint already written down.
	f, err := os.OpenFile(TokenFingerprintSaltPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	// The salt is persisted hex-encoded: raw random bytes can begin or end
	// with whitespace-valued bytes, and a reader that trims whitespace (this
	// file may also be hand-edited) would silently corrupt such a salt —
	// every fingerprint after the next restart would change, breaking the
	// journal correlation without any error anywhere.
	_, err = f.Write([]byte(hex.EncodeToString(salt)))
	return err
}

// decodeSaltFile accepts the current hex-encoded format (whitespace-trimmed:
// hand-edited files keep working) and falls back to raw bytes without any
// trimming, for salts persisted by earlier builds.
func decodeSaltFile(data []byte) []byte {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 {
		if decoded, err := hex.DecodeString(string(trimmed)); err == nil && len(decoded) >= 16 {
			return decoded
		}
	}
	return data
}

// resetTokenFingerprintSaltForTest drops the cached salt so a test can point
// TokenFingerprintSaltPath at its own fixture. Production never calls it; the
// salt is loaded once per process.
func resetTokenFingerprintSaltForTest() {
	tokenFingerprintSaltOnce = sync.Once{}
	tokenFingerprintSalt = nil
}
