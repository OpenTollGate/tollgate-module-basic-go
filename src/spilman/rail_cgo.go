//go:build spilman

package spilman

// This file is the `spilman`-tagged half of the build-tag pair, mirroring the
// proven src/tollwallet/cdk_wallet.go / gonuts_wallet.go precedent: the tagged
// file provides the real implementation, the untagged file (rail_stub.go)
// provides a refusing stub, and the default CGO_ENABLED=0 build never sees this
// file at all.
//
// Build (aarch64 musl, the shipped target):
//
//	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
//	  CC=$HOME/toolchains/aarch64-linux-musl-cross/bin/aarch64-linux-musl-gcc \
//	  CGO_LDFLAGS="-L<checkout>/crates/cdk-spilman-go/packaged/lib/linux-arm64 -lcdk_spilman_go" \
//	  go build -tags spilman ./...
//
// The library path is a build-time input on purpose: packaged/lib/linux-arm64 is
// gitignored upstream, so the `.so` distribution decision is still open and must
// not be smuggled in as a committed module replace.

/*
#include <stdlib.h>
#include <stdint.h>

typedef struct {
    char* data;
    char* error;
} CResult;

// Callback-free, seller-side primitives exported by libcdk_spilman_go.so.
CResult spilman_secret_key_to_pubkey(const char* secret_hex);
CResult spilman_compute_channel_secret(const char* my_secret_hex, const char* their_pubkey_hex);
CResult spilman_compute_funding_token_amount(uint64_t capacity, const char* keyset_info_json, uint64_t maximum_amount);
CResult spilman_create_signed_balance_update(const char* params, const char* keyset, const char* secret, const char* proofs, uint64_t balance);
CResult spilman_channel_parameters_get_channel_id(const char* params, const char* channel_secret, const char* keyset);
void spilman_free_cresult(CResult res);
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"unsafe"
)

// cgoBridge is the ProtocolBridge backed by the linked Rust core.
type cgoBridge struct{}

// newProtocolBridge returns the cgo-backed bridge (tagged build only).
func newProtocolBridge() ProtocolBridge { return cgoBridge{} }

func (cgoBridge) Name() string { return "cdk-spilman-go/cgo" }

// UnilateralCloseData and ExecuteCooperativeClose both need a live
// spilman_bridge_new instance, i.e. the 22-callback SpilmanHostCallbacks host
// adapter. That adapter is the remaining step (see REPORT.md); until it exists
// this refuses rather than returning fabricated close data.
func (cgoBridge) UnilateralCloseData(_ context.Context, channelID string) ([]byte, error) {
	return nil, fmt.Errorf("%w: unilateral close data for %q needs the callback host adapter", ErrSellerBridgeUnwired, channelID)
}

func (cgoBridge) ExecuteCooperativeClose(_ context.Context, req ChannelSettleRequest) (ChannelSettleResult, error) {
	return ChannelSettleResult{}, fmt.Errorf("%w: cooperative close of %q needs the callback host adapter", ErrSellerBridgeUnwired, req.ChannelID)
}

// callCResult runs a CResult-returning FFI call and returns its data string.
func callCResult(res C.CResult) (string, error) {
	defer C.spilman_free_cresult(res)
	if res.error != nil {
		return "", fmt.Errorf("%w: %s", ErrLedger, C.GoString(res.error))
	}
	if res.data == nil {
		return "", nil
	}
	return C.GoString(res.data), nil
}

func cString(s string) (*C.char, func()) {
	c := C.CString(s)
	return c, func() { C.free(unsafe.Pointer(c)) }
}

// SecretKeyToPubkey derives the public key for a hex secret key.
func SecretKeyToPubkey(secretHex string) (string, error) {
	cSecret, freeSecret := cString(secretHex)
	defer freeSecret()
	return callCResult(C.spilman_secret_key_to_pubkey(cSecret))
}

// ComputeChannelSecret derives the shared channel secret (ECDH).
func ComputeChannelSecret(mySecretHex, theirPubkeyHex string) (string, error) {
	cMine, freeMine := cString(mySecretHex)
	defer freeMine()
	cTheirs, freeTheirs := cString(theirPubkeyHex)
	defer freeTheirs()
	return callCResult(C.spilman_compute_channel_secret(cMine, cTheirs))
}

// ComputeFundingTokenAmount returns the funding token amount for a capacity.
func ComputeFundingTokenAmount(capacity uint64, keysetInfoJSON string, maximumAmount uint64) (uint64, error) {
	cKeyset, freeKeyset := cString(keysetInfoJSON)
	defer freeKeyset()
	data, err := callCResult(C.spilman_compute_funding_token_amount(
		C.uint64_t(capacity), cKeyset, C.uint64_t(maximumAmount)))
	if err != nil {
		return 0, err
	}
	var amount uint64
	if err := json.Unmarshal([]byte(data), &amount); err != nil {
		// The binding may wrap the value in an object; accept both shapes.
		var wrapped struct {
			Amount uint64 `json:"amount"`
		}
		if err2 := json.Unmarshal([]byte(data), &wrapped); err2 != nil {
			return 0, fmt.Errorf("%w: funding amount %q: %v", ErrLedger, data, err)
		}
		return wrapped.Amount, nil
	}
	return amount, nil
}

// CreateSignedBalanceUpdate signs a balance update for a channel.
//
// Seller-side use: the payee can produce (and therefore hold) the signed
// balance it is owed. Note that this is *production*, not *verification*:
// verifying an inbound payer signature needs the bridge host callbacks and is
// still outstanding.
func CreateSignedBalanceUpdate(paramsJSON, keysetJSON, channelSecretHex, proofsJSON string, balance uint64) (string, error) {
	cParams, freeParams := cString(paramsJSON)
	defer freeParams()
	cKeyset, freeKeyset := cString(keysetJSON)
	defer freeKeyset()
	cSecret, freeSecret := cString(channelSecretHex)
	defer freeSecret()
	cProofs, freeProofs := cString(proofsJSON)
	defer freeProofs()
	return callCResult(C.spilman_create_signed_balance_update(
		cParams, cKeyset, cSecret, cProofs, C.uint64_t(balance)))
}

// ChannelIDFromParams derives the channel id for a set of channel parameters.
func ChannelIDFromParams(paramsJSON, channelSecretHex, keysetJSON string) (string, error) {
	cParams, freeParams := cString(paramsJSON)
	defer freeParams()
	cSecret, freeSecret := cString(channelSecretHex)
	defer freeSecret()
	cKeyset, freeKeyset := cString(keysetJSON)
	defer freeKeyset()
	return callCResult(C.spilman_channel_parameters_get_channel_id(cParams, cSecret, cKeyset))
}
