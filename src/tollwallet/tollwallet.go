package tollwallet

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/cashu/nuts/nut04"
	"github.com/OpenTollGate/gonuts-tollgate/cashu/nuts/nut10"
	"github.com/OpenTollGate/gonuts-tollgate/wallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/lightning"
)

var ErrTokenAlreadySpent = errors.New("Token already spent")
var ErrLockedToken = errors.New("token has spending conditions and cannot be spent by the gateway")

// ErrWalletNotInitialized is returned by wallet operations when the underlying
// cashu wallet has not been initialized (for example on a bare Merchant or in
// degraded mode), so callers get an error instead of a nil-pointer panic.
var ErrWalletNotInitialized = errors.New("wallet not initialized")

type TollWallet struct {
	wallet                     *wallet.Wallet
	acceptedMints              []string
	allowAndSwapUntrustedMints bool
	registeredMints            map[string]bool
	mu                         sync.Mutex
}

// New creates a new Cashu wallet instance
func New(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool) (*TollWallet, error) {
	log.Printf("TollWallet.New: Initializing wallet at path: %s", walletPath)
	log.Printf("TollWallet.New: Accepted mints: %v", acceptedMints)

	// TODO: We want to restore from our mnemnonic seed phrase on startup as we have to keep our db in memory
	// TODO: Copy approach from alby: https://github.com/getAlby/hub/blob/158d4a2539307bda289149792c3748d44c9fed37/lnclient/cashu/cashu.go#L46

	if len(acceptedMints) < 1 {
		return nil, fmt.Errorf("No mints provided. Wallet requires at least 1 accepted mint, none were provided")
	}

	config := wallet.Config{WalletPath: walletPath, CurrentMintURL: normalizeMintURL(acceptedMints[0])}
	log.Printf("TollWallet.New: Loading wallet with config: %+v", config)

	// TODO: Fix issue where wallet db is not unlocked if it doesn't get a nework connection when the tollgate application boots.
	// This causes issues when receiving later (aka, after first connect to upstream AFTER tollgate starts).
	// The issue arises because of our hacky fork of gonuts for the offline functionatlity we need. Long term fix is switching to CDK.
	cashuWallet, err := wallet.LoadWallet(config)

	if err != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", err)
	}

	tw := &TollWallet{
		wallet:                     cashuWallet,
		acceptedMints:              acceptedMints,
		allowAndSwapUntrustedMints: allowAndSwapUntrustedMints,
		registeredMints:            map[string]bool{normalizeMintURL(acceptedMints[0]): true},
	}

	for _, mintURL := range acceptedMints[1:] {
		tw.registerMint(mintURL)
	}

	return tw, nil
}

// normalizeMintURL returns the canonical form of a mint URL: scheme and
// host lowercased, at most one trailing slash on the path. It is the single
// mint-identity function for the whole package: registry keys
// (registeredMints, and the URLs handed to wallet.AddMint) and URL
// comparison (MintURLMatches) both derive from it, so two spellings of one
// logical mint — e.g. https://mint.example/Bitcoin and
// https://mint.example/Bitcoin/ — can never become two wallet entries
// (issue #375). Path casing is preserved: /Bitcoin and /Liquid are
// different paths on the same mint. Unparseable inputs are returned
// trimmed, so equality still behaves as a plain string comparison there.
// normalizeMintURL reduces a mint URL to its canonical identity:
// scheme+host+path. Mint-identity-irrelevant components are discarded —
// userinfo credentials, query strings and fragments never select a
// different mint — and default ports (443/https, 80/http) are dropped so
// "https://m/Bitcoin" and "https://m:443/Bitcoin" are one mint, not two.
// Trailing slashes collapse (any number of them), but an escaped slash
// ("%2F") is part of the path text and stays distinct from a real one.
// Mints already registered under distinct non-canonical spellings are
// still merged read-side by GetAllMintBalances.
func normalizeMintURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return trimmed
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = canonicalHost(u.Scheme, u.Host)
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""

	escaped := u.EscapedPath()
	escaped = strings.TrimRight(escaped, "/")
	if escaped == "" {
		escaped = "/"
	}
	if decoded, unescapeErr := url.PathUnescape(escaped); unescapeErr == nil {
		u.Path = decoded
		if decoded == escaped {
			u.RawPath = ""
		} else {
			u.RawPath = escaped
		}
	} else {
		u.Path = escaped
	}
	return u.String()
}

// canonicalHost lowercases the host and drops the scheme's default port.
// IPv6 literals keep their brackets.
func canonicalHost(scheme, host string) string {
	hostname := strings.ToLower(host)
	port := ""
	if h, p, err := net.SplitHostPort(host); err == nil {
		hostname = strings.ToLower(h)
		port = p
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port == "" {
		return hostname
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]:" + port
	}
	return hostname + ":" + port
}

func (w *TollWallet) registerMint(mintURL string) {
	canonical := normalizeMintURL(mintURL)

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.registeredMints[canonical] {
		return
	}

	// Register the canonical form: gonuts keys its in-memory mint map by
	// url.Parse(url).String(), which preserves trailing slashes, so passing
	// a non-canonical URL here is exactly how duplicate per-mint entries
	// (and phantom duplicate balances) were created.
	if _, err := w.wallet.AddMint(canonical); err != nil {
		log.Printf("TollWallet: failed to register mint %s: %v", canonical, err)
		return
	}

	w.registeredMints[canonical] = true
	log.Printf("TollWallet: registered mint %s", canonical)
}

func (w *TollWallet) ensureMintRegistered(mintURL string) {
	normalized := normalizeMintURL(mintURL)

	w.mu.Lock()
	if w.registeredMints[normalized] {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	w.registerMint(mintURL)
}

func (w *TollWallet) Shutdown() error {
	if w.wallet != nil {
		return w.wallet.Shutdown()
	}
	return nil
}

// NUT #00: `Carol` can send `(x, C)` to `Bob` who then checks that `k*hash_to_curve(x) == C` (**verification**), and if so treats it as a valid spend of a token, adding `x` to the list of spent secrets.
func (w *TollWallet) Receive(token cashu.Token) (uint64, error) {
	mint := token.Mint()

	if hasLockedProofs(token.Proofs()) {
		return 0, ErrLockedToken
	}

	swapToTrusted := false

	if !contains(w.acceptedMints, mint) {
		if !w.allowAndSwapUntrustedMints {
			err := fmt.Errorf("Token rejected. Token for mint %s is not accepted and wallet does not allow swapping of untrusted mints. Accepted: %v", mint, w.acceptedMints)
			return 0, err
		}
		swapToTrusted = true
	}

	w.ensureMintRegistered(mint)

	amountAfterSwap, err := w.wallet.Receive(token, swapToTrusted)
	if err != nil {
		// The upstream cashu library does not export a typed error for
		// "token already spent", so we match on the error string at this
		// boundary and wrap it with our own sentinel (ErrTokenAlreadySpent).
		// Callers should use errors.Is(err, ErrTokenAlreadySpent) — no
		// further string matching needed downstream. The patterns cover the
		// phrases mints actually use, and only work at all since the gonuts
		// bump: older versions swallowed the mint's rejection entirely
		// (empty "could not swap proofs: " errors), so this mapping could
		// never fire.
		if isAlreadySpentError(err) {
			return 0, fmt.Errorf("%w: %v", ErrTokenAlreadySpent, err)
		}
		return 0, err
	}

	return amountAfterSwap, nil
}

// NUT #03: The swap operation is the most important component of the Cashu system. A swap operation consists of multiple inputs (`Proofs`) and outputs (`BlindedMessages`). Mints verify and invalidate the inputs and issue new promises (`BlindSignatures`). These are then used by the wallet to generate new `Proofs` (see [NUT-00][00]).
func (w *TollWallet) Send(amount uint64, mintUrl string, includeFees bool) (cashu.Token, error) {
	log.Printf("TollWallet.Send: attempting to send %d sats from mint %s (includeFees=%t)", amount, mintUrl, includeFees)

	proofs, err := w.wallet.Send(amount, mintUrl, includeFees)
	if err != nil {
		log.Printf("TollWallet.Send: wallet.Send failed: %v", err)
		return nil, fmt.Errorf("Failed to send %d to %s: %w", amount, mintUrl, err)
	}

	log.Printf("TollWallet.Send: received %d proofs from wallet.Send", len(proofs))

	// Validate proofs array is not empty
	if len(proofs) == 0 {
		log.Printf("TollWallet.Send: ERROR - received empty proofs array from wallet.Send")
		return nil, fmt.Errorf("wallet.Send returned empty proofs array for %d sats from %s", amount, mintUrl)
	}

	// Log proof details for debugging
	totalProofAmount := uint64(0)
	for i, proof := range proofs {
		totalProofAmount += proof.Amount
		log.Printf("TollWallet.Send: proof[%d]: amount=%d", i, proof.Amount)
	}
	log.Printf("TollWallet.Send: total proof amount=%d (requested=%d)", totalProofAmount, amount)

	token, err := cashu.NewTokenV4(proofs, mintUrl, cashu.Sat, true) // TODO: Support multi unit
	if err != nil {
		log.Printf("TollWallet.Send: NewTokenV4 failed: %v", err)
		return nil, fmt.Errorf("Failed to create token: %w", err)
	}

	log.Printf("TollWallet.Send: successfully created token")
	return token, nil
}

// Drain creates a token containing all available balance for a specific mint
// This is used for draining the wallet and does NOT include fees in the amount
// since we want to extract all available funds without triggering swaps
func (w *TollWallet) Drain(mintUrl string) (cashu.Token, uint64, error) {
	log.Printf("TollWallet.Drain: attempting to drain all funds from mint %s", mintUrl)

	// Get the current balance for this mint
	balance := w.GetBalanceByMint(mintUrl)
	if balance == 0 {
		log.Printf("TollWallet.Drain: no balance to drain from mint %s", mintUrl)
		return nil, 0, fmt.Errorf("no balance available for mint %s", mintUrl)
	}

	log.Printf("TollWallet.Drain: draining %d sats from mint %s (includeFees=false)", balance, mintUrl)

	// Use Send with includeFees=false to avoid trying to add fees to the amount
	// This ensures we only send what's available without triggering insufficient funds errors
	proofs, err := w.wallet.Send(balance, mintUrl, false)
	if err != nil {
		log.Printf("TollWallet.Drain: wallet.Send failed: %v", err)
		return nil, 0, fmt.Errorf("Failed to drain %d from %s: %w", balance, mintUrl, err)
	}

	log.Printf("TollWallet.Drain: received %d proofs from wallet.Send", len(proofs))

	// Validate proofs array is not empty
	if len(proofs) == 0 {
		log.Printf("TollWallet.Drain: ERROR - received empty proofs array from wallet.Send")
		return nil, 0, fmt.Errorf("wallet.Send returned empty proofs array for %d sats from %s", balance, mintUrl)
	}

	// Log proof details for debugging
	totalProofAmount := uint64(0)
	for i, proof := range proofs {
		totalProofAmount += proof.Amount
		log.Printf("TollWallet.Drain: proof[%d]: amount=%d", i, proof.Amount)
	}
	log.Printf("TollWallet.Drain: total proof amount=%d (balance was=%d)", totalProofAmount, balance)

	token, err := cashu.NewTokenV4(proofs, mintUrl, cashu.Sat, true)
	if err != nil {
		log.Printf("TollWallet.Drain: NewTokenV4 failed: %v", err)
		return nil, 0, fmt.Errorf("Failed to create token: %w", err)
	}

	log.Printf("TollWallet.Drain: successfully created drain token with %d sats", totalProofAmount)
	return token, totalProofAmount, nil
}

// SendWithOverpayment sends tokens with overpayment capability using gonuts SendWithOptions
func (w *TollWallet) SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, MaxOverpaymentAbsolute uint64) (string, error) {
	// Set up send options with overpayment capability
	options := wallet.SendOptions{
		IncludeFees:            true,
		AllowOverpayment:       true,
		MaxOverpaymentPercent:  uint(maxOverpaymentPercent),
		MaxOverpaymentAbsolute: MaxOverpaymentAbsolute,
	}

	// Use the gonuts SendWithOptions method
	result, err := w.wallet.SendWithOptions(amount, mintUrl, options)
	if err != nil {
		return "", fmt.Errorf("failed to send with overpayment to %s: %w", mintUrl, err)
	}

	// Create token from the proofs
	token, err := cashu.NewTokenV4(result.Proofs, mintUrl, cashu.Sat, true)
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}

	// Encode token to string
	tokenString, err := token.Serialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize token: %w", err)
	}

	log.Printf("Send successful with %d%% overpayment tolerance: requested=%d, overpayment=%d",
		maxOverpaymentPercent, result.RequestedAmount, result.Overpayment)

	return tokenString, nil
}

// NUT #04: To request a mint quote, the wallet of `Alice` makes a `POST /v1/mint/quote/{method}` request where `method` is the payment method requested (e.g., `bolt11`, `bolt12`, etc.). `method` **MUST** match `[a-z0-9_-]+`.
func (w *TollWallet) RequestMintQuote(amount uint64, mintURL string) (*nut04.PostMintQuoteBolt11Response, error) {
	w.ensureMintRegistered(mintURL)
	return w.wallet.RequestMint(amount, mintURL)
}

// NUT #04: To check the current accounting data of a mint quote, the wallet makes a `GET /v1/mint/quote/{method}/{quote_id}`.
func (w *TollWallet) GetMintQuoteState(quoteID string) (*nut04.PostMintQuoteBolt11Response, error) {
	if w.wallet == nil {
		return nil, ErrWalletNotInitialized
	}
	return w.wallet.MintQuoteState(quoteID)
}

// NUT #04: After requesting a mint quote and paying the request, the wallet proceeds with minting new tokens by calling the `POST /v1/mint/{method}` endpoint.
func (w *TollWallet) MintQuoteTokens(quoteID string) (uint64, error) {
	return w.wallet.MintTokens(quoteID)
}

func ParseToken(token string) (cashu.Token, error) {
	return cashu.DecodeToken(token)
}

// MintURLMatches reports whether two mint URLs identify the same logical
// mint. Comparison is canonical equality via normalizeMintURL, so registry
// keys and match results can never disagree (issue #375). Unparseable
// inputs fall back to trimmed string comparison.
func MintURLMatches(a, b string) bool {
	return normalizeMintURL(a) == normalizeMintURL(b)
}

// isAlreadySpentError reports whether err is a mint rejection for reusing
// spent secrets. Mint families phrase it differently — Nutshell-style
// "Token already spent", CDK-style "inputs have already been spent" — and
// the match is case-insensitive on purpose. The empty errors older gonuts
// versions produced (swallowed rejections) deliberately do NOT match.
func isAlreadySpentError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already been spent") ||
		strings.Contains(msg, "already spent")
}

func hasLockedProofs(proofs cashu.Proofs) bool {
	for _, proof := range proofs {
		secret, err := nut10.DeserializeSecret(proof.Secret)
		if err == nil && secret.Kind != nut10.AnyoneCanSpend {
			return true
		}
	}
	return false
}

func contains(slice []string, str string) bool {
	for _, item := range slice {
		if MintURLMatches(item, str) {
			return true
		}
	}
	return false
}

// GetBalance returns the current balance of the wallet
func (w *TollWallet) GetBalance() uint64 {
	balance := w.wallet.GetBalance()

	return balance
}

// GetBalanceByMint returns the balance of a specific mint in the wallet.
// The lookup tolerates historical non-canonical aliases already persisted
// in wallet DBs (e.g. a trailing-slash variant), matching by logical mint
// identity rather than exact string key.
func (w *TollWallet) GetBalanceByMint(mintUrl string) uint64 {
	balanceByMints := w.wallet.GetBalanceByMints()

	if balance, exists := balanceByMints[mintUrl]; exists {
		return balance
	}
	for url, balance := range balanceByMints {
		if MintURLMatches(url, mintUrl) {
			return balance
		}
	}
	return 0
}

// GetAllMintBalances returns a map of all logical mints and their balances
// in the wallet. Wallet DBs created before URL canonicalization can hold
// several URL aliases for one physical mint, each reporting the same
// keyset-backed balance. This view merges such alias groups into a single
// entry (representative: lexicographically smallest alias; balance: the
// group's maximum view) so callers — notably the wallet-drain loop — can
// never observe a phantom duplicate balance for one mint (issue #375).
// Read-side only: the underlying wallet DB is never rewritten.
func (w *TollWallet) GetAllMintBalances() map[string]uint64 {
	balanceByMints := w.wallet.GetBalanceByMints()

	type aliasGroup struct {
		representative string
		balance        uint64
	}
	groups := make(map[string]*aliasGroup, len(balanceByMints))
	for alias, balance := range balanceByMints {
		canonical := normalizeMintURL(alias)
		group, merged := groups[canonical]
		if !merged {
			groups[canonical] = &aliasGroup{representative: alias, balance: balance}
			continue
		}
		if alias < group.representative {
			group.representative = alias
		}
		if balance > group.balance {
			group.balance = balance
		}
	}

	balances := make(map[string]uint64, len(groups))
	for _, group := range groups {
		balances[group.representative] = group.balance
	}
	return balances
}

// NUT #05: To request a melt quote, the wallet of `Alice` makes a `POST /v1/melt/quote/{method}` request where `method` is the payment method requested (e.g., `bolt11`, `bolt12`, etc.). `method` **MUST** match `[a-z0-9_-]+`.

// MeltToLightning melts a token to a lightning invoice using LNURL
// It attempts to melt for the target amount, reducing by 5% each time if fees are too high
func (w *TollWallet) MeltToLightning(mintUrl string, targetAmount uint64, maxCost uint64, lnurl string) error {
	log.Printf("Attempting to melt %d sats to LNURL %s with max %d sats", targetAmount, lnurl, maxCost)

	// Start with the aimed payment amount
	currentAmount := targetAmount
	// Retry the melt up to 5 times.
	maxAttempts := 5
	attempts := 0

	var meltError error

	// Try to melt with reducing amounts if needed
	for attempts < maxAttempts {
		log.Printf("Attempt %d: Trying to melt %d sats", attempts+1, currentAmount)

		// Get a Lightning invoice from the LNURL
		invoice, err := lightning.GetInvoiceFromLightningAddress(lnurl, currentAmount)
		if err != nil {
			log.Printf("Error getting invoice: %v", err)
			meltError = err
			attempts++
			continue
		}

		// Try to pay the invoice using the wallet
		meltQuote, meltQuoteErr := w.wallet.RequestMeltQuote(invoice, mintUrl)

		if meltQuoteErr != nil {
			log.Printf("Error requesting melt quote for %s: %v", mintUrl, meltQuoteErr)
			meltError = meltQuoteErr
			attempts++
			continue
		}

		if meltQuote.Amount > maxCost {
			log.Printf("Melting %d to %s costs too much, reducing by 5%%", targetAmount, lnurl)
			meltError = fmt.Errorf("melt cost exceeds maximum allowed: %d > %d", meltQuote.Amount, maxCost)
			currentAmount = currentAmount - (currentAmount * 5 / 100) // Reduce by 5%
			attempts++
			continue
		}

		meltResult, meltErr := w.wallet.Melt(meltQuote.Quote)

		if meltErr != nil {
			log.Printf("Error melting quote %s for %s: %v", meltQuote.Quote, mintUrl, meltErr)
			meltError = meltErr
			attempts++
			continue
		}

		log.Printf("meltResult: %s", meltResult.State)
		log.Printf("Successfully melted %d sats with %d sats in fees", currentAmount, meltResult.FeeReserve)
		return nil

	}

	// If we get here, all attempts failed
	return fmt.Errorf("failed to melt after %d attempts: %w", attempts, meltError)
}
