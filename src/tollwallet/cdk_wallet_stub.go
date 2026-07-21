//go:build cdk_wallet

package tollwallet

// CdkWallet stub — placeholder implementation of WalletPort for the
// cdk_wallet build tag. The real CdkWallet adapter (wrapping cdk-go FFI
// bindings) is deferred to a follow-up session. This stub allows the
// codebase to compile with -tags cdk_wallet so that build verification
// and CI matrix testing can proceed.
//
// When cdk_wallet is set:
//   - gonuts_wallet.go (!cdk_wallet) is excluded
//   - This file provides NewWalletPort, which returns an error
//   - port.go (no tag) and tollwallet.go (no tag) always compile
//   - The gonuts dependency is still pulled in via tollwallet.go; a
//     future wave will gate tollwallet.go behind !cdk_wallet to fully
//     decouple the cdk_wallet build from gonuts.

import "fmt"

// NewWalletPort creates a WalletPort backed by cdk-go.
// NOT YET IMPLEMENTED — returns an error.
// The real implementation will wrap cdk_ffi.Wallet via CGO FFI,
// manage a wallet-per-mint map, and map FfiError codes to sentinels.
func NewWalletPort(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool) (WalletPort, error) {
	return nil, fmt.Errorf("cdk_wallet build tag: CdkWallet adapter not yet implemented — see tracking issue #271 and branch research/wallet-port-cdk-go")
}

// DecodeToken is the package-level token decoder for the cdk_wallet build.
// NOT YET IMPLEMENTED — returns an error.
// The real implementation will call cdk_ffi.TokenDecode(string) (*Token, error)
// and wrap the result in a cdkToken struct implementing the Token interface.
func DecodeToken(tokenStr string) (Token, error) {
	return nil, fmt.Errorf("cdk_wallet build tag: CdkWallet.DecodeToken not yet implemented — see tracking issue #271")
}
