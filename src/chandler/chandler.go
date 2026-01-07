package chandler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
	"github.com/nbd-wtf/go-nostr"
	"github.com/sirupsen/logrus"
)

// Module-level logger with pre-configured module field
var logger = logrus.WithField("module", "chandler")

// Chandler is the main implementation of ChandlerInterface
type Chandler struct {
	configManager *config_manager.ConfigManager
	merchant      merchant.MerchantInterface
	sessions      map[string]*ChandlerSession // keyed by upstream pubkey
	mu            sync.RWMutex
}

// NewChandler creates a new chandler instance
func NewChandler(configManager *config_manager.ConfigManager, merchantImpl merchant.MerchantInterface) (ChandlerInterface, error) {
	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	chandler := &Chandler{
		configManager: configManager,
		merchant:      merchantImpl,
		sessions:      make(map[string]*ChandlerSession),
	}

	logger.Info("Chandler initialized successfully")
	return chandler, nil
}

// HandleUpstreamTollgate handles a discovered upstream TollGate
func (c *Chandler) HandleUpstreamTollgate(upstream *UpstreamTollgate) error {
	logger.WithFields(logrus.Fields{
		"upstream_pubkey": upstream.Advertisement.PubKey,
		"interface":       upstream.InterfaceName,
		"gateway":         upstream.GatewayIP,
		"mac_address":     upstream.MacAddress,
		"discovered_at":   upstream.DiscoveredAt.Format(time.RFC3339),
	}).Info("🔗 CONNECTED: Processing upstream TollGate connection")

	// Extract advertisement information
	adInfo, err := tollgate_protocol.ExtractAdvertisementInfo(upstream.Advertisement)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Error("Failed to extract advertisement information")
		return err
	}

	// Check trust policy
	config := c.configManager.GetConfig()
	err = ValidateTrustPolicy(
		upstream.Advertisement.PubKey,
		config.Chandler.Trust.Allowlist,
		config.Chandler.Trust.Blocklist,
		config.Chandler.Trust.DefaultPolicy,
	)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Warn("Trust policy validation failed")
		return err
	}

	// Find overlapping mint options and select the best one
	selectedPricing, err := c.selectCompatiblePricingOption(adInfo.PricingOptions)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Error("No compatible pricing options found")
		return err
	}

	// Check if we have sufficient funds for minimum purchase
	minPaymentAmount := selectedPricing.MinSteps * selectedPricing.PricePerStep
	availableBalance := c.merchant.GetBalanceByMint(selectedPricing.MintURL)

	if availableBalance < minPaymentAmount {
		err := fmt.Errorf("insufficient funds: need %d sats, have %d sats", minPaymentAmount, availableBalance)
		logger.WithFields(logrus.Fields{
			"upstream_pubkey":   upstream.Advertisement.PubKey,
			"required_amount":   minPaymentAmount,
			"available_balance": availableBalance,
			"mint_url":          selectedPricing.MintURL,
		}).Error("Insufficient funds for minimum purchase")
		return err
	}

	// Calculate steps based on preferred session increments for granular payments
	var preferredAllotment uint64
	switch adInfo.Metric {
	case "milliseconds":
		preferredAllotment = config.Chandler.Sessions.PreferredSessionIncrementsMilliseconds
	case "bytes":
		preferredAllotment = config.Chandler.Sessions.PreferredSessionIncrementsBytes
	default:
		return fmt.Errorf("unsupported metric: %s", adInfo.Metric)
	}

	// Convert preferred allotment to steps
	preferredSteps := preferredAllotment / adInfo.StepSize
	if preferredSteps == 0 {
		preferredSteps = 1 // Minimum 1 step
	}

	// Calculate affordability constraints
	maxAffordableSteps := availableBalance / selectedPricing.PricePerStep

	// Choose the smallest of: preferred, affordable, or minimum required
	desiredSteps := preferredSteps

	logger.WithFields(logrus.Fields{
		"preferred_allotment":  preferredAllotment,
		"step_size":            adInfo.StepSize,
		"preferred_steps":      preferredSteps,
		"available_balance":    availableBalance,
		"price_per_step":       selectedPricing.PricePerStep,
		"max_affordable_steps": maxAffordableSteps,
		"min_steps":            selectedPricing.MinSteps,
		"desired_steps_before": desiredSteps,
	}).Info("🔍 Step calculation details")

	if desiredSteps < selectedPricing.MinSteps {
		desiredSteps = selectedPricing.MinSteps
		logger.WithFields(logrus.Fields{
			"adjusted_to_min": desiredSteps,
		}).Info("🔍 Adjusted to minimum steps")
	}
	if desiredSteps > maxAffordableSteps {
		desiredSteps = maxAffordableSteps
		logger.WithFields(logrus.Fields{
			"adjusted_to_affordable": desiredSteps,
		}).Info("🔍 Adjusted to affordable steps")
	}

	steps := desiredSteps

	logger.WithFields(logrus.Fields{
		"metric":               adInfo.Metric,
		"preferred_allotment":  preferredAllotment,
		"preferred_steps":      preferredSteps,
		"min_required_steps":   selectedPricing.MinSteps,
		"max_affordable_steps": maxAffordableSteps,
		"final_steps":          steps,
		"step_size":            adInfo.StepSize,
	}).Info("💳 Calculated payment steps for granular session")

	// Final check: ensure we don't send a payment with 0 steps
	if steps == 0 {
		err := fmt.Errorf("cannot make payment with 0 steps: insufficient funds or invalid pricing")
		logger.WithFields(logrus.Fields{
			"upstream_pubkey":      upstream.Advertisement.PubKey,
			"available_balance":    availableBalance,
			"price_per_step":       selectedPricing.PricePerStep,
			"max_affordable_steps": maxAffordableSteps,
			"min_steps":            selectedPricing.MinSteps,
		}).Error("Payment rejected: 0 steps calculated")
		return err
	}

	// Create payment proposal
	proposal := &PaymentProposal{
		UpstreamPubkey:     upstream.Advertisement.PubKey,
		Steps:              steps,
		PricingOption:      selectedPricing,
		Reason:             "initial",
		EstimatedAllotment: CalculateAllotment(steps, adInfo.StepSize),
	}

	// Validate budget constraints
	err = ValidateBudgetConstraints(
		proposal,
		config.Chandler.MaxPricePerMillisecond,
		config.Chandler.MaxPricePerByte,
		adInfo.Metric,
		adInfo.StepSize,
	)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Warn("Budget constraints not met")
		return err
	}

	// Generate unique customer private key for this session
	customerPrivateKey := nostr.GeneratePrivateKey()
	customerPublicKey, err := nostr.GetPublicKey(customerPrivateKey)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Error("Failed to derive customer public key")
		return err
	}

	// Create session first
	session := &ChandlerSession{
		UpstreamTollgate:   upstream,
		CustomerPrivateKey: customerPrivateKey,
		Advertisement:      upstream.Advertisement,
		AdvertisementInfo:  adInfo,
		SelectedPricing:    selectedPricing,
		TotalAllotment:     proposal.EstimatedAllotment,
		RenewalThresholds:  config.Chandler.Sessions.DefaultRenewalThresholds,
		CreatedAt:          time.Now(),
		LastPaymentAt:      time.Now(),
		TotalSpent:         proposal.Steps * selectedPricing.PricePerStep,
		PaymentCount:       1,
		Status:             SessionActive,
	}

	// Create and send payment
	// Log payment proposal details for debugging
	logger.WithFields(logrus.Fields{
		"upstream_pubkey":     proposal.UpstreamPubkey,
		"steps":               proposal.Steps,
		"price_per_step":      proposal.PricingOption.PricePerStep,
		"mint_url":            proposal.PricingOption.MintURL,
		"min_steps":           proposal.PricingOption.MinSteps,
		"reason":              proposal.Reason,
		"estimated_allotment": proposal.EstimatedAllotment,
		"total_amount":        proposal.Steps * proposal.PricingOption.PricePerStep,
	}).Info("💰 Creating payment proposal for upstream TollGate")

	sessionEvent, err := c.createAndSendPayment(session, proposal)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Error("Failed to create and send payment")
		return err
	}

	// Extract actual allotment from session event response
	actualAllotment, err := c.extractAllotment(sessionEvent)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Error("Failed to extract allotment from session event")
		return err
	}

	// Update session with the received session event and actual allotment
	session.SessionEvent = sessionEvent
	session.TotalAllotment = actualAllotment

	// Create and start usage tracker
	err = c.createUsageTracker(session)

	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstream.Advertisement.PubKey,
			"error":           err,
		}).Error("Failed to create usage tracker")
		return err
	}

	// Store session
	c.mu.Lock()
	c.sessions[upstream.Advertisement.PubKey] = session
	c.mu.Unlock()

	logger.WithFields(logrus.Fields{
		"upstream_pubkey": upstream.Advertisement.PubKey,
		"customer_pubkey": customerPublicKey,
		"allotment":       session.TotalAllotment,
		"metric":          adInfo.Metric,
		"amount_spent":    session.TotalSpent,
	}).Info("✅ CONNECTED: Session created successfully with upstream TollGate")

	// Also log advertisement details
	if upstream.Advertisement != nil {
		logger.WithFields(logrus.Fields{
			"interface":        upstream.InterfaceName,
			"advertisement_id": upstream.Advertisement.ID,
			"public_key":       upstream.Advertisement.PubKey,
			"kind":             upstream.Advertisement.Kind,
		}).Info("📡 TollGate advertisement details")
	}

	return nil
}

// HandleDisconnect handles network interface disconnection
func (c *Chandler) HandleDisconnect(interfaceName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var disconnectedSessions []string

	// Find sessions on this interface
	for pubkey, session := range c.sessions {
		if session.UpstreamTollgate.InterfaceName == interfaceName {
			// Stop usage tracker
			if session.UsageTracker != nil {
				session.UsageTracker.Stop()
			}

			// Mark session as disconnected
			session.Status = SessionExpired
			disconnectedSessions = append(disconnectedSessions, pubkey)

			logger.WithFields(logrus.Fields{
				"upstream_pubkey": pubkey,
				"interface":       interfaceName,
				"gateway":         session.UpstreamTollgate.GatewayIP,
			}).Info("❌ DISCONNECTED: Session terminated due to interface disconnect")
		}
	}

	// Remove disconnected sessions
	for _, pubkey := range disconnectedSessions {
		delete(c.sessions, pubkey)
	}

	if len(disconnectedSessions) > 0 {
		logger.WithFields(logrus.Fields{
			"interface":           interfaceName,
			"terminated_sessions": len(disconnectedSessions),
		}).Info("❌ DISCONNECTED: Interface disconnected, sessions cleaned up")
	} else {
		// Interface went down but we didn't have any TollGate connections
		logger.WithField("interface", interfaceName).Info("⬇️ INTERFACE DOWN: Interface disconnected (no TollGate connections)")
	}

	return nil
}

// HandleUpcomingRenewal handles renewal threshold callbacks from usage trackers
func (c *Chandler) HandleUpcomingRenewal(upstreamPubkey string, currentUsage uint64) error {
	c.mu.RLock()
	session, exists := c.sessions[upstreamPubkey]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("session not found for pubkey: %s", upstreamPubkey)
	}

	logger.WithFields(logrus.Fields{
		"upstream_pubkey": upstreamPubkey,
		"current_usage":   currentUsage,
		"total_allotment": session.TotalAllotment,
	}).Info("Processing renewal request")

	// Check if advertisement has changed
	c.checkAdvertisementChanges(session)

	// Create renewal proposal
	config := c.configManager.GetConfig()
	steps := config.Chandler.Sessions.PreferredSessionIncrementsMilliseconds / session.AdvertisementInfo.StepSize
	if session.AdvertisementInfo.Metric == "bytes" {
		steps = config.Chandler.Sessions.PreferredSessionIncrementsBytes / session.AdvertisementInfo.StepSize
	}

	proposal := &PaymentProposal{
		UpstreamPubkey:     upstreamPubkey,
		Steps:              steps,
		PricingOption:      session.SelectedPricing,
		Reason:             "renewal",
		EstimatedAllotment: CalculateAllotment(steps, session.AdvertisementInfo.StepSize),
	}

	// Validate budget for renewal
	err := ValidateBudgetConstraints(
		proposal,
		config.Chandler.MaxPricePerMillisecond,
		config.Chandler.MaxPricePerByte,
		session.AdvertisementInfo.Metric,
		session.AdvertisementInfo.StepSize,
	)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstreamPubkey,
			"error":           err,
		}).Warn("Renewal budget constraints not met, pausing session")

		session.mu.Lock()
		session.Status = SessionPaused
		session.UsageTracker.Stop()
		session.mu.Unlock()

		return err
	}

	// Send renewal payment
	sessionEvent, err := c.createAndSendPayment(session, proposal)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstreamPubkey,
			"error":           err,
		}).Error("Failed to send renewal payment")
		return err
	}

	// Extract new allotment from session event response
	newAllotment, err := c.extractAllotment(sessionEvent)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": upstreamPubkey,
			"error":           err,
		}).Error("Failed to extract allotment from session event")
		return err
	}

	// Update session
	session.mu.Lock()
	session.TotalAllotment = newAllotment
	session.LastRenewalAt = time.Now()
	session.LastPaymentAt = time.Now()
	session.TotalSpent += proposal.Steps * proposal.PricingOption.PricePerStep
	session.PaymentCount++

	if session.UsageTracker != nil {
		err := session.UsageTracker.SessionChanged(session)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"upstream_pubkey": upstreamPubkey,
				"error":           err,
			}).Error("Failed to update usage tracker with session changes")
		}
	}
	session.mu.Unlock()

	logger.WithFields(logrus.Fields{
		"upstream_pubkey": upstreamPubkey,
		"new_allotment":   newAllotment,
		"current_usage":   currentUsage,
		"total_spent":     session.TotalSpent,
	}).Info("Session renewed successfully")

	return nil
}

// GetActiveSessions returns all currently active sessions
func (c *Chandler) GetActiveSessions() map[string]*ChandlerSession {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*ChandlerSession)
	for k, v := range c.sessions {
		if v.Status == SessionActive {
			result[k] = v
		}
	}
	return result
}

// GetSessionByPubkey returns a session by upstream pubkey
func (c *Chandler) GetSessionByPubkey(pubkey string) (*ChandlerSession, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	session, exists := c.sessions[pubkey]
	if !exists {
		return nil, fmt.Errorf("session not found for pubkey: %s", pubkey)
	}
	return session, nil
}

// PauseSession pauses a session
func (c *Chandler) PauseSession(pubkey string) error {
	c.mu.RLock()
	session, exists := c.sessions[pubkey]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("session not found for pubkey: %s", pubkey)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.UsageTracker != nil {
		session.UsageTracker.Stop()
	}
	session.Status = SessionPaused

	logger.WithField("upstream_pubkey", pubkey).Info("Session paused")
	return nil
}

// ResumeSession resumes a paused session
func (c *Chandler) ResumeSession(pubkey string) error {
	c.mu.RLock()
	session, exists := c.sessions[pubkey]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("session not found for pubkey: %s", pubkey)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	err := c.createUsageTracker(session)
	if err != nil {
		return fmt.Errorf("failed to restart usage tracker: %w", err)
	}

	session.Status = SessionActive

	logger.WithField("upstream_pubkey", pubkey).Info("Session resumed")
	return nil
}

// TerminateSession terminates a session
func (c *Chandler) TerminateSession(pubkey string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	session, exists := c.sessions[pubkey]
	if !exists {
		return fmt.Errorf("session not found for pubkey: %s", pubkey)
	}

	if session.UsageTracker != nil {
		session.UsageTracker.Stop()
	}

	delete(c.sessions, pubkey)

	logger.WithField("upstream_pubkey", pubkey).Info("Session terminated")
	return nil
}

// createAndSendPayment creates a payment event and sends it to the upstream TollGate
func (c *Chandler) createAndSendPayment(session *ChandlerSession, proposal *PaymentProposal) (*nostr.Event, error) {
	// Create payment token through merchant
	paymentAmount := proposal.Steps * proposal.PricingOption.PricePerStep
	paymentToken, err := c.merchant.CreatePaymentTokenWithOverpayment(proposal.PricingOption.MintURL, paymentAmount, 10000, 100)
	if err != nil {
		return nil, fmt.Errorf("failed to create payment token: %w", err)
	}

	// Get customer private key from session
	customerPrivateKey := session.CustomerPrivateKey
	customerPublicKey, _ := nostr.GetPublicKey(customerPrivateKey)

	// Get MAC address from session's upstream tollgate object
	macAddress := session.UpstreamTollgate.MacAddress

	// Create payment event
	paymentEvent := nostr.Event{
		Kind:      21000,
		PubKey:    customerPublicKey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", proposal.UpstreamPubkey},
			{"device-identifier", "mac", macAddress},
			{"payment", paymentToken},
		},
		Content: "",
	}

	// Sign with customer identity
	err = paymentEvent.Sign(customerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign payment event: %w", err)
	}

	// Send payment to upstream TollGate
	sessionEvent, err := c.sendPaymentToUpstream(&paymentEvent, session.UpstreamTollgate.GatewayIP)
	if err != nil {
		return nil, fmt.Errorf("failed to send payment to upstream: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"upstream_pubkey": proposal.UpstreamPubkey,
		"amount":          paymentAmount,
		"steps":           proposal.Steps,
		"reason":          proposal.Reason,
	}).Info("Payment sent successfully to upstream TollGate")

	return sessionEvent, nil
}

// sendPaymentToUpstream sends a payment event to an upstream TollGate and returns the session event
func (c *Chandler) sendPaymentToUpstream(paymentEvent *nostr.Event, gatewayIP string) (*nostr.Event, error) {
	// Marshal payment event to JSON
	paymentBytes, err := json.Marshal(paymentEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payment event: %w", err)
	}

	// Send HTTP POST to upstream TollGate (TIP-03)
	url := fmt.Sprintf("http://%s:2121/", gatewayIP)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request with proper headers
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(paymentBytes))
	req.Close = true

	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TollGate-Chandler/1.0")

	logger.WithFields(logrus.Fields{
		"url":          url,
		"payload_size": len(paymentBytes),
	}).Info("Sending payment to upstream TollGate")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for logging
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"url":         url,
			"status_code": resp.StatusCode,
			"error":       err,
		}).Error("Failed to read response body")
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"url":           url,
		"status_code":   resp.StatusCode,
		"response_size": len(responseBody),
		"response_body": string(responseBody),
	}).Info("Received response from upstream TollGate")

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream TollGate rejected payment with status: %d, body: %s", resp.StatusCode, string(responseBody))
	}

	// Parse session event from response
	var sessionEvent nostr.Event
	err = json.Unmarshal(responseBody, &sessionEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to decode session event: %w", err)
	}

	// Validate session event
	if sessionEvent.Kind != 1022 {
		return nil, fmt.Errorf("invalid session event kind: %d", sessionEvent.Kind)
	}

	return &sessionEvent, nil
}

// createUsageTracker creates and starts the appropriate usage tracker for a session
func (c *Chandler) createUsageTracker(session *ChandlerSession) error {
	var tracker UsageTrackerInterface
	var trackerType string

	switch session.AdvertisementInfo.Metric {
	case "milliseconds":
		tracker = NewTimeUsageTracker()
		trackerType = "time-based"
	case "bytes":
		tracker = NewDataUsageTracker(session.UpstreamTollgate.InterfaceName)
		trackerType = "data-based"
	default:
		return fmt.Errorf("unsupported metric: %s", session.AdvertisementInfo.Metric)
	}

	logger.WithFields(logrus.Fields{
		"upstream_pubkey":    session.UpstreamTollgate.Advertisement.PubKey,
		"tracker_type":       trackerType,
		"metric":             session.AdvertisementInfo.Metric,
		"interface":          session.UpstreamTollgate.InterfaceName,
		"total_allotment":    session.TotalAllotment,
		"renewal_thresholds": session.RenewalThresholds,
	}).Info("🔍 Creating usage tracker for session monitoring")

	// Set renewal thresholds
	err := tracker.SetRenewalThresholds(session.RenewalThresholds)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": session.UpstreamTollgate.Advertisement.PubKey,
			"error":           err,
		}).Error("Failed to set renewal thresholds")
		return fmt.Errorf("failed to set renewal thresholds: %w", err)
	}

	// Start tracking
	logger.WithFields(logrus.Fields{
		"upstream_pubkey": session.UpstreamTollgate.Advertisement.PubKey,
		"tracker_type":    trackerType,
	}).Info("▶️  Starting usage tracker monitoring")

	err = tracker.Start(session, c)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"upstream_pubkey": session.UpstreamTollgate.Advertisement.PubKey,
			"tracker_type":    trackerType,
			"error":           err,
		}).Error("Failed to start usage tracker")
		return fmt.Errorf("failed to start usage tracker: %w", err)
	}

	session.UsageTracker = tracker

	logger.WithFields(logrus.Fields{
		"upstream_pubkey": session.UpstreamTollgate.Advertisement.PubKey,
		"tracker_type":    trackerType,
		"metric":          session.AdvertisementInfo.Metric,
	}).Info("✅ Usage tracker successfully started and monitoring session")

	return nil
}

// extractAllotment extracts the allotment value from a session event
func (c *Chandler) extractAllotment(sessionEvent *nostr.Event) (uint64, error) {
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

// checkAdvertisementChanges compares the current advertisement with the latest from upstream
func (c *Chandler) checkAdvertisementChanges(session *ChandlerSession) {
	// TODO: Implement advertisement fetching and comparison
	// For now, we'll just log that we should check for changes
	logger.WithFields(logrus.Fields{
		"upstream_pubkey": session.UpstreamTollgate.Advertisement.PubKey,
	}).Debug("Should check for advertisement changes")
}

// selectCompatiblePricingOption finds the best pricing option that matches our available mints
func (c *Chandler) selectCompatiblePricingOption(upstreamOptions []tollgate_protocol.PricingOption) (*tollgate_protocol.PricingOption, error) {
	ourMints := c.merchant.GetAcceptedMints()

	// Create a map of our mint URLs with their units for quick lookup
	ourMintMap := make(map[string]string) // mint URL -> unit
	for _, mint := range ourMints {
		ourMintMap[mint.URL] = mint.PriceUnit
	}

	var compatibleOptions []tollgate_protocol.PricingOption

	// Find all compatible options (same mint URL and unit)
	for _, upstreamOption := range upstreamOptions {
		if ourUnit, exists := ourMintMap[upstreamOption.MintURL]; exists {
			if ourUnit == upstreamOption.PriceUnit {
				compatibleOptions = append(compatibleOptions, upstreamOption)
			}
		}
	}

	if len(compatibleOptions) == 0 {
		return nil, fmt.Errorf("no compatible mints found - upstream mints don't overlap with our accepted mints")
	}

	// Select the cheapest compatible option
	best := &compatibleOptions[0]
	for i := range compatibleOptions {
		if compatibleOptions[i].PricePerStep < best.PricePerStep {
			best = &compatibleOptions[i]
		}
	}

	logger.WithFields(logrus.Fields{
		"selected_mint":      best.MintURL,
		"selected_unit":      best.PriceUnit,
		"price_per_step":     best.PricePerStep,
		"compatible_options": len(compatibleOptions),
	}).Debug("Selected compatible pricing option")

	return best, nil
}
