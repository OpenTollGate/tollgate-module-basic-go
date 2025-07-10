package upstream_session_manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

// UpstreamSession represents the current upstream session state
type UpstreamSession struct {
	Event         *nostr.Event `json:"event"`
	Allotment     uint64       `json:"allotment"`
	Metric        string       `json:"metric"`
	CreatedAt     time.Time    `json:"created_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	DeviceID      string       `json:"device_id"`
	IsActive      bool         `json:"is_active"`
	ConsumedBytes uint64       `json:"consumed_bytes,omitempty"` // For byte-based metrics
}

// UpstreamSessionManager manages upstream session state and purchases
type UpstreamSessionManager struct {
	currentSession   *UpstreamSession
	upstreamURL      string
	macAddressSelf   string
	configManager    *config_manager.ConfigManager
	sessionMutex     sync.RWMutex
	httpClient       *http.Client
	dataUsageTracker *DataUsageTracker // For byte-based monitoring
}

// DataUsageTracker tracks data consumption for byte-based sessions
type DataUsageTracker struct {
	totalConsumed uint64
	lastCheck     time.Time
	isActive      bool
	mutex         sync.RWMutex
}

// New creates a new UpstreamSessionManager instance
func New(upstreamURL, macAddressSelf string, configManager *config_manager.ConfigManager) *UpstreamSessionManager {
	return &UpstreamSessionManager{
		upstreamURL:    upstreamURL,
		macAddressSelf: macAddressSelf,
		configManager:  configManager,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		dataUsageTracker: &DataUsageTracker{
			lastCheck: time.Now(),
		},
	}
}

// PurchaseUpstreamTime executes payment to upstream router using tokens from merchant
func (usm *UpstreamSessionManager) PurchaseUpstreamTime(amount uint64, paymentToken string) (*nostr.Event, error) {
	if usm.upstreamURL == "" {
		return nil, fmt.Errorf("no upstream URL configured")
	}

	log.Printf("UpstreamSessionManager: Purchasing %d units from upstream %s", amount, usm.upstreamURL)

	// Create payment event (Kind 21000)
	paymentEvent := nostr.Event{
		Kind:      21000,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"device-identifier", "mac", usm.macAddressSelf},
			{"payment", paymentToken},
		},
		Content: "",
	}

	// Sign the payment event with tollgate private key
	config, err := usm.configManager.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config for signing: %w", err)
	}

	err = paymentEvent.Sign(config.TollgatePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign payment event: %w", err)
	}

	log.Printf("UpstreamSessionManager: Signed payment event with ID %s", paymentEvent.ID)

	// Send payment to upstream router (TIP-03)
	sessionEvent, err := usm.sendPaymentToUpstream(paymentEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to send payment to upstream: %w", err)
	}

	// Update current session state
	err = usm.updateSessionFromEvent(sessionEvent)
	if err != nil {
		log.Printf("Warning: Failed to update session state: %v", err)
	}

	log.Printf("UpstreamSessionManager: Successfully purchased upstream time, session expires at %v",
		usm.currentSession.ExpiresAt)

	return sessionEvent, nil
}

// GetUpstreamSessionInfo returns current upstream session details
func (usm *UpstreamSessionManager) GetUpstreamSessionInfo() (*UpstreamSession, error) {
	usm.sessionMutex.RLock()
	defer usm.sessionMutex.RUnlock()

	if usm.currentSession == nil {
		return nil, fmt.Errorf("no active upstream session")
	}

	// Return a copy to prevent external modification
	sessionCopy := *usm.currentSession
	return &sessionCopy, nil
}

// IsUpstreamActive checks if upstream session is still valid
func (usm *UpstreamSessionManager) IsUpstreamActive() bool {
	usm.sessionMutex.RLock()
	defer usm.sessionMutex.RUnlock()

	if usm.currentSession == nil {
		return false
	}

	now := time.Now()

	// Check if session has expired (time-based)
	if usm.currentSession.Metric == "milliseconds" {
		return now.Before(usm.currentSession.ExpiresAt)
	}

	// Check if session has remaining data (byte-based)
	if usm.currentSession.Metric == "bytes" {
		return usm.currentSession.ConsumedBytes < usm.currentSession.Allotment
	}

	return false
}

// GetTimeUntilExpiry returns time until upstream session expires (time-based metrics only)
func (usm *UpstreamSessionManager) GetTimeUntilExpiry() (uint64, error) {
	usm.sessionMutex.RLock()
	defer usm.sessionMutex.RUnlock()

	if usm.currentSession == nil {
		return 0, fmt.Errorf("no active upstream session")
	}

	if usm.currentSession.Metric != "milliseconds" {
		return 0, fmt.Errorf("session metric is not time-based")
	}

	now := time.Now()
	if now.After(usm.currentSession.ExpiresAt) {
		return 0, nil // Already expired
	}

	remaining := usm.currentSession.ExpiresAt.Sub(now)
	return uint64(remaining.Milliseconds()), nil
}

// GetBytesRemaining returns bytes remaining in upstream session (data-based metrics only)
func (usm *UpstreamSessionManager) GetBytesRemaining() (uint64, error) {
	usm.sessionMutex.RLock()
	defer usm.sessionMutex.RUnlock()

	if usm.currentSession == nil {
		return 0, fmt.Errorf("no active upstream session")
	}

	if usm.currentSession.Metric != "bytes" {
		return 0, fmt.Errorf("session metric is not data-based")
	}

	if usm.currentSession.ConsumedBytes >= usm.currentSession.Allotment {
		return 0, nil // No bytes remaining
	}

	return usm.currentSession.Allotment - usm.currentSession.ConsumedBytes, nil
}

// GetCurrentMetric returns the metric type of the upstream session
func (usm *UpstreamSessionManager) GetCurrentMetric() (string, error) {
	usm.sessionMutex.RLock()
	defer usm.sessionMutex.RUnlock()

	if usm.currentSession == nil {
		return "", fmt.Errorf("no active upstream session")
	}

	return usm.currentSession.Metric, nil
}

// MonitorDataUsage tracks data consumption for byte-based upstream sessions
func (usm *UpstreamSessionManager) MonitorDataUsage() error {
	if usm.currentSession == nil || usm.currentSession.Metric != "bytes" {
		return fmt.Errorf("no active byte-based session to monitor")
	}

	// TODO: Implement actual data usage tracking
	// This would integrate with system network monitoring tools
	// For now, this is a placeholder

	usm.dataUsageTracker.mutex.Lock()
	defer usm.dataUsageTracker.mutex.Unlock()

	// Placeholder: Simulate data consumption tracking
	// In a real implementation, this would:
	// 1. Query system network interface statistics
	// 2. Calculate data used since last check
	// 3. Update consumed bytes counter

	log.Printf("UpstreamSessionManager: Monitoring data usage (placeholder)")
	usm.dataUsageTracker.lastCheck = time.Now()

	return nil
}

// SetUpstreamURL updates the upstream router URL
func (usm *UpstreamSessionManager) SetUpstreamURL(url string) {
	usm.upstreamURL = url
	log.Printf("UpstreamSessionManager: Upstream URL updated to %s", url)
}

// SetMacAddressSelf updates the MAC address of this device
func (usm *UpstreamSessionManager) SetMacAddressSelf(macAddress string) {
	usm.macAddressSelf = macAddress
	log.Printf("UpstreamSessionManager: MAC address updated to %s", macAddress)
}

// sendPaymentToUpstream sends a payment event to the upstream router via HTTP
func (usm *UpstreamSessionManager) sendPaymentToUpstream(paymentEvent nostr.Event) (*nostr.Event, error) {
	// Serialize payment event to JSON
	paymentJSON, err := json.Marshal(paymentEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payment event: %w", err)
	}

	log.Printf("UpstreamSessionManager: Sending payment to %s", usm.upstreamURL)

	// Send POST request to upstream router (TIP-03)
	resp, err := usm.httpClient.Post(usm.upstreamURL, "application/json", bytes.NewBuffer(paymentJSON))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode == http.StatusBadRequest {
		// Try to parse as notice event (error response)
		var noticeEvent nostr.Event
		if err := json.Unmarshal(body, &noticeEvent); err == nil && noticeEvent.Kind == 21023 {
			return nil, fmt.Errorf("upstream payment failed: %s", noticeEvent.Content)
		}
		return nil, fmt.Errorf("upstream returned error status %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	// Parse response as session event
	var sessionEvent nostr.Event
	err = json.Unmarshal(body, &sessionEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse session event: %w", err)
	}

	// Verify it's a session event (Kind 1022)
	if sessionEvent.Kind != 1022 {
		return nil, fmt.Errorf("expected session event (kind 1022), got %d", sessionEvent.Kind)
	}

	// Verify event signature
	ok, err := sessionEvent.CheckSignature()
	if err != nil || !ok {
		return nil, fmt.Errorf("invalid session event signature")
	}

	log.Printf("UpstreamSessionManager: Successfully received session event from upstream")
	return &sessionEvent, nil
}

// updateSessionFromEvent updates the current session state from a session event
func (usm *UpstreamSessionManager) updateSessionFromEvent(sessionEvent *nostr.Event) error {
	usm.sessionMutex.Lock()
	defer usm.sessionMutex.Unlock()

	// Extract session information from event tags
	var allotment uint64
	var metric string
	var deviceID string

	for _, tag := range sessionEvent.Tags {
		if len(tag) < 2 {
			continue
		}

		switch tag[0] {
		case "allotment":
			if _, err := fmt.Sscanf(tag[1], "%d", &allotment); err != nil {
				return fmt.Errorf("failed to parse allotment: %w", err)
			}

		case "metric":
			metric = tag[1]

		case "device-identifier":
			if len(tag) >= 3 {
				deviceID = tag[2]
			}
		}
	}

	// Validate required fields
	if allotment == 0 {
		return fmt.Errorf("session event missing allotment")
	}
	if metric == "" {
		return fmt.Errorf("session event missing metric")
	}

	// Create session object
	createdAt := time.Unix(int64(sessionEvent.CreatedAt), 0)
	var expiresAt time.Time

	if metric == "milliseconds" {
		expiresAt = createdAt.Add(time.Duration(allotment) * time.Millisecond)
	} else {
		// For byte-based metrics, there's no time-based expiration
		expiresAt = createdAt.Add(24 * time.Hour) // Set a reasonable default
	}

	usm.currentSession = &UpstreamSession{
		Event:         sessionEvent,
		Allotment:     allotment,
		Metric:        metric,
		CreatedAt:     createdAt,
		ExpiresAt:     expiresAt,
		DeviceID:      deviceID,
		IsActive:      true,
		ConsumedBytes: 0, // Start with no consumption
	}

	// Initialize data tracking for byte-based sessions
	if metric == "bytes" {
		usm.dataUsageTracker.mutex.Lock()
		usm.dataUsageTracker.totalConsumed = 0
		usm.dataUsageTracker.isActive = true
		usm.dataUsageTracker.lastCheck = time.Now()
		usm.dataUsageTracker.mutex.Unlock()
	}

	return nil
}

// ClearSession clears the current upstream session (used when connection is lost)
func (usm *UpstreamSessionManager) ClearSession() {
	usm.sessionMutex.Lock()
	defer usm.sessionMutex.Unlock()

	usm.currentSession = nil

	// Deactivate data usage tracking
	usm.dataUsageTracker.mutex.Lock()
	usm.dataUsageTracker.isActive = false
	usm.dataUsageTracker.mutex.Unlock()

	log.Printf("UpstreamSessionManager: Cleared upstream session")
}
