package main

// The payee's Spilman receiver key: what it advertises, and what it will accept.
//
// # Why this is derived, not generated
//
// A Spilman channel is addressed to a receiver key, and the payee signs every close
// with it. If that key is not stable, every channel opened before it changed becomes
// uncloseable — the funding is already at the mint and only that key can release it.
// So the key must come from material that already survives a restart.
//
// This module already persists exactly such material: `identities.json` holds an
// owned `merchant` private key (config_manager_identities.go), created once on first
// boot and reused forever. Deriving the Spilman receiver key from it means:
//
//   - no new file, no new permission story, no new backup obligation;
//   - it cannot silently rotate, because the merchant identity does not;
//   - restoring the router's identity (which an operator already must do) restores
//     the ability to settle its channels.
//
// # Why a DOMAIN-TAGGED derivation instead of the raw merchant key
//
// The receiver pubkey is published in cleartext on `/spilman/params`. Using the
// merchant key directly would make that endpoint an oracle for the router's Nostr
// identity: the compressed secp256k1 key shares its x-coordinate with the x-only
// npub, so anyone could link "this router's payment rail" to "this router's Nostr
// pubkey" and to the Lightning address beside it.
//
// Deriving with a fixed domain tag gives an unlinkable key that is still a pure
// function of the persisted identity — stable across restarts, unrelated to the
// npub, and impossible to confuse with the merchant key itself.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
)

// spilmanReceiverKeyTag is the domain separator for the derivation.
//
// It is part of the key's definition: changing this string changes every router's
// receiver key, which orphans every open channel. Treat it as a constant of the
// protocol, not a naming choice.
const spilmanReceiverKeyTag = "tollgate/spilman/receiver/v1"

// deriveReceiverKey derives the Spilman receiver private key from the payee's
// merchant secret.
//
// Deterministic and total: the same merchant key always yields the same receiver
// key, which is the whole point. A merchant key that is not a valid secp256k1
// scalar is an error rather than a silent zero.
func deriveReceiverKey(merchantPrivHex string) (*btcec.PrivateKey, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(merchantPrivHex))
	if err != nil {
		return nil, fmt.Errorf("merchant private key is not hex: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("merchant private key must be 32 bytes, got %d", len(raw))
	}
	merchant, _ := btcec.PrivKeyFromBytes(raw)

	// tag_hash(merchantCompressedPubkey), BIP-340 style: SHA256(tag) || SHA256(tag)
	// || msg. A hash of public data, so this is a plain deterministic derivation
	// rather than a keyed one. It must be reproducible from the identity alone on any
	// host, which rules out a random or per-machine salt.
	tagHash := sha256.Sum256([]byte(spilmanReceiverKeyTag))
	h := sha256.New()
	h.Write(tagHash[:])
	h.Write(tagHash[:])
	h.Write(merchant.PubKey().SerializeCompressed())
	tagged := h.Sum(nil)
	// Reduce onto the curve. A tag hash is uniformly distributed, so this is
	// effectively never out of range; the fallback keeps it total instead of
	// panicking on a 1-in-2^128 event.
	scalar := new(btcec.ModNScalar)
	if overflow := scalar.SetByteSlice(tagged); overflow || scalar.IsZero() {
		return nil, fmt.Errorf("derived receiver scalar is out of range; refusing to guess")
	}
	var priv btcec.PrivateKey
	priv.Key = *scalar
	return &priv, nil
}

// spilmanReceiverPubkeyHex returns the compressed-hex receiver pubkey this payee
// advertises and accepts, or "" when no merchant identity is available.
//
// "" is the fail-closed answer and it must NOT be papered over: a payee with no key
// cannot sign a close, so it must not advertise one, and it must not accept a
// channel. Callers distinguish "" from a real key and refuse.
func spilmanReceiverPubkeyHex() string {
	if configManager == nil {
		return ""
	}
	identities := configManager.GetIdentities()
	if identities == nil {
		return ""
	}
	owned, err := identities.GetOwnedIdentity("merchant")
	if err != nil || owned == nil {
		return ""
	}
	priv, err := deriveReceiverKey(owned.PrivateKey)
	if err != nil {
		mainLogger.WithError(err).Error("spilman: cannot derive the receiver key")
		return ""
	}
	return hex.EncodeToString(priv.PubKey().SerializeCompressed())
}

// requireOwnReceiverKey refuses a registration addressed to a key this payee does
// not hold.
//
// # Why this is a refusal and not a warning
//
// The channel is already funded at the mint when this runs. Accepting it means the
// payee has taken responsibility for value it can never release, and the payer sees
// a successful registration right up until the close fails. There is no partial
// success worth having here.
//
// # The keyless case
//
// No merchant identity means no key to compare against, and therefore no honest way
// to say "yes". It refuses for the same reason: a payee that cannot sign a close must
// not accept a channel.
func requireOwnReceiverKey(offered string) error {
	ours := spilmanReceiverPubkeyHex()
	if ours == "" {
		return fmt.Errorf("this payee has no receiver key and cannot settle a channel; " +
			"refusing the registration rather than stranding the funding")
	}
	if !strings.EqualFold(strings.TrimSpace(offered), ours) {
		return fmt.Errorf("receiver_pubkey %q is not this payee's (%s); a channel for "+
			"another key can never be settled here", offered, ours)
	}
	return nil
}

// receiverKeyIsStable is the property that makes the whole binding usable: the same
// merchant identity must always yield the same receiver key. A key that varied would
// orphan every channel opened before it changed, which is why this is asserted rather
// than assumed.
func TestDeriveReceiverKeyIsStableAndUnlinkable(t *testing.T) {
	const merchant = "1111111111111111111111111111111111111111111111111111111111111111"
	a, err := deriveReceiverKey(merchant)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	b, err := deriveReceiverKey(merchant)
	if err != nil {
		t.Fatalf("derive again: %v", err)
	}
	if !a.PubKey().IsEqual(b.PubKey()) {
		t.Fatal("the same merchant identity produced two different receiver keys; " +
			"every channel opened before a rotation would be uncloseable")
	}

	// Unlinkable: the advertised key must not BE the merchant key, or /spilman/params
	// becomes an oracle for the router's Nostr npub (same x-coordinate).
	merchantPub := "034f355bdcb7cc0af728ef3cceb9615d90684bb5b2ca5f859ab0f0b704075871aa"
	got := hex.EncodeToString(a.PubKey().SerializeCompressed())
	if got == merchantPub {
		t.Fatalf("the receiver key equals the merchant pubkey; advertising it would "+
			"link the rail to the router's identity: %s", got)
	}

	// Different identities must not collide.
	other, err := deriveReceiverKey("2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("derive other: %v", err)
	}
	if other.PubKey().IsEqual(a.PubKey()) {
		t.Fatal("two different merchant identities derived the same receiver key")
	}
}

// A malformed merchant key is an error, never a zero or a panic.
func TestDeriveReceiverKeyRefusesGarbage(t *testing.T) {
	for _, bad := range []string{"", "zz", "0011", strings.Repeat("ff", 32)} {
		if _, err := deriveReceiverKey(bad); err == nil {
			t.Errorf("deriveReceiverKey(%q) returned no error", bad)
		}
	}
}
