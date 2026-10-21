//go:build spilman

package spilman

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The tagged build must actually call into the Rust core, not merely link it.
// These assertions run against the real libcdk_spilman_go.so.

// secp256k1 secret key 1 is the generator point; the compressed pubkey is a
// fixed, well-known vector, so this pins the whole cgo call path.
func TestTaggedBuildDerivesTheGeneratorPointPubkey(t *testing.T) {
	pubkey, err := SecretKeyToPubkey("0000000000000000000000000000000000000000000000000000000000000001")
	require.NoError(t, err)
	require.Equal(t, "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", pubkey)
}

func TestTaggedBuildComputesAStableChannelSecret(t *testing.T) {
	const aliceSecret = "0000000000000000000000000000000000000000000000000000000000000001"
	const bobSecret = "0000000000000000000000000000000000000000000000000000000000000002"

	bobPubkey, err := SecretKeyToPubkey(bobSecret)
	require.NoError(t, err)

	first, err := ComputeChannelSecret(aliceSecret, bobPubkey)
	require.NoError(t, err)
	require.NotEmpty(t, first)

	second, err := ComputeChannelSecret(aliceSecret, bobPubkey)
	require.NoError(t, err)
	require.Equal(t, first, second, "the shared secret must be deterministic")

	// ECDH is symmetric: Alice+Bobby must equal Bob+Alice.
	alicePubkey, err := SecretKeyToPubkey(aliceSecret)
	require.NoError(t, err)
	other, err := ComputeChannelSecret(bobSecret, alicePubkey)
	require.NoError(t, err)
	require.Equal(t, first, other, "the channel secret must be symmetric")
}

func TestTaggedBuildNamesItself(t *testing.T) {
	require.Equal(t, "cdk-spilman-go/cgo", newProtocolBridge().Name())

	// The close path still needs the callback host adapter; it must refuse by
	// name rather than fabricate close data.
	_, err := newProtocolBridge().UnilateralCloseData(t.Context(), "chan-1")
	require.ErrorIs(t, err, ErrSellerBridgeUnwired)
}
