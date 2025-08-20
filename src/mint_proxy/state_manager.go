package mint_proxy

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// StateManager manages mint requests tied to MAC addresses
type StateManager struct {
	requests      map[string]*MintRequest // requestID -> MintRequest
	macToRequests map[string][]string     // macAddress -> []requestID
	mutex         sync.RWMutex
	logger        *logrus.Entry
	cleanupTicker *time.Ticker
	stopCleanup   chan bool
}

// NewStateManager creates a new state manager instance
func NewStateManager() *StateManager {
	logger := logrus.WithField("module", "mint_proxy.state_manager")

	sm := &StateManager{
		requests:      make(map[string]*MintRequest),
		macToRequests: make(map[string][]string),
		logger:        logger,
		stopCleanup:   make(chan bool),
	}

	// Start cleanup routine
	sm.startCleanupRoutine()

	return sm
}

// CreateRequest creates a new mint request for the given MAC address
func (sm *StateManager) CreateRequest(macAddress, mintURL string, amount uint64) *MintRequest {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	requestID := uuid.New().String()
	now := time.Now()

	request := &MintRequest{
		RequestID:  requestID,
		MACAddress: macAddress,
		MintURL:    mintURL,
		Amount:     amount,
		Status:     StatusPending,
		CreatedAt:  now,
		ExpiresAt:  now.Add(DefaultRequestTimeout),
	}

	// Store request
	sm.requests[requestID] = request

	// Track by MAC address
	if sm.macToRequests[macAddress] == nil {
		sm.macToRequests[macAddress] = make([]string, 0)
	}
	sm.macToRequests[macAddress] = append(sm.macToRequests[macAddress], requestID)

	sm.logger.WithFields(logrus.Fields{
		"request_id":  requestID,
		"mac_address": macAddress,
		"mint_url":    mintURL,
		"amount":      amount,
	}).Debug("Created new mint request")

	return request
}

// GetRequest retrieves a request by ID
func (sm *StateManager) GetRequest(requestID string) (*MintRequest, bool) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	request, exists := sm.requests[requestID]
	return request, exists
}

// UpdateRequestStatus updates the status of a request
func (sm *StateManager) UpdateRequestStatus(requestID, status string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	request, exists := sm.requests[requestID]
	if !exists {
		return ErrRequestNotFound
	}

	oldStatus := request.Status
	request.Status = status

	sm.logger.WithFields(logrus.Fields{
		"request_id": requestID,
		"old_status": oldStatus,
		"new_status": status,
	}).Debug("Updated request status")

	return nil
}

// SetInvoice sets the invoice for a request
func (sm *StateManager) SetInvoice(requestID, invoice string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	request, exists := sm.requests[requestID]
	if !exists {
		return ErrRequestNotFound
	}

	request.Invoice = invoice
	request.Status = StatusInvoiceReady

	sm.logger.WithFields(logrus.Fields{
		"request_id": requestID,
		"invoice":    invoice[:20] + "...", // Log only first 20 chars for security
	}).Debug("Set invoice for request")

	return nil
}

// SetTokens sets the tokens for a request and marks it as delivered
func (sm *StateManager) SetTokens(requestID, tokens string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	request, exists := sm.requests[requestID]
	if !exists {
		return ErrRequestNotFound
	}

	request.Tokens = tokens
	request.Status = StatusTokensDelivered

	sm.logger.WithFields(logrus.Fields{
		"request_id": requestID,
		"tokens":     tokens[:20] + "...", // Log only first 20 chars for security
	}).Debug("Set tokens for request")

	return nil
}

// GetRequestsByMAC retrieves all requests for a given MAC address
func (sm *StateManager) GetRequestsByMAC(macAddress string) []*MintRequest {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	requestIDs := sm.macToRequests[macAddress]
	if requestIDs == nil {
		return nil
	}

	requests := make([]*MintRequest, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		if request, exists := sm.requests[requestID]; exists {
			requests = append(requests, request)
		}
	}

	return requests
}

// GetActiveRequests returns all requests that are not expired or completed
func (sm *StateManager) GetActiveRequests() []*MintRequest {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	var activeRequests []*MintRequest
	now := time.Now()

	for _, request := range sm.requests {
		if request.Status != StatusTokensDelivered &&
			request.Status != StatusExpired &&
			request.Status != StatusError &&
			request.ExpiresAt.After(now) {
			activeRequests = append(activeRequests, request)
		}
	}

	return activeRequests
}

// RemoveRequest removes a request from the state manager
func (sm *StateManager) RemoveRequest(requestID string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	request, exists := sm.requests[requestID]
	if !exists {
		return
	}

	// Remove from requests map
	delete(sm.requests, requestID)

	// Remove from MAC address tracking
	macAddress := request.MACAddress
	if requestIDs := sm.macToRequests[macAddress]; requestIDs != nil {
		// Filter out the request ID
		filteredIDs := make([]string, 0, len(requestIDs)-1)
		for _, id := range requestIDs {
			if id != requestID {
				filteredIDs = append(filteredIDs, id)
			}
		}

		if len(filteredIDs) == 0 {
			delete(sm.macToRequests, macAddress)
		} else {
			sm.macToRequests[macAddress] = filteredIDs
		}
	}

	sm.logger.WithFields(logrus.Fields{
		"request_id":  requestID,
		"mac_address": macAddress,
	}).Debug("Removed request from state")
}

// startCleanupRoutine starts the background cleanup routine
func (sm *StateManager) startCleanupRoutine() {
	sm.cleanupTicker = time.NewTicker(DefaultCleanupInterval)

	go func() {
		for {
			select {
			case <-sm.cleanupTicker.C:
				sm.cleanupExpiredRequests()
			case <-sm.stopCleanup:
				sm.cleanupTicker.Stop()
				return
			}
		}
	}()

	sm.logger.Info("Started cleanup routine")
}

// cleanupExpiredRequests removes expired requests from memory
func (sm *StateManager) cleanupExpiredRequests() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	now := time.Now()
	var expiredIDs []string

	for requestID, request := range sm.requests {
		if request.ExpiresAt.Before(now) &&
			request.Status != StatusTokensDelivered {
			expiredIDs = append(expiredIDs, requestID)
		}
	}

	for _, requestID := range expiredIDs {
		request := sm.requests[requestID]
		request.Status = StatusExpired

		// Remove from tracking after marking as expired
		sm.RemoveRequest(requestID)
	}

	if len(expiredIDs) > 0 {
		sm.logger.WithField("expired_count", len(expiredIDs)).Debug("Cleaned up expired requests")
	}
}

// Stop stops the cleanup routine and releases resources
func (sm *StateManager) Stop() {
	close(sm.stopCleanup)
	sm.logger.Info("State manager stopped")
}

// GetStats returns statistics about the state manager
func (sm *StateManager) GetStats() map[string]interface{} {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	stats := make(map[string]interface{})
	stats["total_requests"] = len(sm.requests)
	stats["unique_mac_addresses"] = len(sm.macToRequests)

	statusCounts := make(map[string]int)
	for _, request := range sm.requests {
		statusCounts[request.Status]++
	}
	stats["status_counts"] = statusCounts

	return stats
}

// Custom errors
var (
	ErrRequestNotFound = fmt.Errorf("request not found")
)
