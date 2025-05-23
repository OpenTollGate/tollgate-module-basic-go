package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

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

	log.Printf("Setting up wallet...")
	tollwallet, walletErr := tollwallet.New("/etc/tollgate", config.AcceptedMints, false)

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

	print("StartPayoutRoutine not implemented")
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
			Status:      "Sprintf",
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
	for _, mintURL := range config.AcceptedMints {
		mintFee, err := config_manager.GetMintFee(mintURL)
		if err != nil {
			log.Printf("Error getting mint fee for %s: %v", mintURL, err)
			continue
		}
		mintMinPayments[mintURL] = config_manager.CalculateMinPayment(mintFee)
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
