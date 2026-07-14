// Package identity derives deterministic network identity attributes — an IPv4
// address, per-interface MAC addresses, and a BIP39 recovery seed — from an
// existing TollGate merchant Nostr private key.
//
// The merchant private key is a 32-byte secp256k1 scalar stored as lowercase
// hex in /etc/tollgate/identities.json at owned_identities[0].privatekey (see
// config_manager.OwnedIdentity). This package treats that key as the single
// source of truth and derives everything else from it, so a router that
// restores its identities.json — or recovers from the 12-word seed — reproduces
// the same IPv4 and MAC addresses bit-for-bit.
//
// Every derivation hashes the hex-encoded secp256k1 public key (the 32-byte
// X-coordinate, 64 hex chars — what nostr.GetPublicKey returns) together with a
// domain separator using SHA-256. Hashing the public key (rather than the
// private key) means the publicly-derivable attributes depend only on the
// PUBLIC identity, and two TollGates with different keys never collide on
// address space. Using the hex pubkey (rather than the bech32 npub encoding)
// keeps the shell-side uci-defaults script simple: it can extract the same
// value via openssl without a bech32 dependency.
//
// Mnemonic recovery uses NIP-06: a 12-word BIP39 mnemonic (128 bits of entropy)
// derives the Nostr private key via the standard hierarchical path
// m/44'/1237'/0'/0/0. 12 words is sufficient because secp256k1 itself provides
// only ~128 bits of security (Pollard's rho on the 256-bit group). Backing up
// the 12 words is equivalent to backing up the private key.
//
// All functions are pure and side-effect free; failures (bad hex, invalid key,
// invalid mnemonic) return an error rather than panicking, so callers can
// degrade gracefully when identities.json is absent or malformed.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip06"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/tyler-smith/go-bip39"
)

// PrivKeyByteLen is the size, in bytes, of a Nostr / secp256k1 private key.
const PrivKeyByteLen = 32

// StandardInterfaces are the GL-MT6000 / OpenWrt network devices whose MAC
// addresses are derived from the merchant identity: the LAN bridge and the two
// WLAN radios.
var StandardInterfaces = []string{"br-lan", "wlan0", "wlan1"}

// CGNATPrefix is the first octet of the RFC 6598 (100.64.0.0/10) range into
// which DeriveIPv4 maps. CGNAT is reserved for exactly this kind of
// provider-assigned private addressing and will not collide with typical home
// LANs (192.168.x.x / 10.0.0.x) or the router's own WAN address.
const CGNATPrefix = 100

// cgnatSecondBase is the inclusive lower bound of the second octet inside
// 100.64.0.0/10 (the /10 spans second octets 64..127).
const cgnatSecondBase = 64

// DerivedIdentity holds the public, non-sensitive attributes derived from a
// merchant private key. It is safe to return from GET /identity.
type DerivedIdentity struct {
	Npub string            `json:"npub"` // bech32 npub1… public key
	IPv4 string            `json:"ipv4"` // dotted-quad, e.g. 100.71.205.1
	MACs map[string]string `json:"macs"` // interface → aa:bb:cc:dd:ee:ff
}

// FullIdentity extends DerivedIdentity with the sensitive recovery material.
// It is returned only from POST /identity/reveal-seed, which is why that route
// uses POST (intent signalling) rather than GET.
type FullIdentity struct {
	DerivedIdentity
	Mnemonic     string `json:"mnemonic"`
	PrivateKey   string `json:"privatekey"`
	RootPassword string `json:"root_password"`
	WifiPassword string `json:"wifi_password"`
}

// NpubFromPrivateKey returns the bech32 npub1… encoding of the public key that
// corresponds to the given hex private key. It validates that the key is a
// usable secp256k1 scalar.
func NpubFromPrivateKey(hexPrivKey string) (string, error) {
	if err := validatePrivKeyHex(hexPrivKey); err != nil {
		return "", err
	}
	pubHex, err := nostr.GetPublicKey(hexPrivKey)
	if err != nil {
		return "", fmt.Errorf("identity: derive public key: %w", err)
	}
	npub, err := nip19.EncodePublicKey(pubHex)
	if err != nil {
		return "", fmt.Errorf("identity: encode npub: %w", err)
	}
	return npub, nil
}

// DeriveIPv4 maps the public key into the RFC 6598 CGNAT range (100.64.0.0/10)
// and returns a dotted-quad address with a .1 host octet (gateway convention).
//
//	octet2 = 64 + (hash[0] mod 64)   → 64..127  (stays inside /10)
//	octet3 = hash[1]                 → 0..255
//	octet4 = 1                       (host)
//
// Two different keys produce two different hashes, so collisions across the
// ~4M usable addresses are negligible for a fleet of routers.
func DeriveIPv4(pubKeyHex string) string {
	h := deriveHash("tollgate-ipv4-v1:", pubKeyHex)
	octet2 := cgnatSecondBase + int(h[0]%64)
	octet3 := int(h[1])
	return fmt.Sprintf("%d.%d.%d.%d", CGNATPrefix, octet2, octet3, 1)
}

// DeriveMAC returns a colon-separated, locally-administered MAC address for the
// given interface name, derived from the public key. The first octet has the
// locally-administered bit set (0x02) and the multicast bit cleared (unicast),
// per IEEE 802; the remaining five octets come from the hash.
//
// Pass one of StandardInterfaces ("br-lan", "wlan0", "wlan1"); each name feeds
// the domain separator so every interface gets a distinct address.
func DeriveMAC(pubKeyHex, iface string) string {
	h := deriveHash("tollgate-mac-v1:"+iface+":", pubKeyHex)
	mac := make(net.HardwareAddr, 6)
	copy(mac, h[:6])
	// Locally administered (bit 1) set, multicast (bit 0) cleared.
	mac[0] = (mac[0] & 0xFC) | 0x02
	return mac.String()
}

// GenerateMnemonic generates a 12-word BIP39 mnemonic (128 bits of entropy).
// The mnemonic is the single backup for the entire device identity — the
// Nostr private key is derived from it via NIP-06.
func GenerateMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return "", fmt.Errorf("identity: generate entropy: %w", err)
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("identity: encode mnemonic: %w", err)
	}
	return mnemonic, nil
}

// MnemonicToPrivateKey derives the Nostr private key from a BIP39 mnemonic
// using NIP-06 hierarchical derivation (m/44'/1237'/0'/0/0).
func MnemonicToPrivateKey(mnemonic string) (string, error) {
	if !nip06.ValidateWords(mnemonic) {
		return "", fmt.Errorf("identity: invalid mnemonic")
	}
	seed := nip06.SeedFromWords(mnemonic)
	return nip06.PrivateKeyFromSeed(seed)
}

func DeriveRootPassword(privKeyHex string) string {
	h := deriveHash("tollgate-root-pw-v2:", privKeyHex)
	words := bip39.GetWordList()
	parts := make([]string, 6)
	for i := 0; i < 6; i++ {
		idx := (int(h[i*2])<<8 | int(h[i*2+1])) % len(words)
		parts[i] = words[idx]
	}
	return strings.Join(parts, "-")
}

func DeriveWiFiPassword(privKeyHex, network string) string {
	h := deriveHash("tollgate-wifi-pw-v2:"+network+":", privKeyHex)
	words := bip39.GetWordList()
	parts := make([]string, 6)
	for i := 0; i < 6; i++ {
		idx := (int(h[i*2])<<8 | int(h[i*2+1])) % len(words)
		parts[i] = words[idx]
	}
	return strings.Join(parts, "-")
}

// Derive computes the public, non-sensitive identity (npub, IPv4, MACs for the
// standard interfaces) from a hex private key. Use this for GET /identity.
func Derive(hexPrivKey string) (*DerivedIdentity, error) {
	if err := validatePrivKeyHex(hexPrivKey); err != nil {
		return nil, err
	}
	pubHex, err := nostr.GetPublicKey(hexPrivKey)
	if err != nil {
		return nil, fmt.Errorf("identity: derive public key: %w", err)
	}
	npub, err := nip19.EncodePublicKey(pubHex)
	if err != nil {
		return nil, fmt.Errorf("identity: encode npub: %w", err)
	}
	macs := make(map[string]string, len(StandardInterfaces))
	for _, iface := range StandardInterfaces {
		macs[iface] = DeriveMAC(pubHex, iface)
	}
	return &DerivedIdentity{
		Npub: npub,
		IPv4: DeriveIPv4(pubHex),
		MACs: macs,
	}, nil
}

// DeriveFromMnemonic recovers the full identity from a 12-word BIP39 mnemonic.
// The mnemonic is the primary input — the private key and all derived network
// attributes are computed from it via NIP-06.
func DeriveFromMnemonic(mnemonic string) (*FullIdentity, error) {
	hexPrivKey, err := MnemonicToPrivateKey(mnemonic)
	if err != nil {
		return nil, err
	}
	derived, err := Derive(hexPrivKey)
	if err != nil {
		return nil, err
	}
	return &FullIdentity{
		DerivedIdentity: *derived,
		Mnemonic:        mnemonic,
		PrivateKey:      hexPrivKey,
		RootPassword:    DeriveRootPassword(hexPrivKey),
		WifiPassword:    DeriveWiFiPassword(hexPrivKey, "private"),
	}, nil
}

// GenerateIdentity creates a new identity from scratch: generates a fresh
// 12-word mnemonic, derives the Nostr private key via NIP-06, and computes
// all derived network attributes. Use this on first boot or when provisioning
// a new router.
func GenerateIdentity() (*FullIdentity, error) {
	mnemonic, err := GenerateMnemonic()
	if err != nil {
		return nil, err
	}
	return DeriveFromMnemonic(mnemonic)
}

// RevealSeed returns the full identity including secrets (passwords, private key)
// from a hex private key. Intended for loopback-only endpoints.
// Mnemonic is empty — it was shown at generation time via GenerateIdentity().
func RevealSeed(hexPrivKey string) (*FullIdentity, error) {
	derived, err := Derive(hexPrivKey)
	if err != nil {
		return nil, err
	}
	return &FullIdentity{
		DerivedIdentity: *derived,
		PrivateKey:      hexPrivKey,
		RootPassword:    DeriveRootPassword(hexPrivKey),
		WifiPassword:    DeriveWiFiPassword(hexPrivKey, "private"),
	}, nil
}

// deriveHash returns SHA-256(domainSep || pubKeyHex): the 32-byte deterministic
// block every public attribute is sliced from. Writing domainSep before
// pubKeyHex (both via io.WriteString, which never errors for a bytes.Buffer-
// backed hasher) is the domain separation that keeps attribute streams
// independent.
func deriveHash(domainSep, pubKeyHex string) [32]byte {
	h := sha256.New()
	_, _ = io.WriteString(h, domainSep)
	_, _ = io.WriteString(h, pubKeyHex)
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// decodePrivKeyHex decodes and length-checks a hex private key into raw bytes.
func decodePrivKeyHex(hexPrivKey string) ([]byte, error) {
	b, err := hex.DecodeString(hexPrivKey)
	if err != nil {
		return nil, fmt.Errorf("identity: private key is not valid hex: %w", err)
	}
	if len(b) != PrivKeyByteLen {
		return nil, fmt.Errorf("identity: private key is %d bytes, want %d", len(b), PrivKeyByteLen)
	}
	return b, nil
}

// validatePrivKeyHex checks the encoding/length without keeping the bytes.
func validatePrivKeyHex(hexPrivKey string) error {
	_, err := decodePrivKeyHex(hexPrivKey)
	return err
}
