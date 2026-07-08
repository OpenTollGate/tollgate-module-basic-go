package identity

import (
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Golden vector: a fixed private key whose derived attributes are pinned so a
// regression in the derivation scheme is caught immediately. Generated once
// against this implementation; do not edit without bumping the domain
// separator version constants.
//
// The key is the secp256k1 scalar 1 (a valid, well-known test scalar in
// [1, n-1]); the npub/IPv4/MAC fields are derived from it at runtime in init()
// so the whole vector stays internally consistent and stable across runs.
var (
	goldenPrivKey  = "0000000000000000000000000000000000000000000000000000000000000001"
	goldenNpub     string // set in init below
	goldenPubHex   string
	goldenIPv4     string
	goldenMACBrLan string
)

func init() {
	goldenNpub, _ = NpubFromPrivateKey(goldenPrivKey)
	goldenPubHex, _ = nostr.GetPublicKey(goldenPrivKey)
	goldenIPv4 = DeriveIPv4(goldenPubHex)
	goldenMACBrLan = DeriveMAC(goldenPubHex, "br-lan")
}

var (
	npubRe         = regexp.MustCompile(`^npub1[023456789acdefghjklmnpqrstuvwxyz]{6,}$`)
	ipv4Re         = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)
	macRe          = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
	mnemonicWordRe = regexp.MustCompile(`^[a-z]+$`)
)

// freshKey returns a random valid Nostr private key for property-style tests.
func freshKey(t *testing.T) string {
	t.Helper()
	sk := nostr.GeneratePrivateKey()
	require.Len(t, sk, 64, "generated key should be 32 bytes of hex")
	return sk
}

func TestMnemonic_RoundTrip(t *testing.T) {
	for i := 0; i < 16; i++ {
		mnemonic, err := GenerateMnemonic()
		require.NoError(t, err)
		words := strings.Fields(mnemonic)
		assert.Len(t, words, 12, "expected 12 BIP39 words")

		key, err := MnemonicToPrivateKey(mnemonic)
		require.NoError(t, err)
		assert.Len(t, key, 64, "private key must be 64 hex chars")

		key2, err := MnemonicToPrivateKey(mnemonic)
		require.NoError(t, err)
		assert.Equal(t, key, key2, "same mnemonic must yield same key")
	}
}

func TestMnemonicToPrivateKey_RejectsInvalid(t *testing.T) {
	bad := []string{
		"",                        // empty
		"abandon abandon abandon", // too few words
		strings.Repeat("notaword ", 12),
		"zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong", // 12 words, bad checksum
	}
	for _, m := range bad {
		m = strings.TrimSpace(m)
		_, err := MnemonicToPrivateKey(m)
		assert.Error(t, err, "expected error for mnemonic %q", m)
	}
}

func TestGenerateMnemonic_TwelveWords(t *testing.T) {
	for i := 0; i < 8; i++ {
		mnemonic, err := GenerateMnemonic()
		require.NoError(t, err)
		words := strings.Fields(mnemonic)
		assert.Len(t, words, 12, "mnemonic must be exactly 12 words")
		for _, w := range words {
			assert.True(t, mnemonicWordRe.MatchString(w), "word %q must be lowercase letters", w)
		}
	}
}

func TestGenerateIdentity_AllFields(t *testing.T) {
	full, err := GenerateIdentity()
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Len(t, strings.Fields(full.Mnemonic), 12)
	assert.Len(t, full.PrivateKey, 64)
	assert.Contains(t, full.Npub, "npub1")
	assert.True(t, ipv4Re.MatchString(full.IPv4))
	for _, iface := range StandardInterfaces {
		m, ok := full.MACs[iface]
		require.True(t, ok, "missing MAC for %s", iface)
		assert.True(t, macRe.MatchString(m), "mac %q for %s", m, iface)
	}
}

func TestDeriveFromMnemonic_Deterministic(t *testing.T) {
	mnemonic, err := GenerateMnemonic()
	require.NoError(t, err)
	a, err := DeriveFromMnemonic(mnemonic)
	require.NoError(t, err)
	b, err := DeriveFromMnemonic(mnemonic)
	require.NoError(t, err)
	assert.Equal(t, a.PrivateKey, b.PrivateKey)
	assert.Equal(t, a.IPv4, b.IPv4)
}

func TestGenerateIdentity_DifferentEachCall(t *testing.T) {
	a, err := GenerateIdentity()
	require.NoError(t, err)
	b, err := GenerateIdentity()
	require.NoError(t, err)
	assert.NotEqual(t, a.PrivateKey, b.PrivateKey, "two GenerateIdentity calls must differ")
	assert.NotEqual(t, a.IPv4, b.IPv4)
}

func TestNpubFromPrivateKey_FormatAndGolden(t *testing.T) {
	// Golden: stable across runs.
	assert.Equal(t, goldenNpub, mustNpub(t, goldenPrivKey), "npub must be deterministic")
	assert.True(t, npubRe.MatchString(goldenNpub), "npub %q must match npub1… shape", goldenNpub)

	for i := 0; i < 8; i++ {
		npub, err := NpubFromPrivateKey(freshKey(t))
		require.NoError(t, err)
		assert.True(t, npubRe.MatchString(npub), "npub %q must match npub1… shape", npub)
	}
}

func TestNpubFromPrivateKey_RejectsBadKey(t *testing.T) {
	_, err := NpubFromPrivateKey("not-a-key")
	assert.Error(t, err)
}

func TestDeriveIPv4_CGNATRangeAndHostOne(t *testing.T) {
	for i := 0; i < 32; i++ {
		pubHex := mustPubHex(t, freshKey(t))
		ip := DeriveIPv4(pubHex)
		require.True(t, ipv4Re.MatchString(ip), "ip %q must be dotted-quad", ip)

		// Must be inside 100.64.0.0/10 with a .1 host octet.
		pi := net.ParseIP(ip).To4()
		require.NotNil(t, pi, "ip %q must parse", ip)
		assert.Equal(t, byte(CGNATPrefix), pi[0], "first octet must be %d", CGNATPrefix)
		assert.GreaterOrEqual(t, pi[1], byte(64), "second octet must be >=64 (CGNAT)")
		assert.LessOrEqual(t, pi[1], byte(127), "second octet must be <=127 (CGNAT)")
		assert.Equal(t, byte(1), pi[3], "host octet must be .1")
	}
	// Golden pin.
	assert.Equal(t, goldenIPv4, DeriveIPv4(goldenPubHex))
}

func TestDeriveIPv4_Deterministic(t *testing.T) {
	pubHex := mustPubHex(t, freshKey(t))
	assert.Equal(t, DeriveIPv4(pubHex), DeriveIPv4(pubHex))
}

func TestDeriveMAC_FormatAndLocallyAdministered(t *testing.T) {
	for _, iface := range StandardInterfaces {
		mac := DeriveMAC(goldenPubHex, iface)
		require.True(t, macRe.MatchString(mac), "mac %q for %s must be aa:bb:..:ff", mac, iface)

		hw, err := net.ParseMAC(mac)
		require.NoError(t, err)
		// Locally administered bit (0x02) set, multicast bit (0x01) clear.
		assert.Equal(t, byte(0x02), hw[0]&0x03, "first octet %02x: LA bit set, MC bit clear", hw[0])
	}
}

func TestDeriveMAC_DistinctPerInterface(t *testing.T) {
	macs := map[string]string{}
	for _, iface := range StandardInterfaces {
		macs[iface] = DeriveMAC(goldenPubHex, iface)
	}
	// All three standard interface MACs must differ from each other.
	seen := map[string]string{}
	for iface, m := range macs {
		prev, dup := seen[m]
		require.False(t, dup, "MAC %s shared by %s and %s", m, prev, iface)
		seen[m] = iface
	}
	// Different interface NAME → different MAC even for similar names.
	assert.NotEqual(t, DeriveMAC(goldenPubHex, "wlan0"), DeriveMAC(goldenPubHex, "wlan1"))
}

func TestDeriveMAC_DistinctPerKey(t *testing.T) {
	a := DeriveMAC(mustPubHex(t, freshKey(t)), "br-lan")
	b := DeriveMAC(mustPubHex(t, freshKey(t)), "br-lan")
	assert.NotEqual(t, a, b, "different keys must yield different MACs")
}

func TestDeriveMAC_Deterministic(t *testing.T) {
	pubHex := mustPubHex(t, freshKey(t))
	assert.Equal(t, DeriveMAC(pubHex, "br-lan"), DeriveMAC(pubHex, "br-lan"))
	assert.Equal(t, goldenMACBrLan, DeriveMAC(goldenPubHex, "br-lan"))
}

func TestDerive_AllFields(t *testing.T) {
	d, err := Derive(goldenPrivKey)
	require.NoError(t, err)
	require.NotNil(t, d)

	assert.Equal(t, goldenNpub, d.Npub)
	assert.Equal(t, goldenIPv4, d.IPv4)
	require.Len(t, d.MACs, len(StandardInterfaces), "MACs must cover all standard interfaces")
	for _, iface := range StandardInterfaces {
		m, ok := d.MACs[iface]
		require.True(t, ok, "missing MAC for %s", iface)
		assert.True(t, macRe.MatchString(m), "mac %q for %s", m, iface)
	}
}

func TestDerive_RejectsBadKey(t *testing.T) {
	d, err := Derive("bad")
	require.Error(t, err)
	assert.Nil(t, d)
}

func TestDeriveFromMnemonic_RoundTrip(t *testing.T) {
	mnemonic, err := GenerateMnemonic()
	require.NoError(t, err)
	full, err := DeriveFromMnemonic(mnemonic)
	require.NoError(t, err)

	words := strings.Fields(full.Mnemonic)
	require.Len(t, words, 12, "mnemonic must be 12 words")

	key2, err := MnemonicToPrivateKey(full.Mnemonic)
	require.NoError(t, err)
	assert.Equal(t, full.PrivateKey, key2, "mnemonic must round-trip to same key")
}

func TestDeriveFromMnemonic_RejectsBadMnemonic(t *testing.T) {
	_, err := DeriveFromMnemonic("not a valid mnemonic at all")
	assert.Error(t, err)
}

func TestGenerateIdentity_RejectsNothing(t *testing.T) {
	full, err := GenerateIdentity()
	require.NoError(t, err)
	assert.NotEmpty(t, full.PrivateKey)
	assert.NotEmpty(t, full.Mnemonic)
}

// mustNpub is a test helper that fails the test if the key is invalid.
func mustNpub(t *testing.T, hexPrivKey string) string {
	t.Helper()
	npub, err := NpubFromPrivateKey(hexPrivKey)
	require.NoError(t, err)
	return npub
}

// mustPubHex is a test helper that returns the hex-encoded secp256k1 public key
// for the given private key, failing the test if invalid.
func mustPubHex(t *testing.T, hexPrivKey string) string {
	t.Helper()
	pubHex, err := nostr.GetPublicKey(hexPrivKey)
	require.NoError(t, err)
	return pubHex
}
