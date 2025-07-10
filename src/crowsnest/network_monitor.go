//go:build linux
// +build linux

package crowsnest

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
)

// networkMonitor implements the NetworkMonitor interface using event-driven netlink subscriptions
type networkMonitor struct {
	config   *CrowsnestConfig
	events   chan NetworkEvent
	stopChan chan struct{}
	wg       sync.WaitGroup
	running  bool
	mu       sync.RWMutex
}

// NewNetworkMonitor creates a new event-driven network monitor
func NewNetworkMonitor(config *CrowsnestConfig) NetworkMonitor {
	return &networkMonitor{
		config:   config,
		events:   make(chan NetworkEvent, 100),
		stopChan: make(chan struct{}),
	}
}

// Start begins monitoring network changes using netlink subscriptions
func (nm *networkMonitor) Start() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if nm.running {
		return fmt.Errorf("network monitor is already running")
	}

	log.Printf("Starting event-driven network monitor")

	nm.running = true
	nm.wg.Add(2) // One for link updates, one for address updates

	// Start link monitoring
	go nm.monitorLinkChanges()

	// Start address monitoring
	go nm.monitorAddressChanges()

	return nil
}

// Stop stops the network monitor
func (nm *networkMonitor) Stop() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if !nm.running {
		return nil
	}

	log.Printf("Stopping network monitor")

	close(nm.stopChan)
	nm.running = false
	nm.wg.Wait()
	close(nm.events)

	return nil
}

// Events returns the channel for network events
func (nm *networkMonitor) Events() <-chan NetworkEvent {
	return nm.events
}

// monitorLinkChanges monitors network interface link changes (up/down)
func (nm *networkMonitor) monitorLinkChanges() {
	defer nm.wg.Done()

	// Subscribe to network link updates
	updates := make(chan netlink.LinkUpdate)
	done := make(chan struct{})

	// Subscribe to link updates
	if err := netlink.LinkSubscribe(updates, done); err != nil {
		log.Printf("Failed to subscribe to link updates: %v", err)
		return
	}

	log.Println("Monitoring network interface link changes...")

	for {
		select {
		case <-nm.stopChan:
			close(done)
			return
		case update := <-updates:
			nm.handleLinkUpdate(update)
		}
	}
}

// monitorAddressChanges monitors IP address changes on interfaces
func (nm *networkMonitor) monitorAddressChanges() {
	defer nm.wg.Done()

	// Subscribe to address updates
	updates := make(chan netlink.AddrUpdate)
	done := make(chan struct{})

	// Subscribe to address updates
	if err := netlink.AddrSubscribe(updates, done); err != nil {
		log.Printf("Failed to subscribe to address updates: %v", err)
		return
	}

	log.Println("Monitoring network interface address changes...")

	for {
		select {
		case <-nm.stopChan:
			close(done)
			return
		case update := <-updates:
			nm.handleAddressUpdate(update)
		}
	}
}

// handleLinkUpdate processes a network link update
func (nm *networkMonitor) handleLinkUpdate(update netlink.LinkUpdate) {
	link := update.Link
	if link == nil {
		return
	}

	attrs := link.Attrs()
	if attrs == nil {
		return
	}

	interfaceName := attrs.Name

	// Check if we should monitor this interface
	if !nm.shouldMonitorInterface(interfaceName) {
		return
	}

	// Determine if interface is up or down
	isUp := attrs.Flags&net.FlagUp != 0

	// Create interface info
	interfaceInfo := &InterfaceInfo{
		Name:           interfaceName,
		MacAddress:     attrs.HardwareAddr.String(),
		IsUp:           isUp,
		IsLoopback:     attrs.Flags&net.FlagLoopback != 0,
		IsPointToPoint: attrs.Flags&net.FlagPointToPoint != 0,
	}

	// Get IP addresses for the interface
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err == nil {
		for _, addr := range addrs {
			interfaceInfo.IPAddresses = append(interfaceInfo.IPAddresses, addr.IP.String())
		}
	}

	// Get gateway if interface is up
	var gatewayIP string
	if isUp {
		gatewayIP = nm.getGatewayForInterface(interfaceName)
	}

	// Determine event type
	eventType := EventInterfaceUp
	if !isUp {
		eventType = EventInterfaceDown
	}

	// Create and send event
	event := NetworkEvent{
		Type:          eventType,
		InterfaceName: interfaceName,
		InterfaceInfo: interfaceInfo,
		GatewayIP:     gatewayIP,
		Timestamp:     time.Now(),
	}

	nm.sendEvent(event)

	// Log the change
	if isUp {
		log.Printf("Interface %s is UP (MAC: %s, Gateway: %s)", interfaceName, attrs.HardwareAddr.String(), gatewayIP)
	} else {
		log.Printf("Interface %s is DOWN", interfaceName)
	}
}

// handleAddressUpdate processes an IP address update
func (nm *networkMonitor) handleAddressUpdate(update netlink.AddrUpdate) {
	// Get the link for this address update
	link, err := netlink.LinkByIndex(update.LinkIndex)
	if err != nil {
		log.Printf("Error getting link by index %d: %v", update.LinkIndex, err)
		return
	}

	attrs := link.Attrs()
	interfaceName := attrs.Name

	// Check if we should monitor this interface
	if !nm.shouldMonitorInterface(interfaceName) {
		return
	}

	// Create interface info
	interfaceInfo := &InterfaceInfo{
		Name:       interfaceName,
		MacAddress: attrs.HardwareAddr.String(),
		IsUp:       attrs.Flags&net.FlagUp != 0,
	}

	// Get all current IP addresses for the interface
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err == nil {
		for _, addr := range addrs {
			interfaceInfo.IPAddresses = append(interfaceInfo.IPAddresses, addr.IP.String())
		}
	}

	// Determine event type based on whether address was added or deleted
	eventType := EventAddressAdded
	if update.NewAddr == false {
		eventType = EventAddressDeleted
	}

	// Get gateway for the interface
	gatewayIP := nm.getGatewayForInterface(interfaceName)

	// Create and send event
	event := NetworkEvent{
		Type:          eventType,
		InterfaceName: interfaceName,
		InterfaceInfo: interfaceInfo,
		GatewayIP:     gatewayIP,
		Timestamp:     time.Now(),
	}

	nm.sendEvent(event)

	log.Printf("Address %s on interface %s (action: %s)",
		update.LinkAddress.IP.String(), interfaceName,
		map[bool]string{true: "added", false: "deleted"}[update.NewAddr])
}

// shouldMonitorInterface checks if an interface should be monitored
func (nm *networkMonitor) shouldMonitorInterface(name string) bool {
	// Check ignore list
	for _, ignored := range nm.config.IgnoreInterfaces {
		if name == ignored {
			return false
		}
	}

	// Check only list
	if len(nm.config.OnlyInterfaces) > 0 {
		for _, allowed := range nm.config.OnlyInterfaces {
			if name == allowed {
				return true
			}
		}
		return false
	}

	return true
}

// getGatewayForInterface gets the gateway IP for an interface
func (nm *networkMonitor) getGatewayForInterface(interfaceName string) string {
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return ""
	}

	routes, err := netlink.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		return ""
	}

	// Look for default route (destination is nil)
	for _, route := range routes {
		if route.Dst == nil && route.Gw != nil {
			return route.Gw.String()
		}
	}

	return ""
}

// sendEvent safely sends an event to the events channel
func (nm *networkMonitor) sendEvent(event NetworkEvent) {
	select {
	case nm.events <- event:
		// Event sent successfully
	default:
		log.Printf("Network event channel full, dropping event for interface %s", event.InterfaceName)
	}
}
