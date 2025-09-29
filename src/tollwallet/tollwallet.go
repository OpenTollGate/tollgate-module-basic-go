package tollwallet

import (
	"fmt"
	"log"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lightning"
	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/Origami74/gonuts-tollgate/wallet"
)

// TollWallet represents a Cashu wallet that can receive, swap, and send tokens
type TollWallet struct {
	walletIsLoaded             bool
	config                     wallet.Config
	wallet                     *wallet.Wallet
	acceptedMints              []string
	allowAndSwapUntrustedMints bool
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

	config := wallet.Config{WalletPath: walletPath, CurrentMintURL: acceptedMints[0]}
	log.Printf("TollWallet.New: Loading wallet with config: %+v", config)
	cashuWallet, err := wallet.LoadWallet(config)

	var walletIsLoaded = false
	if err == nil {
		walletIsLoaded = true
	} else {
		log.Printf("TollWallet.New: Wallet loadeding fialed, will retry on receive. Error: %s", err)
	}

	// // TODO: Catch warning here and fail fast. The service can be configured to automatically restart..
	// // Sun Apr 13 16:38:57 2025 daemon.info tollgate-wrt[2740]: Warning: network connection issue during adding new mint: network connection issue during fetching active keyset from mint: error getting active keysets from mint: Get "https://nofees.testnut.cashu.space/v1/keysets": dial tcp: lookup nofees.testnut.cashu.space on [::1]:53: server misbehaving

	// // Check for specific network warning that indicates a transient issue
	// // and fail fast to allow service restart logic to kick in.
	// // The warning is logged by the wallet library, but not returned as an error.
	// // We need to check the error string for the warning message.
	// if err != nil {
	// 	log.Printf("TollWallet.New: Failed to load wallet: %v", err)
	// 	errStr := err.Error()
	// 	if strings.Contains(errStr, "server misbehaving") || strings.Contains(errStr, "network connection issue") {
	// 		log.Printf("TollWallet.New: Detected critical network error, failing fast to trigger restart: %s", errStr)
	// 		return nil, fmt.Errorf("critical network error during wallet initialization, failing fast: %w", err)
	// 	}
	// 	return nil, fmt.Errorf("failed to create wallet: %w", err)
	// } else {
	// 	// If there's no error, but the wallet library logged a warning, we need to check for it.
	// 	// This is a hacky way to detect the warning, but it's the best we can do without
	// 	// modifying the wallet library.
	// 	// We'll check if the cashuWallet is nil, which might indicate a problem.
	// 	if cashuWallet == nil {
	// 		log.Printf("TollWallet.New: Wallet is nil after LoadWallet, this might indicate a problem.")
	// 		// We don't have a good way to detect the warning here, so we'll just log it.
	// 		// The service will likely fail later, and the restart logic will kick in.
	// 	}
	// }
	// log.Printf("TollWallet.New: Wallet loaded successfully")

	return &TollWallet{
		walletIsLoaded:             walletIsLoaded,
		config:                     config,
		wallet:                     cashuWallet,
		acceptedMints:              acceptedMints,
		allowAndSwapUntrustedMints: allowAndSwapUntrustedMints,
	}, nil
}

func (w *TollWallet) Receive(token cashu.Token) (uint64, error) {
	log.Printf("TollWallet.Receive: Starting token reception")
	mint := token.Mint()
	log.Printf("TollWallet.Receive: Token mint: %s", mint)

	swapToTrusted := false

	// If mint is untrusted, check if operator allows swapping or rejects untrusted mints.
	if !contains(w.acceptedMints, mint) {
		if !w.allowAndSwapUntrustedMints {
			err := fmt.Errorf("Token rejected. Token for mint %s is not accepted and wallet does not allow swapping of untrusted mints.", mint)
			log.Printf("TollWallet.Receive: %v", err)
			return 0, err
		}
		swapToTrusted = true
		log.Printf("TollWallet.Receive: Token will be swapped to trusted mint")
	}

	if !w.walletIsLoaded {
		cashuWallet, err := wallet.LoadWallet(w.config)

		if err != nil {
			log.Printf("TollWallet.Receive: wallet.Load failed: %v", err)
		} else {
			w.wallet = cashuWallet
			log.Printf("TollWallet.Receive: Sucessfully loaded wallet.")
		}
	}

	log.Printf("TollWallet.Receive: Calling wallet.Receive")
	amountAfterSwap, err := w.wallet.Receive(token, swapToTrusted)
	if err != nil {
		log.Printf("TollWallet.Receive: wallet.Receive failed: %v", err)
		return 0, err
	}
	log.Printf("TollWallet.Receive: Successfully received %d sats", amountAfterSwap)

	return amountAfterSwap, err
}

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
		log.Printf("TollWallet.Send: proof[%d]: amount=%d, secret=%s...", i, proof.Amount, proof.Secret[:min(10, len(proof.Secret))])
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

func ParseToken(token string) (cashu.Token, error) {
	return cashu.DecodeToken(token)
}

// contains checks if a string exists in a slice of strings
func contains(slice []string, str string) bool {
	for _, item := range slice {
		if item == str {
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

// GetBalanceByMint returns the balance of a specific mint in the wallet
func (w *TollWallet) GetBalanceByMint(mintUrl string) uint64 {
	balanceByMints := w.wallet.GetBalanceByMints()

	if balance, exists := balanceByMints[mintUrl]; exists {
		return balance
	}
	return 0
}

// MeltToLightning melts a token to a lightning invoice using LNURL
// It attempts to melt for the target amount, reducing by 5% each time if fees are too high
func (w *TollWallet) MeltToLightning(mintUrl string, targetAmount uint64, maxCost uint64, lnurl string) error {
	log.Printf("Attempting to melt %d sats to LNURL %s with max %d sats", targetAmount, lnurl, maxCost)

	// Start with the aimed payment amount
	currentAmount := targetAmount
	maxAttempts := 10
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
