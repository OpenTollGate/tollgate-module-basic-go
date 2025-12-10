package crowsnest

import (
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/sirupsen/logrus"
)

// simpleDiscoveryTracker implements basic deduplication with timestamps and results
type simpleDiscoveryTracker struct {
	config       *config_manager.CrowsnestConfig
	lastAttempts map[string]DiscoveryAttempt
	mu           sync.RWMutex
}

// NewDiscoveryTracker creates a new simple discovery tracker
func NewDiscoveryTracker(config *config_manager.CrowsnestConfig) DiscoveryTracker {
	return &simpleDiscoveryTracker{
		config:       config,
		lastAttempts: make(map[string]DiscoveryAttempt),
	}
}

// ShouldAttemptDiscovery checks if discovery should be attempted based on previous results
func (dt *simpleDiscoveryTracker) ShouldAttemptDiscovery(interfaceName, gatewayIP string) bool {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	key := interfaceName + ":" + gatewayIP
	lastAttempt, exists := dt.lastAttempts[key]

	if !exists {
		return true // Never attempted before
	}

	// If it was successfully discovered as a TollGate, NEVER retry
	// This is the final state - it's been handed off to the chandler
	if lastAttempt.Result == DiscoveryResultSuccess {
		return false
	}

	// If discovery is currently pending, don't start another attempt
	if lastAttempt.Result == DiscoveryResultPending {
		// Allow retry only if pending attempt is taking too long (timeout)
		return time.Since(lastAttempt.AttemptTime) > dt.config.ProbeTimeout*2
	}

	// For other results (errors, validation failures), allow retry after discovery timeout
	return time.Since(lastAttempt.AttemptTime) > dt.config.DiscoveryTimeout
}

// RecordDiscovery records when a discovery attempt was made with its result
func (dt *simpleDiscoveryTracker) RecordDiscovery(interfaceName, gatewayIP string, result DiscoveryResult) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	key := interfaceName + ":" + gatewayIP
	dt.lastAttempts[key] = DiscoveryAttempt{
		InterfaceName: interfaceName,
		GatewayIP:     gatewayIP,
		AttemptTime:   time.Now(),
		Result:        result,
	}
}

// ClearInterface removes all discovery attempts for a specific interface
func (dt *simpleDiscoveryTracker) ClearInterface(interfaceName string) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	logger.WithField("interface", interfaceName).Info("DiscoveryTracker: Clearing all discovery attempts for interface")
	// Remove all attempts for this interface
	deletedCount := 0
	for key, attempt := range dt.lastAttempts {
		if attempt.InterfaceName == interfaceName {
			delete(dt.lastAttempts, key)
			deletedCount++
		}
	}
	logger.WithFields(logrus.Fields{
		"interface":     interfaceName,
		"deleted_count": deletedCount,
	}).Info("DiscoveryTracker: Finished clearing interface")
}

// Cleanup clears all recorded attempts
func (dt *simpleDiscoveryTracker) Cleanup() {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	dt.lastAttempts = make(map[string]DiscoveryAttempt)
}
