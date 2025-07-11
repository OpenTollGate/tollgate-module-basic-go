package chandler

import (
	"log"
	"sync"
	"time"
)

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
	log.Printf("🔗 CONNECTED: TollGate discovered on interface %s (gateway: %s, mac: %s) at %s",
		upstream.InterfaceName,
		upstream.GatewayIP,
		upstream.MacAddress,
		upstream.DiscoveredAt.Format(time.RFC3339))

	// Log some additional details about the advertisement if available
	if upstream.Advertisement != nil {
		log.Printf("📡 TollGate advertisement: ID=%s, PubKey=%s, Kind=%d",
			upstream.Advertisement.ID,
			upstream.Advertisement.PubKey,
			upstream.Advertisement.Kind)
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
		log.Printf("❌ DISCONNECTED: TollGate on interface %s (gateway: %s) disconnected",
			interfaceName,
			upstream.GatewayIP)

		// Remove from our tracking
		delete(c.upstreamTollgates, interfaceName)
	} else {
		// Interface went down but we didn't have a TollGate connection
		log.Printf("⬇️ INTERFACE DOWN: Interface %s disconnected (no TollGate connection)",
			interfaceName)
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
