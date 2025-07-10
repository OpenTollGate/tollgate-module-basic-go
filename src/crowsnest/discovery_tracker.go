package crowsnest

import (
	"sync"
	"time"
)

// simpleDiscoveryTracker implements basic deduplication with timestamps
type simpleDiscoveryTracker struct {
	config       *CrowsnestConfig
	lastAttempts map[string]time.Time
	mu           sync.RWMutex
}

// NewDiscoveryTracker creates a new simple discovery tracker
func NewDiscoveryTracker(config *CrowsnestConfig) DiscoveryTracker {
	return &simpleDiscoveryTracker{
		config:       config,
		lastAttempts: make(map[string]time.Time),
	}
}

// ShouldAttemptDiscovery checks if enough time has passed since last attempt
func (dt *simpleDiscoveryTracker) ShouldAttemptDiscovery(interfaceName, gatewayIP string) bool {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	key := interfaceName + ":" + gatewayIP
	lastAttempt, exists := dt.lastAttempts[key]

	if !exists {
		return true // Never attempted before
	}

	// Allow retry after discovery timeout (default 5 minutes)
	return time.Since(lastAttempt) > dt.config.DiscoveryTimeout
}

// RecordDiscovery records when a discovery attempt was made
func (dt *simpleDiscoveryTracker) RecordDiscovery(interfaceName, gatewayIP string, result DiscoveryResult) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	key := interfaceName + ":" + gatewayIP
	dt.lastAttempts[key] = time.Now()
}

// Cleanup clears all recorded attempts
func (dt *simpleDiscoveryTracker) Cleanup() {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	dt.lastAttempts = make(map[string]time.Time)
}
