package mint_proxy

import (
	"fmt"
	"time"

	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/Origami74/gonuts-tollgate/wallet"
	"github.com/sirupsen/logrus"
)

// MintClient handles interactions with Cashu mints using gonuts
type MintClient struct {
	wallet  *wallet.Wallet
	logger  *logrus.Entry
	mintURL string
}

// NewMintClient creates a new mint client for the specified mint
func NewMintClient(mintURL string) (*MintClient, error) {
	logger := logrus.WithField("module", "mint_proxy.mint_client")

	// Create temporary wallet for mint operations
	config := wallet.Config{
		WalletPath:     "/tmp/mint_proxy_wallet",
		CurrentMintURL: mintURL,
	}

	gonutsWallet, err := wallet.LoadWallet(config)
	if err != nil {
		logger.WithError(err).WithField("mint_url", mintURL).Error("Failed to create gonuts wallet")
		return nil, fmt.Errorf("failed to create gonuts wallet: %w", err)
	}

	return &MintClient{
		wallet:  gonutsWallet,
		logger:  logger,
		mintURL: mintURL,
	}, nil
}

// RequestInvoiceResponse represents the response from requesting an invoice
type RequestInvoiceResponse struct {
	PaymentRequest string
	PaymentHash    string
	Amount         uint64
	ExpiresAt      time.Time
}

// RequestInvoice requests a Lightning invoice from the mint using gonuts
func (mc *MintClient) RequestInvoice(mintURL string, amount uint64) (*RequestInvoiceResponse, error) {
	mc.logger.WithFields(logrus.Fields{
		"mint_url": mintURL,
		"amount":   amount,
	}).Debug("Requesting invoice from mint")

	// Use gonuts to request mint quote (which includes Lightning invoice)
	quote, err := mc.wallet.RequestMint(amount, mintURL)
	if err != nil {
		mc.logger.WithError(err).WithField("mint_url", mintURL).Error("Failed to request mint quote")
		return nil, fmt.Errorf("failed to request mint quote: %w", err)
	}

	// Calculate expiry time (usually 30 minutes from now)
	expiresAt := time.Now().Add(30 * time.Minute)

	response := &RequestInvoiceResponse{
		PaymentRequest: quote.Request,
		PaymentHash:    quote.Quote, // Quote ID serves as payment hash
		Amount:         amount,
		ExpiresAt:      expiresAt,
	}

	mc.logger.WithFields(logrus.Fields{
		"mint_url": mintURL,
		"amount":   amount,
		"quote_id": quote.Quote,
		"invoice":  quote.Request[:50] + "...", // Log partial invoice
	}).Debug("Successfully requested invoice from mint")

	return response, nil
}

// CheckPaymentStatusResponse represents the payment status response
type CheckPaymentStatusResponse struct {
	Paid    bool
	QuoteID string
}

// CheckPaymentStatus checks if a Lightning invoice has been paid using gonuts
func (mc *MintClient) CheckPaymentStatus(mintURL, quoteID string) (*CheckPaymentStatusResponse, error) {
	mc.logger.WithFields(logrus.Fields{
		"mint_url": mintURL,
		"quote_id": quoteID,
	}).Debug("Checking payment status")

	// Use gonuts to check mint quote status
	quoteState, err := mc.wallet.MintQuoteState(quoteID)
	if err != nil {
		mc.logger.WithError(err).WithFields(logrus.Fields{
			"mint_url": mintURL,
			"quote_id": quoteID,
		}).Error("Failed to check mint quote state")
		return nil, fmt.Errorf("failed to check mint quote state: %w", err)
	}

	// Check if the quote state indicates payment (State field indicates payment status)
	isPaid := (quoteState.State.String() == "PAID")

	response := &CheckPaymentStatusResponse{
		Paid:    isPaid,
		QuoteID: quoteID,
	}

	mc.logger.WithFields(logrus.Fields{
		"mint_url": mintURL,
		"quote_id": quoteID,
		"paid":     isPaid,
	}).Debug("Payment status checked")

	return response, nil
}

// MintTokens creates Cashu tokens after payment is confirmed using gonuts
func (mc *MintClient) MintTokens(mintURL, quoteID string, amount uint64) (string, error) {
	mc.logger.WithFields(logrus.Fields{
		"mint_url": mintURL,
		"quote_id": quoteID,
		"amount":   amount,
	}).Debug("Minting tokens")

	// Use gonuts to mint tokens
	mintAmount, err := mc.wallet.MintTokens(quoteID)
	if err != nil {
		mc.logger.WithError(err).WithFields(logrus.Fields{
			"mint_url": mintURL,
			"quote_id": quoteID,
		}).Error("Failed to mint tokens")
		return "", fmt.Errorf("failed to mint tokens: %w", err)
	}

	// Get the tokens from wallet and create a token string
	// First, we need to send the tokens to get them as a transferable token
	proofs, err := mc.wallet.Send(mintAmount, mintURL, false)
	if err != nil {
		mc.logger.WithError(err).WithFields(logrus.Fields{
			"mint_url":    mintURL,
			"mint_amount": mintAmount,
		}).Error("Failed to create sendable proofs")
		return "", fmt.Errorf("failed to create sendable proofs: %w", err)
	}

	// Create a Cashu token from the proofs
	token, err := cashu.NewTokenV4(proofs, mintURL, cashu.Sat, true)
	if err != nil {
		mc.logger.WithError(err).Error("Failed to create Cashu token")
		return "", fmt.Errorf("failed to create Cashu token: %w", err)
	}

	// Serialize the token to string
	tokenString, err := token.Serialize()
	if err != nil {
		mc.logger.WithError(err).Error("Failed to serialize token")
		return "", fmt.Errorf("failed to serialize token: %w", err)
	}

	mc.logger.WithFields(logrus.Fields{
		"mint_url":    mintURL,
		"quote_id":    quoteID,
		"mint_amount": mintAmount,
	}).Debug("Successfully minted and serialized tokens")

	return tokenString, nil
}

// IsMintReachable checks if a mint is reachable using gonuts
func (mc *MintClient) IsMintReachable(mintURL string) bool {
	// Try to request a small mint quote to check if mint is reachable
	_, err := mc.wallet.RequestMint(1, mintURL)
	if err != nil {
		mc.logger.WithError(err).WithField("mint_url", mintURL).Debug("Mint not reachable")
		return false
	}

	mc.logger.WithField("mint_url", mintURL).Debug("Mint is reachable")
	return true
}

// ValidateToken validates a Cashu token using gonuts
func (mc *MintClient) ValidateToken(tokenString string) error {
	token, err := cashu.DecodeToken(tokenString)
	if err != nil {
		return fmt.Errorf("invalid token format: %w", err)
	}

	// Basic validation - check if token has proofs
	if len(token.Proofs()) == 0 {
		return fmt.Errorf("token has no proofs")
	}

	return nil
}

// GetMintURL returns the current mint URL
func (mc *MintClient) GetMintURL() string {
	return mc.mintURL
}

// Close closes the wallet and releases resources
func (mc *MintClient) Close() error {
	mc.logger.Debug("Mint client closed")
	return nil
}
