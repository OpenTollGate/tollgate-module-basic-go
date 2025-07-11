//go:build linux
// +build linux

package crowsnest

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/vishvananda/netlink"
)

// networkMonitor implements the NetworkMonitor interface using event-driven netlink subscriptions
type networkMonitor struct {
	config        *config_manager.CrowsnestConfig
	events        chan NetworkEvent
	stopChan      chan struct{}
	wg            sync.WaitGroup
	running       bool
	mu            sync.RWMutex
	lastEventTime map[string]time.Time // Track last event time per interface
	eventMutex    sync.RWMutex         // Protect lastEventTime map
}

// NewNetworkMonitor creates a new event-driven network monitor
func NewNetworkMonitor(config *config_manager.CrowsnestConfig) NetworkMonitor {
	return &networkMonitor{
		config:        config,
		events:        make(chan NetworkEvent, 100),
		stopChan:      make(chan struct{}),
		lastEventTime: make(map[string]time.Time),
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

	// Log the change (only for debug level to reduce spam)
	if nm.config.IsDebugLevel() {
		if isUp {
			log.Printf("Interface %s is UP (MAC: %s, Gateway: %s)", interfaceName, attrs.HardwareAddr.String(), gatewayIP)
		} else {
			log.Printf("Interface %s is DOWN", interfaceName)
		}
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

	if nm.config.IsDebugLevel() {
		log.Printf("Address %s on interface %s (action: %s)",
			update.LinkAddress.IP.String(), interfaceName,
			map[bool]string{true: "added", false: "deleted"}[update.NewAddr])
	}
}

// shouldMonitorInterface checks if an interface should be monitored
func (nm *networkMonitor) shouldMonitorInterface(name string) bool {
	// Check ignore list
	for _, ignored := range nm.config.IgnoreInterfaces {
		if name == ignored {
			return false
		}
	}

	// Skip bridge interfaces as they are typically local LAN bridges, not upstream connections
	if strings.HasPrefix(name, "br-") {
		if nm.config.IsDebugLevel() {
			log.Printf("Skipping bridge interface %s - likely local LAN bridge", name)
		}
		return false
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
		if nm.config.IsDebugLevel() {
			log.Printf("Error getting link for interface %s: %v", interfaceName, err)
		}
		return ""
	}

	// Method 1: Check for default route on this specific interface
	routes, err := netlink.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		if nm.config.IsDebugLevel() {
			log.Printf("Error getting routes for interface %s: %v", interfaceName, err)
		}
	} else {
		// Look for default route (destination is nil)
		for _, route := range routes {
			if route.Dst == nil && route.Gw != nil {
				if nm.config.IsDebugLevel() {
					log.Printf("Found default route gateway %s for interface %s", route.Gw.String(), interfaceName)
				}
				return route.Gw.String()
			}
		}
	}

	// Method 2: Check global routing table for default routes that use this interface
	allRoutes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		if nm.config.IsDebugLevel() {
			log.Printf("Error getting global routes: %v", err)
		}
	} else {
		for _, route := range allRoutes {
			if route.Dst == nil && route.Gw != nil && route.LinkIndex == link.Attrs().Index {
				if nm.config.IsDebugLevel() {
					log.Printf("Found global default route gateway %s for interface %s", route.Gw.String(), interfaceName)
				}
				return route.Gw.String()
			}
		}
	}

	// Method 3: Infer gateway from IP address (common pattern: x.x.x.1)
	// Get IP addresses for this interface
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		if nm.config.IsDebugLevel() {
			log.Printf("Error getting addresses for interface %s: %v", interfaceName, err)
		}
		return ""
	}

	for _, addr := range addrs {
		ip := addr.IP
		if ip.To4() != nil && !ip.IsLoopback() {
			// Try common gateway patterns
			gatewayIP := nm.inferGatewayFromIP(ip, addr.Mask)
			if gatewayIP != "" {
				if nm.config.IsDebugLevel() {
					log.Printf("Inferred gateway %s for interface %s from IP %s", gatewayIP, interfaceName, ip.String())
				}
				return gatewayIP
			}
		}
	}

	if nm.config.IsDebugLevel() {
		log.Printf("No gateway found for interface %s", interfaceName)
	}
	return ""
}

// inferGatewayFromIP tries to infer the gateway IP from the interface IP and netmask
func (nm *networkMonitor) inferGatewayFromIP(ip net.IP, mask net.IPMask) string {
	if ip.To4() == nil {
		return "" // Only handle IPv4
	}

	// Calculate network address
	network := ip.Mask(mask)

	// Common gateway patterns to try:
	// 1. Network address + 1 (e.g., 192.168.1.1 for 192.168.1.0/24)
	// 2. Network address + 254 (e.g., 192.168.1.254 for 192.168.1.0/24)

	gatewayOptions := []net.IP{
		net.IP{network[0], network[1], network[2], network[3] + 1},
		net.IP{network[0], network[1], network[2], network[3] + 254},
	}

	for _, gateway := range gatewayOptions {
		// Make sure gateway is in the same network
		if gateway.Mask(mask).Equal(network) && !gateway.Equal(ip) {
			// We could ping test here, but for now just return the first reasonable option
			return gateway.String()
		}
	}

	return ""
}

// GetCurrentInterfaces returns current network interface information
func (nm *networkMonitor) GetCurrentInterfaces() ([]*InterfaceInfo, error) {
	var interfaces []*InterfaceInfo

	// Get all network links
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("failed to list network links: %w", err)
	}

	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}

		// Check if we should monitor this interface
		if !nm.shouldMonitorInterface(attrs.Name) {
			continue
		}

		// Create interface info
		interfaceInfo := &InterfaceInfo{
			Name:           attrs.Name,
			MacAddress:     attrs.HardwareAddr.String(),
			IsUp:           attrs.Flags&net.FlagUp != 0,
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

		interfaces = append(interfaces, interfaceInfo)
	}

	return interfaces, nil
}

// GetGatewayForInterface gets the gateway IP for an interface (public interface method)
func (nm *networkMonitor) GetGatewayForInterface(interfaceName string) string {
	return nm.getGatewayForInterface(interfaceName)
}

// sendEvent safely sends an event to the events channel with deduplication
func (nm *networkMonitor) sendEvent(event NetworkEvent) {
	// Create a unique key for this event type and interface
	eventKey := fmt.Sprintf("%s:%d", event.InterfaceName, event.Type)

	// Check if we should throttle this event
	nm.eventMutex.Lock()
	lastTime, exists := nm.lastEventTime[eventKey]
	now := time.Now()

	// Only send if enough time has passed since last event of this type
	minInterval := 2 * time.Second // Configurable throttling interval
	if exists && now.Sub(lastTime) < minInterval {
		nm.eventMutex.Unlock()
		// Skip this event - too soon since last one
		return
	}

	// Update last event time
	nm.lastEventTime[eventKey] = now
	nm.eventMutex.Unlock()

	select {
	case nm.events <- event:
		// Event sent successfully
	default:
		log.Printf("Network event channel full, dropping event for interface %s", event.InterfaceName)
	}
}
