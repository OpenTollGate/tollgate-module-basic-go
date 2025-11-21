package crowsnest

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

// crowsnest implements the Crowsnest interface
type crowsnest struct {
	config           *config_manager.CrowsnestConfig
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

// NewCrowsnest creates a new crowsnest instance
func NewCrowsnest(configManager *config_manager.ConfigManager) (Crowsnest, error) {
	if configManager == nil {
		return nil, fmt.Errorf("config manager is required")
	}

	// Load configuration from config manager
	mainConfig := configManager.GetConfig()
	if mainConfig == nil {
		return nil, fmt.Errorf("failed to get main configuration")
	}

	config := &mainConfig.Crowsnest

	// Create components
	networkMonitor := NewNetworkMonitor(config)
	tollGateProber := NewTollGateProber(config)
	discoveryTracker := NewDiscoveryTracker(config)

	cs := &crowsnest{
		config:           config,
		configManager:    configManager,
		networkMonitor:   networkMonitor,
		tollGateProber:   tollGateProber,
		discoveryTracker: discoveryTracker,
		stopChan:         make(chan struct{}),
	}

	return cs, nil
}

// Start begins monitoring network changes and discovering TollGates
func (cs *crowsnest) Start() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.running {
		return fmt.Errorf("crowsnest is already running")
	}

	logger.Info("Starting Crowsnest network monitoring and TollGate discovery")

	// Start network monitor
	err := cs.networkMonitor.Start()
	if err != nil {
		return fmt.Errorf("failed to start network monitor: %w", err)
	}

	cs.running = true
	cs.wg.Add(1)
	go cs.eventLoop()

	// Perform initial interface scan to auto-connect after startup/reboot
	go cs.performInitialInterfaceScan()

	logger.Info("Crowsnest started successfully")
	return nil
}

// Stop stops the crowsnest monitoring
func (cs *crowsnest) Stop() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.running {
		return nil
	}

	logger.Info("Stopping Crowsnest")

	// Stop network monitor
	err := cs.networkMonitor.Stop()
	if err != nil {
		logger.WithError(err).Error("Error stopping network monitor")
	}

	// Stop event loop
	close(cs.stopChan)
	cs.running = false
	cs.wg.Wait()

	// Cleanup discovery tracker
	cs.discoveryTracker.Cleanup()

	logger.Info("Crowsnest stopped successfully")
	return nil
}

// SetChandler sets the chandler for upstream TollGate management
func (cs *crowsnest) SetChandler(chandler chandler.ChandlerInterface) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.chandler = chandler
	logger.Info("Chandler set for Crowsnest")
}

// ScanInterface scans a specific interface for TollGates
func (cs *crowsnest) ScanInterface(interfaceName string) {
	logger.WithField("interface", interfaceName).Info("Scanning interface for TollGates")

	// Get gateway for this interface
	gatewayIP := cs.networkMonitor.GetGatewayForInterface(interfaceName)
	if gatewayIP == "" {
		logger.WithField("interface", interfaceName).Warn("No gateway found for interface")
		return
	}

	// Get mac address for this interface
	interfaces, err := cs.networkMonitor.GetCurrentInterfaces()
	if err != nil {
		logger.WithError(err).Error("Error getting current interfaces")
		return
	}

	var macAddress string
	for _, iface := range interfaces {
		if iface.Name == interfaceName {
			macAddress = iface.MacAddress
			break
		}
	}

	if macAddress == "" {
		logger.WithField("interface", interfaceName).Warn("No mac address found for interface")
		return
	}

	logger.WithFields(logrus.Fields{
		"interface":   interfaceName,
		"mac_address": macAddress,
		"gateway_ip":  gatewayIP,
	}).Info("Found interface details, proceeding with discovery")

	// Attempt TollGate discovery asynchronously
	go cs.attemptTollGateDiscovery(interfaceName, macAddress, gatewayIP)
}

// eventLoop is the main event processing loop
func (cs *crowsnest) eventLoop() {
	defer cs.wg.Done()

	logger.Info("Crowsnest event loop started")

	for {
		select {
		case <-cs.stopChan:
			logger.Info("Crowsnest event loop stopping")
			return

		case event := <-cs.networkMonitor.Events():
			cs.handleNetworkEvent(event)
		}
	}
}

// handleNetworkEvent processes a network event
func (cs *crowsnest) handleNetworkEvent(event NetworkEvent) {
	logger.WithFields(logrus.Fields{
		"event_type": cs.eventTypeToString(event.Type),
		"interface":  event.InterfaceName,
	}).Debug("Processing network event")

	switch event.Type {
	case EventInterfaceUp:
		cs.handleInterfaceUp(event)
	case EventInterfaceDown:
		cs.handleInterfaceDown(event)
	case EventAddressAdded:
		cs.handleAddressAdded(event)
	case EventAddressDeleted:
		cs.handleAddressDeleted(event)
	default:
		logger.WithField("event_type", event.Type).Warn("Unhandled network event type")
	}
}

// handleInterfaceUp handles interface up events
func (cs *crowsnest) handleInterfaceUp(event NetworkEvent) {
	if event.GatewayIP == "" {
		logger.WithField("interface", event.InterfaceName).Debug("Interface is up but no gateway found")
		return
	}

	logger.WithFields(logrus.Fields{
		"interface": event.InterfaceName,
		"gateway":   event.GatewayIP,
	}).Debug("Interface is up with gateway - attempting TollGate discovery")

	// Attempt TollGate discovery asynchronously
	go cs.attemptTollGateDiscovery(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
}

// handleInterfaceDown handles interface down events
func (cs *crowsnest) handleInterfaceDown(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Info("Interface is down - cleaning up and notifying chandler")

	// Cancel any active probes for this interface
	cs.tollGateProber.CancelProbesForInterface(event.InterfaceName)

	// Clear discovery attempts for this interface (including successful ones)
	// This allows re-discovery when the interface comes back up
	cs.discoveryTracker.ClearInterface(event.InterfaceName)

	// Notify chandler of disconnect
	if cs.chandler != nil {
		err := cs.chandler.HandleDisconnect(event.InterfaceName)
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
func (cs *crowsnest) handleAddressAdded(event NetworkEvent) {
	// For address changes, we might want to re-check the gateway
	if event.GatewayIP != "" {
		logger.WithFields(logrus.Fields{
			"interface": event.InterfaceName,
			"gateway":   event.GatewayIP,
		}).Debug("Address added to interface with gateway - checking for TollGate")
		go cs.attemptTollGateDiscovery(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
	}
}

// handleAddressDeleted handles address deleted events
func (cs *crowsnest) handleAddressDeleted(event NetworkEvent) {
	logger.WithField("interface", event.InterfaceName).Debug("Address deleted from interface - checking for TollGate disconnection")

	// When an address is deleted, this might indicate a disconnection
	// Check if we had a successful TollGate connection on this interface
	// and treat address deletion as a potential disconnection

	// Cancel any active probes for this interface
	cs.tollGateProber.CancelProbesForInterface(event.InterfaceName)

	// Clear discovery attempts for this interface to allow re-discovery
	cs.discoveryTracker.ClearInterface(event.InterfaceName)

	// Notify chandler of potential disconnect
	if cs.chandler != nil {
		err := cs.chandler.HandleDisconnect(event.InterfaceName)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"interface": event.InterfaceName,
				"error":     err,
			}).Error("Error notifying chandler of disconnect")
		}
	}
}

// attemptTollGateDiscovery attempts to discover a TollGate on the given gateway
func (cs *crowsnest) attemptTollGateDiscovery(interfaceName, macAddress, gatewayIP string) {
	// Check if we should attempt discovery (prevents concurrent attempts)
	if !cs.discoveryTracker.ShouldAttemptDiscovery(interfaceName, gatewayIP) {
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"gateway":   gatewayIP,
		}).Debug("Skipping discovery - recently attempted or already successful")
		return
	}

	// Record the discovery attempt as pending immediately to prevent concurrent attempts
	cs.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultPending)

	// Create a context for this discovery attempt
	ctx, cancel := context.WithTimeout(context.Background(), cs.config.DiscoveryTimeout)
	defer cancel()

	// Probe the gateway with context
	logger.WithFields(logrus.Fields{
		"gateway":   gatewayIP,
		"interface": interfaceName,
	}).Debug("Probing gateway for TollGate advertisement")

	data, err := cs.tollGateProber.ProbeGatewayWithContext(ctx, interfaceName, gatewayIP)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"error":   err,
		}).Error("Failed to probe gateway")
		cs.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultError)
		return
	}

	// Validate the advertisement using tollgate_protocol
	event, err := tollgate_protocol.ValidateAdvertisementFromBytes(data)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"error":   err,
		}).Warn("Invalid TollGate advertisement from gateway")
		cs.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultValidationFailed)
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
	cs.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultSuccess)

	// Hand off to chandler
	if cs.chandler != nil {
		err = cs.chandler.HandleUpstreamTollgate(upstream)
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
func (cs *crowsnest) eventTypeToString(eventType EventType) string {
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
func (cs *crowsnest) performInitialInterfaceScan() {
	// Small delay to allow the system to fully initialize
	time.Sleep(2 * time.Second)

	logger.Info("Performing initial interface scan for TollGate auto-discovery")

	// Get current network interfaces
	interfaces, err := cs.networkMonitor.GetCurrentInterfaces()
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
		gatewayIP := cs.networkMonitor.GetGatewayForInterface(iface.Name)
		if gatewayIP == "" {
			logger.WithField("interface", iface.Name).Debug("Startup scan: Interface is up but no gateway found")
			continue
		}

		logger.WithFields(logrus.Fields{
			"interface": iface.Name,
			"gateway":   gatewayIP,
		}).Info("Startup scan: Found interface with gateway - attempting TollGate discovery")

		// Attempt TollGate discovery asynchronously
		go cs.attemptTollGateDiscovery(iface.Name, iface.MacAddress, gatewayIP)
	}

	logger.Info("Initial interface scan completed")
}
