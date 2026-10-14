package upstream_detector

import (
	"time"
)

// NetworkEvent represents a network state change event
type NetworkEvent struct {
	Type          EventType
	InterfaceName string
	InterfaceInfo *InterfaceInfo
	GatewayIP     string
	Timestamp     time.Time
}

// EventType represents the type of network event
type EventType int

const (
	EventInterfaceUp EventType = iota
	EventInterfaceDown
	EventRouteDeleted
	EventAddressAdded
	EventAddressDeleted
)

// InterfaceInfo contains information about a network interface
type InterfaceInfo struct {
	Name           string
	MacAddress     string
	IPAddresses    []string
	IsUp           bool
	IsLoopback     bool
	IsPointToPoint bool
	// IsBridge marks a bridge device netifd created for this router
	// (br-lan, br-private). Bridges are our own LAN fabric, never an
	// upstream link.
	IsBridge bool
}

// IsUpstreamGatewayCandidate reports whether this interface may carry an
// upstream TollGate, and therefore whether its gateway is worth probing.
//
// Bridges are excluded: netifd's LAN bridges (br-lan, br-private) carry no
// default route, so gateway selection falls through to netmask inference,
// which fabricates the .254 host of their subnet. Nothing serves that
// address, yet the session manager kept a gateway entry for it and probed
// it three times per cycle forever ("no route to host") — and the entry's
// interface was the owner LAN.
func (i *InterfaceInfo) IsUpstreamGatewayCandidate() bool {
	if i == nil || !i.IsUp || i.IsLoopback || i.IsBridge {
		return false
	}
	return len(i.IPAddresses) > 0
}
