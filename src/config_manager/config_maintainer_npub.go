package config_manager

import (
	"fmt"

	"github.com/nbd-wtf/go-nostr/nip19"
)

// MaintainerNpub is the Nostr identity that owns a tollgate. It is both the
// authorization boundary of the CVM control plane and the key its discovery
// announcement is indexed under — see
// docs/architecture/cvm-control-plane-decision.md (D3, D4).

// ValidateMaintainerNpub checks the value of the maintainer_npub config field.
//
// Empty is allowed and means "unclaimed": a router that has not been claimed
// still serves customers, it simply has no remote-management identity and does
// not appear in any maintainer's fleet view. Refusing empty here would make a
// stock config un-saveable.
//
// A non-empty value MUST be a bech32 npub1… public key. Raw hex is refused
// rather than normalized on purpose: `identities.json` stores an owned identity
// as hex, so accepting hex would let an operator paste a *private* key material
// spelling (or any 64-hex string) into a field that is published to relays.
// Failing loudly at write time is the only place the mistake is still cheap.
func ValidateMaintainerNpub(value string) error {
	if value == "" {
		return nil
	}
	prefix, decoded, err := nip19.Decode(value)
	if err != nil {
		return fmt.Errorf("maintainer_npub must be a bech32 npub1… key: %w", err)
	}
	if prefix != "npub" {
		return fmt.Errorf("maintainer_npub must be a bech32 npub1… key, got a %q key", prefix)
	}
	pub, ok := decoded.(string)
	if !ok || len(pub) != 64 {
		return fmt.Errorf("maintainer_npub decoded to a %T, want a 32-byte public key", decoded)
	}
	for _, c := range pub {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("maintainer_npub decoded to a non-hex public key")
		}
	}
	return nil
}
