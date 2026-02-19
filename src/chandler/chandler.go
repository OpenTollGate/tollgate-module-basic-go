package chandler

import (
	"context"
	"fmt"
	"sync"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
	"github.com/nbd-wtf/go-nostr"
	"github.com/sirupsen/logrus"
)

// Module-level logger with pre-configured module field
var logger = logrus.WithField("module", "chandler")

// Gateway represents a discovered gateway with optional session
type Gateway struct {
	InterfaceName string
	MacAddress    string
	GatewayIP     string
	Session       *UpstreamSession // nil if no session
	mu            sync.RWMutex
}

// Chandler manages upstream TollGate sessions
type Chandler struct {
	configManager  *config_manager.ConfigManager
	merchant       merchant.MerchantInterface
	gateways       map[string]*Gateway // keyed by gateway IP
	tollGateProber TollGateProber
	mu             sync.RWMutex
}

// NewChandler creates a new chandler instance
func NewChandler(configManager *config_manager.ConfigManager, merchantImpl merchant.MerchantInterface) (ChandlerInterface, error) {
	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Create TollGateProber
	tollGateProber := NewTollGateProber(&config.UpstreamDetector)

	chandler := &Chandler{
		configManager:  configManager,
		merchant:       merchantImpl,
		gateways:       make(map[string]*Gateway),
		tollGateProber: tollGateProber,
	}

	logger.Info("Chandler initialized successfully")
	return chandler, nil
}

// HandleGatewayConnected is called when upstream_detector discovers a gateway
func (c *Chandler) HandleGatewayConnected(interfaceName, macAddress, gatewayIP string) error {
	c.mu.Lock()

	// Get or create gateway entry
	gateway, exists := c.gateways[gatewayIP]
	if !exists {
		gateway = &Gateway{
			InterfaceName: interfaceName,
			MacAddress:    macAddress,
			GatewayIP:     gatewayIP,
			Session:       nil,
		}
		c.gateways[gatewayIP] = gateway
		logger.WithFields(logrus.Fields{
			"gateway":   gatewayIP,
			"interface": interfaceName,
		}).Info("📝 New gateway discovered")
	}

	// Check if already has session
	if gateway.Session != nil {
		logger.WithField("gateway", gatewayIP).Debug("Gateway already has active session")
		c.mu.Unlock()
		return nil
	}

	c.mu.Unlock()

	logger.WithFields(logrus.Fields{
		"gateway":   gatewayIP,
		"interface": interfaceName,
	}).Info("🔍 Checking if gateway is a TollGate")

	// Check if it's a TollGate
	event, err := c.getUpstreamAdvertisement(gatewayIP, interfaceName)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"error":   err,
		}).Debug("Not a TollGate or probe failed")
		return err
	}

	// Extract advertisement info
	adInfo, err := tollgate_protocol.ExtractAdvertisementInfo(event)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"error":   err,
		}).Error("Failed to extract advertisement information")
		return err
	}

	// Create session - it handles everything (pricing selection, payments, tracking)
	session, err := NewUpstreamSession(
		gatewayIP,
		interfaceName,
		event,
		adInfo,
		c.configManager,
		c.merchant,
	)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"error":   err,
		}).Error("Failed to create session")
		return err
	}

	// Store session in gateway
	gateway.mu.Lock()
	gateway.Session = session
	gateway.mu.Unlock()

	logger.WithFields(logrus.Fields{
		"gateway": gatewayIP,
		"metric":  adInfo.Metric,
	}).Info("✅ Session created and tracking started")

	return nil
}

// HandleDisconnect handles network interface disconnection
func (c *Chandler) HandleDisconnect(interfaceName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	disconnectedCount := 0

	// Find and stop sessions on this interface
	for gatewayIP, gateway := range c.gateways {
		if gateway.InterfaceName == interfaceName && gateway.Session != nil {
			gateway.Session.Stop()
			gateway.Session = nil
			disconnectedCount++

			logger.WithFields(logrus.Fields{
				"gateway":   gatewayIP,
				"interface": interfaceName,
			}).Info("❌ Session terminated due to interface disconnect")
		}
	}

	if disconnectedCount > 0 {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"count":     disconnectedCount,
		}).Info("❌ Interface disconnected, sessions cleaned up")
	} else {
		logger.WithField("interface", interfaceName).Info("⬇️ Interface disconnected (no active sessions)")
	}

	return nil
}

// GetActiveSessions returns all currently active sessions
func (c *Chandler) GetActiveSessions() map[string]*UpstreamSession {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*UpstreamSession)
	for gatewayIP, gateway := range c.gateways {
		if gateway.Session != nil && gateway.Session.Status == SessionActive {
			result[gatewayIP] = gateway.Session
		}
	}
	return result
}

// getUpstreamAdvertisement fetches and validates the TollGate advertisement from a gateway
func (c *Chandler) getUpstreamAdvertisement(gatewayIP, interfaceName string) (*nostr.Event, error) {
	ctx := context.Background()

	// Probe gateway
	data, err := c.tollGateProber.ProbeGatewayWithContext(ctx, interfaceName, gatewayIP)
	if err != nil {
		return nil, fmt.Errorf("failed to probe gateway: %w", err)
	}

	// Validate advertisement
	event, err := tollgate_protocol.ValidateAdvertisementFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("invalid advertisement: %w", err)
	}

	return event, nil
}

// Stop stops the chandler and cleans up resources
func (c *Chandler) Stop() error {
	logger.Info("Stopping Chandler")

	// Stop all sessions
	c.mu.Lock()
	for _, gateway := range c.gateways {
		if gateway.Session != nil {
			gateway.Session.Stop()
		}
	}
	c.gateways = make(map[string]*Gateway)
	c.mu.Unlock()

	logger.Info("Chandler stopped")
	return nil
}
