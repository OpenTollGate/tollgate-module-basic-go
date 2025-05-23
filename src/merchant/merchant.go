package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/elnosh/gonuts/cashu"
	"github.com/nbd-wtf/go-nostr"
)

// TollWallet represents a Cashu wallet that can receive, swap, and send tokens
type Merchant struct {
	config        *config_manager.Config
	tollwallet    tollwallet.TollWallet
	advertisement string
}

func New(configManager *config_manager.ConfigManager) (*Merchant, error) {
	log.Printf("=== Merchant Initializing ===")

	config, _ := configManager.LoadConfig()

	// Extract mint URLs from MintConfig
	mintURLs := make([]string, len(config.AcceptedMints))
	for i, mint := range config.AcceptedMints {
		mintURLs[i] = mint.URL
	}

	log.Printf("Setting up wallet...")
	tollwallet, walletErr := tollwallet.New("/etc/tollgate", mintURLs, false)

	if walletErr != nil {
		log.Fatalf("Failed to create wallet: %v", walletErr)
		os.Exit(1)
	}
	balance := tollwallet.GetBalance()

	// Set advertisement
	var advertisementStr string
	advertisementStr, _ = CreateAdvertisement(config)

	log.Printf("Accepted Mints: %v", config.AcceptedMints)
	log.Printf("Wallet Balance: %d", balance)
	log.Printf("Advertisement: %s", advertisementStr)
	log.Printf("=== Merchant ready ===")

	return &Merchant{
		config:        config,
		tollwallet:    *tollwallet,
		advertisement: advertisementStr,
	}, nil
}

func (m *Merchant) StartPayoutRoutine() {
	log.Printf("Starting payout routine")

	// Create timer for each mint
	for _, mint := range m.config.AcceptedMints {
		go func() {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()

			for range ticker.C {
				m.processPayout(mint)
			}
		}()
	}

	log.Printf("Payout routine started")
}

// processPayout checks balances and processes payouts for each mint
func (m *Merchant) processPayout(mintConfig config_manager.MintConfig) {
	// Get current balance
	// Note: The current implementation only returns total balance, not per mint
	balance := m.tollwallet.GetBalanceByMint(mintConfig.URL)

	// Skip if balance is below minimum payout amount
	if balance < mintConfig.MinPayoutAmount {
		log.Printf("Skipping payout %s, Balance %d does not meet threshold of %d", mintConfig.URL, balance, mintConfig.MinPayoutAmount)
		return
	}

	// Get the amount we intend to payout to the owner.
	// The tolerancePaymentAmount is the max amount we're willing to spend on the transaction, most of which should come back as change.
	aimedPaymentAmount := balance - mintConfig.MinBalance

	for _, profitShare := range m.config.ProfitShare {
		aimedAmount := uint64(math.Round(float64(aimedPaymentAmount) * profitShare.Factor))
		m.PayoutShare(mintConfig, aimedAmount, profitShare.LightningAddress)
	}

	log.Printf("Payout completed for mint %s", mintConfig.URL)
}

func (m *Merchant) PayoutShare(mintConfig config_manager.MintConfig, aimedPaymentAmount uint64, lightningAddress string) {
	tolerancePaymentAmount := aimedPaymentAmount + (aimedPaymentAmount * mintConfig.BalanceTolerancePercent / 100)

	log.Printf("Processing payout for mint %s: aiming for %d sats with %d sats tolerance", mintConfig.URL, aimedPaymentAmount, tolerancePaymentAmount)

	maxCost := aimedPaymentAmount + tolerancePaymentAmount
	meltErr := m.tollwallet.MeltToLightning(mintConfig.URL, aimedPaymentAmount, maxCost, lightningAddress)

	// If melting fails try to return the money to the wallet
	if meltErr != nil {
		log.Printf("Error during payout for mint %s. Error melting to lightning. Skipping... %v", mintConfig.URL, meltErr)
		return
	}
}

type PurchaseSessionResult struct {
	Status      string
	Description string
}

func (m *Merchant) PurchaseSession(paymentToken string, macAddress string) (PurchaseSessionResult, error) {
	valid := utils.ValidateMACAddress(macAddress)

	if !valid {
		return PurchaseSessionResult{
			Status:      "rejected",
			Description: fmt.Sprintf("%s is not a valid MAC address", macAddress),
		}, nil
	}

	// TODO: prevent payment with les than step_size/price in sats (aka, fee > value)

	paymentCashuToken, err := cashu.DecodeToken(paymentToken)

	if err != nil {
		return PurchaseSessionResult{
			Status:      "rejected",
			Description: "Invalid cashu token",
		}, nil
	}
	amountAfterSwap, err := m.tollwallet.Receive(paymentCashuToken)

	// TODO: distinguish between rejection and errors
	if err != nil {
		fmt.Printf("Error Processing payment. %s", err)
		return PurchaseSessionResult{
			Status:      "error",
			Description: fmt.Sprintf("Error Processing payment"),
		}, nil
	}

	print(amountAfterSwap)

	// Calculate minutes based on the net value
	// TODO: Update frontend to show the correct duration after fees
	//       Already tested to verify that allottedMinutes is correct
	var allottedMinutes = uint64(amountAfterSwap / m.config.PricePerMinute)
	if allottedMinutes < 1 {
		allottedMinutes = 1 // Minimum 1 minute
	}

	// Convert to seconds for gate opening
	durationSeconds := int64(allottedMinutes * 60)

	fmt.Printf("Calculated minutes: %d (from value %d, minus fees %d)\n",
		allottedMinutes, amountAfterSwap)

	// Open gate for the specified duration using the valve module
	err = valve.OpenGate(macAddress, durationSeconds)

	if err != nil {
		log.Printf("Error opening gate for MAC %s: %v", macAddress, err)
		return PurchaseSessionResult{
			Status:      "error",
			Description: fmt.Sprintf("Error while opening gate for %s", macAddress),
		}, nil
	}

	// Check if bragging is enabled
	if m.config.Bragging.Enabled {
		// err = bragging.AnnounceSuccessfulPayment(m.config.ConfigManager, int64(amountAfterSwap), durationSeconds)
		// if err != nil {
		// 	log.Printf("Error while bragging: %v", err)
		// 	// Don't return error, continue with success
		// }
	}

	fmt.Printf("Access granted to %s for %d minutes\n", macAddress, allottedMinutes)

	return PurchaseSessionResult{
		Status:      "success",
		Description: "",
	}, nil
}

func (m *Merchant) GetAdvertisement() string {
	return m.advertisement
}

func CreateAdvertisement(config *config_manager.Config) (string, error) {
	// Create a map of accepted mints and their minimum payments
	mintMinPayments := make(map[string]uint64)
	for _, mintConfig := range config.AcceptedMints {
		mintFee, err := config_manager.GetMintFee(mintConfig.URL)
		if err != nil {
			log.Printf("Error getting mint fee for %s: %v", mintConfig.URL, err)
			continue
		}
		paymentAmount := uint64(config_manager.CalculateMinPayment(mintFee))
		mintMinPayments[mintConfig.URL] = paymentAmount
	}

	// Create the nostr event with the mintMinPayments map
	tags := nostr.Tags{
		{"metric", "milliseconds"},
		{"step_size", "60000"},
		{"price_per_step", fmt.Sprintf("%d", config.PricePerMinute), "sat"},
		{"tips", "1", "2", "3"},
	}

	// Create a separate tag for each accepted mint
	for mint, minPayment := range mintMinPayments {
		// TODO: include min payment in future - requires TIP-01 & frontend logic adjustment
		fmt.Printf("TODO: include min payment (%d) for %s in future\n", minPayment, mint)
		//tags = append(tags, nostr.Tag{"mint", mint, fmt.Sprintf("%d", minPayment)})
		tags = append(tags, nostr.Tag{"mint", mint})
	}

	advertisementEvent := nostr.Event{
		Kind:    21021,
		Tags:    tags,
		Content: "",
	}

	// Sign
	err := advertisementEvent.Sign(config.TollgatePrivateKey)
	if err != nil {
		return "", fmt.Errorf("Error signing advertisement event: %v", err)
	}

	// Convert to JSON string for storage
	detailsBytes, err := json.Marshal(advertisementEvent)
	if err != nil {
		return "", fmt.Errorf("Error marshaling advertisement event: %v", err)
	}

	return string(detailsBytes), nil
}
