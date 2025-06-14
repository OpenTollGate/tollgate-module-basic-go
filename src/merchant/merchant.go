package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
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

// PurchaseSession processes a payment event and returns either a session event or a notice event
func (m *Merchant) PurchaseSession(paymentEvent nostr.Event) (*nostr.Event, error) {
	// Extract payment token from payment event
	paymentToken, err := m.extractPaymentToken(paymentEvent)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "invalid-payment-token",
			fmt.Sprintf("Failed to extract payment token: %v", err), paymentEvent.PubKey)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to extract payment token and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Extract device identifier from payment event
	deviceIdentifier, err := m.extractDeviceIdentifier(paymentEvent)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "invalid-device-identifier",
			fmt.Sprintf("Failed to extract device identifier: %v", err), paymentEvent.PubKey)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to extract device identifier and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Validate MAC address
	if !utils.ValidateMACAddress(deviceIdentifier) {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "invalid-mac-address",
			fmt.Sprintf("Invalid MAC address: %s", deviceIdentifier), paymentEvent.PubKey)
		if noticeErr != nil {
			return nil, fmt.Errorf("invalid MAC address and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Process payment
	paymentCashuToken, err := cashu.DecodeToken(paymentToken)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-error-invalid-token",
			fmt.Sprintf("Invalid cashu token: %v", err), paymentEvent.PubKey)
		if noticeErr != nil {
			return nil, fmt.Errorf("invalid cashu token and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	amountAfterSwap, err := m.tollwallet.Receive(paymentCashuToken)
	if err != nil {
		var errorCode string
		var errorMessage string

		// Check for specific error types
		if strings.Contains(err.Error(), "Token already spent") {
			errorCode = "payment-error-token-spent"
			errorMessage = "Token has already been spent"
		} else {
			errorCode = "payment-processing-failed"
			errorMessage = fmt.Sprintf("Payment processing failed: %v", err)
		}

		noticeEvent, noticeErr := m.CreateNoticeEvent("error", errorCode, errorMessage, paymentEvent.PubKey)
		if noticeErr != nil {
			return nil, fmt.Errorf("payment processing failed and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	log.Printf("Amount after swap: %d", amountAfterSwap)

	// Calculate allotment using the configured metric and mint-specific pricing
	mintURL := paymentCashuToken.Mint()
	allotment, err := m.calculateAllotment(amountAfterSwap, mintURL)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "allotment-calculation-failed",
			fmt.Sprintf("Failed to calculate allotment: %v", err), paymentEvent.PubKey)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to calculate allotment and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
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
		sessionEvent, err = m.extendSessionEvent(existingSession, allotment)
		if err != nil {
			noticeEvent, noticeErr := m.CreateNoticeEvent("error", "session-extension-failed",
				fmt.Sprintf("Failed to extend session: %v", err), paymentEvent.PubKey)
			if noticeErr != nil {
				return nil, fmt.Errorf("failed to extend session and failed to create notice: %w", noticeErr)
			}
			return noticeEvent, nil
		}
		log.Printf("Extended session for customer %s", customerPubkey)
	} else {
		// Create new session
		sessionEvent, err = m.createSessionEvent(paymentEvent, allotment)
		if err != nil {
			noticeEvent, noticeErr := m.CreateNoticeEvent("error", "session-creation-failed",
				fmt.Sprintf("Failed to create session: %v", err), paymentEvent.PubKey)
			if noticeErr != nil {
				return nil, fmt.Errorf("failed to create session and failed to create notice: %w", noticeErr)
			}
			return noticeEvent, nil
		}
		log.Printf("Created new session for customer %s", customerPubkey)
	}

	// Update valve with session information
	err = valve.OpenGateForSession(*sessionEvent, m.config)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "gate-opening-failed",
			fmt.Sprintf("Failed to open gate for session: %v", err), paymentEvent.PubKey)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to open gate for session and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Publish session event to local pool only (for privacy)
	err = m.publishLocal(sessionEvent)
	if err != nil {
		log.Printf("Warning: failed to publish session event to local pool: %v", err)
	}

	return sessionEvent, nil
}

func (m *Merchant) GetAdvertisement() string {
	return m.advertisement
}

func CreateAdvertisement(config *config_manager.Config) (string, error) {
	advertisementEvent := nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", config.Metric},
			{"step_size", fmt.Sprintf("%d", config.StepSize)},
			{"tips", "1", "2", "3", "4"},
		},
		Content: "",
	}

	// Create a map of prices mints and their fees
	for _, mintConfig := range config.AcceptedMints {
		advertisementEvent.Tags = append(advertisementEvent.Tags, nostr.Tag{
			"price_per_step",
			"cashu",
			fmt.Sprintf("%d", mintConfig.PricePerStep),
			mintConfig.PriceUnit,
			mintConfig.URL,
			fmt.Sprintf("%d", mintConfig.MinPurchaseSteps),
		})
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

// calculateAllotment calculates allotment using the configured metric and mint-specific pricing
func (m *Merchant) calculateAllotment(amountSats uint64, mintURL string) (uint64, error) {
	// Find the mint configuration for this mint
	var mintConfig *config_manager.MintConfig
	for _, mint := range m.config.AcceptedMints {
		if mint.URL == mintURL {
			mintConfig = &mint
			break
		}
	}

	if mintConfig == nil {
		return 0, fmt.Errorf("mint configuration not found for URL: %s", mintURL)
	}

	steps := amountSats / mintConfig.PricePerStep

	// Check if payment meets minimum purchase requirement
	if steps < mintConfig.MinPurchaseSteps {
		return 0, fmt.Errorf("payment only covers %d steps, but minimum purchase is %d steps", steps, mintConfig.MinPurchaseSteps)
	}

	switch m.config.Metric {
	case "milliseconds":
		return m.calculateAllotmentMs(steps, mintConfig)
	// case "bytes":
	//     return m.calculateAllotmentBytes(steps, mintConfig)
	default:
		return 0, fmt.Errorf("unsupported metric: %s", m.config.Metric)
	}
}

// calculateAllotmentMs calculates allotment in milliseconds from steps
func (m *Merchant) calculateAllotmentMs(steps uint64, mintConfig *config_manager.MintConfig) (uint64, error) {
	// Convert steps to milliseconds using configured step size
	totalMs := steps * m.config.StepSize

	log.Printf("Converting %d steps to %d ms using step size %d",
		steps, totalMs, m.config.StepSize)

	return totalMs, nil
}

// calculateAllotmentBytes calculates allotment in bytes from payment amount using mint-specific pricing
// func (m *Merchant) calculateAllotmentBytes(amountSats uint64, mintURL string) (uint64, error) {
//     // Find the mint configuration for this mint
//     var mintConfig *MintConfig
//     for _, mint := range m.config.AcceptedMints {
//         if mint.URL == mintURL {
//             mintConfig = &mint
//             break
//         }
//     }
//
//     if mintConfig == nil {
//         return 0, fmt.Errorf("mint configuration not found for URL: %s", mintURL)
//     }
//
//     // Calculate steps from payment amount using mint-specific pricing
//     allottedSteps := amountSats / mintConfig.PricePerStep
//     if allottedSteps < 1 {
//         allottedSteps = 1 // Minimum 1 step
//     }
//
//     // Convert steps to bytes using configured step size
//     totalBytes := allottedSteps * m.config.StepSize
//
//     log.Printf("Calculated %d steps (%d bytes) from %d sats at %d sats per step",
//         allottedSteps, totalBytes, amountSats, mintConfig.PricePerStep)
//
//     return totalBytes, nil
// }

// getLatestSession queries the local relay pool for the most recent session by customer pubkey
func (m *Merchant) getLatestSession(customerPubkey string) (*nostr.Event, error) {
	log.Printf("Querying for existing session for customer %s", customerPubkey)

	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(m.config.TollgatePrivateKey)
	if err != nil {
		log.Printf("Error getting public key from private key: %v", err)
		return nil, err
	}

	// Create filter to find session events for this customer created by this tollgate
	filters := []nostr.Filter{
		{
			Kinds:   []int{1022},              // Session events
			Authors: []string{tollgatePubkey}, // Only sessions created by this tollgate
			Tags: map[string][]string{
				"p": {customerPubkey}, // Customer pubkey tag
			},
			Limit: 50, // Get recent sessions to find the latest one
		},
	}

	log.Printf("DEBUG: Querying with filter - Kinds: %v, Authors: %v, Tags: %v",
		filters[0].Kinds, filters[0].Authors, filters[0].Tags)

	// Query the local relay pool
	events, err := m.configManager.GetLocalPoolEvents(filters)
	if err != nil {
		log.Printf("Error querying local pool for sessions: %v", err)
		return nil, err
	}

	log.Printf("DEBUG: Found %d events from local pool", len(events))
	for i, event := range events {
		log.Printf("DEBUG: Event %d - ID: %s, Kind: %d, Author: %s, CreatedAt: %d",
			i, event.ID, event.Kind, event.PubKey, event.CreatedAt)
	}

	if len(events) == 0 {
		log.Printf("No existing sessions found for customer %s", customerPubkey)
		return nil, nil
	}

	// Find the most recent session event
	var latestSession *nostr.Event
	for _, event := range events {
		if latestSession == nil || event.CreatedAt > latestSession.CreatedAt {
			latestSession = event
		}
	}

	if latestSession != nil {
		log.Printf("Found latest session for customer %s: event ID %s, created at %d",
			customerPubkey, latestSession.ID, latestSession.CreatedAt)

		// Check if the session is still active (hasn't expired)
		if m.isSessionActive(latestSession) {
			return latestSession, nil
		} else {
			log.Printf("Latest session for customer %s has expired", customerPubkey)
			return nil, nil
		}
	}

	return nil, nil
}

// isSessionActive checks if a session event is still active (not expired)
func (m *Merchant) isSessionActive(sessionEvent *nostr.Event) bool {
	// Extract allotment from session
	allotmentMs, err := m.extractAllotment(sessionEvent)
	if err != nil {
		log.Printf("Failed to extract allotment from session: %v", err)
		return false
	}

	// Calculate session expiration time
	sessionCreatedAt := time.Unix(int64(sessionEvent.CreatedAt), 0)
	sessionExpiresAt := sessionCreatedAt.Add(time.Duration(allotmentMs) * time.Millisecond)

	// Check if session is still active
	isActive := time.Now().Before(sessionExpiresAt)

	if isActive {
		timeLeft := time.Until(sessionExpiresAt)
		log.Printf("Session is active, %v remaining", timeLeft)
	} else {
		timeExpired := time.Since(sessionExpiresAt)
		log.Printf("Session expired %v ago", timeExpired)
	}

	return isActive
}

// createSessionEvent creates a new session event
func (m *Merchant) createSessionEvent(paymentEvent nostr.Event, allotment uint64) (*nostr.Event, error) {
	customerPubkey := paymentEvent.PubKey
	deviceIdentifier, err := m.extractDeviceIdentifier(paymentEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to extract device identifier: %w", err)
	}

	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(m.config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	sessionEvent := &nostr.Event{
		Kind:      1022,
		PubKey:    tollgatePubkey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", customerPubkey},
			{"device-identifier", "mac", deviceIdentifier},
			{"allotment", fmt.Sprintf("%d", allotment)},
			{"metric", m.config.Metric},
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
func (m *Merchant) extendSessionEvent(existingSession *nostr.Event, additionalAllotment uint64) (*nostr.Event, error) {
	// Extract existing allotment from the session
	existingAllotment, err := m.extractAllotment(existingSession)
	if err != nil {
		return nil, fmt.Errorf("failed to extract existing allotment: %w", err)
	}

	// Calculate leftover allotment based on metric type
	var leftoverAllotment uint64 = 0
	if m.config.Metric == "milliseconds" {
		// For time-based metrics, calculate how much time has passed
		sessionCreatedAt := time.Unix(int64(existingSession.CreatedAt), 0)
		timePassed := time.Since(sessionCreatedAt)
		timePassedInMetric := uint64(timePassed.Milliseconds())

		if existingAllotment > timePassedInMetric {
			leftoverAllotment = existingAllotment - timePassedInMetric
		}

		log.Printf("Session extension: existing=%d %s, passed=%d %s, leftover=%d %s, additional=%d %s",
			existingAllotment, m.config.Metric, timePassedInMetric, m.config.Metric,
			leftoverAllotment, m.config.Metric, additionalAllotment, m.config.Metric)
	} else {
		// For non-time metrics (like bytes), keep the full existing allotment
		leftoverAllotment = existingAllotment
		log.Printf("Session extension: existing=%d %s, leftover=%d %s (no decay), additional=%d %s",
			existingAllotment, m.config.Metric, leftoverAllotment, m.config.Metric,
			additionalAllotment, m.config.Metric)
	}

	// Calculate new total allotment
	newTotalAllotment := leftoverAllotment + additionalAllotment

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

	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(m.config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	// Create new session event with extended duration
	sessionEvent := &nostr.Event{
		Kind:      1022,
		PubKey:    tollgatePubkey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", customerPubkey},
			{"device-identifier", "mac", deviceIdentifier},
			{"allotment", fmt.Sprintf("%d", newTotalAllotment)},
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

// publishLocal publishes a nostr event to the local relay pool
func (m *Merchant) publishLocal(event *nostr.Event) error {
	log.Printf("Publishing event kind=%d id=%s to local pool", event.Kind, event.ID)

	err := m.configManager.PublishToLocalPool(*event)
	if err != nil {
		log.Printf("Failed to publish event to local pool: %v", err)
		return err
	}

	log.Printf("Successfully published event %s to local pool", event.ID)
	return nil
}

// publishPublic publishes a nostr event to public relay pools
func (m *Merchant) publishPublic(event *nostr.Event) error {
	log.Printf("Publishing event kind=%d id=%s to public pools", event.Kind, event.ID)

	for _, relayURL := range m.config.Relays {
		relay, err := m.configManager.GetPublicPool().EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Failed to connect to public relay %s: %v", relayURL, err)
			continue
		}

		err = relay.Publish(m.configManager.GetPublicPool().Context, *event)
		if err != nil {
			log.Printf("Failed to publish event to public relay %s: %v", relayURL, err)
		} else {
			log.Printf("Successfully published event %s to public relay %s", event.ID, relayURL)
		}
	}

	return nil
}

// CreateNoticeEvent creates a notice event for error communication
func (m *Merchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(m.config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	noticeEvent := &nostr.Event{
		Kind:      21023,
		PubKey:    tollgatePubkey,
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
	err = noticeEvent.Sign(m.config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign notice event: %w", err)
	}

	return noticeEvent, nil
}
