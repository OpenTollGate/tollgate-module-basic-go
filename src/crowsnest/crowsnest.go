package crowsnest

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/chandler"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
)

// crowsnest implements the Crowsnest interface
type crowsnest struct {
	config           *CrowsnestConfig
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

	// Load configuration or use defaults
	config := DefaultConfig()

	// TODO: Load from config manager if needed
	// For now, we use the default configuration

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

	log.Printf("Starting Crowsnest network monitoring and TollGate discovery")

	// Start network monitor
	err := cs.networkMonitor.Start()
	if err != nil {
		return fmt.Errorf("failed to start network monitor: %w", err)
	}

	cs.running = true
	cs.wg.Add(1)
	go cs.eventLoop()

	log.Printf("Crowsnest started successfully")
	return nil
}

// Stop stops the crowsnest monitoring
func (cs *crowsnest) Stop() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.running {
		return nil
	}

	log.Printf("Stopping Crowsnest")

	// Stop network monitor
	err := cs.networkMonitor.Stop()
	if err != nil {
		log.Printf("Error stopping network monitor: %v", err)
	}

	// Stop event loop
	close(cs.stopChan)
	cs.running = false
	cs.wg.Wait()

	// Cleanup discovery tracker
	cs.discoveryTracker.Cleanup()

	log.Printf("Crowsnest stopped successfully")
	return nil
}

// SetChandler sets the chandler for upstream TollGate management
func (cs *crowsnest) SetChandler(chandler chandler.ChandlerInterface) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.chandler = chandler
	log.Printf("Chandler set for Crowsnest")
}

// eventLoop is the main event processing loop
func (cs *crowsnest) eventLoop() {
	defer cs.wg.Done()

	log.Printf("Crowsnest event loop started")

	for {
		select {
		case <-cs.stopChan:
			log.Printf("Crowsnest event loop stopping")
			return

		case event := <-cs.networkMonitor.Events():
			cs.handleNetworkEvent(event)
		}
	}
}

// handleNetworkEvent processes a network event
func (cs *crowsnest) handleNetworkEvent(event NetworkEvent) {
	log.Printf("Processing network event: %s on interface %s",
		cs.eventTypeToString(event.Type), event.InterfaceName)

	switch event.Type {
	case EventInterfaceUp:
		cs.handleInterfaceUp(event)
	case EventInterfaceDown:
		cs.handleInterfaceDown(event)
	case EventAddressAdded:
		cs.handleAddressAdded(event)
	case EventRouteAdded:
		cs.handleRouteAdded(event)
	default:
		log.Printf("Unhandled network event type: %d", event.Type)
	}
}

// handleInterfaceUp handles interface up events
func (cs *crowsnest) handleInterfaceUp(event NetworkEvent) {
	if event.GatewayIP == "" {
		log.Printf("Interface %s is up but no gateway found", event.InterfaceName)
		return
	}

	// Check if we should attempt discovery
	if !cs.discoveryTracker.ShouldAttemptDiscovery(event.InterfaceName, event.GatewayIP) {
		log.Printf("Skipping discovery for interface %s (gateway %s) - recently attempted",
			event.InterfaceName, event.GatewayIP)
		return
	}

	log.Printf("Interface %s is up with gateway %s - attempting TollGate discovery",
		event.InterfaceName, event.GatewayIP)

	// Attempt TollGate discovery
	cs.attemptTollGateDiscovery(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
}

// handleInterfaceDown handles interface down events
func (cs *crowsnest) handleInterfaceDown(event NetworkEvent) {
	log.Printf("Interface %s is down - notifying chandler", event.InterfaceName)

	// Notify chandler of disconnect
	if cs.chandler != nil {
		err := cs.chandler.HandleDisconnect(event.InterfaceName)
		if err != nil {
			log.Printf("Error notifying chandler of disconnect for interface %s: %v",
				event.InterfaceName, err)
		}
	} else {
		log.Printf("No chandler set - cannot notify of disconnect for interface %s",
			event.InterfaceName)
	}
}

// handleAddressAdded handles address added events
func (cs *crowsnest) handleAddressAdded(event NetworkEvent) {
	// For address changes, we might want to re-check the gateway
	if event.GatewayIP != "" {
		log.Printf("Address added to interface %s with gateway %s - checking for TollGate",
			event.InterfaceName, event.GatewayIP)
		cs.attemptTollGateDiscovery(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
	}
}

// handleRouteAdded handles route added events
func (cs *crowsnest) handleRouteAdded(event NetworkEvent) {
	// Similar to address added
	if event.GatewayIP != "" {
		log.Printf("Route added for interface %s with gateway %s - checking for TollGate",
			event.InterfaceName, event.GatewayIP)
		cs.attemptTollGateDiscovery(event.InterfaceName, event.InterfaceInfo.MacAddress, event.GatewayIP)
	}
}

// attemptTollGateDiscovery attempts to discover a TollGate on the given gateway
func (cs *crowsnest) attemptTollGateDiscovery(interfaceName, macAddress, gatewayIP string) {
	// Record the discovery attempt
	cs.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultPending)

	// Probe the gateway
	log.Printf("Probing gateway %s on interface %s for TollGate advertisement", gatewayIP, interfaceName)

	data, err := cs.tollGateProber.ProbeGateway(gatewayIP)
	if err != nil {
		log.Printf("Failed to probe gateway %s: %v", gatewayIP, err)
		cs.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultNotTollGate)
		return
	}

	// Validate the advertisement using tollgate_protocol
	event, err := tollgate_protocol.ValidateAdvertisementFromBytes(data)
	if err != nil {
		log.Printf("Invalid TollGate advertisement from gateway %s: %v", gatewayIP, err)
		cs.discoveryTracker.RecordDiscovery(interfaceName, gatewayIP, DiscoveryResultValidationFailed)
		return
	}

	log.Printf("Valid TollGate advertisement discovered on gateway %s (pubkey: %s)",
		gatewayIP, event.PubKey)

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
			log.Printf("Error handing off upstream TollGate to chandler: %v", err)
		} else {
			log.Printf("Successfully handed off upstream TollGate to chandler (interface: %s, gateway: %s)",
				interfaceName, gatewayIP)
		}
	} else {
		log.Printf("No chandler set - cannot hand off upstream TollGate (interface: %s, gateway: %s)",
			interfaceName, gatewayIP)
	}
}

// eventTypeToString converts event type to string for logging
func (cs *crowsnest) eventTypeToString(eventType EventType) string {
	switch eventType {
	case EventInterfaceUp:
		return "InterfaceUp"
	case EventInterfaceDown:
		return "InterfaceDown"
	case EventRouteAdded:
		return "RouteAdded"
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
