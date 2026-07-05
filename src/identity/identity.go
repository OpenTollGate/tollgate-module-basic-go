// Package identity derives stable, router-local identifiers (IPv4 address,
// per-interface MAC addresses, and a BIP-39 recovery mnemonic) from the
// TollGate merchant Nostr key.
//
// All deterministic identifiers are derived from the public npub using a
// domain-separated SHA-256 digest (see DeriveDigest). The mnemonic encodes
// the raw 32-byte private key as 24 BIP-39 words and is therefore secret
// material — it must never be exposed over a non-local transport.
//
// The package is intentionally additive: it reads the existing
// owned_identities[0].privatekey field and never writes back to
// identities.json. A missing or malformed key simply produces an error from
// the relevant function; it never panics and never blocks boot.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/tyler-smith/go-bip39"
)

// DefaultMACInterfaces is the set of interfaces for which the TollGate router
// derives a deterministic, locally-administered MAC address.
var DefaultMACInterfaces = []string{"br-lan", "wlan0", "wlan1"}

// PrivateKeyByteLen is the length of a Nostr (secp256k1) private key in bytes.
const PrivateKeyByteLen = 32

// Domain separators for DeriveDigest. Versioned so the derivation can be
// rotated in the future without renaming functions.
const (
	domainIPv4 = "tollgate:identity:ipv4:v1"
	domainMAC  = "tollgate:identity:mac:v1"
)

// Sentinel errors. Use errors.Is to discriminate.
var (
	ErrEmptyPrivateKey = errors.New("identity: private key is empty")
	ErrEmptyNpub       = errors.New("identity: npub is empty")
	ErrEmptyDomain     = errors.New("identity: domain separator is empty")
	ErrEmptyInterface  = errors.New("identity: interface name is empty")
	ErrBadKeyLength    = errors.New("identity: private key must be 32 bytes")
)

// NpubFromPrivateKey converts a Nostr private key (64-char hex) to its bech32
// npub encoding. The key is validated via secp256k1 scalar multiplication, so
// invalid keys return an error.
func NpubFromPrivateKey(privateKeyHex string) (string, error) {
	if privateKeyHex == "" {
		return "", ErrEmptyPrivateKey
	}
	pubKeyHex, err := nostr.GetPublicKey(privateKeyHex)
	if err != nil {
		return "", fmt.Errorf("identity: derive public key: %w", err)
	}
	npub, err := nip19.EncodePublicKey(pubKeyHex)
	if err != nil {
		return "", fmt.Errorf("identity: encode npub: %w", err)
	}
	return npub, nil
}

// DeriveDigest is the domain-separated derivation primitive backing every
// deterministic identifier in this package. It returns SHA-256(domain || npub).
//
// Derivation is performed from the public npub (not the private key) so that
// the resulting IPv4 / MAC addresses can be recomputed by any party that
// knows the router's public identity, while the seed stays secret.
func DeriveDigest(npub, domain string) ([]byte, error) {
	if npub == "" {
		return nil, ErrEmptyNpub
	}
	if domain == "" {
		return nil, ErrEmptyDomain
	}
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte(npub))
	return h.Sum(nil), nil
}

// DeriveIPv4 derives a deterministic IPv4 address in the RFC 1918 range
// 10.0.0.0/8 from the public npub. The host octets (octets 2-4) are taken
// from DeriveDigest; the last octet is clamped to the range 1-254 so the
// result is always a usable host address rather than a network or broadcast
// address.
func DeriveIPv4(npub string) (net.IP, error) {
	d, err := DeriveDigest(npub, domainIPv4)
	if err != nil {
		return nil, err
	}
	o2 := d[0]
	o3 := d[1]
	o4 := d[2]
	if o4 == 0 {
		o4 = 1
	} else if o4 == 255 {
		o4 = 254
	}
	return net.IPv4(10, o2, o3, o4).To4(), nil
}

// DeriveMAC derives a deterministic, locally-administered (LAA) unicast MAC
// address for the named interface from the public npub. The interface name is
// part of the domain separator, so each interface gets a distinct address.
//
// The first octet has the locally-administered bit set and the multicast bit
// cleared (IEEE 802): mac[0] = (mac[0] & 0xFC) | 0x02.
func DeriveMAC(npub, iface string) (net.HardwareAddr, error) {
	if iface == "" {
		return nil, ErrEmptyInterface
	}
	d, err := DeriveDigest(npub, domainMAC+":"+iface)
	if err != nil {
		return nil, err
	}
	mac := make([]byte, 6)
	copy(mac, d[:6])
	mac[0] = (mac[0] & 0xFC) | 0x02 // bit1=1 (LAA), bit0=0 (unicast)
	return mac, nil
}

// DeriveDefaultMACs returns the deterministic MAC address for every interface
// listed in DefaultMACInterfaces as a map of interface name -> colon-separated
// MAC string. It is a convenience wrapper around DeriveMAC for the
// GET /identity endpoint.
func DeriveDefaultMACs(npub string) (map[string]string, error) {
	out := make(map[string]string, len(DefaultMACInterfaces))
	for _, iface := range DefaultMACInterfaces {
		mac, err := DeriveMAC(npub, iface)
		if err != nil {
			return nil, err
		}
		out[iface] = mac.String()
	}
	return out, nil
}

// PrivateKeyToMnemonic encodes a 32-byte Nostr private key (64-char hex) as 24
// BIP-39 words. The raw key bytes are used directly as BIP-39 entropy, which
// yields exactly 24 words for a 256-bit key.
func PrivateKeyToMnemonic(privateKeyHex string) (string, error) {
	if privateKeyHex == "" {
		return "", ErrEmptyPrivateKey
	}
	b, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return "", fmt.Errorf("identity: decode private key hex: %w", err)
	}
	if len(b) != PrivateKeyByteLen {
		return "", fmt.Errorf("%w: got %d", ErrBadKeyLength, len(b))
	}
	mnemonic, err := bip39.NewMnemonic(b)
	if err != nil {
		return "", fmt.Errorf("identity: encode mnemonic: %w", err)
	}
	return mnemonic, nil
}

// MnemonicToPrivateKey decodes a 24-word BIP-39 mnemonic back into the original
// 32-byte Nostr private key (64-char hex). The mnemonic checksum is validated;
// an invalid or truncated mnemonic returns an error.
func MnemonicToPrivateKey(mnemonic string) (string, error) {
	mnemonic = strings.TrimSpace(mnemonic)
	if mnemonic == "" {
		return "", errors.New("identity: mnemonic is empty")
	}
	if !bip39.IsMnemonicValid(mnemonic) {
		return "", errors.New("identity: invalid mnemonic")
	}
	entropy, err := bip39.EntropyFromMnemonic(mnemonic)
	if err != nil {
		return "", fmt.Errorf("identity: decode mnemonic: %w", err)
	}
	if len(entropy) != PrivateKeyByteLen {
		return "", fmt.Errorf("%w: mnemonic decodes to %d bytes", ErrBadKeyLength, len(entropy))
	}
	return hex.EncodeToString(entropy), nil
}
