package bearerport

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// Derive is the ONE deterministic derivation the whole port is built on. It is
// a direct generalisation of Cashu NUT-13 (deterministic secrets) with every
// Cashu-specific part removed:
//
//	child = HMAC-SHA256(key = SHA256(label), msg = seed || counter_be64)
//
// Properties the port relies on, each of which is a test:
//   - same (seed, counter, label) => same child on every implementation;
//   - different counters => different children, so no derivation is ever
//     reused (the #257/#266/#480 class of bug is impossible by construction);
//   - the seed cannot be read back out of a child (one-way), so an instrument
//     can never leak the wallet's recovery material.
//
// Nothing here is Cashu. Cashu's NUT-13 happens to be an instance of it.
func Derive(seed []byte, counter uint64, label string) []byte {
	keyHash := sha256.Sum256([]byte(label))
	mac := hmac.New(sha256.New, keyHash[:])
	mac.Write(seed)
	var cb [8]byte
	binary.BigEndian.PutUint64(cb[:], counter)
	mac.Write(cb[:])
	return mac.Sum(nil)
}

// NullifierOf is the holder's anti-double-spend tag: deterministic for the
// holder, meaningless to an observer, and the handle a NUT-07 question is
// asked about. In Cashu this is exactly Y = hash_to_curve(secret).
//
// It is scoped by authority because two different authorities must never share
// a nullifier space — otherwise accepting a note at one authority would make
// the port refuse an unrelated note at another.
//
// NOTE the direction of knowledge, which is the crux of the NUT-07 derivation:
// the authority can only answer about a nullifier it has LEARNED. In vanilla
// Cashu it learns Y at redemption, because redemption is when the holder
// reveals the secret. It does not know Y at issuance. That single fact is why
// a raw mint answers "UNSPENT" for values it has simply never seen.
func NullifierOf(seed []byte, counter uint64, authority string) string {
	return hex16(Derive(seed, counter, authority+"|nullifier"))
}

// CommitmentOf is the authority-side identifier for a derived child: the
// analogue of a proof's blinded commitment (Cashu) or a federation note's
// issuance id. Recovery (NUT-09) is asked in terms of this value only, because
// it is what the authority recorded when it signed.
func CommitmentOf(seed []byte, counter uint64, authority string) string {
	return hex16(Derive(seed, counter, authority+"|commitment"))
}

// SeedFingerprint is a one-way tag for a holder's seed, for logs and demos. It
// is not used as an ownership key: ownership is proven by being able to derive
// the commitment in the first place.
func SeedFingerprint(seed []byte) string {
	return hex16(Derive(seed, 0, "seed-fingerprint"))
}

func hex16(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 32)
	for i := 0; i < 16; i++ {
		out[i*2] = hexdigits[b[i]>>4]
		out[i*2+1] = hexdigits[b[i]&0x0f]
	}
	return string(out)
}
