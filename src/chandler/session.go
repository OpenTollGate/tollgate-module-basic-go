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

// UpstreamSession represents a single upstream gateway session
// Simplified: only tracks gateway IP and usage, no customer identity needed
type UpstreamSession struct {
	// Session identification (minimal)
	GatewayIP string // The only identifier we need

	// Advertisement and pricing
	Advertisement     *nostr.Event
	AdvertisementInfo *tollgate_protocol.AdvertisementInfo
	SelectedPricing   *tollgate_protocol.PricingOption

	// Session state
	TotalAllotment uint64
	RenewalOffset  uint64
	CreatedAt      time.Time
	LastPaymentAt  time.Time
	TotalSpent     uint64
	PaymentCount   int
	Status         SessionStatus

	// Usage tracking
	UsageTracker *UpstreamUsageTracker
	trackerMu    sync.Mutex // Protects tracker creation/stopping

	// Dependencies
	configManager *config_manager.ConfigManager
	merchant      merchant.MerchantInterface
}

// NewUpstreamSession creates a new upstream session and starts tracking
// The tracker will automatically trigger initial payment when it sees -1/-1
// Session handles pricing selection and renewal offset determination
func NewUpstreamSession(
	gatewayIP string,
	interfaceName string,
	advertisement *nostr.Event,
	adInfo *tollgate_protocol.AdvertisementInfo,
	configManager *config_manager.ConfigManager,
	merchantImpl merchant.MerchantInterface,
) (*UpstreamSession, error) {
	// Select compatible pricing option
	selectedPricing, err := selectCompatiblePricing(adInfo.PricingOptions, merchantImpl)
	if err != nil {
		return nil, fmt.Errorf("no compatible pricing: %w", err)
	}

	// Get renewal offset from config based on metric
	config := configManager.GetConfig()
	var renewalOffset uint64
	switch adInfo.Metric {
	case "milliseconds":
		renewalOffset = config.Chandler.Sessions.MillisecondRenewalOffset
	case "bytes":
		renewalOffset = config.Chandler.Sessions.BytesRenewalOffset
	default:
		return nil, fmt.Errorf("unsupported metric: %s", adInfo.Metric)
	}

	session := &UpstreamSession{
		GatewayIP:         gatewayIP,
		Advertisement:     advertisement,
		AdvertisementInfo: adInfo,
		SelectedPricing:   selectedPricing,
		TotalAllotment:    0, // Will be set by first payment
		RenewalOffset:     renewalOffset,
		CreatedAt:         time.Now(),
		LastPaymentAt:     time.Time{}, // Not paid yet
		TotalSpent:        0,
		PaymentCount:      0,
		Status:            SessionActive,
		UsageTracker:      nil,
		configManager:     configManager,
		merchant:          merchantImpl,
	}

	// Start tracker immediately - it will handle initial payment via renewal
	if err := session.StartUsageTracker(interfaceName); err != nil {
		return nil, fmt.Errorf("failed to start usage tracker: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"gateway": gatewayIP,
		"metric":  adInfo.Metric,
	}).Info("✅ NEW SESSION: Created and started tracking")

	return session, nil
}

// StartUsageTracker creates and starts the usage tracker for this session
// This should only be called ONCE per session (protected by mutex)
func (s *UpstreamSession) StartUsageTracker(interfaceName string) error {
	s.trackerMu.Lock()
	defer s.trackerMu.Unlock()

	// Prevent creating multiple trackers
	if s.UsageTracker != nil {
		logger.WithField("gateway", s.GatewayIP).Warn("⚠️  Usage tracker already exists, not creating new one")
		return nil
	}

	logger.WithFields(logrus.Fields{
		"gateway":         s.GatewayIP,
		"metric":          s.AdvertisementInfo.Metric,
		"total_allotment": s.TotalAllotment,
		"renewal_offset":  s.RenewalOffset,
		"interface":       interfaceName,
		"tracker_type":    fmt.Sprintf("%s-based", s.AdvertisementInfo.Metric),
	}).Info("🔍 Creating usage tracker for session monitoring")

	// Create renewal callback that calls our HandleRenewal method
	renewalCallback := func(gatewayIP string, currentUsage uint64) error {
		return s.HandleRenewal(currentUsage)
	}

	// Create the new unified upstream usage tracker
	// It works for both time and data metrics by polling :2121/usage
	tracker := NewUpstreamUsageTracker(
		s.GatewayIP,
		s.RenewalOffset,
		renewalCallback,
	)

	// Start the tracker
	err := tracker.Start()
	if err != nil {
		return fmt.Errorf("failed to start usage tracker: %w", err)
	}

	s.UsageTracker = tracker

	logger.WithFields(logrus.Fields{
		"gateway":      s.GatewayIP,
		"metric":       s.AdvertisementInfo.Metric,
		"tracker_type": fmt.Sprintf("%s-based", s.AdvertisementInfo.Metric),
	}).Info("✅ Usage tracker successfully started and monitoring session")

	return nil
}

// StopUsageTracker stops the usage tracker if it exists
func (s *UpstreamSession) StopUsageTracker() {
	s.trackerMu.Lock()
	defer s.trackerMu.Unlock()

	if s.UsageTracker != nil {
		s.UsageTracker.Stop()
		s.UsageTracker = nil
		logger.WithField("gateway", s.GatewayIP).Info("⏹️  Usage tracker stopped")
	}
}

// HandleRenewal is called by the tracker when renewal is needed
// This handles both initial payment (-1/-1) and actual renewals
func (s *UpstreamSession) HandleRenewal(currentUsage uint64) error {
	logger.WithFields(logrus.Fields{
		"gateway":       s.GatewayIP,
		"current_usage": currentUsage,
		"allotment":     s.TotalAllotment,
	}).Info("💳 Processing payment request (initial or renewal)")

	// Re-evaluate pricing options (they may have changed)
	selectedPricing, err := selectCompatiblePricing(s.AdvertisementInfo.PricingOptions, s.merchant)
	if err != nil {
		return fmt.Errorf("no compatible pricing: %w", err)
	}
	s.SelectedPricing = selectedPricing

	// Calculate steps based on preferred increments
	config := s.configManager.GetConfig()
	var preferredAllotment uint64
	switch s.AdvertisementInfo.Metric {
	case "milliseconds":
		preferredAllotment = config.Chandler.Sessions.PreferredSessionIncrementsMilliseconds
	case "bytes":
		preferredAllotment = config.Chandler.Sessions.PreferredSessionIncrementsBytes
	}

	steps := preferredAllotment / s.AdvertisementInfo.StepSize
	if steps < selectedPricing.MinSteps {
		steps = selectedPricing.MinSteps
	}
	if steps == 0 {
		steps = 1
	}

	// Send payment
	allotment, err := s.sendPayment(steps)
	if err != nil {
		return fmt.Errorf("payment failed: %w", err)
	}

	// Update session state
	s.TotalAllotment = allotment
	s.LastPaymentAt = time.Now()
	s.PaymentCount++
	s.TotalSpent += steps * s.SelectedPricing.PricePerStep

	// Notify tracker of new allotment
	// TODO: Update tracker interface to accept UpstreamSession or just allotment value
	// For now, tracker will detect the change on next poll

	logger.WithFields(logrus.Fields{
		"gateway":       s.GatewayIP,
		"new_allotment": allotment,
		"payment_count": s.PaymentCount,
		"total_spent":   s.TotalSpent,
	}).Info("✅ Payment successful, session updated")

	return nil
}

// sendPayment sends a payment (initial or renewal) and returns the allotment
// Uses new simplified protocol: POST plain text Cashu token
func (s *UpstreamSession) sendPayment(steps uint64) (uint64, error) {
	// Create payment token
	amount := steps * s.SelectedPricing.PricePerStep
	token, err := s.merchant.CreatePaymentTokenWithOverpayment(
		s.SelectedPricing.MintURL,
		amount,
		10000, // overpayment tolerance
		100,   // overpayment buffer
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create payment token: %w", err)
	}

	// POST plain text token to upstream (new simplified protocol!)
	url := fmt.Sprintf("http://%s:2121/", s.GatewayIP)
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(token))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "text/plain")
	req.Close = true

	logger.WithFields(logrus.Fields{
		"gateway": s.GatewayIP,
		"amount":  amount,
		"steps":   steps,
	}).Info("💸 Sending payment to upstream")

	resp, err := client.Do(req)
	if err != nil {
		// Try to recover the token
		s.recoverToken(token, err)
		return 0, fmt.Errorf("failed to send payment: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.recoverToken(token, err)
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for error responses (notice events or HTTP errors)
	if resp.StatusCode != http.StatusOK {
		// Try to parse as notice event (kind 21023) to get error message
		var noticeEvent nostr.Event
		if json.Unmarshal(body, &noticeEvent) == nil && noticeEvent.Kind == 21023 {
			s.recoverToken(token, fmt.Errorf("payment rejected: %s", noticeEvent.Content))
			return 0, fmt.Errorf("payment rejected by upstream: %s", noticeEvent.Content)
		}

		// Generic error response
		s.recoverToken(token, fmt.Errorf("upstream rejected payment: %d", resp.StatusCode))
		return 0, fmt.Errorf("payment rejected: %d - %s", resp.StatusCode, string(body))
	}

	// Parse session event from response (kind 1022)
	var sessionEvent nostr.Event
	if err := json.Unmarshal(body, &sessionEvent); err != nil {
		return 0, fmt.Errorf("failed to parse session event: %w", err)
	}

	// Verify it's a session event
	if sessionEvent.Kind != 1022 {
		return 0, fmt.Errorf("unexpected event kind: %d (expected 1022)", sessionEvent.Kind)
	}

	// Extract allotment from session event tags
	var allotment uint64
	for _, tag := range sessionEvent.Tags {
		if len(tag) >= 2 && tag[0] == "allotment" {
			allotment, err = strconv.ParseUint(tag[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid allotment in session event: %s", tag[1])
			}
			break
		}
	}

	if allotment == 0 {
		return 0, fmt.Errorf("no allotment found in session event")
	}

	logger.WithFields(logrus.Fields{
		"gateway":   s.GatewayIP,
		"allotment": allotment,
	}).Info("✅ Payment accepted by upstream")

	return allotment, nil
}

// recoverToken attempts to recover a failed payment token
func (s *UpstreamSession) recoverToken(token string, originalErr error) {
	// Get mint URL from selected pricing
	mintURL := ""
	if s.SelectedPricing != nil {
		mintURL = s.SelectedPricing.MintURL
	}

	// Call the token recovery utility
	recoverFailedPaymentToken(s.merchant, token, mintURL, originalErr)
}

// Stop stops the session (stops tracker and marks as expired)
// This is the main cleanup method
func (s *UpstreamSession) Stop() {
	s.StopUsageTracker()
}

// selectCompatiblePricing finds a pricing option that matches our available mints
func selectCompatiblePricing(options []tollgate_protocol.PricingOption, merchantImpl merchant.MerchantInterface) (*tollgate_protocol.PricingOption, error) {
	ourMints := merchantImpl.GetAcceptedMints()

	for _, option := range options {
		for _, ourMint := range ourMints {
			if option.MintURL == ourMint.URL {
				logger.WithFields(logrus.Fields{
					"mint":           option.MintURL,
					"price_per_step": option.PricePerStep,
				}).Debug("Selected compatible pricing")
				return &option, nil
			}
		}
	}

	return nil, fmt.Errorf("no compatible mints found")
}

// GetIdentifier returns the session identifier (gateway IP)
func (s *UpstreamSession) GetIdentifier() string {
	return s.GatewayIP
}
