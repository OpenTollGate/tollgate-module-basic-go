package upstream_detector

import (
	"fmt"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager"
	"github.com/sirupsen/logrus"
)

// upstreamDetector implements the UpstreamDetector interface
type upstreamDetector struct {
	config                 *config_manager.UpstreamDetectorConfig
	configManager          *config_manager.ConfigManager
	networkMonitor         NetworkMonitor
	upstreamSessionManager upstream_session_manager.UpstreamSessionManagerInterface

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

	// Create network monitor
	networkMonitor := NewNetworkMonitor(config)

	ud := &upstreamDetector{
		config:         config,
		configManager:  configManager,
		networkMonitor: networkMonitor,
		stopChan:       make(chan struct{}),
	}

	return ud, nil
}

// Start begins monitoring network changes
func (ud *upstreamDetector) Start() error {
	ud.mu.Lock()
	defer ud.mu.Unlock()

	if ud.running {
		return fmt.Errorf("upstream detector is already running")
	}

	logger.Info("Starting UpstreamDetector network monitoring")

	// Start network monitor
	err := ud.networkMonitor.Start()
	if err != nil {
		return fmt.Errorf("failed to start network monitor: %w", err)
	}

	ud.running = true
	ud.wg.Add(2) // Event loop + periodic check
	go ud.eventLoop()
	go ud.periodicGatewayCheck()

	// Perform initial interface scan to report existing gateways
	go ud.performInitialInterfaceScan()

	logger.Info("UpstreamDetector started successfully")
	return nil
}

// Stop stops the upstream detector monitoring
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

	logger.Info("UpstreamDetector stopped successfully")
	return nil
}

// SetUpstreamSessionManager sets the upstream session manager for upstream TollGate management
func (ud *upstreamDetector) SetUpstreamSessionManager(usm upstream_session_manager.UpstreamSessionManagerInterface) {
	ud.mu.Lock()
	defer ud.mu.Unlock()

	ud.upstreamSessionManager = usm
	logger.Info("UpstreamSessionManager set for UpstreamDetector")
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
	}).Info("Interface is up with gateway - notifying upstream session manager")

	// Report gateway to upstream session manager
	ud.reportGatewayToUSM(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
}

// handleInterfaceDown handles interface down events
func (ud *upstreamDetector) handleInterfaceDown(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Info("Interface is down - notifying upstream session manager")

	// Notify upstream session manager of disconnect
	if ud.upstreamSessionManager != nil {
		err := ud.upstreamSessionManager.HandleDisconnect(event.InterfaceName)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"interface": event.InterfaceName,
				"error":     err,
			}).Error("Error notifying upstream session manager of disconnect")
		}
	} else {
		logger.WithField("interface", event.InterfaceName).Debug("No upstream session manager set - cannot notify of disconnect")
	}
}

// handleAddressAdded handles address added events
func (ud *upstreamDetector) handleAddressAdded(event NetworkEvent) {
	// For address changes, report the gateway to upstream session manager
	if event.GatewayIP != "" {
		logger.WithFields(logrus.Fields{
			"interface": event.InterfaceName,
			"gateway":   event.GatewayIP,
		}).Debug("Address added to interface with gateway - notifying upstream session manager")
		ud.reportGatewayToUSM(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
	}
}

// handleAddressDeleted handles address deleted events
func (ud *upstreamDetector) handleAddressDeleted(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Debug("Address deleted from interface - notifying upstream session manager of potential disconnection")

	// Notify upstream session manager of potential disconnect
	if ud.upstreamSessionManager != nil {
		err := ud.upstreamSessionManager.HandleDisconnect(event.InterfaceName)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"interface": event.InterfaceName,
				"error":     err,
			}).Error("Error notifying upstream session manager of disconnect")
		}
	}
}

// reportGatewayToUSM reports a discovered gateway to the upstream session manager
func (ud *upstreamDetector) reportGatewayToUSM(interfaceName, macAddress, gatewayIP string) {
	if ud.upstreamSessionManager == nil {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
		}).Warn("⚠️ No upstream session manager set - cannot report gateway")
		return
	}

	logger.WithFields(logrus.Fields{
		"interface": interfaceName,
		"gateway":   gatewayIP,
		"mac":       macAddress,
	}).Info("📡 Reporting gateway to upstream session manager")

	// Report gateway to upstream session manager - it will handle all TollGate logic
	err := ud.upstreamSessionManager.HandleGatewayConnected(interfaceName, macAddress, gatewayIP)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
			"error":     err,
		}).Debug("Upstream session manager reported error for gateway (will retry via polling)")
	} else {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
		}).Info("✅ Successfully reported gateway to upstream session manager")
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

// periodicGatewayCheck periodically checks for gateways on connected interfaces
func (ud *upstreamDetector) periodicGatewayCheck() {
	defer ud.wg.Done()

	// Check every 30 seconds for gateways that may have become available
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	logger.Info("Starting periodic gateway check (every 30s)")

	for {
		select {
		case <-ud.stopChan:
			logger.Info("Periodic gateway check stopping")
			return

		case <-ticker.C:
			ud.checkInterfacesForGateways()
		}
	}
}

// checkInterfacesForGateways checks all interfaces for gateways and reports them
func (ud *upstreamDetector) checkInterfacesForGateways() {
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

		// Report gateway to upstream session manager (it will handle deduplication via knownGateways)
		logger.WithFields(logrus.Fields{
			"interface": iface.Name,
			"gateway":   gatewayIP,
		}).Debug("Periodic check: Found gateway - reporting to upstream session manager")

		ud.reportGatewayToUSM(iface.Name, iface.MacAddress, gatewayIP)
	}
}

// performInitialInterfaceScan scans existing network interfaces on startup
func (ud *upstreamDetector) performInitialInterfaceScan() {
	// Small delay to allow the system to fully initialize
	time.Sleep(2 * time.Second)

	logger.Info("Performing initial interface scan to report existing gateways")

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
		}).Info("Startup scan: Found interface with gateway - reporting to upstream session manager")

		// Report gateway to upstream session manager
		ud.reportGatewayToUSM(iface.Name, iface.MacAddress, gatewayIP)
	}

	logger.Info("Initial interface scan completed")
}
