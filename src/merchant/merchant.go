package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/elnosh/gonuts/cashu"
	"github.com/nbd-wtf/go-nostr"
)

// Merchant represents the financial decision maker for the tollgate
type Merchant struct {
	config        *config_manager.Config
	configManager *config_manager.ConfigManager
	tollwallet    tollwallet.TollWallet
	advertisement string
}

func New(configManager *config_manager.ConfigManager) (*Merchant, error) {
	log.Printf("=== Merchant Initializing ===")

	config, err := configManager.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Extract mint URLs from MintConfig
	mintURLs := make([]string, len(config.AcceptedMints))
	for i, mint := range config.AcceptedMints {
		mintURLs[i] = mint.URL
	}

	log.Printf("Setting up wallet...")
	tollwallet, walletErr := tollwallet.New("/etc/tollgate", mintURLs, false)

	if walletErr != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", walletErr)
	}
	balance := tollwallet.GetBalance()

	// Set advertisement
	var advertisementStr string
	advertisementStr, err = CreateAdvertisement(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement: %w", err)
	}

	log.Printf("Accepted Mints: %v", config.AcceptedMints)
	log.Printf("Wallet Balance: %d", balance)
	log.Printf("Advertisement: %s", advertisementStr)
	log.Printf("=== Merchant ready ===")

	return &Merchant{
		config:        config,
		configManager: configManager,
		tollwallet:    *tollwallet,
		advertisement: advertisementStr,
	}, nil
}

func (m *Merchant) StartPayoutRoutine() {
	log.Printf("Starting payout routine")

	// Create timer for each mint
	for _, mint := range m.config.AcceptedMints {
		go func(mintConfig config_manager.MintConfig) {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()

			for range ticker.C {
				m.processPayout(mintConfig)
			}
		}(mint)
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

// PurchaseSession processes a payment event and returns a session event
func (m *Merchant) PurchaseSession(paymentEvent nostr.Event) (*nostr.Event, error) {
	// Extract payment token from payment event
	paymentToken, err := m.extractPaymentToken(paymentEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to extract payment token: %w", err)
	}

	// Extract device identifier from payment event
	deviceIdentifier, err := m.extractDeviceIdentifier(paymentEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to extract device identifier: %w", err)
	}

	// Validate MAC address
	if !utils.ValidateMACAddress(deviceIdentifier) {
		return nil, fmt.Errorf("invalid MAC address: %s", deviceIdentifier)
	}

	// Process payment
	paymentCashuToken, err := cashu.DecodeToken(paymentToken)
	if err != nil {
		return nil, fmt.Errorf("invalid cashu token: %w", err)
	}

	amountAfterSwap, err := m.tollwallet.Receive(paymentCashuToken)
	if err != nil {
		return nil, fmt.Errorf("payment processing failed: %w", err)
	}

	log.Printf("Amount after swap: %d", amountAfterSwap)

	// Calculate allotment in milliseconds from payment amount
	allotmentMs, err := m.calculateAllotmentMs(amountAfterSwap)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate allotment: %w", err)
	}

	// Check for existing session
	customerPubkey := paymentEvent.PubKey
	existingSession, err := m.getLatestSession(customerPubkey)
	if err != nil {
		log.Printf("Warning: failed to query existing session: %v", err)
	}

	var sessionEvent *nostr.Event
	if existingSession != nil {
		// Extend existing session
		sessionEvent, err = m.extendSessionEvent(existingSession, allotmentMs)
		if err != nil {
			return nil, fmt.Errorf("failed to extend session: %w", err)
		}
		log.Printf("Extended session for customer %s", customerPubkey)
	} else {
		// Create new session
		sessionEvent, err = m.createSessionEvent(paymentEvent, allotmentMs)
		if err != nil {
			return nil, fmt.Errorf("failed to create session: %w", err)
		}
		log.Printf("Created new session for customer %s", customerPubkey)
	}

	// Publish session event to relay pool
	err = m.publishEvent(sessionEvent)
	if err != nil {
		log.Printf("Warning: failed to publish session event: %v", err)
	}

	// Update valve with session information
	err = valve.OpenGateForSession(*sessionEvent, m.config)
	if err != nil {
		return nil, fmt.Errorf("failed to open gate for session: %w", err)
	}

	return sessionEvent, nil
}

// Legacy method for backwards compatibility (will be removed)
func (m *Merchant) PurchaseSessionLegacy(paymentToken string, macAddress string) (PurchaseSessionResult, error) {
	valid := utils.ValidateMACAddress(macAddress)

	if !valid {
		return PurchaseSessionResult{
			Status:      "rejected",
			Description: fmt.Sprintf("%s is not a valid MAC address", macAddress),
		}, nil
	}

	paymentCashuToken, err := cashu.DecodeToken(paymentToken)
	if err != nil {
		return PurchaseSessionResult{
			Status:      "rejected",
			Description: "Invalid cashu token",
		}, nil
	}

	amountAfterSwap, err := m.tollwallet.Receive(paymentCashuToken)
	if err != nil {
		log.Printf("Error Processing payment. %s", err)
		return PurchaseSessionResult{
			Status:      "error",
			Description: fmt.Sprintf("Error Processing payment"),
		}, nil
	}

	log.Printf("Amount after swap: %d", amountAfterSwap)

	var allottedMinutes = uint64(amountAfterSwap / m.config.PricePerMinute)
	if allottedMinutes < 1 {
		allottedMinutes = 1 // Minimum 1 minute
	}

	durationSeconds := int64(allottedMinutes * 60)
	log.Printf("Calculated minutes: %d (from value %d)", allottedMinutes, amountAfterSwap)

	err = valve.OpenGate(macAddress, durationSeconds)
	if err != nil {
		log.Printf("Error opening gate for MAC %s: %v", macAddress, err)
		return PurchaseSessionResult{
			Status:      "error",
			Description: fmt.Sprintf("Error while opening gate for %s", macAddress),
		}, nil
	}

	log.Printf("Access granted to %s for %d minutes", macAddress, allottedMinutes)

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
		log.Printf("TODO: include min payment (%d) for %s in future", minPayment, mint)
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

// extractPaymentToken extracts the payment token from a payment event
func (m *Merchant) extractPaymentToken(paymentEvent nostr.Event) (string, error) {
	for _, tag := range paymentEvent.Tags {
		if len(tag) >= 2 && tag[0] == "payment" {
			return tag[1], nil
		}
	}
	return "", fmt.Errorf("no payment tag found in event")
}

// extractDeviceIdentifier extracts the device identifier (MAC address) from a payment event
func (m *Merchant) extractDeviceIdentifier(paymentEvent nostr.Event) (string, error) {
	for _, tag := range paymentEvent.Tags {
		if len(tag) >= 3 && tag[0] == "device-identifier" {
			return tag[2], nil // Return the actual identifier value
		}
	}
	return "", fmt.Errorf("no device-identifier tag found in event")
}

// getStepSizeMs returns the step size in milliseconds from the merchant configuration
func (m *Merchant) getStepSizeMs() uint64 {
	// Parse the advertisement event to get step_size
	// For now, default to 60000ms (1 minute) as defined in CreateAdvertisement
	return 60000
}

// calculateAllotmentMs calculates allotment in milliseconds from payment amount
func (m *Merchant) calculateAllotmentMs(amountSats uint64) (uint64, error) {
	// Calculate minutes from payment amount
	allottedMinutes := amountSats / m.config.PricePerMinute
	if allottedMinutes < 1 {
		allottedMinutes = 1 // Minimum 1 minute
	}

	// Convert minutes to milliseconds
	totalMs := allottedMinutes * 60000 // Total milliseconds purchased

	return totalMs, nil
}

// getLatestSession queries the relay pool for the most recent session by customer pubkey
func (m *Merchant) getLatestSession(customerPubkey string) (*nostr.Event, error) {
	// For now, return nil to indicate no existing session
	// In a full implementation, this would query the relay pool for existing sessions
	log.Printf("Querying for existing session for customer %s", customerPubkey)
	return nil, nil
}

// createSessionEvent creates a new session event
func (m *Merchant) createSessionEvent(paymentEvent nostr.Event, allotmentMs uint64) (*nostr.Event, error) {
	customerPubkey := paymentEvent.PubKey
	deviceIdentifier, err := m.extractDeviceIdentifier(paymentEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to extract device identifier: %w", err)
	}

	sessionEvent := &nostr.Event{
		Kind:      21022,
		PubKey:    m.config.TollgatePrivateKey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", customerPubkey},
			{"device-identifier", "mac", deviceIdentifier},
			{"allotment", fmt.Sprintf("%d", allotmentMs)},
			{"metric", "milliseconds"},
		},
		Content: "",
	}

	// Sign with tollgate private key
	err = sessionEvent.Sign(m.config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign session event: %w", err)
	}

	return sessionEvent, nil
}

// extendSessionEvent creates a new session event with extended duration
func (m *Merchant) extendSessionEvent(existingSession *nostr.Event, additionalMs uint64) (*nostr.Event, error) {
	// Extract existing allotment from the session
	existingAllotmentMs, err := m.extractAllotment(existingSession)
	if err != nil {
		return nil, fmt.Errorf("failed to extract existing allotment: %w", err)
	}

	// Calculate how much time has passed since session creation
	sessionCreatedAt := time.Unix(int64(existingSession.CreatedAt), 0)
	timePassed := time.Since(sessionCreatedAt)
	timePassedMs := uint64(timePassed.Milliseconds())

	// Calculate leftover time
	leftoverMs := uint64(0)
	if existingAllotmentMs > timePassedMs {
		leftoverMs = existingAllotmentMs - timePassedMs
	}

	log.Printf("Session extension: existing=%d ms, passed=%d ms, leftover=%d ms, additional=%d ms",
		existingAllotmentMs, timePassedMs, leftoverMs, additionalMs)

	// Calculate new total allotment
	newTotalMs := leftoverMs + additionalMs

	// Extract customer and device info from existing session
	customerPubkey := ""
	deviceIdentifier := ""

	for _, tag := range existingSession.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			customerPubkey = tag[1]
		}
		if len(tag) >= 3 && tag[0] == "device-identifier" {
			deviceIdentifier = tag[2]
		}
	}

	if customerPubkey == "" || deviceIdentifier == "" {
		return nil, fmt.Errorf("failed to extract customer or device info from existing session")
	}

	// Create new session event with extended duration
	sessionEvent := &nostr.Event{
		Kind:      21022,
		PubKey:    m.config.TollgatePrivateKey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", customerPubkey},
			{"device-identifier", "mac", deviceIdentifier},
			{"allotment", fmt.Sprintf("%d", newTotalMs)},
			{"metric", "milliseconds"},
		},
		Content: "",
	}

	// Sign with tollgate private key
	err = sessionEvent.Sign(m.config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign extended session event: %w", err)
	}

	return sessionEvent, nil
}

// extractAllotment extracts allotment from a session event
func (m *Merchant) extractAllotment(sessionEvent *nostr.Event) (uint64, error) {
	for _, tag := range sessionEvent.Tags {
		if len(tag) >= 2 && tag[0] == "allotment" {
			allotment, err := strconv.ParseUint(tag[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("failed to parse allotment: %w", err)
			}
			return allotment, nil
		}
	}
	return 0, fmt.Errorf("no allotment tag found in session event")
}

// publishEvent publishes a nostr event to the relay pool
func (m *Merchant) publishEvent(event *nostr.Event) error {
	// For now, just log the event publication
	// In a full implementation, this would publish to a local relay
	log.Printf("Publishing event kind=%d id=%s to relay pool", event.Kind, event.ID)
	return nil
}

// CreateNoticeEvent creates a notice event for error communication
func (m *Merchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	noticeEvent := &nostr.Event{
		Kind:      21023,
		PubKey:    m.config.TollgatePrivateKey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"level", level},
			{"code", code},
		},
		Content: message,
	}

	// Add customer pubkey if provided
	if customerPubkey != "" {
		noticeEvent.Tags = append(noticeEvent.Tags, nostr.Tag{"p", customerPubkey})
	}

	// Sign with tollgate private key
	err := noticeEvent.Sign(m.config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign notice event: %w", err)
	}

	return noticeEvent, nil
}
