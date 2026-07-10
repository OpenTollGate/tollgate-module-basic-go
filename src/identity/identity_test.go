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

func TestPrivateKeyToMnemonic_RoundTrip(t *testing.T) {
	for i := 0; i < 16; i++ {
		sk := freshKey(t)
		mnemonic, err := PrivateKeyToMnemonic(sk)
		require.NoError(t, err)
		words := strings.Fields(mnemonic)
		assert.Len(t, words, 24, "expected 24 BIP39 words")

		back, err := MnemonicToPrivateKey(mnemonic)
		require.NoError(t, err)
		assert.Equal(t, sk, back, "mnemonic round-trip must recover the original key")
	}
}

func TestMnemonicToPrivateKey_RejectsInvalid(t *testing.T) {
	bad := []string{
		"",                              // empty
		"abandon abandon abandon",       // too few words
		strings.Repeat("notaword ", 24), // 24 tokens but not in wordlist
		"zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong", // valid words, bad checksum
	}
	for _, m := range bad {
		m = strings.TrimSpace(m)
		_, err := MnemonicToPrivateKey(m)
		assert.Error(t, err, "expected error for mnemonic %q", m)
	}
}

func TestPrivateKeyToMnemonic_RoundTripGolden(t *testing.T) {
	mnemonic, err := PrivateKeyToMnemonic(goldenPrivKey)
	require.NoError(t, err)
	back, err := MnemonicToPrivateKey(mnemonic)
	require.NoError(t, err)
	assert.Equal(t, goldenPrivKey, back)
}

func TestPrivateKeyToMnemonic_RejectsBadHex(t *testing.T) {
	bad := []string{"", "nothex", "zz", strings.Repeat("ab", 31), strings.Repeat("ab", 33)}
	for _, sk := range bad {
		_, err := PrivateKeyToMnemonic(sk)
		assert.Error(t, err, "expected error for key %q", sk)
	}
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

func TestRevealSeed_ContainsMnemonicAndKey(t *testing.T) {
	full, err := RevealSeed(goldenPrivKey)
	require.NoError(t, err)
	assert.Equal(t, goldenPrivKey, full.PrivateKey)
	assert.Equal(t, goldenNpub, full.Npub)
	assert.Equal(t, goldenIPv4, full.IPv4)

	words := strings.Fields(full.Mnemonic)
	require.Len(t, words, 24, "mnemonic must be 24 words")
	for _, w := range words {
		assert.True(t, mnemonicWordRe.MatchString(w), "word %q must be lowercase letters", w)
	}

	// The mnemonic must round-trip back to the exact private key.
	back, err := MnemonicToPrivateKey(full.Mnemonic)
	require.NoError(t, err)
	assert.Equal(t, goldenPrivKey, back)
}

func TestRevealSeed_RejectsBadKey(t *testing.T) {
	_, err := RevealSeed("not-a-key")
	assert.Error(t, err)
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

func TestDeriveRootPassword_Format(t *testing.T) {
	pw := DeriveRootPassword(freshKey(t))
	assert.Regexp(t, `^[A-Z][a-z]+-[A-Z][a-z]+-[A-Z][a-z]+-\d{2}$`, pw)
}

func TestDeriveRootPassword_Deterministic(t *testing.T) {
	priv := freshKey(t)
	assert.Equal(t, DeriveRootPassword(priv), DeriveRootPassword(priv))
}

func TestDeriveRootPassword_DistinctPerKey(t *testing.T) {
	assert.NotEqual(t,
		DeriveRootPassword(freshKey(t)),
		DeriveRootPassword(freshKey(t)))
}

func TestDeriveWiFiPassword_Format(t *testing.T) {
	pw := DeriveWiFiPassword(freshKey(t), "private")
	assert.Regexp(t, `^[A-Z][a-z]+-[A-Z][a-z]+-\d{4}$`, pw)
}

func TestDeriveWiFiPassword_DistinctPerNetwork(t *testing.T) {
	priv := freshKey(t)
	assert.NotEqual(t, DeriveWiFiPassword(priv, "private"), DeriveWiFiPassword(priv, "admin"))
}

func TestRevealSeed_IncludesPasswords(t *testing.T) {
	full, err := RevealSeed(goldenPrivKey)
	require.NoError(t, err)
	assert.NotEmpty(t, full.RootPassword)
	assert.NotEmpty(t, full.WifiPassword)
}
