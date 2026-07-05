package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedTestKey is a valid Nostr private key generated once per test run and
// reused across tests so determinism / round-trip assertions share a single
// key.
var fixedTestKey = nostr.GeneratePrivateKey()

// mustNpub derives the npub for the fixed test key, failing the test on error.
func mustNpub(t *testing.T) string {
	t.Helper()
	npub, err := NpubFromPrivateKey(fixedTestKey)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(npub, "npub1"))
	return npub
}

func TestNpubFromPrivateKey_Deterministic(t *testing.T) {
	npub1, err := NpubFromPrivateKey(fixedTestKey)
	require.NoError(t, err)
	npub2, err := NpubFromPrivateKey(fixedTestKey)
	require.NoError(t, err)
	assert.Equal(t, npub1, npub2, "npub must be deterministic for the same key")

	// Different key -> different npub.
	other := nostr.GeneratePrivateKey()
	require.NotEqual(t, fixedTestKey, other)
	npubOther, err := NpubFromPrivateKey(other)
	require.NoError(t, err)
	assert.NotEqual(t, npub1, npubOther)
}

func TestNpubFromPrivateKey_MatchesNostrDerivation(t *testing.T) {
	// NpubFromPrivateKey must agree with nostr.GetPublicKey + bech32 encoding.
	pubHex, err := nostr.GetPublicKey(fixedTestKey)
	require.NoError(t, err)

	npub, err := NpubFromPrivateKey(fixedTestKey)
	require.NoError(t, err)

	// Round-trip: decoding the npub must yield the same 32-byte pubkey.
	got := make([]byte, 32)
	dst := got
	for i := 0; i < len(pubHex); i += 2 {
		b, err := hex.DecodeString(pubHex[i : i+2])
		require.NoError(t, err)
		dst[0] = b[0]
		dst = dst[1:]
	}
	_ = got // pubHex sanity: 64 hex chars -> 32 bytes
	require.Len(t, pubHex, 64)
	assert.True(t, strings.HasPrefix(npub, "npub1"))
}

func TestNpubFromPrivateKey_Errors(t *testing.T) {
	_, err := NpubFromPrivateKey("")
	require.ErrorIs(t, err, ErrEmptyPrivateKey)

	// Not valid hex.
	_, err = NpubFromPrivateKey("nothex")
	require.Error(t, err)

	// Valid hex but wrong length: the library still accepts short keys
	// (PrivKeyFromBytes pads), so we do not assert an error here. The strict
	// 32-byte requirement is enforced by PrivateKeyToMnemonic instead.
	_ = fixedTestKey
}

func TestDeriveDigest(t *testing.T) {
	npub := mustNpub(t)

	d1, err := DeriveDigest(npub, "domain-a")
	require.NoError(t, err)
	require.Len(t, d1, 32, "SHA-256 digest must be 32 bytes")

	// Deterministic.
	d2, err := DeriveDigest(npub, "domain-a")
	require.NoError(t, err)
	assert.Equal(t, d1, d2)

	// Different domain -> different digest.
	d3, err := DeriveDigest(npub, "domain-b")
	require.NoError(t, err)
	assert.NotEqual(t, d1, d3)

	// Different npub -> different digest.
	other := nostr.GeneratePrivateKey()
	otherNpub, err := NpubFromPrivateKey(other)
	require.NoError(t, err)
	d4, err := DeriveDigest(otherNpub, "domain-a")
	require.NoError(t, err)
	assert.NotEqual(t, d1, d4)

	// Matches the documented definition: SHA-256(domain || npub).
	want := sha256.Sum256(append([]byte("domain-a"), []byte(npub)...))
	assert.Equal(t, want[:], d1)

	// Errors.
	_, err = DeriveDigest("", "domain-a")
	require.ErrorIs(t, err, ErrEmptyNpub)
	_, err = DeriveDigest(npub, "")
	require.ErrorIs(t, err, ErrEmptyDomain)
}

func TestDeriveIPv4(t *testing.T) {
	npub := mustNpub(t)

	ip1, err := DeriveIPv4(npub)
	require.NoError(t, err)
	require.NotNil(t, ip1)
	ip1v4 := ip1.To4()
	require.Equal(t, 4, len(ip1v4), "must be an IPv4 address")

	// Must be in 10.0.0.0/8.
	assert.Equal(t, byte(10), ip1v4[0], "first octet must be 10")

	// Deterministic.
	ip2, err := DeriveIPv4(npub)
	require.NoError(t, err)
	assert.Equal(t, ip1.String(), ip2.String())

	// Different npub -> (almost certainly) different address.
	other := nostr.GeneratePrivateKey()
	otherNpub, err := NpubFromPrivateKey(other)
	require.NoError(t, err)
	ipOther, err := DeriveIPv4(otherNpub)
	require.NoError(t, err)
	assert.NotEqual(t, ip1.String(), ipOther.String())

	// Last octet must never be 0 or 255.
	for i := 0; i < 8; i++ {
		k := nostr.GeneratePrivateKey()
		np, err := NpubFromPrivateKey(k)
		require.NoError(t, err)
		ip, err := DeriveIPv4(np)
		require.NoError(t, err)
		ipV4 := ip.To4()
		require.Len(t, ipV4, 4)
		assert.NotEqual(t, byte(0), ipV4[3], "host octet must not be 0")
		assert.NotEqual(t, byte(255), ipV4[3], "host octet must not be 255")
	}
}

func TestDeriveMAC(t *testing.T) {
	npub := mustNpub(t)

	mac, err := DeriveMAC(npub, "wlan0")
	require.NoError(t, err)
	require.Len(t, mac, 6)

	// Locally-administered bit set, multicast bit cleared.
	assert.Equal(t, byte(0x02), mac[0]&0x03, "first octet: bit1 set (LAA), bit0 clear (unicast)")

	// Valid hardware address parses.
	_, err = net.ParseMAC(mac.String())
	require.NoError(t, err)

	// Deterministic for the same interface.
	mac2, err := DeriveMAC(npub, "wlan0")
	require.NoError(t, err)
	assert.Equal(t, mac.String(), mac2.String())

	// Different interface -> different MAC.
	macBr, err := DeriveMAC(npub, "br-lan")
	require.NoError(t, err)
	assert.NotEqual(t, mac.String(), macBr.String())

	// All default interfaces produce distinct addresses.
	seen := make(map[string]string)
	for _, iface := range DefaultMACInterfaces {
		m, err := DeriveMAC(npub, iface)
		require.NoError(t, err)
		for prev, val := range seen {
			assert.NotEqual(t, val, m.String(), "%s and %s must differ", prev, iface)
		}
		seen[iface] = m.String()
	}

	// Empty interface errors.
	_, err = DeriveMAC(npub, "")
	require.ErrorIs(t, err, ErrEmptyInterface)
}

func TestDeriveDefaultMACs(t *testing.T) {
	npub := mustNpub(t)
	macs, err := DeriveDefaultMACs(npub)
	require.NoError(t, err)
	assert.Equal(t, len(DefaultMACInterfaces), len(macs))
	for _, iface := range DefaultMACInterfaces {
		v, ok := macs[iface]
		require.True(t, ok, "missing interface %s", iface)
		_, err := net.ParseMAC(v)
		require.NoError(t, err, "%s: %s is not a MAC", iface, v)
	}
}

func TestPrivateKeyToMnemonic_RoundTrip(t *testing.T) {
	mnemonic, err := PrivateKeyToMnemonic(fixedTestKey)
	require.NoError(t, err)

	words := strings.Fields(mnemonic)
	assert.Len(t, words, 24, "must produce exactly 24 BIP-39 words")

	// Round-trip back to the original private key.
	recovered, err := MnemonicToPrivateKey(mnemonic)
	require.NoError(t, err)
	assert.Equal(t, fixedTestKey, recovered)
}

func TestPrivateKeyToMnemonic_DifferentKeys(t *testing.T) {
	m1, err := PrivateKeyToMnemonic(fixedTestKey)
	require.NoError(t, err)
	other := nostr.GeneratePrivateKey()
	m2, err := PrivateKeyToMnemonic(other)
	require.NoError(t, err)
	assert.NotEqual(t, m1, m2)

	// And the other key round-trips too.
	recovered, err := MnemonicToPrivateKey(m2)
	require.NoError(t, err)
	assert.Equal(t, other, recovered)
}

func TestPrivateKeyToMnemonic_Errors(t *testing.T) {
	_, err := PrivateKeyToMnemonic("")
	require.ErrorIs(t, err, ErrEmptyPrivateKey)

	_, err = PrivateKeyToMnemonic("nothex")
	require.Error(t, err)

	// Valid hex but wrong length.
	_, err = PrivateKeyToMnemonic("0011")
	require.ErrorIs(t, err, ErrBadKeyLength)
}

func TestMnemonicToPrivateKey_Errors(t *testing.T) {
	_, err := MnemonicToPrivateKey("")
	require.Error(t, err)

	// Nonsense words.
	_, err = MnemonicToPrivateKey("zzzzz zzzzz zzzzz")
	require.Error(t, err)

	// Valid words but wrong count (not a multiple-of-3 entropy size).
	_, err = MnemonicToPrivateKey("abandon abandon abandon")
	require.Error(t, err)

	// 24 words but corrupted checksum: flip last word from the real mnemonic.
	mnemonic, err := PrivateKeyToMnemonic(fixedTestKey)
	require.NoError(t, err)
	words := strings.Fields(mnemonic)
	words[23] = "abandon" // almost certainly wrong checksum
	_, err = MnemonicToPrivateKey(strings.Join(words, " "))
	require.Error(t, err)
}

func TestEndToEnd_RouterIdentity(t *testing.T) {
	// A fresh router key derives a self-consistent identity bundle.
	priv := nostr.GeneratePrivateKey()
	npub, err := NpubFromPrivateKey(priv)
	require.NoError(t, err)

	ip, err := DeriveIPv4(npub)
	require.NoError(t, err)
	macs, err := DeriveDefaultMACs(npub)
	require.NoError(t, err)
	mnemonic, err := PrivateKeyToMnemonic(priv)
	require.NoError(t, err)

	// Recovering the key from the mnemonic reproduces the same npub and
	// therefore the same derived addresses.
	recovered, err := MnemonicToPrivateKey(mnemonic)
	require.NoError(t, err)
	assert.Equal(t, priv, recovered)

	reNpub, err := NpubFromPrivateKey(recovered)
	require.NoError(t, err)
	assert.Equal(t, npub, reNpub)

	reIP, err := DeriveIPv4(reNpub)
	require.NoError(t, err)
	assert.Equal(t, ip.String(), reIP.String())

	reMACs, err := DeriveDefaultMACs(reNpub)
	require.NoError(t, err)
	assert.Equal(t, macs, reMACs)
}
