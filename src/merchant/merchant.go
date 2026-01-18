package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sync"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/nbd-wtf/go-nostr"
)

// CustomerSession represents an active session
type CustomerSession struct {
	MacAddress string
	StartTime  int64  // Unix timestamp
	Metric     string // "milliseconds" or "bytes"
	Allotment  uint64 // Total allotment for this session
}

// MerchantInterface defines the interface for merchant payment operations
type MerchantInterface interface {
	CreatePaymentToken(mintURL string, amount uint64) (string, error)
	CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error)
	GetAcceptedMints() []config_manager.MintConfig
	GetBalance() uint64
	GetBalanceByMint(mintURL string) uint64
	PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error)
	GetAdvertisement() string
	StartPayoutRoutine()
	StartDataUsageMonitoring()
	CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error)
	// New session management methods
	GetSession(macAddress string) (*CustomerSession, error)
	AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error)
	GetUsage(macAddress string) (string, error)
	// Wallet funding methods
	Fund(cashuToken string) (uint64, error)
}

// Merchant represents the financial decision maker for the tollgate
type Merchant struct {
	config        *config_manager.Config
	configManager *config_manager.ConfigManager
	tollwallet    tollwallet.TollWallet
	advertisement string
	// In-memory session store
	customerSessions map[string]*CustomerSession
	sessionMu        sync.RWMutex
}

func New(configManager *config_manager.ConfigManager) (MerchantInterface, error) {
	log.Printf("=== Merchant Initializing ===")

	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("main config is nil")
	}

	// Extract mint URLs from MintConfig
	mintURLs := make([]string, len(config.AcceptedMints))
	for i, mint := range config.AcceptedMints {
		mintURLs[i] = mint.URL
	}

	log.Printf("Setting up wallet...")
	walletDirPath := filepath.Dir(configManager.ConfigFilePath)
	if err := os.MkdirAll(walletDirPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %w", walletDirPath, err)
	}
	tollwallet, walletErr := tollwallet.New(walletDirPath, mintURLs, false)

	if walletErr != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", walletErr)
	}
	balance := tollwallet.GetBalance()

	// Set advertisement
	advertisementStr, err := CreateAdvertisement(configManager)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement: %w", err)
	}

	log.Printf("Accepted Mints: %v", config.AcceptedMints)
	log.Printf("Wallet Balance: %d", balance)
	log.Printf("Advertisement: %s", advertisementStr)
	log.Printf("=== Merchant ready ===")

	return &Merchant{
		config:           config,
		configManager:    configManager,
		tollwallet:       *tollwallet,
		advertisement:    advertisementStr,
		customerSessions: make(map[string]*CustomerSession),
	}, nil
}

// GetUsage returns the current usage in format "[usage]/[allotment]"
// Returns "-1" if no session exists
// Returns error for actual errors (caller should return 500)
func (m *Merchant) GetUsage(macAddress string) (string, error) {
	// Get session for this MAC
	session, err := m.GetSession(macAddress)
	if err != nil {
		return "-1/-1", nil
	}

	var usageStr string
	switch session.Metric {
	case "bytes":
		// Get data usage since baseline
		usage, err := valve.GetDataUsageSinceBaseline(macAddress)
		if err != nil {
			return "", fmt.Errorf("error getting data usage: %w", err)
		}
		usageStr = fmt.Sprintf("%d/%d", usage, session.Allotment)

	case "milliseconds":
		// Calculate time usage in milliseconds
		elapsed := time.Now().Unix() - session.StartTime
		elapsedMs := uint64(elapsed * 1000)
		usageStr = fmt.Sprintf("%d/%d", elapsedMs, session.Allotment)

	default:
		return "", fmt.Errorf("unknown session metric: %s", session.Metric)
	}

	return usageStr, nil
}

// StartDataUsageMonitoring starts a background routine to monitor data usage for active sessions
func (m *Merchant) StartDataUsageMonitoring() {
	log.Printf("Starting data usage monitoring routine")

	ticker := time.NewTicker(2 * time.Second) // Check every 2 seconds
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			m.checkDataUsage()
		}
	}()
}

// checkDataUsage checks all active data-based sessions and closes gates when allotment is reached
func (m *Merchant) checkDataUsage() {
	m.sessionMu.RLock()
	sessions := make(map[string]*CustomerSession)
	for mac, session := range m.customerSessions {
		if session.Metric == "bytes" {
			sessions[mac] = session
		}
	}
	m.sessionMu.RUnlock()

	for mac, session := range sessions {
		// Check if baseline exists (gate is open)
		if !valve.HasDataBaseline(mac) {
			continue
		}

		// Get current usage
		usage, err := valve.GetDataUsageSinceBaseline(mac)
		if err != nil {
			log.Printf("Error getting data usage for %s: %v", mac, err)
			continue
		}

		// Check if allotment is reached
		if usage >= session.Allotment {
			log.Printf("Data allotment reached for %s: %s / %s",
				mac,
				utils.BytesToHumanReadable(usage),
				utils.BytesToHumanReadable(session.Allotment))

			// Close the gate
			err = valve.CloseGate(mac)
			if err != nil {
				log.Printf("Error closing gate for %s: %v", mac, err)
			} else {
				log.Printf("Successfully closed gate for %s", mac)
			}
		} else {
			// Log progress periodically (every ~10 checks = 20 seconds)
			if usage > 0 && usage%(10*1024*1024) < 2*1024*1024 { // Log around every 10MB
				log.Printf("Data usage for %s: %s / %s (%.1f%%)",
					mac,
					utils.BytesToHumanReadable(usage),
					utils.BytesToHumanReadable(session.Allotment),
					float64(usage)/float64(session.Allotment)*100)
			}
		}
	}
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

	identities := m.configManager.GetIdentities()
	if identities == nil {
		return
	}

	for _, profitShare := range m.config.ProfitShare {
		aimedAmount := uint64(math.Round(float64(aimedPaymentAmount) * profitShare.Factor))
		// Lookup lightning address from identities based on the profitShare.Identity name
		profitShareIdentity, err := identities.GetPublicIdentity(profitShare.Identity)
		if err != nil {
			log.Printf("Warning: Could not find public identity for profit share: %v", err)
			continue // Skip this profit share if identity not found
		}
		m.PayoutShare(mintConfig, aimedAmount, profitShareIdentity.LightningAddress)
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

// PurchaseSession processes a payment with cashu token and MAC address, returns either a session event or a notice event
func (m *Merchant) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	// Validate MAC address
	if !utils.ValidateMACAddress(macAddress) {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "invalid-mac-address",
			fmt.Sprintf("Invalid MAC address: %s", macAddress), macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("invalid MAC address and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Process payment
	paymentCashuToken, err := cashu.DecodeToken(cashuToken)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-error-invalid-token",
			fmt.Sprintf("Invalid cashu token: %v", err), macAddress)
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

		noticeEvent, noticeErr := m.CreateNoticeEvent("error", errorCode, errorMessage, macAddress)
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
			fmt.Sprintf("Failed to calculate allotment: %v", err), macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to calculate allotment and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Add allotment to session (creates new session if doesn't exist)
	metric := m.config.Metric
	session, err := m.AddAllotment(macAddress, metric, allotment)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "session-management-failed",
			fmt.Sprintf("Failed to manage session: %v", err), macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to manage session and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Open the gate based on session type
	switch session.Metric {
	case "milliseconds":
		// Time-based session: open gate until end timestamp
		endTimestamp := session.StartTime + int64(session.Allotment/1000)
		err = valve.OpenGateUntil(macAddress, endTimestamp)
		if err != nil {
			noticeEvent, noticeErr := m.CreateNoticeEvent("error", "gate-open-failed",
				fmt.Sprintf("Failed to open gate: %v", err), macAddress)
			if noticeErr != nil {
				return nil, fmt.Errorf("failed to open gate and failed to create notice: %w", noticeErr)
			}
			return noticeEvent, nil
		}
	case "bytes":
		// Data-based session: only open gate if baseline doesn't exist (new session)
		// For session extensions, the gate is already open and baseline should not be reset
		if !valve.HasDataBaseline(macAddress) {
			err = valve.OpenGate(macAddress)
			if err != nil {
				noticeEvent, noticeErr := m.CreateNoticeEvent("error", "gate-open-failed",
					fmt.Sprintf("Failed to open gate: %v", err), macAddress)
				if noticeErr != nil {
					return nil, fmt.Errorf("failed to open gate and failed to create notice: %w", noticeErr)
				}
				return noticeEvent, nil
			}
			// The valve module automatically sets the data baseline
			log.Printf("Opened gate for new data session: %s", macAddress)
		} else {
			log.Printf("Gate already open for %s, extending allotment without resetting baseline", macAddress)
		}
		// The merchant will periodically check usage and close the gate when allotment is reached
	default:
		return nil, fmt.Errorf("unsupported metric: %s", session.Metric)
	}

	// Create a success session event (using MAC address as identifier in logs)
	sessionEvent, err := m.createSessionEvent(session, macAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to create session event: %w", err)
	}

	return sessionEvent, nil
}

func (m *Merchant) GetAdvertisement() string {
	return m.advertisement
}

func CreateAdvertisement(configManager *config_manager.ConfigManager) (string, error) {
	config := configManager.GetConfig()
	if config == nil {
		return "", fmt.Errorf("main config is nil")
	}

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

	identities := configManager.GetIdentities()
	if identities == nil {
		return "", fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return "", fmt.Errorf("merchant identity not found: %w", err)
	}
	// Sign
	err = advertisementEvent.Sign(merchantIdentity.PrivateKey)
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
	case "bytes":
		return m.calculateAllotmentBytes(steps, mintConfig)
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

// calculateAllotmentBytes calculates allotment in bytes from steps
func (m *Merchant) calculateAllotmentBytes(steps uint64, mintConfig *config_manager.MintConfig) (uint64, error) {
	// Convert steps to bytes using configured step size
	totalBytes := steps * m.config.StepSize

	log.Printf("Converting %d steps to %d bytes using step size %d",
		steps, totalBytes, m.config.StepSize)

	return totalBytes, nil
}

// getLatestSession queries the local relay pool for the most recent session by customer pubkey
func (m *Merchant) getLatestSession(customerPubkey string) (*nostr.Event, error) {
	log.Printf("Querying for existing session for customer %s", customerPubkey)

	identities := m.configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}
	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
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

// createSessionEvent creates a session event from the MAC-address based session
func (m *Merchant) createSessionEvent(session *CustomerSession, customerPubkey string) (*nostr.Event, error) {
	deviceIdentifier := session.MacAddress

	identities := m.configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}

	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
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
			{"allotment", fmt.Sprintf("%d", session.Allotment)},
			{"metric", session.Metric},
			{"start-time", fmt.Sprintf("%d", session.StartTime)},
		},
		Content: "",
	}

	// Sign with tollgate private key
	err = sessionEvent.Sign(merchantIdentity.PrivateKey)
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
	newTotalAllotment := existingAllotment + additionalAllotment

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

	identities := m.configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}
	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
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
	err = sessionEvent.Sign(merchantIdentity.PrivateKey)
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

	config := m.configManager.GetConfig()
	if config == nil {
		return fmt.Errorf("main config is nil")
	}
	for _, relayURL := range config.Relays {
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
	identities := m.configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}
	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	noticeEvent := &nostr.Event{
		Kind:      21023, // NIP-94 notice event
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
	err = noticeEvent.Sign(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign notice event: %w", err)
	}

	return noticeEvent, nil
}

// MerchantInterface method implementations

// CreatePaymentToken creates a payment token for the specified mint and amount
func (m *Merchant) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	// Check balance before attempting to send
	balance := m.tollwallet.GetBalanceByMint(mintURL)
	totalBalance := m.tollwallet.GetBalance()

	log.Printf("Creating payment token: amount=%d, mintURL=%s, balance_by_mint=%d, total_balance=%d",
		amount, mintURL, balance, totalBalance)

	if balance < amount {
		return "", fmt.Errorf("insufficient balance: need %d sats, have %d sats for mint %s (total balance: %d)",
			amount, balance, mintURL, totalBalance)
	}

	// Use the tollwallet to create a payment token with basic send
	token, err := m.tollwallet.Send(amount, mintURL, true)
	if err != nil {
		return "", fmt.Errorf("failed to create payment token: %w", err)
	}

	// Validate token has proofs
	if token == nil {
		return "", fmt.Errorf("token creation returned nil token")
	}

	// Serialize token to string
	tokenString, err := token.Serialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize token: %w", err)
	}

	// Validate serialized token is not empty
	if tokenString == "" {
		return "", fmt.Errorf("token serialization returned empty string")
	}

	log.Printf("Successfully created payment token: length=%d, token_preview=%s...",
		len(tokenString), tokenString[:min(50, len(tokenString))])

	return tokenString, nil
}

// CreatePaymentTokenWithOverpayment creates a payment token with overpayment capability
func (m *Merchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	// Use the tollwallet's new SendWithOverpayment method
	tokenString, err := m.tollwallet.SendWithOverpayment(amount, mintURL, maxOverpaymentPercent, maxOverpaymentAbsolute)
	if err != nil {
		return "", fmt.Errorf("failed to create payment token with overpayment: %w", err)
	}
	return tokenString, nil
}

// GetAcceptedMints returns the list of accepted mints from the configuration
func (m *Merchant) GetAcceptedMints() []config_manager.MintConfig {
	return m.config.AcceptedMints
}

// GetBalance returns the total balance across all mints
func (m *Merchant) GetBalance() uint64 {
	return m.tollwallet.GetBalance()
}

// GetBalanceByMint returns the balance for a specific mint
func (m *Merchant) GetBalanceByMint(mintURL string) uint64 {
	return m.tollwallet.GetBalanceByMint(mintURL)
}

// GetSession retrieves a customer session by MAC address
func (m *Merchant) GetSession(macAddress string) (*CustomerSession, error) {
	m.sessionMu.RLock()
	defer m.sessionMu.RUnlock()

	session, exists := m.customerSessions[macAddress]
	if !exists {
		return nil, fmt.Errorf("session not found for MAC address: %s", macAddress)
	}

	return session, nil
}

// AddAllotment adds allotment to a customer session, creating it if it doesn't exist
func (m *Merchant) AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error) {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	session, exists := m.customerSessions[macAddress]
	if !exists {
		// Create new session
		session = &CustomerSession{
			MacAddress: macAddress,
			StartTime:  time.Now().Unix(),
			Metric:     metric,
			Allotment:  amount,
		}
		m.customerSessions[macAddress] = session
	} else {
		// Add to existing session and reset start time to now
		session.Allotment += amount
		session.StartTime = time.Now().Unix()
	}

	return session, nil
}

// Fund adds a cashu token to the wallet
func (m *Merchant) Fund(cashuToken string) (uint64, error) {
	log.Printf("Funding wallet with cashu token (length: %d)", len(cashuToken))

	// Basic validation - cashu tokens typically start with "cashuA" and are much longer
	if len(cashuToken) < 10 {
		return 0, fmt.Errorf("invalid cashu token: token too short (expected cashu token format)")
	}

	// Parse the cashu token with error recovery
	tokenPreview := cashuToken
	if len(cashuToken) > 50 {
		tokenPreview = cashuToken[:50] + "..."
	}
	log.Printf("Attempting to decode token (length: %d, preview: %s)", len(cashuToken), tokenPreview)

	parsedToken, err := cashu.DecodeTokenV4(cashuToken)
	if err != nil {
		log.Printf("Failed to decode cashu token (length: %d): %v", len(cashuToken), err)
		return 0, fmt.Errorf("invalid cashu token format: %w", err)
	}

	// Add token to wallet
	amountReceived, err := m.tollwallet.Receive(parsedToken)
	if err != nil {
		log.Printf("Failed to receive cashu token: %v", err)
		return 0, fmt.Errorf("failed to receive token: %w", err)
	}

	log.Printf("Successfully funded wallet with %d sats", amountReceived)
	return amountReceived, nil
}
