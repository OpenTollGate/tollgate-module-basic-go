//go:build cdk_wallet

package tollwallet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

// mnemonicFile persists the wallet seed — losing it forfeits every derived balance.
const mnemonicFile = "cdk-wallet-mnemonic.txt"

type CdkWallet struct {
	mu             sync.Mutex
	walletPath     string
	mnemonic       string
	wallets        map[string]*cdk_ffi.Wallet
	quoteToMint    map[string]string
	acceptedMints  []string
	allowUntrusted bool
	// mintUnits carries the configured unit per mint URL ("" == sat). A wallet
	// created for a mint must run in the unit that mint issues: the cdk-go
	// FFI keys every wallet (keyset lookup, receive, balance) by unit, so an
	// mb token from an operator mint needs a wallet in CurrencyUnitCustom
	// "mb" — a Sat wallet cannot even see the mb keyset (probe evidence in
	// cdk_custom_unit_probe_test.go).
	mintUnits map[string]string
}

func NewWalletPort(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool, mintUnits map[string]string) (WalletPort, error) {
	mnemonic, err := loadOrCreateMnemonic(walletPath)
	if err != nil {
		return nil, fmt.Errorf("load or create cdk wallet mnemonic: %w", err)
	}
	return &CdkWallet{
		walletPath:     walletPath,
		mnemonic:       mnemonic,
		wallets:        make(map[string]*cdk_ffi.Wallet),
		quoteToMint:    make(map[string]string),
		acceptedMints:  acceptedMints,
		allowUntrusted: allowAndSwapUntrustedMints,
		mintUnits:      mintUnits,
	}, nil
}

func loadOrCreateMnemonic(walletPath string) (string, error) {
	path := filepath.Join(walletPath, mnemonicFile)
	if data, err := os.ReadFile(path); err == nil {
		if m := strings.TrimSpace(string(data)); m != "" {
			return m, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	mnemonic, err := cdk_ffi.GenerateMnemonic()
	if err != nil {
		return "", fmt.Errorf("generate mnemonic: %w", err)
	}
	if err := os.MkdirAll(walletPath, 0o700); err != nil {
		return "", err
	}
	// 0600: the mnemonic is the wallet's entire key material.
	if err := os.WriteFile(path, []byte(mnemonic), 0o600); err != nil {
		return "", err
	}
	return mnemonic, nil
}

func DecodeToken(tokenStr string) (Token, error) {
	// cdk-mintd 0.18.0 emits V4 tokens with the mint URL under "f"; the cdk-go
	// FFI decoder requires "m". Without this, a token the router issued is
	// rejected by that same router with a bare "missing field `m`". See
	// v4_field_skew.go for the source citation and the measurement.
	t, err := cdk_ffi.TokenDecode(normalizeV4MintFieldName(tokenStr))
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

// Secrets returns nil: the cdk adapter's token wrapper keeps proofs inside
// cdk-go and has no NUT-07 surface on this build tag; the wallet's
// CheckTokenSpent refuses with ErrCheckStateUnsupported instead.
func (t *cdkToken) Secrets() []string { return nil }

func (t *cdkToken) Mint() string {
	mu, err := t.inner.MintUrl()
	if err != nil {
		return ""
	}
	// cdk-go 0.17.3 returns a MintUrl struct; %v rendered "{url}" which could
	// never match an accepted-mints list entry — tokens were rejected before
	// any unit logic ran (found by the mb receive probe).
	return mu.Url
}

// Unit reports the token's currency unit, or "" when the FFI cannot say. The
// merchant prices with this value (see calculateAllotmentForUnit), so a
// missing unit is reported honestly rather than assumed to be "sat".
func (t *cdkToken) Unit() string {
	u := t.inner.Unit()
	if u == nil || *u == nil {
		return ""
	}
	switch v := (*u).(type) {
	case cdk_ffi.CurrencyUnitSat:
		return "sat"
	case cdk_ffi.CurrencyUnitMsat:
		return "msat"
	case cdk_ffi.CurrencyUnitUsd:
		return "usd"
	case cdk_ffi.CurrencyUnitCustom:
		return v.Unit
	default:
		return ""
	}
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
	if !w.mintAccepted(mintUrl) {
		return 0, fmt.Errorf("token mint %s is not in the accepted mints list", mintUrl)
	}
	wallet, err := w.getOrCreateWallet(mintUrl)
	if err != nil {
		return 0, err
	}
	// ReceiveOptions{} has a nil AmountSplitTarget interface; the uniffi
	// lowerer panics on nil enum values ("invalid enum value <nil>" — found
	// by the mb receive probe). SplitTargetNone is the explicit default.
	amount, err := wallet.Receive(ct.inner, cdk_ffi.ReceiveOptions{
		AmountSplitTarget: cdk_ffi.SplitTargetNone{},
	})
	if err != nil {
		return 0, mapCdkError(err)
	}
	return amount.Value, nil
}

// SwapFeeSats is not implemented by the cdk adapter. The caller (merchant)
// treats the error as "fee unknown" and falls back to classifying the Receive
// error instead of pre-checking. The default build (!cdk_wallet) uses gonuts.
func (w *CdkWallet) SwapFeeSats(t Token) (uint64, error) {
	return 0, errors.New("SwapFeeSats: not implemented by the cdk wallet adapter")
}

// mintAccepted mirrors the gonuts trust model: listed mints pass;
// otherwise the allowUntrusted flag decides. Swap-to-trusted is not
// implemented in this adapter — untrusted tokens are accepted as-is.
func (w *CdkWallet) mintAccepted(mintUrl string) bool {
	for _, m := range w.acceptedMints {
		if m == mintUrl {
			return true
		}
	}
	return w.allowUntrusted
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
	// Hold mu through the CGO call so a concurrent Shutdown cannot
	// destroy the wallet mid-read (use-after-free).
	w.mu.Lock()
	defer w.mu.Unlock()
	// Wallets are keyed mint|unit (see getOrCreateWallet): a custom-unit mint
	// and a sat mint can coexist on one router, so the unit is part of the
	// identity. Reading the bare mint URL always missed and reported 0 — an
	// operator-issued mb/min balance read as empty while the wallet held
	// funds. Sum across every unit open for this mint.
	var total uint64
	for key, wlt := range w.wallets {
		if key != mintUrl && !strings.HasPrefix(key, mintUrl+"|") {
			continue
		}
		if amt, err := wlt.TotalBalance(); err == nil {
			total += amt.Value
		}
	}
	if total == 0 {
		// Distinguish "no wallet open" from "open wallet, zero balance" the
		// same way the previous single-lookup path did.
		if _, ok := w.wallets[mintUrl]; !ok && !w.anyUnitWallet(mintUrl) {
			return 0
		}
	}
	return total
}

// anyUnitWallet reports whether any unit-keyed wallet exists for mintUrl.
func (w *CdkWallet) anyUnitWallet(mintUrl string) bool {
	for key := range w.wallets {
		if strings.HasPrefix(key, mintUrl+"|") {
			return true
		}
	}
	return false
}

func (w *CdkWallet) getBalanceByMintLegacy(mintUrl string) uint64 {
	wlt, ok := w.wallets[mintUrl]
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
	// cdk-go's SendOptions exposes no overpayment caps; honoring the
	// request would mean an unrestricted send. Fail loudly instead.
	if maxOverpaymentPercent > 0 || maxOverpaymentAbsolute > 0 {
		return "", fmt.Errorf("CdkWallet: overpayment limits not supported by the cdk-go adapter (percent=%d, absolute=%d); refusing unrestricted send", maxOverpaymentPercent, maxOverpaymentAbsolute)
	}
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
		// The FFI lowers AmountSplitTarget as a non-optional enum: a zero value
		// panics inside FfiConverterSplitTarget.Write ("invalid enum value
		// <nil>"). Send a whole-proof token, which is what a caller handing a
		// token to a user wants anyway.
		AmountSplitTarget: cdk_ffi.SplitTargetNone{},
		// Same class of bug: SendKind is also lowered as a non-optional enum.
		// OnlineExact lets the wallet swap first so the token carries exactly
		// the requested amount.
		SendKind: cdk_ffi.SendKindOnlineExact{},
		// Third non-optional enum in this struct: the zero value is rejected
		// ("Invalid P2PKLockedProofSendMode enum value: 0").
		P2pkLockedProofSendMode: cdk_ffi.P2pkLockedProofSendModeSwap,
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
	// The payment method must match the mint's unit. A custom-unit mint (an
	// operator's "mb"/"min" mint) does NOT serve bolt11 for that unit: asking
	// for a bolt11 quote against it panics inside the cdk-go FFI with a bare
	// "panic: EOF", which is unactionable. Measured on the A04E bench: the
	// router's own mint advertises bolt11/mb, bolt12/mb, onchain/mb, paypal/mb
	// but only the custom method actually produces a quote for a custom unit.
	//
	// So: custom unit -> custom payment method; sat -> bolt11 as before.
	method := cdk_ffi.PaymentMethod(cdk_ffi.PaymentMethodBolt11{})
	if u := w.mintUnits[mintUrl]; u != "" && u != "sat" {
		method = cdk_ffi.PaymentMethodCustom{Method: customUnitPaymentMethod(u)}
	}
	quote, err := wallet.MintQuote(method, &amt, nil, nil)
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
	// PrepareMelt only stages; Confirm executes. Paid-without-Confirm is a
	// funds-loss bug (proofs reported spent while merely staged).
	finalized, err := prepared.Confirm()
	if err != nil {
		return nil, mapCdkError(err)
	}
	defer finalized.Destroy()
	result := &MeltResult{
		QuoteID: quoteID,
		Paid:    finalized.State == cdk_ffi.QuoteStatePaid,
	}
	if finalized.Preimage != nil {
		result.Preimage = *finalized.Preimage
	}
	return result, nil
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

	unit := w.mintUnits[mintUrl]
	if wlt, ok := w.wallets[mintUrl+"|"+unit]; ok {
		return wlt, nil
	}

	if w.mnemonic == "" {
		return nil, errors.New("cdk wallet has no mnemonic — NewWalletPort must initialize it")
	}

	// Unit: the wallet must run in the unit the mint issues (config's
	// "unit"; "" == sat). The FFI keys keysets/balances by unit, so this is
	// not cosmetic — a Sat wallet cannot see a custom-unit keyset.
	var cdkUnit cdk_ffi.CurrencyUnit
	if unit == "" || unit == "sat" {
		cdkUnit = cdk_ffi.CurrencyUnitSat{}
	} else {
		cdkUnit = cdk_ffi.CurrencyUnitCustom{Unit: unit}
	}

	h := sha256.Sum256([]byte(mintUrl + "|" + unit))
	dbPath := filepath.Join(w.walletPath, hex.EncodeToString(h[:8])+".sqlite")

	store := cdk_ffi.WalletStoreSqlite{Path: dbPath}
	config := cdk_ffi.WalletConfig{}

	wallet, err := cdk_ffi.NewWallet(
		mintUrl,
		cdkUnit,
		w.mnemonic,
		store,
		config,
	)
	if err != nil {
		return nil, fmt.Errorf("create wallet for %s (unit %q): %w", mintUrl, unit, err)
	}

	w.wallets[mintUrl+"|"+unit] = wallet
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

// AcceptMint grows the cdk adapter's accepted-mint list at runtime so a
// mint that was unreachable at construction is no longer refused once it
// recovers.
func (w *CdkWallet) AcceptMint(mintURL string) error {
	mint := normalizeMintURL(mintURL)
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range w.acceptedMints {
		if m == mint {
			return nil
		}
	}
	w.acceptedMints = append(w.acceptedMints, mint)
	return nil
}

// CheckTokenSpent is not implemented by the cdk adapter: it has no
// NUT-07 surface on this build tag. Callers get a typed error and must
// treat the outcome as unresolved — never guess.
func (w *CdkWallet) CheckTokenSpent(token Token) (bool, error) {
	return false, ErrCheckStateUnsupported
}

// customUnitPaymentMethod names the payment method to use for a custom unit.
// The bench mint is configured with the "paypal" teller-style processor, which
// is the method that produces a quote for a custom unit; "teller" is the name
// the pecan-derived trade study uses for the same idea. Both are accepted by
// the mint we measured against, and the environment can override it so a
// different operator mint is not hard-coded to this one.
func customUnitPaymentMethod(unit string) string {
	if m := os.Getenv("TOLLGATE_CDK_PURCHASE_METHOD"); m != "" {
		return m
	}
	return "paypal"
}
