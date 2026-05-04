package merchant

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/nbd-wtf/go-nostr"
	"github.com/sirupsen/logrus"
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
	DrainMint(mintURL string) (string, uint64, error)
	GetAcceptedMints() []config_manager.MintConfig
	GetBalance() uint64
	GetBalanceByMint(mintURL string) uint64
	GetAllMintBalances() map[string]uint64
	PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error)
	GetAdvertisement() string
	StartPayoutRoutine()
	StartDataUsageMonitoring()
	CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error)
	// New session management methods
	GetSession(macAddress string) (*CustomerSession, error)
	AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error)
	GetUsage(macAddress string) (string, error)
	RestoreSessions()
	Fund(cashuToken string) (uint64, error)
}

// Merchant represents the financial decision maker for the tollgate
type Merchant struct {
	config        *config_manager.Config
	configManager *config_manager.ConfigManager
	tollwallet    tollwallet.TollWallet
	advertisement string
	customerSessions map[string]*CustomerSession
	sessionMu        sync.RWMutex
	sessionFile      string
}

const defaultSessionFile = "/etc/tollgate/sessions.json"

func (m *Merchant) saveSessions() {
	if m.sessionFile == "" {
		return
	}
	data, err := json.Marshal(m.customerSessions)
	if err != nil {
		logger.WithError(err).Error("Error marshaling sessions")
		return
	}
	tmp := m.sessionFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		logger.WithError(err).Error("Error writing sessions")
		return
	}
	os.Rename(tmp, m.sessionFile)
}

func (m *Merchant) RestoreSessions() {
	if m.sessionFile == "" {
		return
	}
	data, err := os.ReadFile(m.sessionFile)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.WithError(err).Error("Error reading sessions file")
		}
		return
	}
	var sessions map[string]*CustomerSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		logger.WithError(err).Error("Error parsing sessions file")
		return
	}
	now := time.Now().Unix()
	for mac, session := range sessions {
		if session.Metric == "milliseconds" {
			endTime := session.StartTime + int64(session.Allotment/1000)
			if endTime <= now {
				logger.WithFields(logrus.Fields{
					"mac":     mac,
					"expired": now - endTime,
				}).Debug("Skipping expired time session")
				continue
			}
			valve.OpenGateUntil(mac, endTime)
			m.customerSessions[mac] = session
			logger.WithFields(logrus.Fields{
				"mac":        mac,
				"remaining_s": endTime - now,
			}).Info("Restored time session")
		} else if session.Metric == "bytes" {
			valve.OpenGate(mac)
			m.customerSessions[mac] = session
			logger.WithFields(logrus.Fields{
				"mac":       mac,
				"allotment": session.Allotment,
			}).Info("Restored data session")
		}
	}
	logger.WithField("count", len(m.customerSessions)).Info("Sessions restored")
}

func New(configManager *config_manager.ConfigManager) (MerchantInterface, error) {
	logger.Info("Merchant initializing")

	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("main config is nil")
	}

	mintURLs := make([]string, len(config.AcceptedMints))
	for i, mint := range config.AcceptedMints {
		mintURLs[i] = mint.URL
	}

	logger.Info("Setting up wallet")
	walletDirPath := filepath.Dir(configManager.ConfigFilePath)
	if err := os.MkdirAll(walletDirPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %w", walletDirPath, err)
	}
	tollwallet, walletErr := tollwallet.New(walletDirPath, mintURLs, false)

	if walletErr != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", walletErr)
	}
	balance := tollwallet.GetBalance()

	advertisementStr, err := CreateAdvertisement(configManager)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"mints":   mintURLs,
		"balance": balance,
	}).Debug("Wallet configured")

	logger.Info("Merchant ready")

	return &Merchant{
		config:           config,
		configManager:    configManager,
		tollwallet:       *tollwallet,
		advertisement:    advertisementStr,
		customerSessions: make(map[string]*CustomerSession),
		sessionFile:      filepath.Join(filepath.Dir(configManager.ConfigFilePath), "sessions.json"),
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
	logger.Info("Starting data usage monitoring routine")

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
			logger.WithError(err).WithField("mac", mac).Error("Error getting data usage")
			continue
		}

		if usage >= session.Allotment {
			logger.WithFields(logrus.Fields{
				"mac":       mac,
				"usage":     utils.BytesToHumanReadable(usage),
				"allotment": utils.BytesToHumanReadable(session.Allotment),
			}).Info("Data allotment reached")

			err = valve.CloseGate(mac)
			if err != nil {
				logger.WithError(err).WithField("mac", mac).Error("Error closing gate")
			} else {
				logger.WithField("mac", mac).Info("Gate closed")
			}

			m.sessionMu.Lock()
			delete(m.customerSessions, mac)
			m.saveSessions()
			m.sessionMu.Unlock()
		} else {
			if usage > 0 && usage%(10*1024*1024) < 2*1024*1024 {
				logger.WithFields(logrus.Fields{
					"mac":    mac,
					"usage":  utils.BytesToHumanReadable(usage),
					"limit":  utils.BytesToHumanReadable(session.Allotment),
					"pct":    fmt.Sprintf("%.1f", float64(usage)/float64(session.Allotment)*100),
				}).Debug("Data usage progress")
			}
		}
	}
}

func (m *Merchant) StartPayoutRoutine() {
	logger.Info("Starting payout routine")

	for _, mint := range m.config.AcceptedMints {
		go func(mintConfig config_manager.MintConfig) {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()

			for range ticker.C {
				m.processPayout(mintConfig)
			}
		}(mint)
	}

	logger.Info("Payout routine started")
}

// processPayout checks balances and processes payouts for each mint
func (m *Merchant) processPayout(mintConfig config_manager.MintConfig) {
	// Get current balance
	// Note: The current implementation only returns total balance, not per mint
	balance := m.tollwallet.GetBalanceByMint(mintConfig.URL)

	// Skip if balance is below minimum payout amount
	if balance < mintConfig.MinPayoutAmount {
		logger.WithFields(logrus.Fields{
			"mint":      mintConfig.URL,
			"balance":   balance,
			"threshold": mintConfig.MinPayoutAmount,
		}).Debug("Skipping payout: below threshold")
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
			logger.WithError(err).WithField("identity", profitShare.Identity).Warn("Could not find public identity for profit share")
			continue
		}
		m.PayoutShare(mintConfig, aimedAmount, profitShareIdentity.LightningAddress)
	}

	logger.WithField("mint", mintConfig.URL).Info("Payout completed")
}

func (m *Merchant) PayoutShare(mintConfig config_manager.MintConfig, aimedPaymentAmount uint64, lightningAddress string) {
	tolerancePaymentAmount := aimedPaymentAmount + (aimedPaymentAmount * mintConfig.BalanceTolerancePercent / 100)

	logger.WithFields(logrus.Fields{
		"mint":       mintConfig.URL,
		"aimed":      aimedPaymentAmount,
		"tolerance":  tolerancePaymentAmount,
	}).Debug("Processing payout")

	maxCost := aimedPaymentAmount + tolerancePaymentAmount
	meltErr := m.tollwallet.MeltToLightning(mintConfig.URL, aimedPaymentAmount, maxCost, lightningAddress)

	if meltErr != nil {
		logger.WithError(meltErr).WithField("mint", mintConfig.URL).Error("Error melting to lightning")
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

	logger.WithFields(logrus.Fields{
		"amount": amountAfterSwap,
	}).Debug("Amount after swap")

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
			logger.WithField("mac", macAddress).Info("Opened gate for new data session")
		} else {
			logger.WithField("mac", macAddress).Debug("Gate already open, extending allotment")
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
	totalMs := steps * m.config.StepSize

	logger.WithFields(logrus.Fields{
		"steps":    steps,
		"total_ms": totalMs,
		"step_size": m.config.StepSize,
	}).Debug("Converting steps to milliseconds")

	return totalMs, nil
}

// calculateAllotmentBytes calculates allotment in bytes from steps
func (m *Merchant) calculateAllotmentBytes(steps uint64, mintConfig *config_manager.MintConfig) (uint64, error) {
	totalBytes := steps * m.config.StepSize

	logger.WithFields(logrus.Fields{
		"steps":      steps,
		"total_bytes": totalBytes,
		"step_size":  m.config.StepSize,
	}).Debug("Converting steps to bytes")

	return totalBytes, nil
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

		logger.WithFields(logrus.Fields{
			"existing":  existingAllotment,
			"passed":    timePassedInMetric,
			"leftover":  leftoverAllotment,
			"additional": additionalAllotment,
			"metric":    m.config.Metric,
		}).Debug("Session extension")
	} else {
		leftoverAllotment = existingAllotment
		logger.WithFields(logrus.Fields{
			"existing":  existingAllotment,
			"leftover":  leftoverAllotment,
			"additional": additionalAllotment,
			"metric":    m.config.Metric,
		}).Debug("Session extension (no decay)")
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

	logger.WithFields(logrus.Fields{
		"amount":        amount,
		"mint":          mintURL,
		"mint_balance":  balance,
		"total_balance": totalBalance,
	}).Debug("Creating payment token")

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

	logger.WithFields(logrus.Fields{
		"length": len(tokenString),
	}).Debug("Payment token created")

	return tokenString, nil
}

// DrainMint drains all available balance from a specific mint
// This method is designed for wallet draining and does NOT include fees
// to avoid insufficient funds errors when extracting all available balance
func (m *Merchant) DrainMint(mintURL string) (string, uint64, error) {
	// Check balance before attempting to drain
	balance := m.tollwallet.GetBalanceByMint(mintURL)

	logger.WithFields(logrus.Fields{
		"mint":    mintURL,
		"balance": balance,
	}).Info("Draining mint")

	if balance == 0 {
		return "", 0, fmt.Errorf("no balance available for mint %s", mintURL)
	}

	// Use the tollwallet's Drain method which doesn't include fees
	token, actualAmount, err := m.tollwallet.Drain(mintURL)
	if err != nil {
		return "", 0, fmt.Errorf("failed to drain mint: %w", err)
	}

	// Validate token has proofs
	if token == nil {
		return "", 0, fmt.Errorf("drain returned nil token")
	}

	// Serialize token to string
	tokenString, err := token.Serialize()
	if err != nil {
		return "", 0, fmt.Errorf("failed to serialize drain token: %w", err)
	}

	// Validate serialized token is not empty
	if tokenString == "" {
		return "", 0, fmt.Errorf("drain token serialization returned empty string")
	}

	logger.WithFields(logrus.Fields{
		"mint":   mintURL,
		"amount": actualAmount,
	}).Info("Mint drained")

	return tokenString, actualAmount, nil
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

// GetAllMintBalances returns a map of all mints and their balances in the wallet
func (m *Merchant) GetAllMintBalances() map[string]uint64 {
	return m.tollwallet.GetAllMintBalances()
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
		session = &CustomerSession{
			MacAddress: macAddress,
			StartTime:  time.Now().Unix(),
			Metric:     metric,
			Allotment:  amount,
		}
		m.customerSessions[macAddress] = session
	} else {
		session.Allotment += amount
		session.StartTime = time.Now().Unix()
	}

	m.saveSessions()
	return session, nil
}

// Fund adds a cashu token to the wallet
func (m *Merchant) Fund(cashuToken string) (uint64, error) {
	logger.WithField("length", len(cashuToken)).Info("Funding wallet with cashu token")

	if len(cashuToken) < 10 {
		return 0, fmt.Errorf("invalid cashu token: token too short (expected cashu token format)")
	}

	tokenPreview := cashuToken
	if len(cashuToken) > 50 {
		tokenPreview = cashuToken[:50] + "..."
	}
	logger.WithFields(logrus.Fields{
		"length":  len(cashuToken),
		"preview": tokenPreview,
	}).Debug("Decoding token")

	parsedToken, err := cashu.DecodeTokenV4(cashuToken)
	if err != nil {
		logger.WithError(err).WithField("length", len(cashuToken)).Error("Failed to decode cashu token")
		return 0, fmt.Errorf("invalid cashu token format: %w", err)
	}

	amountReceived, err := m.tollwallet.Receive(parsedToken)
	if err != nil {
		logger.WithError(err).Error("Failed to receive cashu token")
		return 0, fmt.Errorf("failed to receive token: %w", err)
	}

	logger.WithField("amount", amountReceived).Info("Wallet funded")
	return amountReceived, nil
}
