//go:build !spilman

package spilman

import (
	"context"
	"fmt"
)

// This file is the untagged half of the build-tag pair. It exists so the default
// CGO_ENABLED=0 build (the shipped OpenWrt path) links and *behaves identically*
// for everything that does not need the Rust core — and refuses, by name, every
// operation that does. Mirror of the cdk_wallet / gonuts_wallet precedent.

// notCompiledBridge is the default-build ProtocolBridge.
type notCompiledBridge struct{}

// newProtocolBridge is the build-tag seam: rail_stub.go (!spilman) returns the
// refusing stub, rail_cgo.go (spilman) returns the cgo-backed bridge.
func newProtocolBridge() ProtocolBridge { return notCompiledBridge{} }

func (notCompiledBridge) Name() string { return "not-compiled" }

func (notCompiledBridge) UnilateralCloseData(_ context.Context, channelID string) ([]byte, error) {
	return nil, fmt.Errorf("%w: unilateral close data for channel %q", ErrNotCompiled, channelID)
}

func (notCompiledBridge) ExecuteCooperativeClose(_ context.Context, req ChannelSettleRequest) (ChannelSettleResult, error) {
	return ChannelSettleResult{}, fmt.Errorf("%w: cooperative close of channel %q", ErrNotCompiled, req.ChannelID)
}

// newProtocolValidator is the default-build validator: it REFUSES every payment,
// because the verification primitive lives in the Rust core and this binary does
// not link it. The error names the build tag, so an operator sees "build with
// -tags spilman" rather than a confusing signature failure.
func newProtocolValidator() PaymentValidator { return refusingErrValidator(ErrNotCompiled) }

// The seller-side primitives below are the same names the `spilman`-tagged build
// implements against libcdk_spilman_go.so. In the default build they refuse.

// SecretKeyToPubkey is unavailable without the protocol binding.
func SecretKeyToPubkey(string) (string, error) {
	return "", fmt.Errorf("%w: SecretKeyToPubkey", ErrNotCompiled)
}

// ComputeChannelSecret is unavailable without the protocol binding.
func ComputeChannelSecret(string, string) (string, error) {
	return "", fmt.Errorf("%w: ComputeChannelSecret", ErrNotCompiled)
}

// ComputeFundingTokenAmount is unavailable without the protocol binding.
func ComputeFundingTokenAmount(uint64, string, uint64) (uint64, error) {
	return 0, fmt.Errorf("%w: ComputeFundingTokenAmount", ErrNotCompiled)
}

// CreateSignedBalanceUpdate is unavailable without the protocol binding.
func CreateSignedBalanceUpdate(string, string, string, string, uint64) (string, error) {
	return "", fmt.Errorf("%w: CreateSignedBalanceUpdate", ErrNotCompiled)
}

// ChannelIDFromParams is unavailable without the protocol binding.
func ChannelIDFromParams(string, string, string) (string, error) {
	return "", fmt.Errorf("%w: ChannelIDFromParams", ErrNotCompiled)
}
