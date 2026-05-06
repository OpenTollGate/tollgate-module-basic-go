package upstream_session_manager

import (
	"fmt"
	"os"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/sirupsen/logrus"
)

const tokenRecoveryFile = "/etc/tollgate/tokens-to-recover.txt"

// recoverFailedPaymentToken attempts to recover a failed payment token
// First tries to receive it back via merchant.WalletReceive(), then saves to file as fallback
func recoverFailedPaymentToken(merchant merchant.MerchantInterface, token, mintURL string, paymentErr error) {
	logger.WithFields(logrus.Fields{
		"mint":  mintURL,
		"error": paymentErr,
	}).Warn("Payment failed, attempting token recovery")

	// Try to receive the token back into our wallet
	_, err := merchant.Fund(token)
	if err == nil {
		logger.WithFields(logrus.Fields{
			"mint": mintURL,
		}).Info("✅ Token successfully recovered back to wallet")
		return
	}

	// If merchant.WalletReceive() failed, save token to file for manual recovery
	logger.WithFields(logrus.Fields{
		"mint":          mintURL,
		"fund_error":    err,
		"payment_error": paymentErr,
	}).Warn("Failed to auto-recover token, saving to recovery file")

	saveTokenForRecovery(token, mintURL, paymentErr)
}

// saveTokenForRecovery saves a failed payment token to a file for manual recovery
func saveTokenForRecovery(token, mintURL string, originalErr error) {
	// Ensure directory exists
	dir := "/etc/tollgate"
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.WithError(err).Error("Failed to create token recovery directory")
		return
	}

	// Open file in append mode
	f, err := os.OpenFile(tokenRecoveryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.WithError(err).Error("Failed to open token recovery file")
		return
	}
	defer f.Close()

	// Write token with timestamp and error
	timestamp := time.Now().Format(time.RFC3339)
	line := fmt.Sprintf("%s | %s | %s | %v\n", timestamp, mintURL, token, originalErr)

	if _, err := f.WriteString(line); err != nil {
		logger.WithError(err).Error("Failed to write token to recovery file")
		return
	}

	logger.WithFields(logrus.Fields{
		"file": tokenRecoveryFile,
		"mint": mintURL,
	}).Warn("💾 Token saved to recovery file for manual processing")
}
