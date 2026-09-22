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
	"strings"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/cashu/nuts/nut04"
	"github.com/OpenTollGate/gonuts-tollgate/wallet"
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
func (t *gonutsToken) Serialize() (string, error) {
	return t.inner.Serialize()
}
func (t *gonutsToken) Close() {} // no-op: gonuts tokens are GC-managed

// --- Token operations ---

// DecodeToken is a package-level convenience that parses a Cashu token string
// (V3 "cashuA..." or V4 "cashuB...") without requiring a wallet instance.
// Token decoding is a pure parsing operation (base64 + unmarshal) with no
// wallet-state dependency, so callers that may not have a wallet yet (e.g.
// merchant.Fund on a degraded/zero-value Merchant) can use this directly.
func DecodeToken(tokenStr string) (Token, error) {
	t, err := cashu.DecodeToken(tokenStr)
	if err != nil {
		return nil, err
	}
	return &gonutsToken{inner: t}, nil
}

// DecodeToken on GonutsWallet delegates to the package-level function.
func (w *GonutsWallet) DecodeToken(tokenStr string) (Token, error) {
	return DecodeToken(tokenStr)
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

// mintKeysetFees returns a map of keyset ID -> InputFeePpk covering the mint's
// active and inactive keysets (the same set gonuts' feesForProofs consults).
func mintKeysetFees(mintURL string) (map[string]uint, error) {
	fees := map[string]uint{}

	active, err := wallet.GetMintActiveKeyset(mintURL, cashu.Sat)
	if err != nil {
		return nil, fmt.Errorf("get active keyset: %w", err)
	}
	fees[strings.ToLower(active.Id)] = active.InputFeePpk

	if inactive, err := wallet.GetMintInactiveKeysets(mintURL, cashu.Sat); err == nil {
		for id, ks := range inactive {
			fees[strings.ToLower(id)] = ks.InputFeePpk
		}
	}
	return fees, nil
}

// resolveKeysetID maps a token proof's (possibly short) keyset ID to a full
// keyset ID present in fees. V4 tokens store the first 8 bytes (16 hex) of the
// full ID, so a prefix match resolves both v1 and v2 keyset IDs. Unknown IDs
// return "" and contribute no fee (matching gonuts).
func resolveKeysetID(id string, fees map[string]uint) string {
	lower := strings.ToLower(id)
	if _, ok := fees[lower]; ok {
		return lower
	}
	for full := range fees {
		if strings.HasPrefix(full, lower) {
			return full
		}
	}
	return ""
}

// SwapFeeSats delegates to gonuts' fee semantics: sum each proof's keyset
// InputFeePpk, then ceil(sum/1000). The token's proofs may carry short keyset
// IDs (V4), which are resolved against the mint's keysets first.
func (w *GonutsWallet) SwapFeeSats(t Token) (uint64, error) {
	gt, ok := t.(*gonutsToken)
	if !ok {
		return 0, fmt.Errorf("GonutsWallet.SwapFeeSats: expected *gonutsToken, got %T", t)
	}

	fees, err := mintKeysetFees(gt.inner.Mint())
	if err != nil {
		return 0, err
	}

	var ppk uint64
	for _, proof := range gt.inner.Proofs() {
		if full := resolveKeysetID(proof.Id, fees); full != "" {
			ppk += uint64(fees[full])
		}
	}
	return (ppk + 999) / 1000, nil
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

func (w *GonutsWallet) Send(amount uint64, mintUrl string, includeFees bool) (Token, error) {
	t, err := w.inner.Send(amount, mintUrl, includeFees)
	if err != nil {
		return nil, err
	}
	return &gonutsToken{inner: t}, nil
}

func (w *GonutsWallet) Drain(mintUrl string) (Token, uint64, error) {
	t, amount, err := w.inner.Drain(mintUrl)
	if err != nil {
		return nil, 0, err
	}
	return &gonutsToken{inner: t}, amount, nil
}

// CheckTokenSpendable reports whether every proof of the token is still
// UNSPENT at the token's mint (NUT-07). It does not touch wallet state:
// any serialized token can be checked, which is what journal-based drain
// recovery relies on.
func (w *GonutsWallet) CheckTokenSpendable(tokenStr string) (bool, error) {
	return CheckTokenSpendable(tokenStr)
}

func (w *GonutsWallet) MeltToLightning(mintUrl string, targetAmount uint64, maxCost uint64, lnurl string) error {
	return w.inner.MeltToLightning(mintUrl, targetAmount, maxCost, lnurl)
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

// NUT #05: To request a melt quote, the wallet of `Alice` makes a `POST /v1/melt/quote/{method}` request where `method` is the payment method requested (e.g., `bolt11`, `bolt12`, etc.). `method` **MUST** match `[a-z0-9_-]+`.
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

// AcceptMint delegates runtime mint admission to the inner TollWallet.
func (w *GonutsWallet) AcceptMint(mintURL string) error {
	return w.inner.AcceptMint(mintURL)
}
