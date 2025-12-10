//go:build linux
// +build linux

package crowsnest

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/sirupsen/logrus"
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
		events:        make(chan NetworkEvent, 512),
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

	logger.Info("Starting event-driven network monitor")

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

	logger.Info("Stopping network monitor")

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
		logger.WithError(err).Error("Failed to subscribe to link updates")
		return
	}

	logger.Info("Monitoring network interface link changes...")

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
		logger.WithError(err).Error("Failed to subscribe to address updates")
		return
	}

	logger.Info("Monitoring network interface address changes...")

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

	logger.WithFields(logrus.Fields{
		"iface_name": attrs.Name,
		"is_up":      (attrs.Flags&net.FlagUp) != 0,
	}).Debug("Netlink: Received Link Update")

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
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"mac":       attrs.HardwareAddr.String(),
			"gateway":   gatewayIP,
		}).Debug("Interface is UP")
	} else {
		logger.WithField("interface", interfaceName).Debug("Interface is DOWN")
	}
}

// handleAddressUpdate processes an IP address update
func (nm *networkMonitor) handleAddressUpdate(update netlink.AddrUpdate) {
	logger.WithFields(logrus.Fields{
		"address":   update.LinkAddress.IP.String(),
		"new_addr":  update.NewAddr,
		"linkIndex": update.LinkIndex,
	}).Debug("Netlink: Received Address Update")
	// Get the link for this address update
	link, err := netlink.LinkByIndex(update.LinkIndex)
	if err != nil {
		// This is a race condition: if the link is deleted, we might get an address update
		// for an index that no longer exists. If the address is being deleted, it's safe
		// to ignore this, as a link deletion event will follow.
		if !update.NewAddr {
			logger.WithFields(logrus.Fields{
				"link_index": update.LinkIndex,
				"address":    update.LinkAddress.IP.String(),
			}).Debug("Ignoring address deletion for a link that is already gone.")
			return
		}

		logger.WithFields(logrus.Fields{
			"link_index": update.LinkIndex,
			"error":      err,
		}).Error("Error getting link by index")
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

	logger.WithFields(logrus.Fields{
		"address":   update.LinkAddress.IP.String(),
		"interface": interfaceName,
		"action":    map[bool]string{true: "added", false: "deleted"}[update.NewAddr],
	}).Debug("Address change on interface")
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
		logger.WithField("interface", name).Debug("Skipping bridge interface - likely local LAN bridge")
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
		logger.WithFields(logrus.Fields{
			"interface": interfaceName,
			"error":     err,
		}).Debug("Error getting link for interface")
		return ""
	}

	// Method 1: Check for default route on this specific interface
	if gw := nm.getGatewayFromRoutes(link); gw != "" {
		return gw
	}

	// Method 2: Check global routing table for default routes that use this interface
	if gw := nm.getGatewayFromGlobalRoutes(link); gw != "" {
		return gw
	}

	// Method 3: Infer gateway from IP address
	if gw := nm.getGatewayByInference(link); gw != "" {
		return gw
	}

	logger.WithField("interface", interfaceName).Debug("No gateway found for interface")
	return ""
}

// getGatewayFromRoutes checks for a default route on a specific interface.
func (nm *networkMonitor) getGatewayFromRoutes(link netlink.Link) string {
	routes, err := netlink.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"interface": link.Attrs().Name,
			"error":     err,
		}).Debug("Error getting routes for interface")
		return ""
	}

	for _, route := range routes {
		if route.Dst == nil && route.Gw != nil {
			logger.WithFields(logrus.Fields{
				"gateway":   route.Gw.String(),
				"interface": link.Attrs().Name,
			}).Debug("Found default route gateway for interface")
			return route.Gw.String()
		}
	}
	return ""
}

// getGatewayFromGlobalRoutes checks the global routing table for default routes that use this interface.
func (nm *networkMonitor) getGatewayFromGlobalRoutes(link netlink.Link) string {
	allRoutes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		logger.WithError(err).Debug("Error getting global routes")
		return ""
	}

	for _, route := range allRoutes {
		if route.Dst == nil && route.Gw != nil && route.LinkIndex == link.Attrs().Index {
			logger.WithFields(logrus.Fields{
				"gateway":   route.Gw.String(),
				"interface": link.Attrs().Name,
			}).Debug("Found global default route gateway for interface")
			return route.Gw.String()
		}
	}
	return ""
}

// getGatewayByInference infers the gateway from the IP address of the interface.
func (nm *networkMonitor) getGatewayByInference(link netlink.Link) string {
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"interface": link.Attrs().Name,
			"error":     err,
		}).Debug("Error getting addresses for interface")
		return ""
	}

	for _, addr := range addrs {
		ip := addr.IP
		if ip.To4() != nil && !ip.IsLoopback() {
			gatewayIP := nm.inferGatewayFromIP(ip, addr.Mask)
			if gatewayIP != "" {
				logger.WithFields(logrus.Fields{
					"gateway":   gatewayIP,
					"interface": link.Attrs().Name,
					"ip":        ip.String(),
				}).Debug("Inferred gateway for interface from IP")
				return gatewayIP
			}
		}
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

	// Only send if enough time has passed since last event of this type.
	// CRITICAL: Do NOT throttle InterfaceDown or AddressDeleted events, as they are essential for state clearing.
	minInterval := 2 * time.Second // Configurable throttling interval
	if event.Type != EventInterfaceDown && event.Type != EventAddressDeleted && exists && now.Sub(lastTime) < minInterval {
		nm.eventMutex.Unlock()
		logger.WithFields(logrus.Fields{
			"event_type":    event.Type,
			"interface":     event.InterfaceName,
			"min_interval":  minInterval,
			"time_since":    now.Sub(lastTime),
			"decision":      "throttled",
			"event_key":     eventKey,
			"last_event_at": lastTime,
		}).Debug("sendEvent: Throttling event")
		// Skip this event - too soon since last one
		return
	}
	logger.WithFields(logrus.Fields{
		"event_type":    event.Type,
		"interface":     event.InterfaceName,
		"min_interval":  minInterval,
		"decision":      "sent",
		"event_key":     eventKey,
		"last_event_at": lastTime,
	}).Debug("sendEvent: Sending event")

	// Update last event time
	nm.lastEventTime[eventKey] = now
	nm.eventMutex.Unlock()

	select {
	case nm.events <- event:
		// Event sent successfully
	default:
		logger.WithField("interface", event.InterfaceName).Warn("Network event channel full, dropping event for interface")
	}
}
