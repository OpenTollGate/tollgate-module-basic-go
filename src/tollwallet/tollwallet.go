package tollwallet

import (
	"fmt"
	"log"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lightning"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/wallet"
)

// TollWallet represents a Cashu wallet that can receive, swap, and send tokens
type TollWallet struct {
	wallet                     *wallet.Wallet
	acceptedMints              []string
	allowAndSwapUntrustedMints bool
}

// New creates a new Cashu wallet instance
func New(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool) (*TollWallet, error) {

	// TODO: We want to restore from our mnemnonic seed phrase on startup as we have to keep our db in memory
	// TODO: Copy approach from alby: https://github.com/getAlby/hub/blob/158d4a2539307bda289149792c3748d44c9fed37/lnclient/cashu/cashu.go#L46

	if len(acceptedMints) < 1 {
		return nil, fmt.Errorf("No mints provided. Wallet requires at least 1 accepted mint, none were provided")
	}

	config := wallet.Config{WalletPath: walletPath, CurrentMintURL: acceptedMints[0]}
	cashuWallet, err := wallet.LoadWallet(config)

	if err != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", err)
	}

	return &TollWallet{
		wallet:                     cashuWallet,
		acceptedMints:              acceptedMints,
		allowAndSwapUntrustedMints: allowAndSwapUntrustedMints,
	}, nil
}

func (w *TollWallet) Receive(token cashu.Token) (uint64, error) {
	mint := token.Mint()

	swapToTrusted := false

	// If mint is untrusted, check if operator allows swapping or rejects untrusted mints.
	if !contains(w.acceptedMints, mint) {
		if !w.allowAndSwapUntrustedMints {
			return 0, fmt.Errorf("Token rejected. Token for mint %s is not accepted and wallet does not allow swapping of untrusted mints.", mint)
		}
		swapToTrusted = true
	}

	amountAfterSwap, err := w.wallet.Receive(token, swapToTrusted)
	return amountAfterSwap, err
}

func (w *TollWallet) Send(amount uint64, mintUrl string, includeFees bool) (cashu.Token, error) {
	proofs, err := w.wallet.Send(amount, mintUrl, includeFees)

	if err != nil {
		return nil, fmt.Errorf("Failed to send %d to %s: %w", amount, mintUrl, err)
	}

	token, err := cashu.NewTokenV4(proofs, mintUrl, cashu.Sat, true) // TODO: Support multi unit

	return token, nil
}

func (w *TollWallet) ParseToken(token string) (cashu.Token, error) {
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
