package tollwallet

import (
	"fmt"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lightning"
	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/Origami74/gonuts-tollgate/wallet"
)

type TollWallet struct {
	wallet                     *wallet.Wallet
	acceptedMints              []string
	allowAndSwapUntrustedMints bool
}

func New(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool) (*TollWallet, error) {
	logger.WithFields(map[string]interface{}{
		"path":  walletPath,
		"mints": acceptedMints,
	}).Info("Initializing wallet")

	if len(acceptedMints) < 1 {
		return nil, fmt.Errorf("No mints provided. Wallet requires at least 1 accepted mint, none were provided")
	}

	config := wallet.Config{WalletPath: walletPath, CurrentMintURL: acceptedMints[0]}
	logger.WithField("mint", acceptedMints[0]).Debug("Loading wallet")

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

	if !contains(w.acceptedMints, mint) {
		if !w.allowAndSwapUntrustedMints {
			err := fmt.Errorf("Token rejected. Token for mint %s is not accepted and wallet does not allow swapping of untrusted mints.", mint)
			logger.WithField("mint", mint).Warn("Token rejected: untrusted mint")
			return 0, err
		}
		swapToTrusted = true
		logger.WithField("mint", mint).Info("Swapping untrusted mint token to trusted mint")
	}

	amountAfterSwap, err := w.wallet.Receive(token, swapToTrusted)
	if err != nil {
		logger.WithError(err).Error("wallet.Receive failed")
		return 0, err
	}
	logger.WithField("amount", amountAfterSwap).Info("Received token")
	return amountAfterSwap, err
}

func (w *TollWallet) Send(amount uint64, mintUrl string, includeFees bool) (cashu.Token, error) {
	logger.WithFields(map[string]interface{}{
		"amount":       amount,
		"mint":         mintUrl,
		"includeFees":  includeFees,
	}).Info("Sending tokens")

	proofs, err := w.wallet.Send(amount, mintUrl, includeFees)
	if err != nil {
		logger.WithError(err).Error("wallet.Send failed")
		return nil, fmt.Errorf("Failed to send %d to %s: %w", amount, mintUrl, err)
	}

	if len(proofs) == 0 {
		logger.Error("wallet.Send returned empty proofs array")
		return nil, fmt.Errorf("wallet.Send returned empty proofs array for %d sats from %s", amount, mintUrl)
	}

	totalProofAmount := uint64(0)
	for i, proof := range proofs {
		totalProofAmount += proof.Amount
		logger.WithFields(map[string]interface{}{
			"index":  i,
			"amount": proof.Amount,
		}).Debug("Proof detail")
	}
	logger.WithFields(map[string]interface{}{
		"total":     totalProofAmount,
		"requested": amount,
	}).Debug("Proof totals")

	token, err := cashu.NewTokenV4(proofs, mintUrl, cashu.Sat, true)
	if err != nil {
		logger.WithError(err).Error("NewTokenV4 failed")
		return nil, fmt.Errorf("Failed to create token: %w", err)
	}

	return token, nil
}

func (w *TollWallet) Drain(mintUrl string) (cashu.Token, uint64, error) {
	balance := w.GetBalanceByMint(mintUrl)
	if balance == 0 {
		logger.WithField("mint", mintUrl).Info("No balance to drain")
		return nil, 0, fmt.Errorf("no balance available for mint %s", mintUrl)
	}

	logger.WithFields(map[string]interface{}{
		"mint":    mintUrl,
		"balance": balance,
	}).Info("Draining mint")

	proofs, err := w.wallet.Send(balance, mintUrl, false)
	if err != nil {
		logger.WithError(err).Error("Drain wallet.Send failed")
		return nil, 0, fmt.Errorf("Failed to drain %d from %s: %w", balance, mintUrl, err)
	}

	if len(proofs) == 0 {
		logger.Error("Drain: wallet.Send returned empty proofs array")
		return nil, 0, fmt.Errorf("wallet.Send returned empty proofs array for %d sats from %s", balance, mintUrl)
	}

	totalProofAmount := uint64(0)
	for i, proof := range proofs {
		totalProofAmount += proof.Amount
		logger.WithFields(map[string]interface{}{
			"index":  i,
			"amount": proof.Amount,
		}).Debug("Drain proof detail")
	}

	token, err := cashu.NewTokenV4(proofs, mintUrl, cashu.Sat, true)
	if err != nil {
		logger.WithError(err).Error("Drain NewTokenV4 failed")
		return nil, 0, fmt.Errorf("Failed to create token: %w", err)
	}

	logger.WithField("total", totalProofAmount).Info("Drain complete")
	return token, totalProofAmount, nil
}

func (w *TollWallet) SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, MaxOverpaymentAbsolute uint64) (string, error) {
	options := wallet.SendOptions{
		IncludeFees:            true,
		AllowOverpayment:       true,
		MaxOverpaymentPercent:  uint(maxOverpaymentPercent),
		MaxOverpaymentAbsolute: MaxOverpaymentAbsolute,
	}

	result, err := w.wallet.SendWithOptions(amount, mintUrl, options)
	if err != nil {
		return "", fmt.Errorf("failed to send with overpayment to %s: %w", mintUrl, err)
	}

	token, err := cashu.NewTokenV4(result.Proofs, mintUrl, cashu.Sat, true)
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}

	tokenString, err := token.Serialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize token: %w", err)
	}

	logger.WithFields(map[string]interface{}{
		"requested":    result.RequestedAmount,
		"overpayment":  result.Overpayment,
		"tolerance_pct": maxOverpaymentPercent,
	}).Info("Send with overpayment complete")

	return tokenString, nil
}

func ParseToken(token string) (cashu.Token, error) {
	return cashu.DecodeToken(token)
}

func contains(slice []string, str string) bool {
	for _, item := range slice {
		if item == str {
			return true
		}
	}
	return false
}

func (w *TollWallet) GetBalance() uint64 {
	return w.wallet.GetBalance()
}

func (w *TollWallet) GetBalanceByMint(mintUrl string) uint64 {
	balanceByMints := w.wallet.GetBalanceByMints()
	if balance, exists := balanceByMints[mintUrl]; exists {
		return balance
	}
	return 0
}

func (w *TollWallet) GetAllMintBalances() map[string]uint64 {
	return w.wallet.GetBalanceByMints()
}

func (w *TollWallet) MeltToLightning(mintUrl string, targetAmount uint64, maxCost uint64, lnurl string) error {
	logger.WithFields(map[string]interface{}{
		"amount": targetAmount,
		"lnurl":  lnurl,
		"max":    maxCost,
	}).Info("Starting melt to lightning")

	currentAmount := targetAmount
	maxAttempts := 10

	var meltError error

	for attempts := 0; attempts < maxAttempts; attempts++ {
		logger.WithFields(map[string]interface{}{
			"attempt": attempts + 1,
			"amount":  currentAmount,
		}).Debug("Trying to melt")

		invoice, err := lightning.GetInvoiceFromLightningAddress(lnurl, currentAmount)
		if err != nil {
			logger.WithError(err).Debug("Error getting invoice")
			meltError = err
			continue
		}

		meltQuote, meltQuoteErr := w.wallet.RequestMeltQuote(invoice, mintUrl)
		if meltQuoteErr != nil {
			logger.WithError(meltQuoteErr).Debug("Error requesting melt quote")
			meltError = meltQuoteErr
			continue
		}

		if meltQuote.Amount > maxCost {
			logger.WithFields(map[string]interface{}{
				"cost": meltQuote.Amount,
				"max":  maxCost,
			}).Debug("Melt cost too high, reducing by 5%")
			meltError = fmt.Errorf("melt cost exceeds maximum allowed: %d > %d", meltQuote.Amount, maxCost)
			currentAmount = currentAmount - (currentAmount * 5 / 100)
			continue
		}

		meltResult, meltErr := w.wallet.Melt(meltQuote.Quote)
		if meltErr != nil {
			logger.WithError(meltErr).Debug("Error melting quote")
			meltError = meltErr
			continue
		}

		logger.WithFields(map[string]interface{}{
			"amount": currentAmount,
			"fees":   meltResult.FeeReserve,
		}).Info("Melt successful")
		return nil
	}

	return fmt.Errorf("failed to melt after %d attempts: %w", maxAttempts, meltError)
}
