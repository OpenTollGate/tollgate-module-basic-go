package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPrivKey is a valid secp256k1 scalar (the value 1) used as a stable
// stand-in for the merchant private key in handler tests.
const testPrivKey = "0000000000000000000000000000000000000000000000000000000000000001"

func TestHandleIdentityDerive_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/identity", nil)
	rec := httptest.NewRecorder()

	handleIdentityDerive(testPrivKey).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var d identity.DerivedIdentity
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&d))
	assert.Contains(t, d.Npub, "npub1")
	assert.NotEmpty(t, d.IPv4)
	require.Len(t, d.MACs, len(identity.StandardInterfaces))
	for _, iface := range identity.StandardInterfaces {
		m, ok := d.MACs[iface]
		require.True(t, ok, "missing MAC for %s", iface)
		assert.Equal(t, 17, len(m), "MAC should be aa:bb:cc:dd:ee:ff")
	}
}

func TestHandleIdentityDerive_BadKey_500(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/identity", nil)
	rec := httptest.NewRecorder()
	handleIdentityDerive("not-a-key").ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleIdentityRevealSeed_RejectsGET(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/identity/reveal-seed", nil)
		rec := httptest.NewRecorder()
		handleIdentityRevealSeed(testPrivKey).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "method %s", method)
		assert.NotContains(t, rec.Body.String(), "mnemonic", "seed must not leak on non-POST")
	}
}

func TestHandleIdentityRevealSeed_POST_ReturnsMnemonic(t *testing.T) {
	mnemonic, err := identity.GenerateMnemonic()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/identity/reveal-seed", strings.NewReader(mnemonic))
	rec := httptest.NewRecorder()

	handleIdentityRevealSeed(testPrivKey).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var full identity.FullIdentity
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&full))
	assert.NotEmpty(t, full.PrivateKey)
	assert.Contains(t, full.Npub, "npub1")
	assert.NotEmpty(t, full.IPv4)

	words := strings.Fields(full.Mnemonic)
	require.Len(t, words, 12, "mnemonic must be 12 words")

	key2, err := identity.MnemonicToPrivateKey(full.Mnemonic)
	require.NoError(t, err)
	assert.Equal(t, full.PrivateKey, key2, "mnemonic must round-trip to same key")
}

func TestHandleIdentityRevealSeed_BadMnemonic_400(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/identity/reveal-seed", strings.NewReader("not a valid mnemonic"))
	rec := httptest.NewRecorder()
	handleIdentityRevealSeed(testPrivKey).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
