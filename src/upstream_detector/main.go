package upstream_detector

import (
	"fmt"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/chandler"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/sirupsen/logrus"
)

// upstreamDetector implements the UpstreamDetector interface
type upstreamDetector struct {
	config         *config_manager.UpstreamDetectorConfig
	configManager  *config_manager.ConfigManager
	networkMonitor NetworkMonitor
	chandler       chandler.ChandlerInterface

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
	ud.wg.Add(1) // Event loop only
	go ud.eventLoop()

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
	}).Info("Interface is up with gateway - notifying chandler")

	// Report gateway to chandler
	ud.reportGatewayToChandler(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
}

// handleInterfaceDown handles interface down events
func (ud *upstreamDetector) handleInterfaceDown(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Info("Interface is down - notifying chandler")

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
	// For address changes, report the gateway to chandler
	if event.GatewayIP != "" {
		logger.WithFields(logrus.Fields{
			"interface": event.InterfaceName,
			"gateway":   event.GatewayIP,
		}).Debug("Address added to interface with gateway - notifying chandler")
		ud.reportGatewayToChandler(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
	}
}

// handleAddressDeleted handles address deleted events
func (ud *upstreamDetector) handleAddressDeleted(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Debug("Address deleted from interface - notifying chandler of potential disconnection")

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

// reportGatewayToChandler reports a discovered gateway to chandler
func (ud *upstreamDetector) reportGatewayToChandler(interfaceName, macAddress, gatewayIP string) {
	if ud.chandler == nil {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
		}).Warn("⚠️ No chandler set - cannot report gateway")
		return
	}

	logger.WithFields(logrus.Fields{
		"interface": interfaceName,
		"gateway":   gatewayIP,
		"mac":       macAddress,
	}).Info("📡 Reporting gateway to chandler")

	// Report gateway to chandler - chandler will handle all TollGate logic
	err := ud.chandler.HandleGatewayConnected(interfaceName, macAddress, gatewayIP)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
			"error":     err,
		}).Debug("Chandler reported error for gateway (will retry via polling)")
	} else {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
		}).Info("✅ Successfully reported gateway to chandler")
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
		}).Info("Startup scan: Found interface with gateway - reporting to chandler")

		// Report gateway to chandler
		ud.reportGatewayToChandler(iface.Name, iface.MacAddress, gatewayIP)
	}

	logger.Info("Initial interface scan completed")
}
