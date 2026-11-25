// Package fips_exit derives addresses compatible with fips2go exit nodes.
// It performs no network or operating-system operations.
package fips_exit

import (
	"crypto/sha256"
	"net/netip"
)

// Derive returns the FIPS outer IPv6 and exit-tunnel inner IPv4 addresses
// for a decoded 32-byte x-only public key, matching fips2go v0.11.0.
// Callers must decode and validate their identity before calling Derive;
// this function neither parses npubs nor checks curve membership.
// Inner addresses have only 22 identity bits and can collide: callers must
// not treat them as unique identities or as proof of authorization.
func Derive(publicKey [32]byte) (outer, inner netip.Addr) {
	digest := sha256.Sum256(publicKey[:])
	var address [16]byte
	address[0] = 0xfd
	copy(address[1:], digest[:15])
	return netip.AddrFrom16(address), netip.AddrFrom4([4]byte{
		100, 64 | (digest[0] & 0x3f), digest[1], digest[2],
	})
}
