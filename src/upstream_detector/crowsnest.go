package upstream_detector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/chandler"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
	"github.com/sirupsen/logrus"
)

// upstreamDetector implements the UpstreamDetector interface
type upstreamDetector struct {
	config           *config_manager.UpstreamDetectorConfig
	configManager    *config_manager.ConfigManager
	networkMonitor   NetworkMonitor
	tollGateProber   TollGateProber
	discoveryTracker DiscoveryTracker
	chandler         chandler.ChandlerInterface

	// Control channels
	stopChan chan struct{}
	wg       sync.WaitGroup
	running  bool
	mu       sync.RWMutex
}

// NewUpstreamDetector creates a new upstream detector instance
func NewUpstreamDetector(configManager *config_manager.ConfigManager) (UpstreamDetector, error) {
	if configManager == nil {
		return nil, fmt.Errorf("config manager is required")
	}

	// Load configuration from config manager
	mainConfig := configManager.GetConfig()
	if mainConfig == nil {
		return nil, fmt.Errorf("failed to get main configuration")
	}

	config := &mainConfig.UpstreamDetector

	// Create components
	networkMonitor := NewNetworkMonitor(config)
	tollGateProber := NewTollGateProber(config)
	discoveryTracker := NewDiscoveryTracker(config)

	ud := &upstreamDetector{
		config:           config,
		configManager:    configManager,
		networkMonitor:   networkMonitor,
		tollGateProber:   tollGateProber,
		discoveryTracker: discoveryTracker,
		stopChan:         make(chan struct{}),
	}

	return ud, nil
}

// Start begins monitoring network changes and discovering TollGates
func (ud *upstreamDetector) Start() error {
	ud.mu.Lock()
	defer ud.mu.Unlock()

	if ud.running {
		return fmt.Errorf("upstream detector is already running")
	}

	logger.Info("Starting UpstreamDetector network monitoring and TollGate discovery")

	// Start network monitor
	err := ud.networkMonitor.Start()
	if err != nil {
		return fmt.Errorf("failed to start network monitor: %w", err)
	}

	ud.running = true
	ud.wg.Add(2) // One for event loop, one for periodic check
	go ud.eventLoop()
	go ud.periodicUpstreamCheck()

	// Perform initial interface scan to auto-connect after startup/reboot
	go ud.performInitialInterfaceScan()

	logger.Info("UpstreamDetector started successfully")
	return nil
}

// Stop stops the crowsnest monitoring
func (ud *upstreamDetector) Stop() error {
	ud.mu.Lock()
	defer ud.mu.Unlock()

	if !ud.running {
		return nil
	}

	logger.Info("Stopping UpstreamDetector")

	// Stop network monitor
	err := ud.networkMonitor.Stop()
	if err != nil {
		logger.WithError(err).Error("Error stopping network monitor")
	}

	// Stop event loop
	close(ud.stopChan)
	ud.running = false
	ud.wg.Wait()

	// Cleanup discovery tracker
	ud.discoveryTracker.Cleanup()

	logger.Info("UpstreamDetector stopped successfully")
	return nil
}

// SetChandler sets the chandler for upstream TollGate management
func (ud *upstreamDetector) SetChandler(chandler chandler.ChandlerInterface) {
	ud.mu.Lock()
	defer ud.mu.Unlock()

	ud.chandler = chandler
	logger.Info("Chandler set for UpstreamDetector")
}

// eventLoop is the main event processing loop
func (ud *upstreamDetector) eventLoop() {
	defer ud.wg.Done()

	logger.Info("UpstreamDetector event loop started")

	for {
		select {
		case <-ud.stopChan:
			logger.Info("UpstreamDetector event loop stopping")
			return

		case event := <-ud.networkMonitor.Events():
			ud.handleNetworkEvent(event)
		}
	}
}

// handleNetworkEvent processes a network event
func (ud *upstreamDetector) handleNetworkEvent(event NetworkEvent) {
	logger.WithFields(logrus.Fields{
		"event_type": ud.eventTypeToString(event.Type),
		"interface":  event.InterfaceName,
	}).Debug("Processing network event")

	switch event.Type {
	case EventInterfaceUp:
		ud.handleInterfaceUp(event)
	case EventInterfaceDown:
		ud.handleInterfaceDown(event)
	case EventAddressAdded:
		ud.handleAddressAdded(event)
	case EventAddressDeleted:
		ud.handleAddressDeleted(event)
	default:
		logger.WithField("event_type", event.Type).Warn("Unhandled network event type")
	}
}

// handleInterfaceUp handles interface up events
func (ud *upstreamDetector) handleInterfaceUp(event NetworkEvent) {
	if event.GatewayIP == "" {
		logger.WithField("interface", event.InterfaceName).Debug("Interface is up but no gateway found")
		return
	}

	logger.WithFields(logrus.Fields{
		"interface": event.InterfaceName,
		"gateway":   event.GatewayIP,
	}).Debug("Interface is up with gateway - attempting TollGate discovery")

	// Attempt TollGate discovery asynchronously
	go ud.attemptTollGateDiscovery(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
}

// handleInterfaceDown handles interface down events
func (ud *upstreamDetector) handleInterfaceDown(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Info("Interface is down - cleaning up and notifying chandler")

	// Cancel any active probes for this interface
	ud.tollGateProber.CancelProbesForInterface(event.InterfaceName)

	// Clear discovery attempts for this interface (including successful ones)
	// This allows re-discovery when the interface comes back up
	ud.discoveryTracker.ClearInterface(event.InterfaceName)

	// Notify chandler of disconnect
	if ud.chandler != nil {
		err := ud.chandler.HandleDisconnect(event.InterfaceName)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"interface": event.InterfaceName,
				"error":     err,
			}).Error("Error notifying chandler of disconnect")
		}
	} else {
		logger.WithField("interface", event.InterfaceName).Debug("No chandler set - cannot notify of disconnect")
	}
}

// handleAddressAdded handles address added events
func (ud *upstreamDetector) handleAddressAdded(event NetworkEvent) {
	// For address changes, we might want to re-check the gateway
	if event.GatewayIP != "" {
		logger.WithFields(logrus.Fields{
			"interface": event.InterfaceName,
			"gateway":   event.GatewayIP,
		}).Debug("Address added to interface with gateway - checking for TollGate")
		go ud.attemptTollGateDiscovery(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
	}
}

// handleAddressDeleted handles address deleted events
func (ud *upstreamDetector) handleAddressDeleted(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Debug("Address deleted from interface - checking for TollGate disconnection")

	// When an address is deleted, this might indicate a disconnection
	// Check if we had a successful TollGate connection on this interface
	// and treat address deletion as a potential disconnection

	// Cancel any active probes for this interface
	ud.tollGateProber.CancelProbesForInterface(event.InterfaceName)

	// Clear discovery attempts for this interface to allow re-discovery
	ud.discoveryTracker.ClearInterface(event.InterfaceName)

	// Notify chandler of potential disconnect
	if ud.chandler != nil {
		err := ud.chandler.HandleDisconnect(event.InterfaceName)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"interface": event.InterfaceName,
				"error":     err,
			}).Error("Error notifying chandler of disconnect")
		}
	}
}

// attemptTollGateDiscovery attempts to discover a TollGate on the given gateway
func (ud *upstreamDetector) attemptTollGateDiscovery(interfaceName, macAddress, gatewayIP string) {
	// Check if we should attempt discovery (prevents concurrent attempts)
	if !ud.discoveryTracker.ShouldAttemptDiscovery(interfaceName, gatewayIP) {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
		}).Debug("Skipping discovery - recently attempted or already successful")
		return
	}

	// Record the discovery attempt as pending immediately to prevent concurrent attempts
	ud.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultPending)

	// Create a context for this discovery attempt
	ctx, cancel := context.WithTimeout(context.Background(), ud.config.DiscoveryTimeout)
	defer cancel()

	// Probe the gateway with context
	logger.WithFields(logrus.Fields{
		"gateway":   gatewayIP,
		"interface": interfaceName,
	}).Debug("Probing gateway for TollGate advertisement")

	data, err := ud.tollGateProber.ProbeGatewayWithContext(ctx, interfaceName, gatewayIP)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"error":   err,
		}).Error("Failed to probe gateway")
		ud.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultError)
		return
	}

	// Validate the advertisement using tollgate_protocol
	event, err := tollgate_protocol.ValidateAdvertisementFromBytes(data)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"error":   err,
		}).Warn("Invalid TollGate advertisement from gateway")
		ud.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultValidationFailed)
		return
	}

	logger.WithFields(logrus.Fields{
		"gateway":    gatewayIP,
		"public_key": event.PubKey,
	}).Info("Valid TollGate advertisement discovered")

	// Create UpstreamTollgate object
	upstream := &chandler.UpstreamTollgate{
		InterfaceName: interfaceName,
		MacAddress:    macAddress,
		GatewayIP:     gatewayIP,
		Advertisement: event,
		DiscoveredAt:  time.Now(),
	}

	// Record successful discovery
	ud.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultSuccess)

	// Hand off to chandler
	if ud.chandler != nil {
		err = ud.chandler.HandleUpstreamTollgate(upstream)
		if err != nil {
			logger.WithError(err).Error("Error handing off upstream TollGate to chandler")
		} else {
			logger.WithFields(logrus.Fields{
				"interface": interfaceName,
				"gateway":   gatewayIP,
			}).Debug("Successfully handed off upstream TollGate to chandler")
		}
	} else {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
		}).Debug("No chandler set - cannot hand off upstream TollGate")
	}
}

// eventTypeToString converts event type to string for logging
func (ud *upstreamDetector) eventTypeToString(eventType EventType) string {
	switch eventType {
	case EventInterfaceUp:
		return "InterfaceUp"
	case EventInterfaceDown:
		return "InterfaceDown"
	case EventRouteDeleted:
		return "RouteDeleted"
	case EventAddressAdded:
		return "AddressAdded"
	case EventAddressDeleted:
		return "AddressDeleted"
	default:
		return fmt.Sprintf("Unknown(%d)", eventType)
	}
}

// performInitialInterfaceScan scans existing network interfaces on startup
func (ud *upstreamDetector) performInitialInterfaceScan() {
	// Small delay to allow the system to fully initialize
	time.Sleep(2 * time.Second)

	logger.Info("Performing initial interface scan for TollGate auto-discovery")

	// Get current network interfaces
	interfaces, err := ud.networkMonitor.GetCurrentInterfaces()
	if err != nil {
		logger.WithError(err).Error("Error getting current interfaces during startup scan")
		return
	}

	// Check each interface that is up and has IP addresses
	for _, iface := range interfaces {
		if !iface.IsUp || len(iface.IPAddresses) == 0 {
			continue
		}

		// Get gateway for this interface
		gatewayIP := ud.networkMonitor.GetGatewayForInterface(iface.Name)
		if gatewayIP == "" {
			logger.WithField("interface", iface.Name).Debug("Startup scan: Interface is up but no gateway found")
			continue
		}

		logger.WithFields(logrus.Fields{
			"interface": iface.Name,
			"gateway":   gatewayIP,
		}).Info("Startup scan: Found interface with gateway - attempting TollGate discovery")

		// Attempt TollGate discovery asynchronously
		go ud.attemptTollGateDiscovery(iface.Name, iface.MacAddress, gatewayIP)
	}

	logger.Info("Initial interface scan completed")
}

// periodicUpstreamCheck periodically checks for upstream TollGates on connected interfaces
func (ud *upstreamDetector) periodicUpstreamCheck() {
	defer ud.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	logger.Info("Starting periodic upstream TollGate check")

	for {
		select {
		case <-ticker.C:
			ud.checkConnectedInterfaces()
		case <-ud.stopChan:
			logger.Info("Periodic upstream check stopping")
			return
		}
	}
}

// checkConnectedInterfaces checks all connected interfaces for upstream TollGates
func (ud *upstreamDetector) checkConnectedInterfaces() {
	// Skip if no chandler is set
	if ud.chandler == nil {
		return
	}

	// Check if we already have active sessions
	activeSessions := ud.chandler.GetActiveSessions()
	if len(activeSessions) > 0 {
		// Already have active session(s), skip check
		return
	}

	// Get current network interfaces
	interfaces, err := ud.networkMonitor.GetCurrentInterfaces()
	if err != nil {
		logger.WithError(err).Debug("Error getting current interfaces during periodic check")
		return
	}

	// Check each interface that is up and has IP addresses
	for _, iface := range interfaces {
		if !iface.IsUp || len(iface.IPAddresses) == 0 {
			continue
		}

		// Get gateway for this interface
		gatewayIP := ud.networkMonitor.GetGatewayForInterface(iface.Name)
		if gatewayIP == "" {
			continue
		}

		// Try to discover TollGate on this gateway
		// Use a short timeout context for the probe
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

		data, err := ud.tollGateProber.ProbeGatewayWithContext(ctx, iface.Name, gatewayIP)
		cancel()

		if err != nil {
			// Not a TollGate or error probing, continue to next interface
			continue
		}

		// Validate the advertisement
		event, err := tollgate_protocol.ValidateAdvertisementFromBytes(data)
		if err != nil {
			// Invalid advertisement, continue to next interface
			continue
		}

		logger.WithFields(logrus.Fields{
			"interface":  iface.Name,
			"gateway":    gatewayIP,
			"public_key": event.PubKey,
		}).Info("Periodic check discovered upstream TollGate")

		// Create UpstreamTollgate object
		upstream := &chandler.UpstreamTollgate{
			InterfaceName: iface.Name,
			MacAddress:    iface.MacAddress,
			GatewayIP:     gatewayIP,
			Advertisement: event,
			DiscoveredAt:  time.Now(),
		}

		// Hand off to chandler
		err = ud.chandler.HandleUpstreamTollgate(upstream)
		if err != nil {
			logger.WithError(err).Warn("Error handing off upstream TollGate to chandler from periodic check")
		} else {
			logger.WithFields(logrus.Fields{
				"interface": iface.Name,
				"gateway":   gatewayIP,
			}).Info("Successfully initiated session with upstream TollGate from periodic check")
			// Successfully created a session, no need to check other interfaces
			return
		}
	}
}
