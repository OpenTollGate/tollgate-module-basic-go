package chandler

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Module-level logger with pre-configured module field
var logger = logrus.WithField("module", "chandler")

// loggerChandler is a simple implementation of ChandlerInterface that just logs messages
type loggerChandler struct {
	mu                sync.RWMutex
	upstreamTollgates map[string]*UpstreamTollgate // keyed by interface name
}

// NewLoggerChandler creates a new logger-based chandler implementation
func NewLoggerChandler() ChandlerInterface {
	return &loggerChandler{
		upstreamTollgates: make(map[string]*UpstreamTollgate),
	}
}

// HandleUpstreamTollgate handles a discovered upstream TollGate
func (c *loggerChandler) HandleUpstreamTollgate(upstream *UpstreamTollgate) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Store the upstream TollGate
	c.upstreamTollgates[upstream.InterfaceName] = upstream

	// Log the connection
	logger.WithFields(logrus.Fields{
		"interface":     upstream.InterfaceName,
		"gateway":       upstream.GatewayIP,
		"mac_address":   upstream.MacAddress,
		"discovered_at": upstream.DiscoveredAt.Format(time.RFC3339),
	}).Info("🔗 CONNECTED: TollGate discovered")

	// Log some additional details about the advertisement if available
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
func (c *loggerChandler) HandleDisconnect(interfaceName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we had an upstream TollGate on this interface
	if upstream, exists := c.upstreamTollgates[interfaceName]; exists {
		// Log the disconnection
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   upstream.GatewayIP,
		}).Info("❌ DISCONNECTED: TollGate disconnected")

		// Remove from our tracking
		delete(c.upstreamTollgates, interfaceName)
	} else {
		// Interface went down but we didn't have a TollGate connection
		logger.WithField("interface", interfaceName).Info("⬇️ INTERFACE DOWN: Interface disconnected (no TollGate connection)")
	}

	return nil
}

// GetUpstreamTollgates returns all currently tracked upstream TollGates
// This is a utility method for monitoring/debugging
func (c *loggerChandler) GetUpstreamTollgates() map[string]*UpstreamTollgate {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make(map[string]*UpstreamTollgate)
	for k, v := range c.upstreamTollgates {
		result[k] = v
	}
	return result
}

// GetStats returns basic statistics about the chandler
func (c *loggerChandler) GetStats() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]int{
		"active_connections": len(c.upstreamTollgates),
	}
}
