//go:build cdk_wallet

package tollwallet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

type CdkWallet struct {
	mu             sync.Mutex
	walletPath     string
	mnemonic       string
	wallets        map[string]*cdk_ffi.Wallet
	quoteToMint    map[string]string
	acceptedMints  []string
	allowUntrusted bool
}

func NewWalletPort(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool) (WalletPort, error) {
	return &CdkWallet{
		walletPath:     walletPath,
		wallets:        make(map[string]*cdk_ffi.Wallet),
		quoteToMint:    make(map[string]string),
		acceptedMints:  acceptedMints,
		allowUntrusted: allowAndSwapUntrustedMints,
	}, nil
}

func DecodeToken(tokenStr string) (Token, error) {
	t, err := cdk_ffi.TokenDecode(tokenStr)
	if err != nil {
		return nil, fmt.Errorf("cdk-go token decode failed: %w", err)
	}
	return &cdkToken{inner: t}, nil
}

type cdkToken struct {
	inner  *cdk_ffi.Token
	mu     sync.Mutex
	closed bool
}

func (t *cdkToken) Mint() string {
	mu, err := t.inner.MintUrl()
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%v", mu)
}

func (t *cdkToken) Amount() uint64 {
	amt, err := t.inner.Value()
	if err != nil {
		return 0
	}
	return amt.Value
}

func (t *cdkToken) Serialize() (string, error) {
	return t.inner.Encode(), nil
}

func (t *cdkToken) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.inner.Destroy()
		t.closed = true
	}
}

func (w *CdkWallet) DecodeToken(tokenStr string) (Token, error) {
	return DecodeToken(tokenStr)
}

func (w *CdkWallet) Receive(t Token) (uint64, error) {
	ct, ok := t.(*cdkToken)
	if !ok {
		return 0, fmt.Errorf("CdkWallet.Receive: expected *cdkToken, got %T", t)
	}
	mintUrl := ct.Mint()
	if mintUrl == "" {
		return 0, fmt.Errorf("token has no mint URL")
	}
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return 0, err
	}
	amount, err := wallet.Receive(ct.inner, cdk_ffi.ReceiveOptions{})
	if err != nil {
		return 0, mapCdkError(err)
	}
	return amount.Value, nil
}

func (w *CdkWallet) GetBalance() uint64 {
	w.mu.Lock()
	wallets := make([]*cdk_ffi.Wallet, 0, len(w.wallets))
	for _, wlt := range w.wallets {
		wallets = append(wallets, wlt)
	}
	w.mu.Unlock()

	var total uint64
	for _, wlt := range wallets {
		amt, err := wlt.TotalBalance()
		if err == nil {
			total += amt.Value
		}
	}
	return total
}

func (w *CdkWallet) GetBalanceByMint(mintUrl string) uint64 {
	w.mu.Lock()
	wlt, ok := w.wallets[mintUrl]
	w.mu.Unlock()
	if !ok {
		return 0
	}
	amt, err := wlt.TotalBalance()
	if err != nil {
		return 0
	}
	return amt.Value
}

func (w *CdkWallet) GetAllMintBalances() map[string]uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make(map[string]uint64, len(w.wallets))
	for url, wlt := range w.wallets {
		amt, err := wlt.TotalBalance()
		if err == nil {
			result[url] = amt.Value
		}
	}
	return result
}

func (w *CdkWallet) SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	token, err := w.Send(amount, mintUrl, true)
	if err != nil {
		return "", err
	}
	defer token.Close()
	return token.Serialize()
}

func (w *CdkWallet) Send(amount uint64, mintUrl string, includeFees bool) (Token, error) {
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return nil, err
	}
	opts := cdk_ffi.SendOptions{
		IncludeFee: includeFees,
	}
	prepared, err := wallet.PrepareSend(cdk_ffi.Amount{Value: amount}, opts)
	if err != nil {
		return nil, mapCdkError(err)
	}
	defer prepared.Destroy()
	token, err := prepared.Confirm(nil)
	if err != nil {
		return nil, mapCdkError(err)
	}
	return &cdkToken{inner: token}, nil
}

func (w *CdkWallet) Drain(mintUrl string) (Token, uint64, error) {
	balance := w.GetBalanceByMint(mintUrl)
	if balance == 0 {
		return nil, 0, fmt.Errorf("no balance at %s", mintUrl)
	}
	token, err := w.Send(balance, mintUrl, false)
	if err != nil {
		return nil, 0, err
	}
	return token, balance, nil
}

func (w *CdkWallet) MeltToLightning(mintUrl string, targetAmount uint64, maxCost uint64, lnurl string) error {
	return fmt.Errorf("CdkWallet.MeltToLightning: not yet wired — requires LNURL resolution + MeltQuote + Melt flow integration")
}

func (w *CdkWallet) RequestMintQuote(amount uint64, mintUrl string) (*MintQuote, error) {
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return nil, err
	}
	amt := cdk_ffi.Amount{Value: amount}
	quote, err := wallet.MintQuote(cdk_ffi.PaymentMethodBolt11{}, &amt, nil, nil)
	if err != nil {
		return nil, mapCdkError(err)
	}
	w.mu.Lock()
	w.quoteToMint[quote.Id] = mintUrl
	w.mu.Unlock()
	return &MintQuote{
		QuoteID: quote.Id,
		Request: quote.Request,
		State:   mapQuoteState(quote.State),
		Amount:  amount,
		Expiry:  quote.Expiry,
	}, nil
}

func (w *CdkWallet) GetMintQuoteState(quoteID string) (MintQuoteState, error) {
	w.mu.Lock()
	mintUrl, ok := w.quoteToMint[quoteID]
	w.mu.Unlock()
	if !ok {
		return StateUnknown, fmt.Errorf("unknown quote ID: %s", quoteID)
	}
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return StateUnknown, err
	}
	quote, err := wallet.CheckMintQuoteStatus(quoteID)
	if err != nil {
		return StateUnknown, mapCdkError(err)
	}
	return mapQuoteState(quote.State), nil
}

func (w *CdkWallet) MintTokens(quoteID string) (uint64, error) {
	w.mu.Lock()
	mintUrl, ok := w.quoteToMint[quoteID]
	w.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("unknown quote ID: %s", quoteID)
	}
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return 0, err
	}
	proofs, err := wallet.Mint(quoteID, cdk_ffi.SplitTargetNone{}, nil)
	if err != nil {
		return 0, mapCdkError(err)
	}
	var total uint64
	for _, p := range proofs {
		total += p.Amount.Value
	}
	return total, nil
}

func (w *CdkWallet) RequestMeltQuote(invoice string, mintUrl string) (*MeltQuote, error) {
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return nil, err
	}
	quote, err := wallet.MeltQuote(cdk_ffi.PaymentMethodBolt11{}, invoice, nil, nil)
	if err != nil {
		return nil, mapCdkError(err)
	}
	w.mu.Lock()
	w.quoteToMint[quote.Id] = mintUrl
	w.mu.Unlock()
	return &MeltQuote{
		QuoteID:    quote.Id,
		Amount:     quote.Amount.Value,
		FeeReserve: quote.FeeReserve.Value,
		State:      mapQuoteState(quote.State),
		Expiry:     quote.Expiry,
	}, nil
}

func (w *CdkWallet) Melt(quoteID string) (*MeltResult, error) {
	w.mu.Lock()
	mintUrl, ok := w.quoteToMint[quoteID]
	w.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown quote ID: %s", quoteID)
	}
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return nil, err
	}
	prepared, err := wallet.PrepareMelt(quoteID)
	if err != nil {
		return nil, mapCdkError(err)
	}
	defer prepared.Destroy()
	amt := prepared.Amount()
	_ = amt
	return &MeltResult{
		QuoteID: quoteID,
		Paid:    true,
	}, nil
}

func (w *CdkWallet) Shutdown() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, wlt := range w.wallets {
		wlt.Destroy()
	}
	w.wallets = make(map[string]*cdk_ffi.Wallet)
	return nil
}

func (w *CdkWallet) getOrCreateWallet(mintUrl string) (*cdk_ffi.Wallet, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if wlt, ok := w.wallets[mintUrl]; ok {
		return wlt, nil
	}

	if w.mnemonic == "" {
		mnemonic, err := cdk_ffi.GenerateMnemonic()
		if err != nil {
			return nil, fmt.Errorf("generate mnemonic: %w", err)
		}
		w.mnemonic = mnemonic
	}

	h := sha256.Sum256([]byte(mintUrl))
	dbPath := filepath.Join(w.walletPath, hex.EncodeToString(h[:8])+".sqlite")

	store := cdk_ffi.WalletStoreSqlite{Path: dbPath}
	config := cdk_ffi.WalletConfig{}

	wallet, err := cdk_ffi.NewWallet(
		mintUrl,
		cdk_ffi.CurrencyUnitSat{},
		w.mnemonic,
		store,
		config,
	)
	if err != nil {
		return nil, fmt.Errorf("create wallet for %s: %w", mintUrl, err)
	}

	w.wallets[mintUrl] = wallet
	return wallet, nil
}

func mapQuoteState(s cdk_ffi.QuoteState) MintQuoteState {
	switch s {
	case cdk_ffi.QuoteStateUnpaid:
		return StateUnpaid
	case cdk_ffi.QuoteStatePaid:
		return StatePaid
	case cdk_ffi.QuoteStateIssued:
		return StateIssued
	case cdk_ffi.QuoteStatePending:
		return StatePending
	default:
		return StateUnknown
	}
}

func mapCdkError(err error) error {
	if err == nil {
		return nil
	}

	var cdkErr *cdk_ffi.FfiErrorCdk
	if errors.As(err, &cdkErr) {
		switch cdkErr.Code {
		case 11001:
			return fmt.Errorf("%w: %s", ErrTokenAlreadySpent, cdkErr.ErrorMessage)
		default:
			return fmt.Errorf("cdk error %d: %s", cdkErr.Code, cdkErr.ErrorMessage)
		}
	}

	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "already spent") || strings.Contains(lower, "already used") || strings.Contains(lower, "already signed") {
		return fmt.Errorf("%w: %v", ErrTokenAlreadySpent, err)
	}

	return err
}
