package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveRootPassword_UsesPrivateKey(t *testing.T) {
	privKey := freshKey(t)
	pubKey := mustPubHex(t, privKey)

	pwFromPriv := DeriveRootPassword(privKey)
	pwFromPub := DeriveRootPassword(pubKey)

	assert.NotEqual(t, pwFromPub, pwFromPriv,
		"DeriveRootPassword must produce different output for private vs public key. "+
			"If they match, the function is still using public key derivation (the security bug from #209).")
}

func TestDeriveWiFiPassword_UsesPrivateKey(t *testing.T) {
	privKey := freshKey(t)
	pubKey := mustPubHex(t, privKey)

	pwFromPriv := DeriveWiFiPassword(privKey, "private")
	pwFromPub := DeriveWiFiPassword(pubKey, "private")

	assert.NotEqual(t, pwFromPub, pwFromPriv,
		"DeriveWiFiPassword must produce different output for private vs public key.")
}

func TestRevealSeed_PasswordsDeriveFromPrivateKey(t *testing.T) {
	privKey := freshKey(t)
	pubKey := mustPubHex(t, privKey)

	full, err := RevealSeed(privKey)
	require.NoError(t, err)

	directPrivRoot := DeriveRootPassword(privKey)
	directPrivWifi := DeriveWiFiPassword(privKey, "private")

	assert.Equal(t, directPrivRoot, full.RootPassword,
		"RevealSeed.RootPassword must match DeriveRootPassword(privKey)")
	assert.Equal(t, directPrivWifi, full.WifiPassword,
		"RevealSeed.WifiPassword must match DeriveWiFiPassword(privKey)")

	directPubRoot := DeriveRootPassword(pubKey)
	assert.NotEqual(t, directPubRoot, full.RootPassword,
		"RevealSeed must NOT use the public key for password derivation")
}
