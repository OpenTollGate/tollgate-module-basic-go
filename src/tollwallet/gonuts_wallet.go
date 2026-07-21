//go:build !cdk_wallet

package tollwallet

// GonutsWallet adapter — default implementation of WalletPort.
// Wraps *TollWallet (gonuts-tollgate) and translates between primitive
// port types (Token, MintQuoteState, MintQuote) and gonuts types
// (cashu.Token, nut04.State, nut04.PostMintQuoteBolt11Response).
//
// This file is excluded when the cdk_wallet build tag is set; in that
// case cdk_wallet_stub.go (or a future cdk_wallet.go) provides the
// alternative implementation.

import (
	"fmt"

	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
)

// GonutsWallet wraps a *TollWallet and implements WalletPort by delegation
// and type translation. It is behavior-identical to using *TollWallet directly.
type GonutsWallet struct {
	inner *TollWallet
}

// NewWalletPort creates a WalletPort backed by gonuts-tollgate.
// This is the default factory; the cdk_wallet build tag provides an alternative.
func NewWalletPort(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool) (WalletPort, error) {
	tw, err := New(walletPath, acceptedMints, allowAndSwapUntrustedMints)
	if err != nil {
		return nil, err
	}
	return &GonutsWallet{inner: tw}, nil
}

// gonutsToken wraps cashu.Token (gonuts interface) and implements the
// port's Token interface. Close() is a no-op because Go's GC handles
// gonuts token cleanup (no CGO resources to release).
type gonutsToken struct {
	inner cashu.Token
}

func (t *gonutsToken) Mint() string   { return t.inner.Mint() }
func (t *gonutsToken) Amount() uint64 { return t.inner.Amount() }
func (t *gonutsToken) Close()         {} // no-op: gonuts tokens are GC-managed

// --- Token operations ---

// DecodeToken parses a Cashu token string (V3 "cashuA..." or V4 "cashuB...")
// using gonuts's cashu.DecodeToken, which tries both formats.
func (w *GonutsWallet) DecodeToken(tokenStr string) (Token, error) {
	t, err := cashu.DecodeToken(tokenStr)
	if err != nil {
		return nil, err
	}
	return &gonutsToken{inner: t}, nil
}

// Receive unwraps the port Token to a gonutsToken, extracts the inner
// cashu.Token, and delegates to TollWallet.Receive.
func (w *GonutsWallet) Receive(t Token) (uint64, error) {
	gt, ok := t.(*gonutsToken)
	if !ok {
		return 0, fmt.Errorf("GonutsWallet.Receive: expected *gonutsToken, got %T", t)
	}
	return w.inner.Receive(gt.inner)
}

// --- Balance (direct delegation) ---

func (w *GonutsWallet) GetBalance() uint64 { return w.inner.GetBalance() }
func (w *GonutsWallet) GetBalanceByMint(mintUrl string) uint64 {
	return w.inner.GetBalanceByMint(mintUrl)
}
func (w *GonutsWallet) GetAllMintBalances() map[string]uint64 { return w.inner.GetAllMintBalances() }

// --- Send (direct delegation) ---

func (w *GonutsWallet) SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return w.inner.SendWithOverpayment(amount, mintUrl, maxOverpaymentPercent, maxOverpaymentAbsolute)
}

// --- Lightning mint quotes (NUT-04) ---

// RequestMintQuote delegates to TollWallet.RequestMintQuote and translates
// the gonuts response type to the port's MintQuote struct.
func (w *GonutsWallet) RequestMintQuote(amount uint64, mintUrl string) (*MintQuote, error) {
	resp, err := w.inner.RequestMintQuote(amount, mintUrl)
	if err != nil {
		return nil, err
	}
	return &MintQuote{
		QuoteID: resp.Quote,
		Request: resp.Request,
		State:   mapNut04State(resp.State),
		Amount:  resp.Amount,
		Expiry:  resp.Expiry,
	}, nil
}

// GetMintQuoteState delegates to TollWallet.GetMintQuoteState and extracts
// just the MintQuoteState value (lightning.go only uses the state field).
func (w *GonutsWallet) GetMintQuoteState(quoteID string) (MintQuoteState, error) {
	resp, err := w.inner.GetMintQuoteState(quoteID)
	if err != nil {
		return StateUnknown, err
	}
	return mapNut04State(resp.State), nil
}

// MintTokens delegates to TollWallet.MintQuoteTokens (note the name
// difference: port says MintTokens, TollWallet says MintQuoteTokens).
func (w *GonutsWallet) MintTokens(quoteID string) (uint64, error) {
	return w.inner.MintQuoteTokens(quoteID)
}

// --- Lightning melt quotes (NUT-05) ---
// NOTE: TollWallet exposes MeltToLightning (a higher-level wrapper), not
// the lower-level RequestMeltQuote/Melt pair. Wave 4 does not touch melt
// code paths, so these return an error for now. A follow-up wave can wire
// them by either adding passthrough methods to TollWallet or refactoring
// MeltToLightning to use the port pattern.

func (w *GonutsWallet) RequestMeltQuote(invoice string, mintUrl string) (*MeltQuote, error) {
	return nil, fmt.Errorf("GonutsWallet.RequestMeltQuote: not yet wired; TollWallet uses MeltToLightning at a higher level")
}

func (w *GonutsWallet) Melt(quoteID string) (*MeltResult, error) {
	return nil, fmt.Errorf("GonutsWallet.Melt: not yet wired; TollWallet uses MeltToLightning at a higher level")
}

// --- Lifecycle ---

func (w *GonutsWallet) Shutdown() error { return w.inner.Shutdown() }

// --- Internal helpers ---

// mapNut04State converts a gonuts nut04.State to the port's MintQuoteState.
func mapNut04State(s nut04.State) MintQuoteState {
	switch s {
	case nut04.Unpaid:
		return StateUnpaid
	case nut04.Paid:
		return StatePaid
	case nut04.Issued:
		return StateIssued
	case nut04.Pending:
		return StatePending
	default:
		return StateUnknown
	}
}
