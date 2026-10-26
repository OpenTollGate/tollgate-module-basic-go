package main

import (
	"fmt"

	"github.com/nbd-wtf/go-nostr/nip19"
)

// encodeNpub is the inverse of the npub decode ValidateMaintainerNpub does, and
// exists so the daemon (and its tests) can turn a router's identity into the
// bech32 spelling config.json holds.
func encodeNpub(hexPubKey string) (string, error) {
	npub, err := nip19.EncodePublicKey(hexPubKey)
	if err != nil {
		return "", fmt.Errorf("encode npub: %w", err)
	}
	return npub, nil
}

// maintainerHexFromNpub converts the config field's bech32 npub into the HEX
// pubkey a NIP-01 `p` tag must carry. An empty input yields empty output (the
// unclaimed case, which emits no tag). A malformed value is an error rather
// than a silent empty tag: a tag that cannot be indexed must never be published
// as if it were a working discovery key.
func maintainerHexFromNpub(npub string) (string, error) {
	if npub == "" {
		return "", nil
	}
	prefix, decoded, err := nip19.Decode(npub)
	if err != nil {
		return "", fmt.Errorf("maintainer npub %q is not decodable: %w", npub, err)
	}
	if prefix != "npub" {
		return "", fmt.Errorf("maintainer key must be an npub, got a %q key", prefix)
	}
	hex, ok := decoded.(string)
	if !ok || len(hex) != 64 {
		return "", fmt.Errorf("maintainer npub decoded to %T of unexpected length", decoded)
	}
	return hex, nil
}
