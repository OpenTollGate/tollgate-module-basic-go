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

// newProtocolValidator is the default-build validator. It verifies balance-update
// signatures with the pure-Go implementation in verify_purego.go, so the shipped
// CGO_ENABLED=0 binary can AFFIRM a genuine payment instead of refusing
// everything.
//
// The contract is unchanged and must stay unchanged: a validator is required,
// never optional, and every error is a refusal. `pureGoValidator` returns a
// non-nil error for every failure mode — unparseable record, wrong channel id,
// balance out of range, malformed signature, BIP-340 failure — and there is no
// path through it that returns nil without a verified signature.
func newProtocolValidator() PaymentValidator { return pureGoValidator{} }

// pureGoValidator is the CGO_ENABLED=0 PaymentValidator. It exists so the
// OpenWrt/shipped build never links the Rust core to authenticate a payment.
//
// It is a VERIFIER only: it holds no secret and can produce no signature. That
// asymmetry is the whole security argument for shipping this path — a payee that
// could sign balance updates could mint itself credit.
type pureGoValidator struct{}

func (pureGoValidator) Name() string { return "pure-go" }

func (pureGoValidator) ValidatePayment(_ context.Context, funding FundingRecord, balance uint64, signature string) error {
	return VerifyBalanceUpdate(funding, balance, signature)
}

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
